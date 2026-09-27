.PHONY: test vet build synth demo demo-nats demo-day demo-day-stop env up down ps ddl web gate2
COMPOSE := docker compose --env-file .env -f infra/docker-compose.yml

test: ; go test ./...
vet:  ; go vet ./...
build: ; mkdir -p bin && go build -o bin/ ./cmd/...
synth: ; go run ./cmd/syngen > testdata/synthetic_day.ndjson
# Demos replay SYNTHETIC data; they opt in to ALLOW_SYNTHETIC_ON_BUS themselves (rule 5: local only).
demo: build ; ALLOW_SYNTHETIC_ON_BUS=1 SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson bin/collector | bin/engine > out.ndjson && echo "wrote out.ndjson"
# Needs docker. Replays the synthetic day over a THROWAWAY NATS container (not the `make up` stack).
demo-nats: build ; infra/demo-nats.sh
# Web app: typecheck, unit tests, static export (web/out, served by the gateway).
web: ; cd web && npm ci --no-audit --no-fund && npx tsc --noEmit && npm test && NEXT_TELEMETRY_DISABLED=1 npm run build
# Needs docker + Chromium. Gate 2: 1500 synthetic symbols, real-time replay, THROWAWAY NATS and Centrifugo.
gate2: ; infra/gate2.sh
# Needs docker. Synthetic day on a DEMO CLOCK (default: last trading day from 11:40, x5), THROWAWAY
# NATS and Centrifugo; dashboard on http://127.0.0.1:8090. Run again to restart the day (docs/demo-clock.md).
demo-day: ; infra/demo-day.sh start
demo-day-stop: ; infra/demo-day.sh stop

# Local stack (I-01). `make env` once, then `make up ddl`.
env: ; infra/gen-env.sh
up:  ; $(COMPOSE) up -d --wait
down: ; $(COMPOSE) down
ps:  ; $(COMPOSE) ps
ddl:
	@for f in infra/clickhouse/*.sql; do echo "apply $$f"; \
	  $(COMPOSE) exec -T clickhouse sh -c 'clickhouse-client --user dev --password "$$CLICKHOUSE_PASSWORD" --multiquery' < $$f || exit 1; done
	@$(COMPOSE) exec -T clickhouse sh -c 'clickhouse-client --user dev --password "$$CLICKHOUSE_PASSWORD" -q "SHOW TABLES FROM market"'
