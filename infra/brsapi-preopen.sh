#!/bin/sh
# D-03 day-reset check: records raw BrsApi AllSymbols polls around the pre-open (default Saturday
# 08:20–09:10 Tehran, every 60 s) to recordings/brsapi-preopen-<Tehran date>.ndjson, one line per
# poll: {"ingest": UTC time, "type", "http", "body": <vendor JSON>}. Question it answers: does the
# vendor still show yesterday's day totals (tvol/tval/Buy_*/Sell_*) after 08:25, and when do they
# reset? (docs/source-mapping.md, BrsApi).
# Usage, from the repo root, before 08:20 Tehran: infra/brsapi-preopen.sh   (waits for the window)
#   PREOPEN_FROM=08:20 PREOPEN_TO=09:10 PREOPEN_INTERVAL=60 BRSAPI_TYPES=1 BRSAPI_DAILY_LIMIT=100
#   PREOPEN_ANYDAY=1 to run on a day other than Saturday.
# The key comes from BRSAPI_KEY or .env and is handed to curl on stdin and to awk via its environment:
# never in argv or output; the recording is scrubbed of it (raw and URL-encoded). Requests are
# counted against BRSAPI_DAILY_LIMIT before starting; the collector shares the same daily quota.
set -eu
cd "$(dirname "$0")/.."
from=${PREOPEN_FROM:-08:20} to=${PREOPEN_TO:-09:10} every=${PREOPEN_INTERVAL:-60}
types=$(printf '%s' "${BRSAPI_TYPES:-1}" | tr ',' ' ')
limit=${BRSAPI_DAILY_LIMIT:-100}
url=${BRSAPI_URL:-https://api.brsapi.ir/Tsetmc/AllSymbols.php}
key=${BRSAPI_KEY:-$(sed -n 's/^BRSAPI_KEY=//p' .env 2>/dev/null | tr -d '\r' | tail -n 1)}
[ -n "$key" ] || { echo "BRSAPI_KEY is not set (environment or .env)" >&2; exit 1; }
UA='Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36'

# Tehran = UTC+03:30 (no DST since 2022; internal/tehran uses the tz database).
tehran_epoch() { echo $(($(date -u +%s) + 12600)); }
tehran_fmt() { date -u -d "@$(tehran_epoch)" "+$1"; }
mins() { h=${1%:*} m=${1#*:}; echo $((${h#0} * 60 + ${m#0})); }
start=$(mins "$from") end=$(mins "$to")
[ "$end" -gt "$start" ] && [ "$every" -gt 0 ] || { echo "bad window $from-$to / interval $every" >&2; exit 1; }
ntypes=$(echo $types | wc -w)
need=$(((((end - start) * 60) / every + 1) * ntypes))
[ "$need" -le "$limit" ] || { echo "window needs $need requests, over BRSAPI_DAILY_LIMIT=$limit: raise PREOPEN_INTERVAL" >&2; exit 1; }
if [ "$(tehran_fmt %u)" != 6 ] && [ "${PREOPEN_ANYDAY:-}" != 1 ]; then
	echo "today is not Saturday in Tehran ($(tehran_fmt '%F %a')); set PREOPEN_ANYDAY=1 to record anyway" >&2
	exit 1
fi

mkdir -p recordings
out="recordings/brsapi-preopen-$(tehran_fmt %F).ndjson"
echo "recording $from-$to Tehran every ${every}s, types: $types ($need requests) -> $out"
now_min() { mins "$(tehran_fmt %H:%M)"; }
while [ "$(now_min)" -lt "$start" ]; do sleep 20; done

# The key percent-encoded for the query string (a '#', '&' or '+' would otherwise cut or alter it).
qkey=$(printf '%s\n' "$key" | LC_ALL=C awk 'BEGIN { for (n = 1; n < 256; n++) ord[sprintf("%c", n)] = n }
	{ for (i = 1; i <= length($0); i++) { c = substr($0, i, 1)
		if (c ~ /[A-Za-z0-9._~-]/) printf "%s", c; else printf "%%%02X", ord[c] } }')
# scrub replaces both key forms literally; the keys reach awk through its environment, never argv.
scrub() {
	BRS_K="$key" BRS_Q="$qkey" awk 'BEGIN { k[1] = ENVIRON["BRS_K"]; k[2] = ENVIRON["BRS_Q"] }
	{ for (j = 1; j <= 2; j++) while ((i = index($0, k[j])) > 0) $0 = substr($0, 1, i - 1) "***" substr($0, i + length(k[j]))
	  printf "%s", $0 }' "$1"
}
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
while [ "$(now_min)" -lt "$end" ]; do
	t0=$(date -u +%s)
	for typ in $types; do
		ingest=$(date -u +%Y-%m-%dT%H:%M:%SZ)
		: >"$tmp"
		# A failed or cut-off transfer (timeout) records http 0 and no body: never half a JSON value.
		code=$(printf 'url = "%s?type=%s&key=%s"\n' "$url" "$typ" "$qkey" |
			curl -s -K - --compressed -A "$UA" -H 'Accept: application/json, text/plain, */*' --max-time 60 \
				-o "$tmp" -w '%{http_code}' 2>/dev/null) || { code=0; : >"$tmp"; }
		body=$(scrub "$tmp" | tr -d '\r\n')
		case "$body" in
		\[*\] | \{*\}) printf '{"ingest":"%s","type":"%s","http":%d,"body":%s}\n' "$ingest" "$typ" "$code" "$body" >>"$out" ;;
		*) printf '{"ingest":"%s","type":"%s","http":%d,"body":null}\n' "$ingest" "$typ" "$code" >>"$out" ;;
		esac
		echo "$(tehran_fmt %H:%M:%S) type=$typ http=$code bytes=$(wc -c <"$tmp")"
	done
	left=$((every - ($(date -u +%s) - t0)))
	[ "$left" -gt 0 ] || left=1
	sleep "$left"
done
echo "done: $out"
