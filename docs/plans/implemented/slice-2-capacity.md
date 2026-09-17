---
type: Implementation Plan
title: Slice 2, capacity-aware selection
description: Neutral usage snapshots, per-profile availability, and the expiry preference for included subscription usage, built onto the slice 1 router.
status: implemented
---

# Slice 2: capacity-aware selection

Implementation: `td-3af594`; planning and approval: `td-d398e8`. This implemented plan supports the controlling [model-router plan](../active/model-router.md), sections "Capacity input" and "Choosing with subscription capacity".

Outcome: `frost route --capacity snapshot.json` (or a configured `capacity_file`) makes a fresh, applicable, exact observation of remaining subscription usage affect the choice among profiles that are already adequate, never the quality floor. Absent, stale, estimated, or unknown observations are visible in the result and change nothing unless the operator opts in.

## Settled decisions

| Decision | Choice |
| --- | --- |
| Where capacity enters `Recommend` | Two points only: an availability gate after adequacy (step 3 of the controlling order) and an expiry preference inside the objective tie group (step 5). Cost and stable ID remain last. |
| Default enforcement | `enforce_availability = true`: a fresh, exact (or opted-in estimated), exhausted required pool excludes the candidate, matching step 3 of the controlling plan. `false` demotes within the tie group instead. A per-call override `frost route --availability exclude\|demote\|ignore` exists because the right behavior depends on timing: a live request wants exclusion, a plan running across a reset seam may prefer demotion. |
| Which measurements count | `exact` always; `estimated` when `allow_estimated_measurements = true`, and every use of an estimated value is labeled. The code default is false; the example and Marcus's config set it true so OpenCode Go (estimated) counts and DeepSeek keeps winning easy tasks. `unknown` never counts. |
| Which windows drive expiry preference | Only the pool's `expiry_preference_window_ids` (weekly or monthly allowances by operator declaration). Five-hour windows gate headroom but never earn the preference. |
| Headroom rule | Every window in `required_window_ids` must be known and above `reserve_percent`. One unknown required window makes the pool unknown. |
| Snapshot authority | The snapshot supplies observations keyed by pool and window IDs the operator config already declares. Unknown IDs are errors when a snapshot is supplied explicitly. It cannot add pools, windows, or bindings. |
| Frost never runs a producer | `frost` reads a file. The CodexBar wrapper is an example script; `examples/capacity/install-launchd.sh` schedules it every five minutes as a user LaunchAgent, because problems with bindings show up fastest in real use. |
| Time source | `Inputs.Now` already exists; all freshness and reset comparisons use it, so tests pin the clock. |

## Snapshot contract: `internal/capacity`

Package `capacity` parses and validates the neutral file. It knows nothing about CodexBar. It converts a file into `router.Capacity` and a sha256 hash of the raw bytes for provenance.

### Schema (version 1)

```json
{
  "schema_version": 1,
  "generated_at": "2026-09-17T01:30:00Z",
  "producer": "codexbar-example",
  "pools": [
    {
      "id": "codex-main",
      "source_status": "ok",
      "measurement": "exact",
      "observed_at": "2026-09-17T01:29:00Z",
      "valid_until": "2026-09-17T01:44:00Z",
      "windows": [
        {"id": "primary", "remaining_percent": null, "resets_at": null, "duration_seconds": 18000},
        {"id": "secondary", "remaining_percent": 58, "resets_at": "2026-09-19T13:29:45Z", "duration_seconds": 604800}
      ]
    }
  ]
}
```

| Field | Type | Rule |
| --- | --- | --- |
| `schema_version` | int | Must be 1. |
| `generated_at` | RFC 3339 | Required. Informational; never freshens a window. |
| `producer` | string | Optional label. |
| `pools[].id` | string | Required, unique within the file, must match a configured pool. |
| `pools[].source_status` | enum | `ok`, `unavailable`, `error`, `ambiguous_account`. Anything but `ok` makes every window in the pool unknown regardless of values present. |
| `pools[].measurement` | enum | `exact`, `estimated`, `unknown`. |
| `pools[].observed_at` | RFC 3339 or null | Source time. Null means unknown. Must not be more than 5 minutes after `now` (clock skew allowance); later is an error. |
| `pools[].valid_until` | RFC 3339 or null | Producer's freshness limit. Effective freshness limit is `min(valid_until, observed_at + max_snapshot_age)`. |
| `pools[].windows[].id` | string | Required, unique within the pool, must be declared in the configured pool's `required_window_ids` or `expiry_preference_window_ids`. |
| `pools[].windows[].remaining_percent` | number or null | 0 to 100 inclusive. Null is unknown; 0 is exhausted. Out of range is an error. |
| `pools[].windows[].resets_at` | RFC 3339 or null | Required for a window to earn expiry preference. |
| `pools[].windows[].duration_seconds` | int or null | Informational; used only to sanity-check `resets_at` is not further out than one duration plus skew. |

