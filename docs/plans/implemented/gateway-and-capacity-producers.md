---
type: Implementation Plan
title: Gateway profiles and multiple capacity producers
description: A gateway access surface, a producer-written profiles file, several independent capacity snapshots, per-pool expiry horizons, and local-fleet and API-credit pools, so Frost can be the model "auto" decision behind a LiteLLM gateway.
status: implemented
---

# Gateway profiles and multiple capacity producers

Implementation: `td-e60a38` (gateway access surface) and `td-628ef9` (multiple capacity producers). Approved by Marcus on 2026-10-21 as part of the personal-OpenRouter work: a LiteLLM proxy is the front door, rook is the inventory tool that generates profiles and local-fleet capacity, and Frost is the deterministic brain behind `model: "auto"`. The media work is a separate issue (`td-670172`) and is not part of this plan. This extends [slice 2](slice-2-capacity.md) and leaves the routing core's behavior for existing configs unchanged.

## Settled decisions

| Decision | Choice |
| --- | --- |
| What a gateway profile is | An ordinary profile with `access_surface = "gateway"`, an `alias` (the string sent as `model`), and exactly one of `gateway` (a `[[gateways]]` id) or `gateway_url`. It keeps a catalog `model`, a quality prior or measured rules, pool bindings, and capabilities, so selection is the same policy as for any surface. |
| How a caller executes it | `recommendation.gateway = {id?, base_url, alias}` in `frost route --json`, present on exactly the gateway surface (also on alternatives). It is the one documented field a LiteLLM pre-call hook reads. `profiles list --json` shows `alias`, `gateway`, and `source` on the profile. |
| Making the answer executable | A new hard constraint, `--surface` / `constraints.access_surfaces`, restricts candidates by surface. Without it Frost could recommend a CLI profile the gateway cannot run. No match is `no_match` (exit 3), never a silent fallback to a CLI profile. |
| Where generated profiles live | `generated_profiles_file`: JSON, schema 1, strictly decoded, 1 MiB cap, with `gateways`, `pools`, and `profiles` in the operator's field vocabulary. JSON because producers emit it easily and snapshots and catalogs already are JSON. |
| Precedence | The operator's declarations win by kind and id; a generated entry with an operator's id is ignored (info), which is also how to retire one. Ids duplicated inside the generated file, or empty, are ignored (warning). Generated entries append after the operator's. |
| Failure containment | The generated file is optional input and never fails the config. A whole-file problem (missing, unreadable, malformed, wrong schema) or an entry that fails validation becomes a warning tagged `Source: "generated"` and the entry is dropped; `frost route` prints these on stderr and `config check` lists them with exit 0. Operator entries keep their original strict errors. |
| Provenance | `provenance.generated_profiles_hash` pins the generated file a decision used. It is omitted when no file loaded, so existing decisions are byte-identical. |
| Several capacity producers | `capacity_file` stays; `capacity_files` adds more; the effective list is `capacity_file` first. Each file is loaded, parsed, and topology-checked independently (`capacity.LoadSources`), then usable files are merged in list order (`capacity.Merge`). The first listed file owns a pool both report. |
| Per-producer freshness | Freshness remains per pool: `observed_at`, the producer's `valid_until`, and the policy maximum age, earliest wins. A producer therefore sets its own freshness through `valid_until`. No new config; the policy semantics are untouched. |
| Degradation | Missing, stale, failed, or invalid producers leave only their pools unknown. Unknown never means exhausted. `route` notes the failed file on stderr and continues; explicit `--capacity` paths must each be valid (exit 2), as before. `capacity check` is strict (exit 2 if any listed producer is unusable) but still evaluates the healthy ones. |
| Hash | One contributing producer: that file's hash, so single-producer provenance is unchanged. Several: a digest of the contributing hashes in list order. |
| Local fleet pools | Pool `kind = "local"`; windows `online`, `vram_free`, `queue_slots` as headroom percentages with `resets_at: null`. Exhaustion excludes/demotes by the existing rules; a producer that cannot tell reports `source_status: error` or omits the pool. No new router concepts. |
| Expiring API credit | A `prepaid`, `marginal_cost = "included"` pool with one `non_rollover` expiry-preference window, bound to the profile that spends it via `pool_ids`, so `prefer_expiring_included_usage` applies unchanged. One addition: `expiry_horizon_hours` per pool overrides the policy horizon, because 24 hours of lead is meaningless for a monthly allowance. `credit-snapshot.sh` turns a grant, spend, and cycle end into the window. |

## Compatibility

`TestLegacyDecisionsGolden` (`internal/cli/testdata/legacy/`) freezes the September 2026 example config, catalog, questions, and a snapshot with one expiry-eligible and one exhausted pool, then compares 86 recorded `frost route --json` decisions (levels 0–4, all four policy modes, capacity absent, present, demoted, ignored, plus allow/exclude/effort/latency cases), the human rendering, and `profiles list --json` byte for byte. It was recorded on the code before this change and must keep passing without regeneration. Every new output field is `omitempty` and absent for old configs; `capacity check --json` gains a `producers` array and `config check --json` gains `capacity_files`.

## Open points

- A local model must resolve in `catalog_file` for a generated profile to load. rook is expected to emit or ensure catalog entries; a missing model drops that profile with a warning rather than failing.
- Generated profiles bring their own `prior`. Quality and cost ranks are the operator's ordinal scale, so a producer that cannot place a model on that scale should omit the prior (adequacy then rests on measured rules or is unknown) and let the operator pin it by declaring the profile id.
- LiteLLM's hook and the aerie wiring are rook's work; Frost's contract is the `--surface gateway` JSON above. Calling Frost in-process (promoting `internal/router` and `internal/capacity` to `pkg/`) is deferred until the process-per-request cost matters.

## Changelog

- 2026-10-21: Implemented (`td-e60a38`, `td-628ef9`): `[[gateways]]`, gateway profiles, `recommendation.gateway`, `--surface`, `generated_profiles_file`, `capacity_files`, per-pool `expiry_horizon_hours`, pool kind `local`, `credit-snapshot.sh`, the legacy golden regression test, and docs.
