#!/bin/sh
# Demo clock (docs/demo-clock.md): replays a SYNTHETIC trading day on a virtual clock, so the
# dashboard can be reviewed while the market is closed. DEV ONLY; everything on 127.0.0.1:
#   syngen (DEMO_SYMBOLS instruments on DEMO_DAY) → collector (DEMO_CLOCK) → THROWAWAY NATS
#   (127.0.0.1:4224) → engine → gateway (http://127.0.0.1:DEMO_PORT) → THROWAWAY Centrifugo
#   (127.0.0.1:8002, ephemeral secrets never printed) → browser. Both containers go on stop, so no
#   synthetic data or engine state is left anywhere (rule 5), as with `make gate2`.
# Usage, from the repo root: infra/demo-day.sh [start|stop]   (make demo-day / make demo-day-stop)
#   DEMO_DAY=last DEMO_AT=11:40 DEMO_RATE=5 DEMO_SYMBOLS=300 DEMO_FROM=11:30 DEMO_PORT=8090
# start stops a previous run first: running it again restarts the day from DEMO_AT. It stays in
# the foreground until Ctrl+C or `infra/demo-day.sh stop` (from any shell).
set -eu
cd "$(dirname "$0")/.."
# Git Bash (Windows): keep MSYS from rewriting /container/paths in docker arguments; native
# programs get Windows paths (D:/...). Elsewhere `pwd -W` fails and plain pwd is used.
export MSYS_NO_PATHCONV=1
root=$(pwd -W 2>/dev/null || pwd)
out=demo-out
nats=bourse-demo-day-nats
cent=bourse-demo-day-centrifugo
pidfile=$out/pids

stop_run() { # kill the processes listed in the pid file (MSYS pid, Windows pid) and the containers
	if [ -f "$pidfile" ]; then
		while read -r pid winpid; do
			[ "$pid" = run ] && continue
			kill "$pid" 2>/dev/null || { [ -n "$winpid" ] && taskkill /F /PID "$winpid" >/dev/null 2>&1; } || true
		done <"$pidfile"
		rm -f "$pidfile"
	fi
	docker rm -f "$nats" "$cent" >/dev/null 2>&1 || true
}

case "${1:-start}" in
stop)
	stop_run
	echo "demo day stopped"
	exit 0
	;;
start) ;;
*)
	echo "usage: $0 [start|stop]" >&2
	exit 2
	;;
esac

day=${DEMO_DAY:-last} at=${DEMO_AT:-11:40} rate=${DEMO_RATE:-5}
port=${DEMO_PORT:-8090} symbols=${DEMO_SYMBOLS:-300} from=${DEMO_FROM-11:30} # DEMO_FROM= : whole day

stop_run
mkdir -p "$out"
go build -o bin/ ./cmd/...
if [ ! -d web/out ]; then
	if [ ! -d web/node_modules ]; then (cd web && npm ci --no-audit --no-fund); fi
	(cd web && NEXT_TELEMETRY_DISABLED=1 npm run build >/dev/null)
fi
bin/syngen -day "$day" -n "$symbols" ${from:+-from "$from"} -map "$out/sessions.json" >"$out/day.ndjson"

pids=""
echo "run $$" >"$pidfile"
track() { # remember a started process (MSYS pid and, on Windows, its Windows pid)
	pids="$pids $1"
	echo "$1 $(cat "/proc/$1/winpid" 2>/dev/null || true)" >>"$pidfile"
}
cleanup() { # only this run: a newer run may already own the pid file and the container names
	for p in $pids; do kill "$p" 2>/dev/null || true; done
	if [ -f "$pidfile" ] && [ "$(head -n 1 "$pidfile")" = "run $$" ]; then
		rm -f "$pidfile"
		docker rm -f "$nats" "$cent" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

api_key=$(openssl rand -hex 24)
secret=$(openssl rand -hex 24)
sed "s#\"allowed_origins\": \[#\"allowed_origins\": [\"http://127.0.0.1:$port\", \"http://localhost:$port\", #" \
	infra/centrifugo/config.json >"$out/centrifugo.json"
docker run -d --rm --name "$nats" -p 127.0.0.1:4224:4222 -p 127.0.0.1:8224:8222 \
	-v "$root/infra/nats:/etc/nats:ro" nats:2.10-alpine -c /etc/nats/nats.conf >/dev/null
docker run -d --rm --name "$cent" -p 127.0.0.1:8002:8000 -e CENTRIFUGO_API_KEY="$api_key" \
	-e CENTRIFUGO_TOKEN_HMAC_SECRET_KEY="$secret" -v "$root/$out/centrifugo.json:/centrifugo/config.json:ro" \
	centrifugo/centrifugo:v5 centrifugo -c /centrifugo/config.json >/dev/null
wait_url() { i=0; until curl -fs "$1" >/dev/null 2>&1; do i=$((i + 1)); [ "$i" -lt 300 ] || { echo "timeout: $1 (logs in $out/)" >&2; exit 1; }; sleep 0.2; done; }
wait_url "http://127.0.0.1:8224/healthz?js-enabled-only=true"
wait_url "http://127.0.0.1:8002/health"

export NATS_URL=nats://127.0.0.1:4224 BUS=nats ALLOW_SYNTHETIC_ON_BUS=1 SESSIONS_FILE="$root/$out/sessions.json"
bin/engine 2>"$out/engine.log" &
track $!
sleep 2 # the engine creates the streams
# One timeline for gateway and collector: the demo clock reads DEMO_AT on DEMO_DAY at this instant.
export DEMO_CLOCK="$day $at" DEMO_CLOCK_RATE="$rate" DEMO_CLOCK_ANCHOR="$(date +%s)"
GATEWAY_ADDR="127.0.0.1:$port" CENTRIFUGO_API_URL=http://127.0.0.1:8002/api CENTRIFUGO_API_KEY="$api_key" \
	CENTRIFUGO_SECRET="$secret" CENTRIFUGO_WS_URL=ws://127.0.0.1:8002/connection/websocket GATEWAY_DEV_TOKEN=1 \
	WEB_DIR="$root/web/out" bin/gateway 2>"$out/gateway.log" &
track $!
SOURCE=replay REPLAY_FILE="$root/$out/day.ndjson" bin/collector 2>"$out/collector.log" &
track $!
wait_url "http://127.0.0.1:$port/healthz"
echo "demo day running: http://127.0.0.1:$port/  (DEMO_CLOCK=$DEMO_CLOCK x$rate; logs in $out/)"
echo "stop: Ctrl+C here, or infra/demo-day.sh stop; restart the day: infra/demo-day.sh again, then reload the page"
wait
