.PHONY: test vet build synth demo demo-nats env up down ps ddl
COMPOSE := docker compose --env-file .env -f infra/docker-compose.yml

test: ; go test ./...
vet:  ; go vet ./...
build: ; mkdir -p bin && go build -o bin/ ./cmd/...
synth: ; go run ./cmd/syngen > testdata/synthetic_day.ndjson
# Demos replay SYNTHETIC data; they opt in to ALLOW_SYNTHETIC_ON_BUS themselves (rule 5: local only).
demo: build ; ALLOW_SYNTHETIC_ON_BUS=1 SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson bin/collector | bin/engine > out.ndjson && echo "wrote out.ndjson"
# Needs `make up`. Publishes the synthetic day to the local JetStream and runs the engine until caught up.
demo-nats: build
	ALLOW_SYNTHETIC_ON_BUS=1 BUS=nats SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson bin/collector
	BUS=nats ENGINE_EXIT_WHEN_IDLE=1 bin/engine && echo "engine caught up; outputs are in JetStream streams FLOW, AI, QUALITY"

# Local stack (I-01). `make env` once, then `make up ddl`.
env: ; infra/gen-env.sh
up:  ; $(COMPOSE) up -d --wait
down: ; $(COMPOSE) down
ps:  ; $(COMPOSE) ps
ddl:
	@for f in infra/clickhouse/*.sql; do echo "apply $$f"; \
	  $(COMPOSE) exec -T clickhouse sh -c 'clickhouse-client --user dev --password "$$CLICKHOUSE_PASSWORD" --multiquery' < $$f || exit 1; done
	@$(COMPOSE) exec -T clickhouse sh -c 'clickhouse-client --user dev --password "$$CLICKHOUSE_PASSWORD" -q "SHOW TABLES FROM market"'
