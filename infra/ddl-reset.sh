#!/bin/sh
# Recreates the ClickHouse `market` tables from infra/clickhouse (W-01 schema) — ONLY when every
# table is empty. `make ddl` is CREATE IF NOT EXISTS and cannot change an existing table's engine
# or key; a database holding rows needs a real migration, never this.
# Usage (from the repo root): infra/ddl-reset.sh   (or: make ddl-reset)
set -eu
cd "$(dirname "$0")/.."
ch() {
	MSYS_NO_PATHCONV=1 docker compose --env-file .env -f infra/docker-compose.yml exec -T clickhouse \
		sh -c 'clickhouse-client --user dev --password "$CLICKHOUSE_PASSWORD" "$@"' ch "$@"
}
rows=$(ch -q "SELECT sum(total_rows) FROM system.tables WHERE database = 'market'" | tr -d '\r')
if [ "$rows" != "0" ]; then
	echo "market holds $rows rows: refusing to drop anything (write a migration)" >&2
	exit 1
fi
for t in snapshots flow_events flow_10m game_totals quality_issues; do
	ch -q "DROP TABLE IF EXISTS market.$t"
done
for f in infra/clickhouse/*.sql; do
	echo "apply $f"
	ch --multiquery <"$f"
done
ch -q "SELECT name, engine FROM system.tables WHERE database = 'market' ORDER BY name"
