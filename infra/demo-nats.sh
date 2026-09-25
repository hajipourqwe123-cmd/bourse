#!/bin/sh
# Replays the SYNTHETIC day through collector -> JetStream -> engine on a THROWAWAY NATS
# container (127.0.0.1:4223), never the shared local stack, so no SYN* data (rule 5) and no
# engine durable/checkpoint state is left behind. Run from the repo root via `make demo-nats`.
set -eu
cd "$(dirname "$0")/.."
name=bourse-demo-nats
docker rm -f "$name" >/dev/null 2>&1 || true
docker run -d --rm --name "$name" -p 127.0.0.1:4223:4222 -p 127.0.0.1:8223:8222 \
	-v "$PWD/infra/nats:/etc/nats:ro" nats:2.10-alpine -c /etc/nats/nats.conf >/dev/null
trap 'docker stop "$name" >/dev/null 2>&1 || true' EXIT
i=0
until curl -fs "http://127.0.0.1:8223/healthz?js-enabled-only=true" >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 50 ] || { echo "demo NATS did not start" >&2; exit 1; }
	sleep 0.2
done
export NATS_URL=nats://127.0.0.1:4223 BUS=nats ALLOW_SYNTHETIC_ON_BUS=1
SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson bin/collector
ENGINE_EXIT_WHEN_IDLE=1 bin/engine
echo "stream message counts (throwaway NATS, removed on exit):"
curl -fs "http://127.0.0.1:8223/jsz?streams=true" | tr ',{' '\n\n' | grep -E '"name"|"messages"' | paste - - || true
