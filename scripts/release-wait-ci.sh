#!/usr/bin/env bash
set -euo pipefail

source_repo=${FROST_SOURCE_REPO:-marcus/frost}
workflow=${FROST_CI_WORKFLOW:-go-ci.yml}
timeout_seconds=${FROST_RELEASE_TIMEOUT:-900}
poll_interval=${FROST_RELEASE_POLL_INTERVAL:-5}

die() {
  echo "Error: $*" >&2
  exit 1
}

[[ $timeout_seconds =~ ^[1-9][0-9]*$ ]] || die "FROST_RELEASE_TIMEOUT must be a positive integer"
[[ $poll_interval =~ ^[1-9][0-9]*$ ]] || die "FROST_RELEASE_POLL_INTERVAL must be a positive integer"

head_sha=$(git rev-parse HEAD)
deadline=$((SECONDS + timeout_seconds))
dispatched=false

while ((SECONDS < deadline)); do
  runs=$(gh run list \
    --repo "$source_repo" \
    --workflow "$workflow" \
    --branch main \
    --limit 30 \
    --json databaseId,headBranch,headSha,status,conclusion,url,workflowName,event)
  selected=$(jq -c --arg sha "$head_sha" '
    [.[] | select(
      .headBranch == "main" and
      .headSha == $sha and
      (.event == "push" or .event == "workflow_dispatch")
    )] | sort_by(.databaseId) | last // empty
  ' <<<"$runs")

  if [[ -z $selected ]]; then
    if ! $dispatched; then
      echo "no CI run found for $head_sha; dispatching $workflow on main"
      gh workflow run "$workflow" --repo "$source_repo" --ref main
      dispatched=true
    fi
    sleep "$poll_interval"
    continue
  fi

  run_id=$(jq -r .databaseId <<<"$selected")
  status=$(jq -r .status <<<"$selected")
  if [[ $status != completed ]]; then
    echo "waiting for exact-head CI run $run_id ($head_sha)"
    gh run watch "$run_id" --repo "$source_repo" --exit-status
  fi

  verified=$(gh run view "$run_id" --repo "$source_repo" \
    --json databaseId,headBranch,headSha,status,conclusion,url,workflowName,event)
  jq -e --arg sha "$head_sha" '
    .headBranch == "main" and
    .headSha == $sha and
    (.event == "push" or .event == "workflow_dispatch") and
    .status == "completed" and
    .conclusion == "success"
  ' <<<"$verified" >/dev/null || die "CI run $run_id is not a successful exact-head run"

  echo "verified exact-head CI: $(jq -r .url <<<"$verified")"
  exit 0
done

die "timed out waiting for $workflow at $head_sha"
