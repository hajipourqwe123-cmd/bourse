#!/usr/bin/env bash
# LOCAL live review (never production, never exposed): SourceArena → collector → engine → gateway
# → dashboard at http://127.0.0.1:8088/ (8080 is often taken; LIVE_GATEWAY_ADDR), plus vendorcmp -watch (BrsApi, comparison only, not
# into the engine). Needs the local stack (make up) and .env with SOURCEARENA_TOKEN, BRSAPI_KEY,
# CENTRIFUGO_*. Everything binds to 127.0.0.1; secrets stay in .env.
#
#   infra/local-live.sh start | stop | restart | status | score [YYYY-MM-DD]
#
# SourceArena quota: LIVE_SA_DAILY_LIMIT (default 40, UNVERIFIED: the token was refused after ~45
# requests on 2026-09-27; confirm the plan in the vendor panel). LIVE_POLL_INTERVAL defaults to the
# shortest interval that fits that limit over the calendar's 08:25–18:00 union (34,500 s):
# ⌊34500 / interval⌋ + 1 ≤ limit. Other defaults: LIVE_STALE_AFTER = 2 × interval + 20 s,
# LIVE_BRSAPI_MAX=90 (free plan: 100/day), LIVE_CMP_EVERY=5m. The day's SourceArena request count
# persists in .local/sourcearena-budget.json; a vendor "limit reached" pauses to the next day.
#
# Secrets: each service gets only what it needs (collector: SOURCEARENA_TOKEN; vendorcmp:
# BRSAPI_KEY; gateway: CENTRIFUGO_*; engine: none).
set -euo pipefail
cd "$(dirname "$0")/.."
L=.local
mkdir -p "$L/bin" "$L/logs" "$L/vendorcmp"

env_up() {
	[ -f .env ] || { echo "local-live: .env missing (make env)"; exit 1; }
	set -a; . ./.env; set +a
	unset ALLOW_SYNTHETIC_ON_BUS REPLAY_REBASE
	export SOURCE=sourcearena BUS=nats NATS_URL=nats://127.0.0.1:4222
	local limit="${LIVE_SA_DAILY_LIMIT:-40}" span=34500
	local secs=$((span / limit + 1))
	export POLL_INTERVAL="${LIVE_POLL_INTERVAL:-${secs}s}" SOURCEARENA_DAILY_LIMIT="$limit"
	export SOURCEARENA_SAVE_LATEST="$L/sourcearena-latest.json" SOURCEARENA_BUDGET_FILE="$L/sourcearena-budget.json"
	export STREAM_PROFILE=local SESSIONS_FILE="$L/sessions-provisional.json" STALE_AFTER="${LIVE_STALE_AFTER:-$((2 * secs + 20))s}"
	export GATEWAY_ADDR="${LIVE_GATEWAY_ADDR:-127.0.0.1:8088}" CENTRIFUGO_API_URL=http://127.0.0.1:8000/api \
		CENTRIFUGO_WS_URL=ws://127.0.0.1:8000/connection/websocket GATEWAY_DEV_TOKEN=1 WEB_DIR=web/out
}

# Secrets each service must NOT receive (least privilege; .env exports all of them).
drop_secrets() {
	case $1 in
	collector) echo BRSAPI_KEY CENTRIFUGO_SECRET CENTRIFUGO_API_KEY CLICKHOUSE_PASSWORD POSTGRES_PASSWORD ;;
	engine) echo SOURCEARENA_TOKEN BRSAPI_KEY CENTRIFUGO_SECRET CENTRIFUGO_API_KEY CLICKHOUSE_PASSWORD POSTGRES_PASSWORD ;;
	gateway) echo SOURCEARENA_TOKEN BRSAPI_KEY CLICKHOUSE_PASSWORD POSTGRES_PASSWORD ;;
	vendorcmp) echo SOURCEARENA_TOKEN CENTRIFUGO_SECRET CENTRIFUGO_API_KEY CLICKHOUSE_PASSWORD POSTGRES_PASSWORD ;;
	esac
}