Parsing is strict (`DisallowUnknownFields`, trailing data rejected, 1 MiB size cap). `Load(path, now) (router.Capacity, hash, error)` and `Parse(bytes, now)` return structural errors. `Validate(cap, pools []config.Pool) []Problem` returns topology errors (unknown pool or window IDs, duplicate IDs) and warnings (a configured pool absent from the snapshot; a required window absent from a present pool). The CLI treats errors as exit 2 when the snapshot was supplied explicitly.

Every freshness-derived state (stale, post-reset, unknown) is computed in the router, not here, because the same snapshot must be re-evaluable at a different `now` during replay.

## Router types and evaluation

Replace the placeholder in `internal/router/types.go`:

```go
type Capacity struct {
    GeneratedAt time.Time   `json:"generated_at"`
    Producer    string      `json:"producer,omitempty"`
    Pools       []PoolState `json:"pools"`
}

type PoolState struct {
    ID           string        `json:"id"`
    SourceStatus string        `json:"source_status"` // ok | unavailable | error | ambiguous_account
    Measurement  string        `json:"measurement"`   // exact | estimated | unknown
    ObservedAt   *time.Time    `json:"observed_at"`
    ValidUntil   *time.Time    `json:"valid_until"`
    Windows      []WindowState `json:"windows"`
}

type WindowState struct {
    ID               string     `json:"id"`
    RemainingPercent *float64   `json:"remaining_percent"` // nil = unknown
    ResetsAt         *time.Time `json:"resets_at"`
    DurationSeconds  int        `json:"duration_seconds,omitempty"`
}
```

`router.Pool` moves from `config` into router (config keeps its TOML shape and converts), so the core owns the topology it reasons about:

```go
type Pool struct {
    ID                        string
    Kind                      string // subscription | metered | prepaid
    MarginalCost              string // included | metered
    MappingVerified           bool
    RequiredWindowIDs         []string
    ExpiryPreferenceWindowIDs []string
    NonRolloverWindowIDs      []string
}
```

`Inputs` gains `Pools []Pool`. `Policy` gains `Capacity CapacityPolicy` (fields below).

### `EvaluatePools`

```go
type Availability string // available | exhausted | unknown

type PoolEvaluation struct {
    PoolID          string
    Availability    Availability
    Basis           string     // exact | estimated | unknown
    ObservedAt      *time.Time
    AgeMinutes      *int
    ExpiryEligible  bool       // earns the expiry preference
    ExpiryAt        *time.Time // earliest designated window reset when eligible
    Reasons         []string
}

type ProfileAvailability struct {
    ProfileID      string
    Availability   Availability // worst of its pools; no pools = unknown with reason "no pool binding"
    ExpiryEligible bool         // all required pools available AND at least one pool expiry-eligible
    ExpiryAt       *time.Time   // earliest eligible expiry among its pools
    Estimated      bool         // any counted value was estimated
    Reasons        []string
}

func EvaluatePools(cap *Capacity, pools []Pool, profiles []Profile, pol CapacityPolicy, now time.Time) CapacityEvaluation
```

Per pool, in order:

1. No snapshot, pool absent from snapshot, or `source_status != ok` → unknown, reason names which.
2. `MappingVerified == false` → unknown ("binding unverified"); values are still reported in reasons.
3. Freshness: `observed_at` nil → unknown. `now > min(valid_until, observed_at + max_snapshot_age)` → unknown ("stale, observed N minutes ago").
4. Measurement: `unknown` → unknown. `estimated` without opt-in → unknown ("estimated measurement, opt-in off"). `estimated` with opt-in → counted, `Basis = estimated`.
5. Per required window: absent or `remaining_percent` nil → unknown. `resets_at` known and `resets_at <= now` → unknown ("reset boundary passed since observation"); the value is never refilled to 100 locally. `remaining_percent == 0` → exhausted. `remaining_percent < reserve_percent` → exhausted ("below reserve"). Otherwise headroom.
6. Pool availability: any window exhausted → exhausted; any window unknown (and none exhausted) → unknown; all headroom → available. An exhausted required window wins over an unknown one because exhaustion is a positive observation.
7. Expiry eligibility requires: availability available, `MarginalCost == included`, `prefer_expiring_included_usage` on, and at least one window in `ExpiryPreferenceWindowIDs` present with known headroom above reserve and `resets_at` within `[now, now + expiry_horizon]`. `ExpiryAt` is the earliest such reset. Windows not in the designated list never contribute, so a five-hour window cannot qualify.

