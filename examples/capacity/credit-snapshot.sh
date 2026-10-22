#!/usr/bin/env bash
# Write a neutral Frost capacity snapshot for a prepaid credit that refreshes
# each billing cycle with no rollover (for example a monthly API credit).
#
# Frost never runs this. It converts a grant and the spend so far into one
# window of remaining percent; where the spend comes from (a billing console,
# a cost report, the gateway's spend logs) is the operator's choice.
#
# Usage: credit-snapshot.sh --granted USD --spent USD --resets-at RFC3339 \
#          [--pool anthropic-credit] [--window monthly] [--cycle-days 30] \
#          [--valid-minutes 60] [--out FILE]
set -euo pipefail

pool="anthropic-credit"
window="monthly"
cycle_days=30
valid_minutes=60
granted=""
spent=""
resets_at=""
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    --pool) pool="$2"; shift 2 ;;
    --window) window="$2"; shift 2 ;;
    --granted) granted="$2"; shift 2 ;;
    --spent) spent="$2"; shift 2 ;;
    --resets-at) resets_at="$2"; shift 2 ;;
    --cycle-days) cycle_days="$2"; shift 2 ;;
    --valid-minutes) valid_minutes="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
    *) echo "credit-snapshot.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done
for v in granted spent resets_at; do
  [ -n "${!v}" ] || { echo "credit-snapshot.sh: --${v//_/-} is required" >&2; exit 2; }
done
command -v jq >/dev/null || { echo "credit-snapshot.sh: jq not on PATH" >&2; exit 3; }

snapshot="$(jq -n \
  --arg pool "$pool" --arg window "$window" --arg resets "$resets_at" \
  --argjson granted "$granted" --argjson spent "$spent" \
  --argjson days "$cycle_days" --argjson valid "$valid_minutes" '
  if $granted <= 0 then error("granted must be positive") else . end |
  (now | floor) as $t |
  {
    schema_version: 1,
    generated_at: ($t | todate),
    producer: "credit-snapshot",
    pools: [{
      id: $pool,
      source_status: "ok",
      measurement: "exact",
      observed_at: ($t | todate),
      valid_until: ($t + $valid * 60 | todate),
      windows: [{
        id: $window,
        remaining_percent: ((($granted - $spent) / $granted * 100) | if . < 0 then 0 elif . > 100 then 100 else . end),
        resets_at: $resets,
        duration_seconds: ($days * 86400)
      }]
    }]
  }')"

if [ -z "$out" ]; then
  printf '%s\n' "$snapshot"
  exit 0
fi
tmp="$(mktemp "$out.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
printf '%s\n' "$snapshot" > "$tmp"
mv "$tmp" "$out"
trap - EXIT
