# External capacity snapshots

Frost reads neutral usage snapshots and never runs a producer. This directory is an example producer for [CodexBar](https://github.com/steipete/CodexBar) 0.60.2 that writes the snapshot Frost's `capacity_file` points at. Any script, usage tool, or hand-edited file that emits the same shape works too, and several producers can feed one Frost through `capacity_files`; the contract is in the [slice 2 plan](../../docs/plans/implemented/slice-2-capacity.md) and the multi-producer rules are in the [extension plan](../../docs/plans/implemented/gateway-and-capacity-producers.md).

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

- `credit-snapshot.sh`: turns a prepaid credit's grant, spend so far, and cycle end into a one-window snapshot for an expiring pool such as a monthly API credit. Needs `jq`.
- `local-fleet.example.json`: what a local-fleet producer such as rook writes for a host.

## Several producers

Give each producer its own file and list them in the config (`capacity_file` is the first entry; `capacity_files` adds the rest). Frost loads and validates each file independently, so a producer that is down, stale, or wrong costs only the pools it owns. A pool should have one owner; if two files report the same pool the earlier file in the list wins. `valid_until` is how a producer sets its own freshness: codexbar's fifteen minutes suits a weekly allowance, while a local-fleet producer should say a minute or two, because queue depth and VRAM headroom go stale fast. Frost takes the earlier of `valid_until` and the policy's `max_snapshot_age_minutes`. Atomic replace (write a temp file, then rename) is expected of every producer, as in `refresh.sh`.

## Local fleet contract (rook and similar)

A producer that watches owned hardware reports each host or fleet as a pool. Topology stays in the operator config (or the generated profiles file): the pool's `required_window_ids` name the windows below, and the snapshot may report only windows the pool declares.

| Window | `remaining_percent` | Notes |
| --- | --- | --- |
| `online` | 100 or 0 | 0 is a positive observation that the host is down and excludes its profiles. Use `source_status: error` (or omit the pool) when the producer could not tell. |
| `vram_free` | free VRAM as a percentage | Below `reserve_percent` (default 5) counts as exhausted. |
| `queue_slots` | 100 × (1 − queued ÷ allowed) | 0 means the queue is full. |

Set `measurement` to `exact` for probed values, `observed_at` to the time of the probe, `resets_at` to `null` (local windows do not reset, so they never earn the expiry preference), and a short `valid_until`. `local-fleet.example.json` is a complete example; declare its pool as `kind = "local"` (see [Local fleet pools](../../config/README.md#local-fleet-pools)). A producer may name its windows differently as long as the pool declares them.

## Expiring API credit

A monthly credit that refreshes each cycle with no rollover is an expiring included-usage pool, so `prefer_expiring_included_usage` applies to it once its reset is inside the horizon. Declare it as a `prepaid` pool with `expiry_preference_window_ids` and `non_rollover_window_ids` naming its window and a pool-level `expiry_horizon_hours` (see [Expiring API credit](../../config/README.md#expiring-api-credit)), bind the profile that spends it with `pool_ids`, and publish the snapshot:

```sh
examples/capacity/credit-snapshot.sh --granted 200 --spent 52.40 --resets-at 2026-11-01T00:00:00Z \
  --out ~/.config/frost/credit-capacity.json
```

then add `credit-capacity.json` to `capacity_files`. `--spent` is whatever your billing view or the gateway's spend logs report; Frost only sees the resulting percentage.

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
