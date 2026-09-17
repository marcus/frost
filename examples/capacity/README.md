# External capacity snapshots

Frost reads one neutral usage snapshot and never runs a producer. This directory is an example producer for [CodexBar](https://github.com/steipete/CodexBar) 0.60.2 that writes the snapshot Frost's `capacity_file` points at. Any script, usage tool, or hand-edited file that emits the same shape works too; the contract is in the [slice 2 plan](../../docs/plans/implemented/slice-2-capacity.md).

## Requirements

Install CodexBar and `jq` and ensure both are on `PATH` before running the wrapper. Configure the provider access CodexBar needs and edit the bindings for your accounts.

## Files

- `codexbar-to-frost.jq`: converts CodexBar `usage --json` rows into the snapshot. Account emails, credit inventory, tokens, and raw error bodies are dropped.
- `bindings.json`: which CodexBar provider (and source) feeds which Frost pool, and which CodexBar window IDs to read. Copy it to `~/.config/frost/capacity-bindings.json` and edit; it is the operator's document.
- `refresh.sh`: runs CodexBar per provider, converts, and atomically replaces the output. A provider that fails becomes a `source_status: error` pool; if every provider fails the previous file stays. Afterwards it runs `frost capacity check` when `frost` is on PATH.
- `install-launchd.sh`: schedules `refresh.sh` every five minutes as the user LaunchAgent `net.vorwaller.frost-capacity`, logging to `~/Library/Logs/frost/capacity.log`. `--uninstall` removes it.

```sh
cp examples/capacity/bindings.json ~/.config/frost/capacity-bindings.json
examples/capacity/refresh.sh --bindings ~/.config/frost/capacity-bindings.json --dry-run | head
examples/capacity/install-launchd.sh
frost capacity check ~/.config/frost/capacity.json
```

## What the snapshot can and cannot say

The operator config owns topology: which pools exist, which windows each requires, which windows earn the expiry preference, and which profiles draw from which pools. The snapshot only supplies observations keyed by those IDs; unknown pools or windows are validation errors. `generated_at` never freshens an observation; `observed_at` comes from CodexBar's `updatedAt`. A null window stays unknown, never 100%. After a reset boundary passes, Frost treats the old value as unknown until the next refresh.

Measurements follow CodexBar's `dataConfidence`: `exact` and `percentOnly` (Codex OAuth, Claude web) become `exact`, `estimated` (OpenCode Go) stays `estimated`, and a row with no confidence takes the binding's `measurement` field, else `unknown`. Estimated values count only when the policy opts in.

## Verified and unverified bindings

| Pool | CodexBar windows | Status on September 16, 2026 |
| --- | --- | --- |
| `codex-main` | `secondary` (weekly) | Codex is a flat weekly limit; the five-hour `primary` is null and not declared. |
| `claude-main` | `primary` (five-hour), `secondary` (weekly) | Claude web source reports both. |
| `claude-fable` | `claude-weekly-scoped-fable` | A Fable-only weekly allowance CodexBar exposes as an extra window; bound to the Fable profile alongside `claude-main`. CodexBar omits it on some runs, and then the pool is unknown until the next refresh, which leaves Fable unknown rather than excluded. |
| `opencode-go` | `primary`, `secondary`, `tertiary` | Estimated five-hour, weekly, monthly. |
| `codex-spark` | `codex-spark`, `codex-spark-weekly` | Which native models draw from Spark is unverified; `mapping_verified = false` keeps it unknown. |

Antigravity and Grok pools wait for confirmed window IDs. Reset credits are inventory, not capacity, and are never emitted.
