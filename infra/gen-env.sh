#!/bin/sh
# Creates .env from .env.example with generated LOCAL-ONLY secrets. Never prints secret values.
# Usage (from the repo root): infra/gen-env.sh
set -eu
cd "$(dirname "$0")/.."
if [ -e .env ]; then
	echo ".env already exists; not overwriting (delete it first to regenerate)" >&2
	exit 1
fi
umask 077
tmp=$(mktemp .env.XXXXXX)
trap 'rm -f "$tmp"' EXIT
while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
	*_PASSWORD= | *_SECRET= | *_API_KEY=)
		printf '%s%s\n' "$line" "$(openssl rand -hex 24)" ;;
	*) printf '%s\n' "$line" ;;
	esac
done < .env.example >"$tmp"
mv "$tmp" .env
trap - EXIT
echo "wrote .env (local-only generated secrets; never commit it)"
