// Command unstuck runs the Unstuck backend.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/yuchia329/unstuck/internal/api"
)

func main() {
	var cfg api.Config
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.StringVar(&cfg.DBPath, "db", "unstuck.db", "SQLite database path")
	flag.DurationVar(&cfg.ClaimWindow, "claim-window", 60*time.Second, "how long a Task may wait in the Queue before it Expires (demo: 30s)")
	flag.DurationVar(&cfg.SolveWindow, "solve-window", 120*time.Second, "how long a Solver has after Claim before the Task Fails (demo: 60s)")
	flag.DurationVar(&cfg.PingInterval, "ping-interval", 20*time.Second, "how often to ping Bridge and Queue sockets so proxies keep them open (0 disables)")
	stun := flag.String("stun", "stun:stun.l.google.com:19302", "comma-separated STUN URLs for direct Bridge-to-Solver connections (empty for none)")
	turn := flag.String("turn", "", "comma-separated TURN URLs, e.g. turn:host:3478; needs $UNSTUCK_TURN_SECRET, coturn's static-auth-secret")
	flag.Parse()
	cfg.STUNURLs, cfg.TURNURLs = urlList(*stun), urlList(*turn)
	cfg.TURNSecret = os.Getenv("UNSTUCK_TURN_SECRET")

	srv, err := api.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	httpSrv := &http.Server{Addr: *addr, Handler: srv}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()

	log.Printf("unstuck listening on %s (claim %v, solve %v, stun %v, turn %v)",
		*addr, cfg.ClaimWindow, cfg.SolveWindow, cfg.STUNURLs, cfg.TURNURLs)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-drained // in-flight requests finish before the database closes
	if err := srv.Close(); err != nil {
		log.Printf("close: %v", err)
	}
}

// urlList splits a comma-separated flag value, dropping empty entries.
func urlList(s string) []string {
	var urls []string
	for _, u := range strings.Split(s, ",") {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}