Per profile: availability is the worst across `PoolIDs` (exhausted > unknown > available). A profile with no pools is unknown with reason "no pool binding; capacity cannot apply". Expiry eligibility requires every bound pool available and at least one eligible.

This function is pure and unit-tested on its own; `Recommend` only consumes its map.

## Policy fields

TOML, under the existing `[policy]` table:

```toml
[policy.capacity]
prefer_expiring_included_usage = true
expiry_horizon_hours = 24
reserve_percent = 5
max_snapshot_age_minutes = 15
allow_estimated_measurements = false
enforce_availability = false
```

Validation: horizon and max age positive; reserve in [0, 50]; all fields default to the values above when the table is absent. `frost config check` prints the resolved capacity policy.

## Changes to `Recommend`

Current structure: gate → adequacy → objective `order()` → winner → alternatives. Capacity slots in as follows.

**After adequacy, before ordering (step 3).** Compute `avail := EvaluatePools(...)` once. For each adequate candidate, attach `cand.avail`. If `enforce_availability` is true, move candidates with `Availability == exhausted` to `ExcludedCandidates` with the pool reason. If it is false, keep them but add a warning per exhausted candidate that reaches the top three of the ordering. Unknown availability never excludes.

**Tie groups (step 5).** `order()` currently sorts by objective key, then cost, then ID. Split it: an `objectiveKey(cand, mode)` returns the comparable used by the mode (quality rank, latency rank, or a constant for `adequate` and `relaxed`). Candidates sharing the same key form a tie group. Within a tie group, sort by:

1. Availability class when enforcement is off: available before unknown before exhausted. (With enforcement on, exhausted candidates are already gone.)
2. Expiry preference: eligible before not eligible; among eligible, earlier `ExpiryAt` first.
3. Cost rank, cheaper first.
4. Stable ID.

Tie groups themselves keep the objective order. This is the controlling plan's order exactly: objective, then expiry, then cost. The quality floor was fixed before any of this, so capacity cannot admit an inadequate profile.

**Reasons and status.** When expiry preference or availability changed the winner relative to the cost-only ordering, add a decision reason: "Preferred sol because its codex-main weekly allowance (58% remaining, exact, observed 4 minutes ago) resets in 19 hours." When the winner's availability is unknown or exhausted, add the reason text to warnings. Status stays as slice 1 computes it; capacity never promotes a result to `recommended`.

**Alternatives.** The existing cheaper/stronger/faster logic runs unchanged on the new ordering. Add one more role, `available`, when the winner is exhausted or unknown under enforcement off and an available adequate candidate exists lower in the order.

**Large workload.** The existing large-workload warning gains the reserve caveat when capacity was used: "the reserve is a headroom policy, not a fit guarantee."

## Output surface

`Selection` gains:

| Field | Values |
| --- | --- |
| `availability` | `available`, `exhausted`, `unknown`, or `not_considered` (no snapshot). Replaces today's constant `configured`. |
| `availability_basis` | `exact`, `estimated`, `unknown`, `none`. |
| `capacity_observed_at` | RFC 3339 or null. |
| `expiry_preferred` | bool, true when the expiry preference selected this profile. |

`Provenance.CapacityUsed` becomes true only when a valid snapshot was loaded and at least one pool evaluated to a known state. `CapacityHash` is the snapshot hash (already plumbed).

Warnings, each emitted once per decision:

- "Capacity was not considered: no snapshot supplied." (existing)
- "Capacity snapshot is stale for pools X, Y (observed N minutes ago, maximum M)."
- "Pool X reports estimated measurements; ignored because allow_estimated_measurements is off."
- "Pool X source status is error; its windows are unknown. This does not mean the harness is unavailable."
- "Profile P is bound to pool X whose mapping is unverified; capacity was ignored for it."

Human basis line: `Basis: provisional, from operator priors; quota observed 4 minutes ago (exact); analyzer jev-1.13.0.` When stale: `quota observed 41 minutes ago, stale`. When absent: `capacity not considered` (existing).

## CLI and config surface