launch() { # name, args...
	local name=$1; shift
	local unset=() v
	for v in $(drop_secrets "$name"); do unset+=(-u "$v"); done
	nohup env "${unset[@]}" ".local/bin/$name.exe" "$@" >>"$L/logs/$name.log" 2>&1 &
	echo "local-live: $name started, log $L/logs/$name.log"
}

# Windows PIDs of this checkout's .local/bin/<name>.exe (other programs with the same name are ignored).
winpids() {
	powershell -NoProfile -Command "Get-Process '$1' -ErrorAction SilentlyContinue | Where-Object { \$_.Path -eq '$(cygpath -w "$PWD/$L/bin/$1.exe")' } | ForEach-Object { \$_.Id }" | tr -d '\r'
}
alive() { [ -n "$(winpids "$1")" ]; }

start() {
	env_up
	for c in collector engine gateway vendorcmp classmap; do go build -o "$L/bin/$c.exe" "./cmd/$c"; done
	[ -f web/out/index.html ] || { echo "local-live: web/out missing (make web)"; exit 1; }
	# Provisional class map: from the collector's last payload if there is one (no quota), else one
	# request. Only when collector, engine and gateway are all stopped: they must load the same file.
	if alive collector || alive engine || alive gateway; then
		[ -f "$SESSIONS_FILE" ] || { echo "local-live: $SESSIONS_FILE missing; stop first"; exit 1; }
	elif [ -f "$L/sourcearena-latest.json" ]; then
		"$L/bin/classmap.exe" -sourcearena "$L/sourcearena-latest.json" -out "$SESSIONS_FILE" >"$L/logs/classmap.txt"
	elif [ ! -f "$SESSIONS_FILE" ]; then
		"$L/bin/classmap.exe" -out "$SESSIONS_FILE" >"$L/logs/classmap.txt" # one SourceArena request (not in the budget file)
	fi
	# Start only what is not running (a crashed service can be restarted alone with `start`).
	alive collector || launch collector
	alive engine || launch engine
	alive gateway || launch gateway
	alive vendorcmp || launch vendorcmp -watch "${LIVE_CMP_EVERY:-5m}" -brsapi-max "${LIVE_BRSAPI_MAX:-90}" \
		-sourcearena "$SOURCEARENA_SAVE_LATEST" -out "$L/vendorcmp/latest.md" -log "$L/vendorcmp/runs.ndjson" 		-sa-budget "$SOURCEARENA_BUDGET_FILE" -sa-limit "$SOURCEARENA_DAILY_LIMIT" -brsapi-limit "${BRSAPI_DAILY_LIMIT:-100}"
	echo "local-live: SourceArena POLL_INTERVAL=$POLL_INTERVAL (limit $SOURCEARENA_DAILY_LIMIT/day, UNVERIFIED), STALE_AFTER=$STALE_AFTER"
	echo "local-live: dashboard http://$GATEWAY_ADDR/"
}

stop() {
	for c in vendorcmp gateway engine collector; do
		for p in $(winpids "$c"); do taskkill //PID "$p" //F >/dev/null && echo "local-live: $c stopped (pid $p)"; done
	done
	# The engine/gateway single-instance leases expire after their TTL (15 s): wait before a restart.
}

status() {
	for c in collector engine gateway vendorcmp; do
		if alive "$c"; then echo "$c: running (pid $(winpids "$c" | tr '\n' ' '))"; else echo "$c: stopped"; fi
	done
}

case "${1:-}" in
start) start ;;
stop) stop ;;
restart) stop; sleep 20; start ;;
status) status ;;
score) # daily Persian vendor scorecard (also rewritten by vendorcmp after every round)
	env_up
	go run ./cmd/vendorcmp -score "${2:-today}" -log "$L/vendorcmp/runs.ndjson" -sa-budget "$SOURCEARENA_BUDGET_FILE" 		-sa-limit "$SOURCEARENA_DAILY_LIMIT" -brsapi-limit "${BRSAPI_DAILY_LIMIT:-100}" ;;
*) echo "usage: $0 start|stop|restart|status|score [YYYY-MM-DD]"; exit 2 ;;
esac
