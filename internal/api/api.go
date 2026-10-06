// Package api serves the Unstuck HTTP API.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yuchia329/unstuck/internal/queue"
	"github.com/yuchia329/unstuck/internal/session"
	"github.com/yuchia329/unstuck/internal/store"
	"github.com/yuchia329/unstuck/internal/task"
	"github.com/yuchia329/unstuck/internal/web"
)

const maxBodyBytes = 64 << 10

// Config holds the settings chosen by whoever runs the backend.
type Config struct {
	DBPath       string
	ClaimWindow  time.Duration
	SolveWindow  time.Duration
	PingInterval time.Duration // how often to ping Bridge and Queue sockets; zero disables
	// STUNURLs and TURNURLs are handed to a Session's peers for a direct
	// WebRTC connection. TURN needs TURNSecret, coturn's static-auth-secret.
	STUNURLs   []string
	TURNURLs   []string
	TURNSecret string
}

// Server is the backend: an http.Handler plus the resources behind it.
type Server struct {
	cfg   Config
	db    *sql.DB
	tasks *task.Lifecycle
	queue *queue.Hub
	relay *session.Relay
	mux   *http.ServeMux
}

func (c Config) validate() error {
	switch {
	case c.ClaimWindow <= 0 || c.SolveWindow <= 0:
		return fmt.Errorf("claim and solve windows must be positive, got %v and %v", c.ClaimWindow, c.SolveWindow)
	case c.PingInterval < 0:
		return fmt.Errorf("ping interval must not be negative, got %v", c.PingInterval)
	case len(c.TURNURLs) > 0 && c.TURNSecret == "":
		return errors.New("TURN servers need a TURN secret")
	}
	return nil
}

func New(cfg Config) (*Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	db, err := store.Open(cfg.DBPath, task.Schema)
	if err != nil {
		return nil, err
	}
	if err := task.Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	hub := queue.New()
	relay := session.New()
	s := &Server{
		cfg: cfg,
		db:  db,
		tasks: task.New(db, task.Config{ClaimWindow: cfg.ClaimWindow, SolveWindow: cfg.SolveWindow}, func(e task.Event) {
			hub.Publish(e)
			relay.Publish(e)
		}),
		queue: hub,
		relay: relay,
		mux:   http.NewServeMux(),
	}
	if err := s.tasks.Resume(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	s.mux.HandleFunc("POST /v1/tasks", s.handleCreateTask)
	s.mux.HandleFunc("GET /v1/queue", s.handleQueue)
	s.mux.HandleFunc("GET /v1/tasks/{id}/bridge", s.handleBridge)
	if err := s.routeQueuePage(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// routeQueuePage serves each Queue page file at its own path, index.html at
// "/", so unknown API paths still 404. Every file is sent with no-cache, and
// index.html loads queue.js under a content version, so a proxy or browser
// that cached an older copy cannot pair it with a newer page.
func (s *Server) routeQueuePage() error {
	files, err := fs.ReadDir(web.Queue, ".")
	if err != nil {
		return fmt.Errorf("queue page: %w", err)
	}
	index, err := versionedIndex()
	if err != nil {
		return fmt.Errorf("queue page: %w", err)
	}
	noCache := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			h(w, r)
		}
	}
	s.mux.HandleFunc("GET /{$}", noCache(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	}))
	fileServer := http.FileServerFS(web.Queue)
	for _, f := range files {
		if f.Name() != "index.html" {
			s.mux.HandleFunc("GET /"+f.Name(), noCache(fileServer.ServeHTTP))
		}
	}
	return nil
}

// versionedIndex returns index.html with its queue.js reference carrying a
// hash of queue.js.
func versionedIndex() ([]byte, error) {
	index, err := fs.ReadFile(web.Queue, "index.html")
	if err != nil {
		return nil, err
	}
	script, err := fs.ReadFile(web.Queue, "queue.js")
	if err != nil {
		return nil, err
	}
	const ref = `src="queue.js"`
	if !bytes.Contains(index, []byte(ref)) {
		return nil, fmt.Errorf("index.html has no %s", ref)
	}
	sum := sha256.Sum256(script)
	versioned := fmt.Sprintf(`src="queue.js?v=%x"`, sum[:6])
	return bytes.Replace(index, []byte(ref), []byte(versioned), 1), nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) Close() error {
	s.tasks.Close()
	return s.db.Close()
}

// handleCreateTask creates a Task for an Agent's blocked page. Anyone may
// create one: an Agent needs no key and no account.
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PageURL  string `json:"page_url"`
		Obstacle string `json:"obstacle"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !isPageURL(req.PageURL) {
		writeError(w, http.StatusBadRequest, "invalid_page_url")
		return
	}
	obstacle := strings.TrimSpace(req.Obstacle)
	if !validObstacle(obstacle) {
		writeError(w, http.StatusBadRequest, "invalid_obstacle")
		return
	}
	created, err := s.tasks.Create(r.Context(), req.PageURL, obstacle)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"task_id":         created.ID,
		"session_token":   created.SessionToken,
		"claim_deadline":  created.ClaimDeadline.UTC().Format(time.RFC3339Nano),
		"solve_window_ms": s.cfg.SolveWindow.Milliseconds(),
	})
}

// maxObstacle bounds the characters of a Task's obstacle description.
const maxObstacle = 200

// validObstacle reports whether s can describe a Task's obstacle to Solvers:
// empty, or one line of at most maxObstacle characters.
func validObstacle(s string) bool {
	return utf8.RuneCountInString(s) <= maxObstacle && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

// isPageURL reports whether s is an absolute http(s) URL a Solver can be shown.
func isPageURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// decodeJSON reads a bounded JSON body into v, or responds 400.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal")
}
