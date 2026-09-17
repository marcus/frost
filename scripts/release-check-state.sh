#!/usr/bin/env bash
set -euo pipefail

mode=${1:-}
case "$mode" in
  pre-tag | tagged) ;;
  *)
    echo "usage: RELEASE_VERSION=vX.Y.Z $0 pre-tag|tagged" >&2
    exit 2
    ;;
esac

release_version=${RELEASE_VERSION:-}
if [[ ! $release_version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Error: RELEASE_VERSION must be strict SemVer vX.Y.Z" >&2
  exit 1
fi

if [[ -n $(git status --porcelain) ]]; then
  echo "Error: working tree is not clean" >&2
  exit 1
fi
if ! git remote get-url origin >/dev/null 2>&1; then
  echo "Error: origin remote is not configured" >&2
  exit 1
fi

remote_head=$(git ls-remote origin refs/heads/main | awk '{print $1}')
if [[ -z $remote_head ]]; then
  echo "Error: origin/main does not exist" >&2
  exit 1
fi

plain_version=${release_version#v}
escaped_version=${plain_version//./\\.}
if ! grep -Eq "^## \\[$escaped_version\\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md; then
  echo "Error: CHANGELOG.md has no dated $plain_version release entry" >&2
  exit 1
fi

case "$mode" in
  pre-tag)
    if [[ $(git branch --show-current) != main ]]; then
      echo "Error: releases must be cut from main" >&2
      exit 1
    fi
    if [[ $(git rev-parse HEAD) != "$remote_head" ]]; then
      echo "Error: HEAD does not match live origin/main" >&2
      exit 1
    fi
    if git rev-parse --verify --quiet "refs/tags/$release_version" >/dev/null; then
      echo "Error: local tag $release_version already exists" >&2
      exit 1
    fi
    if [[ -n $(git ls-remote --tags origin \
      "refs/tags/$release_version" "refs/tags/$release_version^{}") ]]; then
      echo "Error: remote tag $release_version already exists" >&2
      exit 1
    fi
    ;;
  tagged)
    if ! git fetch --force --no-tags origin \
      "refs/tags/$release_version:refs/tags/$release_version"; then
      echo "Error: could not fetch remote tag $release_version" >&2
      exit 1
    fi
    if [[ $(git cat-file -t "refs/tags/$release_version" 2>/dev/null || true) != tag ]]; then
      echo "Error: $release_version must be an annotated tag" >&2
      exit 1
    fi
    tag_commit=$(git rev-parse "refs/tags/$release_version^{commit}")
    if [[ $(git rev-parse HEAD) != "$tag_commit" ]]; then
      echo "Error: checked-out HEAD does not match $release_version" >&2
      exit 1
    fi
    remote_tag_commit=$(git ls-remote origin \
      "refs/tags/$release_version^{}" | awk '{print $1}')
    if [[ $remote_tag_commit != "$tag_commit" ]]; then
      echo "Error: remote $release_version does not resolve to the checked-out commit" >&2
      exit 1
    fi
    if ! git cat-file -e "$remote_head^{commit}" 2>/dev/null; then
      git fetch --no-tags --quiet origin refs/heads/main
    fi
    if ! git merge-base --is-ancestor "$tag_commit" "$remote_head"; then
      echo "Error: $release_version is not an ancestor of live origin/main" >&2
      exit 1
    fi
    ;;
esac

echo "release state verified for $release_version ($mode)"
