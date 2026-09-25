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
# openssl (Git Bash ships one); /dev/urandom where it is missing.
rand_hex() { openssl rand -hex 24 2>/dev/null || od -An -tx1 -N24 /dev/urandom | tr -d ' \n'; }
cr=$(printf '\r')
tmp=$(mktemp .env.XXXXXX)
trap 'rm -f "$tmp"' EXIT
while IFS= read -r line || [ -n "$line" ]; do
	line=${line%"$cr"} # a CRLF checkout (core.autocrlf=true) must still match below
	case "$line" in
	*_PASSWORD= | *_SECRET= | *_API_KEY=)
		printf '%s%s\n' "$line" "$(rand_hex)" ;;
	*) printf '%s\n' "$line" ;;
	esac
done < .env.example >"$tmp"
mv "$tmp" .env
trap - EXIT
echo "wrote .env (local-only generated secrets; never commit it)"
