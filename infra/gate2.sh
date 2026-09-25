#!/bin/sh
# Gate 2 (PRD): runs the whole pipeline on ONE machine and measures the dashboard in Chromium:
# syngen (1500 synthetic symbols) → collector (REPLAY_REBASE=now, real-time pacing) → JetStream →
# engine → gateway → Centrifugo → browser. Everything runs on a THROWAWAY NATS (127.0.0.1:4223)
# and a THROWAWAY Centrifugo (127.0.0.1:8001) with ephemeral secrets that are never printed,
# so no synthetic data reaches the regular local stack (rule 5). Report: gate2-out/report.json.
# Run from the repo root via `make gate2`. GATE_SECONDS (default 300) sets the measured window.
set -eu
cd "$(dirname "$0")/.."
out=gate2-out
rm -rf "$out" && mkdir -p "$out"
go build -o bin/ ./cmd/...
if [ ! -d web/node_modules ]; then (cd web && npm ci --no-audit --no-fund); fi
(cd web && NEXT_TELEMETRY_DISABLED=1 npm run build >/dev/null)

bin/syngen -n 1500 -from 12:05 -to 12:14 -map "$out/sessions.json" >"$out/day.ndjson"

api_key=$(openssl rand -hex 24)
secret=$(openssl rand -hex 24)
sed 's#"allowed_origins": \[#"allowed_origins": ["http://127.0.0.1:8090", #' infra/centrifugo/config.json >"$out/centrifugo.json"
nats=bourse-gate2-nats
cent=bourse-gate2-centrifugo
pids=""
cleanup() {
	for p in $pids; do kill "$p" 2>/dev/null || true; done
	docker stop "$nats" "$cent" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM
docker rm -f "$nats" "$cent" >/dev/null 2>&1 || true
docker run -d --rm --name "$nats" -p 127.0.0.1:4223:4222 -p 127.0.0.1:8223:8222 \
	-v "$PWD/infra/nats:/etc/nats:ro" nats:2.10-alpine -c /etc/nats/nats.conf >/dev/null
docker run -d --rm --name "$cent" -p 127.0.0.1:8001:8000 -e CENTRIFUGO_API_KEY="$api_key" \
	-e CENTRIFUGO_TOKEN_HMAC_SECRET_KEY="$secret" -v "$PWD/$out/centrifugo.json:/centrifugo/config.json:ro" \
	centrifugo/centrifugo:v5 centrifugo -c /centrifugo/config.json >/dev/null
wait_url() { i=0; until curl -fs "$1" >/dev/null 2>&1; do i=$((i + 1)); [ "$i" -lt 150 ] || { echo "timeout: $1" >&2; exit 1; }; sleep 0.2; done; }
wait_url "http://127.0.0.1:8223/healthz?js-enabled-only=true"
wait_url "http://127.0.0.1:8001/health"

export NATS_URL=nats://127.0.0.1:4223 BUS=nats ALLOW_SYNTHETIC_ON_BUS=1 SESSIONS_FILE="$PWD/$out/sessions.json"
bin/engine 2>"$out/engine.log" & pids="$pids $!"
sleep 2 # the engine creates the streams
GATEWAY_ADDR=127.0.0.1:8090 CENTRIFUGO_API_URL=http://127.0.0.1:8001/api CENTRIFUGO_API_KEY="$api_key" \
	CENTRIFUGO_SECRET="$secret" CENTRIFUGO_WS_URL=ws://127.0.0.1:8001/connection/websocket GATEWAY_DEV_TOKEN=1 \
	WEB_DIR=web/out bin/gateway 2>"$out/gateway.log" & pids="$pids $!"
wait_url "http://127.0.0.1:8090/healthz"
SOURCE=replay REPLAY_FILE="$out/day.ndjson" REPLAY_REBASE=now REPLAY_AT=12:06 bin/collector 2>"$out/collector.log" & pids="$pids $!"

(cd web && GATE_URL=http://127.0.0.1:8090/ GATE_SECONDS="${GATE_SECONDS:-300}" npx playwright test)
echo "gate 2 report: $out/report.json; screenshots: $out/desktop-1440.png, $out/mobile-390.png"
