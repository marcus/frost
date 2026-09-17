#!/usr/bin/env bash
# Produce a neutral Frost capacity snapshot from CodexBar.
#
# Frost never runs this; the operator (or a LaunchAgent) does. It reads
# CodexBar usage per provider named in the bindings file, converts it with
# codexbar-to-frost.jq, and atomically replaces the output file. Account
# emails, credit inventory, tokens, and raw error bodies never reach the
# snapshot or this script's output.
#
# Usage: refresh.sh --bindings FILE --out FILE [--providers a,b] [--dry-run]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bindings=""
out=""
providers=""
dry_run=0
while [ $# -gt 0 ]; do
  case "$1" in
    --bindings) bindings="$2"; shift 2 ;;
    --out) out="$2"; shift 2 ;;
    --providers) providers="$2"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    -h|--help) sed -n '2,11p' "$0"; exit 0 ;;
    *) echo "refresh.sh: unknown argument $1" >&2; exit 2 ;;
  esac
done
[ -n "$bindings" ] || { echo "refresh.sh: --bindings is required" >&2; exit 2; }
[ -f "$bindings" ] || { echo "refresh.sh: bindings $bindings not found" >&2; exit 2; }
if [ "$dry_run" -eq 0 ] && [ -z "$out" ]; then
  echo "refresh.sh: --out is required unless --dry-run" >&2; exit 2
fi
command -v codexbar >/dev/null || { echo "refresh.sh: codexbar not on PATH" >&2; exit 3; }
command -v jq >/dev/null || { echo "refresh.sh: jq not on PATH" >&2; exit 3; }

if [ -z "$providers" ]; then
  providers="$(jq -r '[.[].provider] | unique | join(",")' "$bindings")"
fi

rows="$(mktemp)"
trap 'rm -f "$rows" "${out:-/nonexistent}.tmp"' EXIT
echo '[]' > "$rows"
ok_count=0
IFS=',' read -r -a list <<< "$providers"
for provider in "${list[@]}"; do
  [ -n "$provider" ] || continue
  # Per-provider failure is preserved as an error row; the rest still publish.
  if payload="$(codexbar usage --json --provider "$provider" 2>/dev/null)" && [ -n "$payload" ]; then
    jq -s '.[0] + .[1]' "$rows" <(printf '%s' "$payload") > "$rows.next" && mv "$rows.next" "$rows"
    ok_count=$((ok_count + 1))
  else
    jq --arg p "$provider" '. + [{provider: $p, error: "collection failed"}]' "$rows" > "$rows.next" && mv "$rows.next" "$rows"
    echo "refresh.sh: provider $provider: collection failed" >&2
  fi
done
if [ "$ok_count" -eq 0 ]; then
  echo "refresh.sh: every provider failed; previous snapshot left in place" >&2
  exit 4
fi

snapshot="$(jq --slurpfile bindings "$bindings" -f "$here/codexbar-to-frost.jq" "$rows")"
if [ "$dry_run" -eq 1 ]; then
  printf '%s\n' "$snapshot"
  exit 0
fi
mkdir -p "$(dirname "$out")"
printf '%s\n' "$snapshot" > "$out.tmp"
mv -f "$out.tmp" "$out"
echo "refresh.sh: wrote $out ($ok_count providers)"
if command -v frost >/dev/null; then
  if frost capacity check "$out" >/dev/null 2>&1; then
    echo "refresh.sh: frost capacity check passed"
  else
    echo "refresh.sh: frost capacity check failed (exit $?); run it by hand to see why" >&2
  fi
fi