- `frost route --capacity PATH`: explicit snapshot; parse or topology errors exit 2. Overrides `capacity_file`.
- `capacity_file` (top-level TOML key, resolved relative to the config file): default snapshot. A missing default file is a warning, not an error, so a scheduled producer that has not run yet does not break routing.
- `frost capacity check [PATH] [--json]`: loads the snapshot (explicit or configured), validates it against the config's pools, runs `EvaluatePools` at `now`, and prints per-pool and per-profile availability with reasons. This subcommand is justified: it is the only way to see why a snapshot did or did not affect a route without making a billable analyzer call, and it is what the producer wrapper runs after publishing. `--json` returns `{schema_version, ok, problems, pools[], profiles[]}`.
- `frost route --replay` accepts `--capacity` so recorded assessments can be re-decided under a new snapshot; records store `capacity_hash` only, never the snapshot.
- `frost explain` renders the new fields.

## External producer

Promote the jq converter into `examples/capacity/refresh.sh`:

```sh
examples/capacity/refresh.sh --bindings ~/.config/frost/capacity-bindings.json --out ~/.config/frost/capacity.json [--providers codex,opencodego,antigravity]
```

Behavior: `set -euo pipefail`; runs `codexbar usage --json` per provider with `--source oauth` where the bindings say so; feeds all rows through `codexbar-to-frost.jq`; writes to `"$out.tmp"` and renames; exits non-zero and leaves the previous file in place when CodexBar fails entirely; keeps rows for providers that failed individually as `source_status: error` (the jq already does this). It never prints account emails, credit inventory, tokens, or raw error bodies. Afterwards it runs `frost capacity check "$out"` when `frost` is on PATH and reports the exit code. A `--dry-run` prints the snapshot to stdout.

The bindings file stays the operator's document. The example continues to omit Spark and Claude.

Pool topology settled from Marcus's account facts and the September 16 CodexBar output:

| Pool | Windows | Required | Expiry preference | Notes |
| --- | --- | --- | --- | --- |
| `codex-main` | `secondary` (weekly) | `secondary` | `secondary` | Codex is a flat weekly limit; there is no five-hour window, so `primary` is not declared and a null primary from CodexBar is dropped. |
| `claude-main` | `primary` (five-hour), `secondary` (weekly) | both | `secondary` | Claude Code has five-hour blocks under a weekly limit. CodexBar's Claude source returned usage on September 16 after the earlier token expiry. |
| `opencode-go` | `primary`, `secondary`, `tertiary` (monthly) | all three | `secondary`, `tertiary` | Measurements are estimated; counted through the opt-in. |

Spark, Antigravity, and Grok pools wait for verified window IDs. Bindings: Codex-surface profiles to `codex-main`, Claude Code profiles to `claude-main`, DeepSeek to `opencode-go`; `mapping_verified = true` for these three after the first live snapshot passes `frost capacity check`.

## Offline acceptance matrix

Every case runs `Recommend` with a fixed clock (`2026-09-17T01:00:00Z`), the slice 1 fixture profiles bound to fixture pools, and a synthetic snapshot. `expiry_horizon_hours = 24`, `reserve_percent = 5`, `max_snapshot_age_minutes = 15`.

