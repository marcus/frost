#!/usr/bin/env bash
set -euo pipefail

release_version=${1:-${RELEASE_VERSION:-}}
if [[ ! $release_version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

plain_version=${release_version#v}
heading="## [$plain_version] - "
changelog=${CHANGELOG_FILE:-CHANGELOG.md}

notes=$(awk -v heading="$heading" '
  index($0, heading) == 1 { found = 1; next }
  found && /^## / { exit }
  found && !content && /^[[:space:]]*$/ { next }
  found {
    print
    if ($0 !~ /^[[:space:]]*$/) content = 1
  }
  END { if (!found || !content) exit 1 }
' "$changelog") || {
  echo "$changelog has no non-empty release notes for $plain_version" >&2
  exit 1
}

printf '%s\n' "$notes"
