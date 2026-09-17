#!/usr/bin/env bash
set -euo pipefail

source_repo=${FROST_SOURCE_REPO:-marcus/frost}
tap_repo=${FROST_TAP_REPO:-marcus/homebrew-tap}
release_workflow=${FROST_RELEASE_WORKFLOW:-release.yml}
timeout_seconds=${FROST_RELEASE_TIMEOUT:-900}
poll_interval=${FROST_RELEASE_POLL_INTERVAL:-5}

die() {
  echo "Error: $*" >&2
  exit 1
}

validate_release_version() {
  [[ ${1:-} =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

compare_versions() {
  local left=${1#v}
  local right=${2#v}
  local left_major left_minor left_patch right_major right_minor right_patch

  IFS=. read -r left_major left_minor left_patch <<<"$left"
  IFS=. read -r right_major right_minor right_patch <<<"$right"
  if ((10#$left_major != 10#$right_major)); then
    ((10#$left_major > 10#$right_major)) && echo 1 || echo -1
  elif ((10#$left_minor != 10#$right_minor)); then
    ((10#$left_minor > 10#$right_minor)) && echo 1 || echo -1
  elif ((10#$left_patch != 10#$right_patch)); then
    ((10#$left_patch > 10#$right_patch)) && echo 1 || echo -1
  else
    echo 0
  fi
}

formula_version() {
  local formula=$1 line
  while IFS= read -r line; do
    if [[ $line =~ /archive/refs/tags/(v[0-9]+\.[0-9]+\.[0-9]+)\.tar\.gz ]]; then
      echo "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"$formula"
  return 1
}

formula_sha256() {
  local formula=$1 line
  while IFS= read -r line; do
    if [[ $line =~ ^[[:space:]]*sha256[[:space:]]+\"([0-9a-f]{64})\" ]]; then
      echo "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"$formula"
  return 1
}

# Return 10 when the exact formula is already public. Older versions may be
# upgraded; newer or same-version-different content always stops for review.
check_formula_transition() {
  local current_formula=$1 expected_formula=$2 release_version=$3
  local current_version comparison

  [[ -f $current_formula ]] || return 0
  current_version=$(formula_version "$current_formula") || {
    echo "tap formula has no recognizable release URL: $current_formula" >&2
    return 1
  }
  comparison=$(compare_versions "$current_version" "$release_version")
  if [[ $comparison == 1 ]]; then
    echo "tap formula $current_version is newer than requested $release_version" >&2
    return 1
  fi
  if [[ $comparison == 0 ]]; then
    if cmp -s "$current_formula" "$expected_formula"; then
      return 10
    fi
    echo "tap formula already names $release_version but differs from the exact rendered formula" >&2
    return 1
  fi
}

select_release_run() {
  local runs_json=$1 release_version=$2 tag_commit=$3
  jq -c --arg version "$release_version" --arg sha "$tag_commit" '
    [.[] | select(
      .event == "push" and
      .headBranch == $version and
      .headSha == $sha
    )] | sort_by(.databaseId) | last // empty
  ' <<<"$runs_json"
}

verify_release_run() {
  local run_json=$1 release_version=$2 tag_commit=$3
  jq -e --arg version "$release_version" --arg sha "$tag_commit" '
    .event == "push" and
    .headBranch == $version and
    .headSha == $sha and
    .status == "completed" and
    .conclusion == "success"
  ' <<<"$run_json" >/dev/null
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

tap_repo_url() {
  if [[ -n ${FROST_TAP_REPO_URL:-} ]]; then
    echo "$FROST_TAP_REPO_URL"
  elif [[ $(gh config get git_protocol --host github.com 2>/dev/null || true) == ssh ]]; then
    echo "git@github.com:${tap_repo}.git"
  else
    echo "https://github.com/${tap_repo}.git"
  fi
}

remote_tag_commit() {
  local release_version=$1 refs tag_object tag_commit
  refs=$(git ls-remote --tags "https://github.com/${source_repo}.git" \
    "refs/tags/$release_version" "refs/tags/$release_version^{}")
  tag_object=$(awk -v ref="refs/tags/$release_version" '$2 == ref {print $1}' <<<"$refs")
  tag_commit=$(awk -v ref="refs/tags/$release_version^{}" '$2 == ref {print $1}' <<<"$refs")
  [[ $tag_object =~ ^[0-9a-f]{40}$ ]] || die "public tag $release_version does not exist"
  [[ $tag_commit =~ ^[0-9a-f]{40}$ ]] || die "public tag $release_version is not annotated"
  echo "$tag_commit"
}

check_prerequisites() {
  local command_name
  for command_name in brew curl gh git jq ruby tar; do
    command -v "$command_name" >/dev/null 2>&1 || die "required command is not installed: $command_name"
  done
  if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
    die "required checksum command is not installed: shasum or sha256sum"
  fi
  [[ $timeout_seconds =~ ^[1-9][0-9]*$ ]] || die "FROST_RELEASE_TIMEOUT must be a positive integer"
  [[ $poll_interval =~ ^[1-9][0-9]*$ ]] || die "FROST_RELEASE_POLL_INTERVAL must be a positive integer"

  gh auth status --hostname github.com >/dev/null
  [[ $(gh api "repos/$source_repo" --jq .full_name) == "$source_repo" ]] ||
    die "GitHub authentication cannot read $source_repo"
  [[ $(gh api "repos/$tap_repo" --jq .permissions.push) == true ]] ||
    die "GitHub authentication does not have push access to $tap_repo"
  git ls-remote "$(tap_repo_url)" refs/heads/main >/dev/null ||
    die "Git authentication cannot read $tap_repo main"
  git config --get user.name >/dev/null || die "git user.name is required for the tap commit"
  git config --get user.email >/dev/null || die "git user.email is required for the tap commit"
}

wait_for_release_run() {
  local release_version=$1 tag_commit=$2
  local deadline=$((SECONDS + timeout_seconds)) runs selected
  while ((SECONDS < deadline)); do
    runs=$(gh run list --repo "$source_repo" --workflow "$release_workflow" \
      --event push --branch "$release_version" --limit 20 \
      --json databaseId,headBranch,headSha,status,conclusion,url,workflowName,event)
    selected=$(select_release_run "$runs" "$release_version" "$tag_commit")
    if [[ -n $selected ]]; then
      echo "$selected"
      return 0
    fi
    sleep "$poll_interval"
  done
  return 1
}

wait_for_public_release() {
  local release_version=$1 deadline=$((SECONDS + timeout_seconds)) release_json
  while ((SECONDS < deadline)); do
    if release_json=$(gh release view "$release_version" --repo "$source_repo" \
      --json tagName,isDraft,isPrerelease,url,publishedAt 2>/dev/null) &&
      jq -e --arg version "$release_version" \
        '.tagName == $version and .isDraft == false and .isPrerelease == false' \
        <<<"$release_json" >/dev/null; then
      echo "$release_json"
      return 0
    fi
    sleep "$poll_interval"
  done
  return 1
}

validate_formula() {
  local formula=$1 release_version=$2 expected_sha=$3
  local validation_tap="frost-release/validation-$$-$RANDOM"

  ruby -c "$formula" >/dev/null
  [[ $(formula_version "$formula") == "$release_version" ]] || die "formula version mismatch"
  [[ $(formula_sha256 "$formula") == "$expected_sha" ]] || die "formula checksum mismatch"

  (
    trap 'brew untap --force "$validation_tap" >/dev/null 2>&1 || true' EXIT
    brew tap-new --no-git "$validation_tap" >/dev/null
    validation_repo=$(brew --repository "$validation_tap")
    mkdir -p "$validation_repo/Formula"
    cp "$formula" "$validation_repo/Formula/frost.rb"
    brew style --formula "$validation_tap/frost"
    brew audit --strict --online --formula "$validation_tap/frost"
  )
}

push_tap_commit() {
  local tap_dir=$1 expected_formula=$2 attempt
  for attempt in 1 2 3; do
    if git -C "$tap_dir" push origin HEAD:main; then
      return 0
    fi
    if [[ $attempt == 3 ]]; then
      break
    fi
    echo "tap push raced or failed; rebasing onto origin/main (attempt $((attempt + 1))/3)" >&2
    git -C "$tap_dir" fetch origin main
    if ! git -C "$tap_dir" rebase origin/main; then
      git -C "$tap_dir" rebase --abort >/dev/null 2>&1 || true
      die "tap update conflicts with origin/main; no force-push was attempted"
    fi
    cmp -s "$tap_dir/Formula/frost.rb" "$expected_formula" ||
      die "tap formula changed during rebase; refusing to overwrite it"
  done
  die "could not push the tap update after 3 race-safe attempts"
}

publish_tap() (
  local release_version=$1 tag_commit run_json run_id verified_run release_json
  local temporary assets source_archive source_sha source_tree expected_formula tap_dir tap_formula
  local transition_status remote_commit remote_formula remote_version remote_sha

  tag_commit=$(remote_tag_commit "$release_version")
  echo "waiting for exact $release_workflow run for $release_version ($tag_commit)"
  run_json=$(wait_for_release_run "$release_version" "$tag_commit") ||
    die "timed out waiting for the exact release workflow"
  run_id=$(jq -r .databaseId <<<"$run_json")
  gh run watch "$run_id" --repo "$source_repo" --exit-status
  verified_run=$(gh run view "$run_id" --repo "$source_repo" \
    --json databaseId,headBranch,headSha,status,conclusion,url,workflowName,event)
  verify_release_run "$verified_run" "$release_version" "$tag_commit" ||
    die "workflow $run_id is not a successful exact-tag release run"
  release_json=$(wait_for_public_release "$release_version") ||
    die "timed out waiting for public release $release_version"
  echo "verified $(jq -r .url <<<"$release_json") via $(jq -r .url <<<"$verified_run")"

  temporary=$(mktemp -d)
  trap 'rm -rf "$temporary"' EXIT
  source_archive="$temporary/source.tar.gz"
  curl --fail --location --retry 3 --output "$source_archive" \
    "https://github.com/${source_repo}/archive/refs/tags/${release_version}.tar.gz"
  source_sha=$(sha256_file "$source_archive")
  tar -xzf "$source_archive" -C "$temporary"
  source_tree="$temporary/${source_repo##*/}-${release_version#v}"
  [[ -x $source_tree/scripts/release-render-formula.sh ]] ||
    die "tagged source does not contain the executable formula renderer"
  [[ -x $source_tree/scripts/release-verify-assets.sh ]] ||
    die "tagged source does not contain the executable asset verifier"

  assets="$temporary/assets"
  mkdir "$assets"
  gh release download "$release_version" --repo "$source_repo" --dir "$assets" \
    --pattern '*.tar.gz' --pattern checksums.txt
  "$source_tree/scripts/release-verify-assets.sh" "$assets" "$release_version"

  expected_formula="$temporary/frost.rb"
  "$source_tree/scripts/release-render-formula.sh" "$release_version" "$source_sha" "$expected_formula"
  validate_formula "$expected_formula" "$release_version" "$source_sha"

  tap_dir="$temporary/homebrew-tap"
  git clone --branch main --single-branch "$(tap_repo_url)" "$tap_dir"
  tap_formula="$tap_dir/Formula/frost.rb"
  mkdir -p "$(dirname "$tap_formula")"
  set +e
  check_formula_transition "$tap_formula" "$expected_formula" "$release_version"
  transition_status=$?
  set -e
  if [[ $transition_status == 10 ]]; then
    echo "$tap_repo already contains the exact $release_version formula"
  elif [[ $transition_status != 0 ]]; then
    die "unsafe tap formula transition"
  else
    cp "$expected_formula" "$tap_formula"
    git -C "$tap_dir" add Formula/frost.rb
    git -C "$tap_dir" diff --cached --check
    git -C "$tap_dir" commit -m "frost $release_version"
    push_tap_commit "$tap_dir" "$expected_formula"
  fi

  git -C "$tap_dir" fetch origin main
  remote_commit=$(git -C "$tap_dir" rev-parse FETCH_HEAD)
  remote_formula="$temporary/remote-frost.rb"
  git -C "$tap_dir" show "$remote_commit:Formula/frost.rb" >"$remote_formula"
  cmp -s "$remote_formula" "$expected_formula" || die "remote formula differs from the exact candidate"
  remote_version=$(formula_version "$remote_formula")
  remote_sha=$(formula_sha256 "$remote_formula")
  [[ $remote_version == "$release_version" && $remote_sha == "$source_sha" ]] ||
    die "remote tap formula version or checksum is stale"
  echo "published and verified $tap_repo@$remote_commit: frost $remote_version ($remote_sha)"
)

main() {
  local mode=${1:-publish} release_version=${RELEASE_VERSION:-}
  if [[ $mode != publish && $mode != --check ]]; then
    echo "usage: RELEASE_VERSION=vX.Y.Z $0 [--check]" >&2
    exit 2
  fi
  validate_release_version "$release_version" || die "RELEASE_VERSION must be strict SemVer vX.Y.Z"
  check_prerequisites
  if [[ $mode == --check ]]; then
    echo "release prerequisites verified for $source_repo -> $tap_repo"
    return
  fi
  publish_tap "$release_version"
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  main "$@"
fi