| Case | Inputs | Expected |
| --- | --- | --- |
| no-snapshot-static | `Capacity == nil` | Same decision as slice 1; warning "not considered"; `CapacityUsed == false`. |
| fresh-exact-changes-tie | Two adequate profiles tied on objective and cost; A's pool weekly 58% resets in 19h, B's in 6d | A wins with reason naming the reset; `expiry_preferred == true`. |
| stale-zero-not-exhausted | Pool weekly 0%, observed 41 minutes ago | Availability unknown, warning stale, candidate not excluded even with enforcement on. |
| estimated-zero-no-optin | `measurement = estimated`, weekly 0%, opt-in off | Unknown; warning about ignored estimated data. |
| estimated-with-optin | Same, opt-in on | Exhausted; `availability_basis == estimated`; label in reason. |
| primary-full-weekly-empty | primary 100%, secondary 0%, both required | Exhausted (weekly gates). |
| shared-pool-counted-once | Astra and Luna on `codex-main`; Astra inadequate at the level | Luna wins; Astra never appears via the pool. |
| reset-cannot-override-quality | Cheap pool expiring tonight, cheap profile below floor | Cheap profile excluded for adequacy; expiry irrelevant. |
| reset-cannot-override-exhausted-overlap | Profile bound to two pools; one expiring soon, other exhausted | Exhausted and excluded; no expiry preference. |
| unverified-binding-ignored | Pool `mapping_verified = false` | Unknown, warning names the binding. |
| null-primary-required | primary null, listed in `required_window_ids` | Unknown (not exhausted, not available). |
| spark-not-main | Snapshot has pool `codex-spark` unknown to config | `capacity check` error; route with explicit `--capacity` exits 2; configured file warns and ignores. |
| claude-source-error | Pool `source_status = error` | Unknown; warning says harness availability is not implied. |
| identical-five-hour-different-weekly | Two pools, same primary headroom, weekly resets 6h vs 6d | The 6h pool's profile preferred. |
| five-hour-not-designated | Only primary in `expiry_preference_window_ids` is absent; primary resets in 2h | No expiry preference. |
| missing-required-weekly | Snapshot omits secondary for a pool requiring it | Unknown; warning "required window absent". |
| snapshot-cannot-add-binding | Snapshot lists window `tertiary` not declared for the pool | Validation error. |
| generated-at-cannot-freshen | `generated_at` now, `observed_at` 40 minutes ago | Stale. |
| post-reset-not-refilled | `resets_at` 10 minutes before now, value 30% | Unknown; no 100% assumed. |
| future-observed-rejected | `observed_at` one hour ahead | Parse error. |
| tiny-task-not-upgraded | Level 0 task; a pool expiring tonight holds a strong profile and a cheap one | The expiring pool is favored (step 5), and the cheapest adequate profile in it wins (step 6); the strong profile is not selected merely because its pool expires. |
| large-workload-caveat | Workload 3 with capacity used | Warning includes the reserve caveat. |
| enforce-excludes | `enforce_availability = true`, fresh exact 0% | Excluded with reason; `available` alternative absent. |
| enforce-off-demotes | Same with enforcement off (or `--availability demote`) | Winner is the available tied candidate with a reason naming the demotion. In `quality` mode an exhausted stronger profile still wins its tie group, with a warning and an `available` alternative. |
| replay-new-snapshot | Recorded assessment, replay with `--capacity` | Decision differs only in capacity-driven fields; `capacity_hash` updated. |
| per-call-ignore | `--availability ignore` with a snapshot | Capacity not considered; hash still recorded. |
| deterministic | Same inputs twice | Identical JSON. |

Fixture: `internal/capacity/testdata/codexbar-shape.json`, the real CodexBar-derived shape from September 16 with synthetic values (main primary null, weekly known, Spark as a separate pool, OpenCode Go three windows estimated, Antigravity two family pools, Claude source error).

## Work sequence

Each thread lands with its tests, `make check` green, and a td log entry.

1. **Snapshot parsing and `capacity check`.** `internal/capacity` Load/Parse/Validate; router `Capacity`, `PoolState`, `WindowState`, `Pool` types; config `[policy.capacity]` and `capacity_file`; `frost capacity check`. Evidence: fixture parses; every validation rule has a failing input; `capacity check --json` shows per-pool state; `route` still ignores the snapshot but records its hash.
2. **Availability evaluation, enforcement off.** `EvaluatePools`; `Selection.availability*` fields; warnings; basis line; tie-group availability ordering. Evidence: matrix cases through `enforce-off-demotes` except expiry cases; human output shows "quota observed N minutes ago".
3. **Expiry preference and enforcement on.** Eligibility, `ExpiryAt` ordering, `expiry_preferred`, `enforce_availability`. Evidence: remaining matrix cases; the plan's worked adequacy fixture still selects A under `adequate` with expiry preference.
4. **Producer wrapper.** `examples/capacity/refresh.sh`, README rewrite, dry-run test with a recorded CodexBar JSON fixture (synthetic values). Evidence: wrapper output passes `capacity check` against the example config with the pools Marcus verifies.
5. **Plan and docs.** Move this plan to `implemented/`, update the controlling plan's slice table, README usage, Fractal model (`frost.capacity` from proposed to current), CHANGELOG.

## Decisions from review

1. Bind and verify `codex-main` and `claude-main` first, with `opencode-go` alongside because its estimated measurements count.
2. Estimated measurements count via the opt-in from the start.
3. Enforcement is configurable, defaults to exclude, and can be overridden per call.
4. The refresh script is scheduled now via a LaunchAgent; trust comes from real use.

## Changelog

- 2026-09-16: Implemented (td-d398e8): `internal/capacity`, router `EvaluatePools` and tie-group ordering, `[policy.capacity]`, `capacity_file`, `--capacity`, `--availability`, `frost capacity check`, the CodexBar refresh wrapper and LaunchAgent installer, and the offline matrix as tests. Live CodexBar exposed a Fable-only Claude weekly window and separate Codex Spark windows; both are declared as their own pools (`claude-fable` verified, `codex-spark` unverified). `EvaluatePools` returns a `CapacityEvaluation` (pool and profile results plus warnings) rather than only the profile map.
- 2026-09-16: Approved with decisions recorded; implementation started.
