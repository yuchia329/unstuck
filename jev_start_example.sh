#!/bin/sh
# Runs the Jev Ultrafast demo Agent (bridge/demo/jev/run.py) against the public backend.
# Opens the site, then asks for the task in the terminal.
# Copy this file, then fill in the two keys and the wallet address below. Do not commit the copy: it holds API keys.
#
# No Unstuck API key: the backend runs with payments off, so the Agent names its Customer by wallet address.

# Jev picks each action (console.typesafe.ai/keys).
export TYPESAFE_API_KEY="apikey_..."
# Gemini writes the text Jev types into fields.
export GEMINI_API_KEY="AQ...."
# export TEXT_MODEL=gemini-3.1-flash-lite-preview
# Starting page size; resizing Chrome's window resizes the page. 480x720 suits a Solver on a phone.
# export JEV_VIEWPORT=800x900

# Any Solana wallet address you own. It names your Customer; nothing is charged and nothing is signed.
unset UNSTUCK_API_KEY
export UNSTUCK_WALLET="..."
export UNSTUCK_URL=https://unstuck.yuchia.dev

for k in TYPESAFE_API_KEY GEMINI_API_KEY UNSTUCK_WALLET; do
  eval "v=\$$k"
  case "$v" in
    "" | *...*) echo "Set $k in $0." >&2; exit 1 ;;
  esac
done

# The demo opens Indiana's business registry in Chrome, then asks for the task at "User:".
# The task should say when to stop.
export AGENT_URL="https://bsd.sos.in.gov/publicbusinesssearch"
export AGENT_ASK_TASK=1

cd "$(dirname "$0")/bridge" || exit 1
exec npm run demo:jev
