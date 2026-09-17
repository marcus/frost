#!/usr/bin/env bash
set -euo pipefail

# One-command release: derive the version, stamp the changelog, push main,
# then run the fail-closed publisher (checks, tag, GitHub release, tap).
#
# The version is stated ONCE, in order of precedence:
#   RELEASE_VERSION=vX.Y.Z   explicit override
#   CHANGELOG.md             top heading already stamped `## [X.Y.Z] - date`
#   BUMP=major|minor|patch   top heading is `## [Unreleased]`; bump latest tag
#
# `--dry-run` prints the plan and exits before any mutation.

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

die() {
  echo "Error: $*" >&2
  exit 1
}

valid_release_version() {
  [[ ${1:-} =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

changelog_top_heading() {
  grep -m1 '^## \[' CHANGELOG.md
}

changelog_top_section_has_notes() {
  local section_body
  section_body=$(awk '/^## \[/{n++; next} n==1' CHANGELOG.md)
  [[ -n ${section_body//[[:space:]]/} ]]
}

# Prints "vX.Y.Z<TAB>stamp|ready" on success.
derive_release_version() {
  local top_heading release_version needs_stamp=false bump latest major minor patch

  top_heading=$(changelog_top_heading) || {
    echo "Error: CHANGELOG.md has no '## [' heading" >&2
    return 1
  }
  changelog_top_section_has_notes || {
    echo "Error: the top CHANGELOG.md section is empty — write the changelog first" >&2
    return 1
  }

  release_version=${RELEASE_VERSION:-}
  if [[ -n $release_version ]]; then
    valid_release_version "$release_version" || {
      echo "Error: RELEASE_VERSION must be strict SemVer vX.Y.Z" >&2
      return 1
    }
    if [[ $top_heading == '## [Unreleased]' ]]; then
      needs_stamp=true
    elif [[ $top_heading != "## [${release_version#v}] - "* ]]; then
      echo "Error: RELEASE_VERSION=$release_version but the top CHANGELOG.md heading is '$top_heading'" >&2
      return 1
    fi
  elif [[ $top_heading == '## [Unreleased]' ]]; then
    bump=${BUMP:-}
    case "$bump" in
      major | minor | patch) ;;
      *)
        echo "Error: top CHANGELOG.md heading is [Unreleased]: set BUMP=major|minor|patch (or RELEASE_VERSION=vX.Y.Z)" >&2
        return 1
        ;;
    esac
    latest=$(git tag --list 'v*' --sort=-v:refname)
    latest=${latest%%$'\n'*}
    valid_release_version "$latest" || {
      echo "Error: no SemVer tag found to bump from" >&2
      return 1
    }
    IFS=. read -r major minor patch <<<"${latest#v}"
    case "$bump" in
      major) release_version="v$((major + 1)).0.0" ;;
      minor) release_version="v$major.$((minor + 1)).0" ;;
      patch) release_version="v$major.$minor.$((patch + 1))" ;;
    esac
    needs_stamp=true
  elif [[ $top_heading =~ ^'## ['([0-9]+\.[0-9]+\.[0-9]+)']' ]]; then
    release_version="v${BASH_REMATCH[1]}"
    valid_release_version "$release_version" || {
      echo "Error: cannot parse a version from '$top_heading'" >&2
      return 1
    }
  else
    echo "Error: cannot derive a version from the top CHANGELOG.md heading '$top_heading'" >&2
    return 1
  fi

  if $needs_stamp; then
    printf '%s\tstamp\n' "$release_version"
  else
    printf '%s\tready\n' "$release_version"
  fi
}

stamp_changelog() {
  local release_version=$1 today
  today=$(date +%Y-%m-%d)
  perl -0pi -e "s/^## \[Unreleased\]/## [${release_version#v}] - $today/m" CHANGELOG.md
  grep -Fq "## [${release_version#v}] - $today" CHANGELOG.md ||
    die "failed to stamp CHANGELOG.md"
}

push_release_tag() {
  local release_version=$1 expected_main=$2

  if [[ $(git rev-parse HEAD) != "$expected_main" ]]; then
    echo "Error: local HEAD changed before tag creation" >&2
    return 1
  fi
  git tag -a "$release_version" "$expected_main" -m "Release $release_version"
  if git push --atomic \
    --force-with-lease="refs/heads/main:$expected_main" \
    origin \
    "$expected_main:refs/heads/main" \
    "refs/tags/$release_version"; then
    return 0
  fi
  git tag -d "$release_version" >/dev/null
  echo "Error: main or the release tag changed before the atomic push; no release tag was published" >&2
  return 1
}

main() {
  local dry_run=false release_version needs_stamp=false derived expected_main dirty remote_head
  [[ ${1:-} == --dry-run ]] && dry_run=true
  if [[ $# -gt 1 || (${1:-} != "" && ${1:-} != --dry-run) ]]; then
    echo "usage: [BUMP=major|minor|patch | RELEASE_VERSION=vX.Y.Z] $0 [--dry-run]" >&2
    exit 2
  fi

  derived=$(derive_release_version) || exit 1
  release_version=${derived%%$'\t'*}
  [[ ${derived#*$'\t'} == stamp ]] && needs_stamp=true

  git rev-parse --verify --quiet "refs/tags/$release_version" >/dev/null &&
    die "tag $release_version already exists — nothing to release"

  dirty=$(git status --porcelain)
  if [[ -n $dirty && $dirty != "M  CHANGELOG.md" && $dirty != " M CHANGELOG.md" ]]; then
    die "working tree has changes beyond CHANGELOG.md — commit or stash them first"
  fi

  echo "release plan: $release_version"
  $needs_stamp && echo "  - stamp CHANGELOG.md [Unreleased] -> [${release_version#v}] - $(date +%Y-%m-%d)"
  [[ -n $dirty || $needs_stamp == true ]] && echo "  - commit 'release: prepare $release_version'"
  echo "  - push origin main"
  echo "  - verify clean live main, changelog, credentials, and tap access"
  echo "  - run local checks plus four-platform GoReleaser snapshot proof"
  echo "  - require successful CI for the exact release commit"
  echo "  - create and push one annotated tag"
  echo "  - verify the exact tag workflow and public release assets"
  echo "  - publish and verify Formula/frost.rb without force-pushing"

  RELEASE_VERSION=$release_version ./scripts/release-tap.sh --check

  if $dry_run; then
    echo "dry run: stopping before any mutation"
    exit 0
  fi

  if $needs_stamp; then
    stamp_changelog "$release_version"
  fi
  if [[ -n $(git status --porcelain) ]]; then
    git add CHANGELOG.md
    git commit -m "release: prepare $release_version"
  fi

  remote_head=$(git ls-remote origin refs/heads/main | awk '{print $1}')
  if [[ $(git rev-parse HEAD) != "$remote_head" ]]; then
    git push origin main
  fi

  # Do not export RELEASE_VERSION into `make`; pass it per command instead.
  RELEASE_VERSION=$release_version ./scripts/release-check-state.sh pre-tag

  make check
  git diff --check
  make release-verify
  ./scripts/release-wait-ci.sh

  # Close the race between the checks above and the only source-repository
  # mutation. A changed main or newly created tag must stop here.
  RELEASE_VERSION=$release_version ./scripts/release-check-state.sh pre-tag
  expected_main=$(git rev-parse HEAD)
  push_release_tag "$release_version" "$expected_main"

  RELEASE_VERSION=$release_version exec ./scripts/release-tap.sh
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  main "$@"
fi
