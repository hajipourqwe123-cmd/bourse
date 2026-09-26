#!/bin/sh
# D-03 day-reset check, BOTH vendors on the same morning: records raw all-symbols polls of BrsApi
# and SourceArena around the pre-open (default Saturday 08:20–09:10 Tehran, every 60 s) to
# recordings/preopen-<Tehran date>.ndjson, one line per request:
#   {"ingest": UTC time, "vendor", "type", "http", "body": <vendor JSON or null>}
# Question it answers: does each vendor still show yesterday's day totals after 08:25, and when
# do they reset? (docs/source-mapping.md; cmd/vendorcmp compares the vendors on one poll).
# Usage, from the repo root, before 08:20 Tehran: infra/preopen-record.sh   (waits for the window)
#   PREOPEN_FROM=08:20 PREOPEN_TO=09:10 PREOPEN_INTERVAL=60 PREOPEN_VENDORS="brsapi sourcearena"
#   BRSAPI_TYPES=1 BRSAPI_DAILY_LIMIT=100 SOURCEARENA_DAILY_LIMIT=100 (each vendor's plan quota)
#   PREOPEN_ANYDAY=1 to run on a day other than Saturday.
# Secrets (BRSAPI_KEY, SOURCEARENA_TOKEN) come from the environment or .env and are handed to curl
# on stdin and to awk via its environment: never in argv or output; the recording is scrubbed of
# them (raw and URL-encoded). Each vendor's requests are counted against its daily quota before
# starting; the collector shares the same quotas.
set -eu
cd "$(dirname "$0")/.."
from=${PREOPEN_FROM:-08:20} to=${PREOPEN_TO:-09:10} every=${PREOPEN_INTERVAL:-60}
vendors=${PREOPEN_VENDORS:-brsapi sourcearena}
UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36'

envval() { sed -n "s/^$1=//p" .env 2>/dev/null | tr -d '\r' | tail -n 1; }
# urlenc: percent-encode stdin (a '#', '&' or '+' in a secret would otherwise cut or alter the URL).
urlenc() {
	LC_ALL=C awk 'BEGIN { for (n = 1; n < 256; n++) ord[sprintf("%c", n)] = n }
	{ for (i = 1; i <= length($0); i++) { c = substr($0, i, 1)
		if (c ~ /[A-Za-z0-9._~-]/) printf "%s", c; else printf "%%%02X", ord[c] } }'
}
bkey=${BRSAPI_KEY:-$(envval BRSAPI_KEY)}
stok=${SOURCEARENA_TOKEN:-$(envval SOURCEARENA_TOKEN)}
bq=$(printf '%s\n' "$bkey" | urlenc) sq=$(printf '%s\n' "$stok" | urlenc)
btypes=$(printf '%s' "${BRSAPI_TYPES:-1}" | tr ',' ' ')

# Tehran = UTC+03:30 (no DST since 2022; internal/tehran uses the tz database).
tehran_fmt() { date -u -d "@$(($(date -u +%s) + 12600))" "+$1"; }
mins() { h=${1%:*} m=${1#*:}; echo $((${h#0} * 60 + ${m#0})); }
start=$(mins "$from") end=$(mins "$to")
[ "$end" -gt "$start" ] && [ "$every" -gt 0 ] || { echo "bad window $from-$to / interval $every" >&2; exit 1; }
polls=$((((end - start) * 60) / every + 1))
for v in $vendors; do
	case $v in
	brsapi)
		[ -n "$bkey" ] || { echo "BRSAPI_KEY is not set (environment or .env)" >&2; exit 1; }
		need=$((polls * $(echo $btypes | wc -w))) limit=${BRSAPI_DAILY_LIMIT:-100} ;;
	sourcearena)
		[ -n "$stok" ] || { echo "SOURCEARENA_TOKEN is not set (environment or .env)" >&2; exit 1; }
		need=$polls limit=${SOURCEARENA_DAILY_LIMIT:-100} ;;
	*) echo "unknown vendor $v (brsapi, sourcearena)" >&2; exit 1 ;;
	esac
	[ "$need" -le "$limit" ] || { echo "$v: window needs $need requests, over its daily limit $limit: raise PREOPEN_INTERVAL" >&2; exit 1; }
	echo "$v: $need requests (daily limit $limit)"
done
if [ "$(tehran_fmt %u)" != 6 ] && [ "${PREOPEN_ANYDAY:-}" != 1 ]; then
	echo "today is not Saturday in Tehran ($(tehran_fmt '%F %a')); set PREOPEN_ANYDAY=1 to record anyway" >&2
	exit 1
fi

mkdir -p recordings
out="recordings/preopen-$(tehran_fmt %F).ndjson"
echo "recording $from-$to Tehran every ${every}s, vendors: $vendors -> $out"
now_min() { mins "$(tehran_fmt %H:%M)"; }
while [ "$(now_min)" -lt "$start" ]; do sleep 20; done

# scrub replaces every secret form literally; the secrets reach awk through its environment.
scrub() {
	S1="$bkey" S2="$bq" S3="$stok" S4="$sq" awk 'BEGIN { for (j = 1; j <= 4; j++) k[j] = ENVIRON["S" j] }
	{ for (j = 1; j <= 4; j++) if (k[j] != "") while ((i = index($0, k[j])) > 0) $0 = substr($0, 1, i - 1) "***" substr($0, i + length(k[j]))
	  printf "%s", $0 }' "$1"
}
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
# record VENDOR TYPE URL: one request; the URL (with its secret) goes to curl on stdin.
record() {
	ingest=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	: >"$tmp"
	# A failed or cut-off transfer (timeout) records http 0 and no body: never half a JSON value.
	code=$(printf 'url = "%s"\n' "$3" |
		curl -s -K - --compressed -A "$UA" -H 'Accept: application/json, text/plain, */*' --max-time 60 \
			-o "$tmp" -w '%{http_code}' 2>/dev/null) || { code=0; : >"$tmp"; }
	body=$(scrub "$tmp" | tr -d '\r\n')
	case "$body" in
	\[*\] | \{*\}) printf '{"ingest":"%s","vendor":"%s","type":"%s","http":%d,"body":%s}\n' "$ingest" "$1" "$2" "$code" "$body" >>"$out" ;;
	*) printf '{"ingest":"%s","vendor":"%s","type":"%s","http":%d,"body":null}\n' "$ingest" "$1" "$2" "$code" >>"$out" ;;
	esac
	echo "$(tehran_fmt %H:%M:%S) $1 type=$2 http=$code bytes=$(wc -c <"$tmp")"
}
while [ "$(now_min)" -lt "$end" ]; do
	t0=$(date -u +%s)
	for v in $vendors; do
		case $v in
		brsapi)
			for typ in $btypes; do
				record brsapi "$typ" "${BRSAPI_URL:-https://api.brsapi.ir/Tsetmc/AllSymbols.php}?type=$typ&key=$bq"
			done ;;
		sourcearena)
			record sourcearena 0 "${SOURCEARENA_URL:-https://apis.sourcearena.ir/api/}?token=$sq&all&type=0" ;;
		esac
	done
	left=$((every - ($(date -u +%s) - t0)))
	[ "$left" -gt 0 ] || left=1
	sleep "$left"
done
echo "done: $out"
