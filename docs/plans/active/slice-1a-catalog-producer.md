---
type: Implementation Plan
title: Slice 1a, public catalog producer
description: An external Go command that fetches permitted public sources, normalizes them into Frost's neutral catalog with provenance, and publishes it atomically with a reviewable diff.
status: active
---

# Slice 1a: public catalog producer

td: `td-b850c7`. This plan supports the [controlling router plan](../active/model-router.md), the [catalog and evidence specification](../active/catalog-and-profile-evidence.md), and the [public-source research brief](../../research/model-data-sources.md). Read those first; this document decides how the producer is built. Decisions marked **settled** are ready to implement. The open questions were answered on September 16, 2026; the answers are folded into the decisions below.

## Outcome

`frost route` keeps reading one validated `catalog.json` and never touches the network for it. A separate command, `catalog-build`, produces that file from public sources so that:

- every model the operator can configure has a source-attributed identity, capabilities, context limit, and price basis;
- software-change tasks get their first measured adequacy rule from SWE-bench evidence, so at least one task family can leave `operator_prior` and return `measured` results;
- a refresh is reviewable (a diff), reproducible (recorded fixtures, digests, and source revisions), and safe (a failed source keeps the last good catalog).

Nothing here changes the router's selection policy. Adding a source changes a connector and an overlay entry, never `internal/router`.

## Settled decisions

| Decision | Choice |
| --- | --- |
| Location | `tools/catalog-build/` is a second `main` package in this module, built by `make build` into `bin/catalog-build`. It imports `internal/catalog` for validation and `internal/router` for types; it never imports `internal/cli` or the analyzer. |
| Sources in slice 1a | models.dev (required, first), SWE-bench Verified leaderboard (required, second), Artificial Analysis (optional, bring-your-own-key, third). OpenRouter is deferred. |
| Identity | A hand-maintained overlay file, `tools/catalog-build/overlay.json`, is the only way a source ID becomes a Frost model ID. Fuzzy matches are proposals in the diff output, never applied. |
| Output | The existing catalog contract (`internal/catalog`, schema_version 1). No schema change is required for the first thread; measurement records use the existing fields. |
| Publication | Write to a temp file in the destination directory, validate with `catalog.Parse`, then rename. The previous file becomes `catalog.previous.json` until the next successful publish. |
| Restricted data | AA-derived measurements are written only to a catalog path the operator names with `--restricted-out`, listed in `.gitignore` guidance, and never to a distributable fixture. |
| Operator overrides | `~/.config/frost/catalog.overrides.json` (flag `--overrides`) is applied as the last normalization step on every publish, so a hand-tweaked field, an added or removed measurement, or a pinned value survives every refresh. The diff labels overridden fields. |
| No scheduler | Refresh is manual (`catalog-build refresh`). A user-owned cron or launchd job can call it later; this plan installs nothing. |

## Command

```sh
catalog-build refresh --out ~/.config/frost/catalog.json [--restricted-out ~/.config/frost/catalog.local.json]
catalog-build refresh --from-fixtures tools/catalog-build/testdata/2026-09-16 --out /tmp/catalog.json
catalog-build fetch --source models.dev --record tools/catalog-build/testdata/<date>/   # save raw payloads
catalog-build diff --current ~/.config/frost/catalog.json --candidate /tmp/catalog.json [--json]
catalog-build validate catalog.json
catalog-build propose-aliases [--json]      # unmatched source IDs with ranked suggestions, for overlay review
```

Flags shared by every subcommand: `--overlay PATH` (default `tools/catalog-build/overlay.json`), `--json` (one structured result on stdout, diagnostics on stderr), `--timeout`. Exit codes: 0 success, 2 input/overlay/validation error, 3 nothing published because every source failed, 4 a source failed but a partial catalog was published (the diff says which). `refresh` is `fetch → normalize → validate → diff → publish` in one run and prints the diff before writing; `--dry-run` stops before publish.

Network access happens only in `fetch` and `refresh`. `--from-fixtures` replaces fetch with recorded payloads and is what tests and CI use.

## Source connectors

One interface; each source is a file under `tools/catalog-build/source/`:

```go
type Source interface {
    Name() string                                   // "models.dev", "swebench", "artificialanalysis"
    Fetch(ctx, client, creds) (Payload, error)      // raw bytes, URL, fetched_at, sha256, HTTP metadata
    Normalize(Payload, Overlay) (Contribution, []Note, error)
}
// Contribution: model facts keyed by source ID, measurements, price observations, latency observations.
// Note: a non-fatal finding surfaced in the diff (unmapped ID, missing field, unit change).
```

Payloads are saved verbatim under `testdata/<date>/<source>.json` by `fetch --record`, with a sidecar `meta.json` holding URL, fetched_at, digest, and any revision the source exposes. Normalization runs on the recorded bytes, so offline runs are byte-identical to live runs.

### models.dev (required, first)

| Item | Value |
| --- | --- |
| Endpoints | `GET https://models.dev/models.json` (model-only metadata, keyed by `<org>/<model>`) and `GET https://models.dev/api.json` (providers with per-provider model records). No auth. Observed September 16, 2026: models.json about 318 KB. |
| Fields used | From models.json: `id`, `name`, `reasoning`, `tool_call`, `attachment`, `modalities.input`, `limit.context`, `open_weights`, `release_date`, `last_updated`, `license`. From api.json per provider model: `cost.input`, `cost.output`, `cost.cache_read` (USD per million tokens), `reasoning_options` (`{type: "effort", values: [...]}`), `limit`. |
| Maps to | Model `label`, `input_modalities` (text/image/audio; drop others with a Note), `context_tokens` from `limit.context`, `output_contracts` derived: `generated_text` always; `code_edit` when `tool_call` is true; `structured_object` when the record advertises structured output or tool calling. Prices become measurements with metric `price.input_usd_per_mtok` and `price.output_usd_per_mtok`, `provider` set to the models.dev provider ID, `unit` `usd_per_million_tokens`. Effort options are recorded per provider as measurement-free notes in the diff; they are a hint for operator config, not copied into profiles. |
| Refresh cadence | Community sync proposes updates hourly; treat daily as the useful refresh. |
| License | Repository is MIT; retain the copyright notice in `tools/catalog-build/NOTICES.md`. Benchmark observations embedded in models.dev records are not imported in slice 1a. |
| Failure | HTTP or parse failure keeps the previous catalog's models.dev-derived facts, marks the run partial (exit 4), and records `source_status: unavailable` in the diff. |

### SWE-bench Verified (required, second)

| Item | Value |
| --- | --- |
| Endpoint | `GET https://raw.githubusercontent.com/SWE-bench/swe-bench.github.io/master/data/leaderboards.json`. No auth. Observed September 16, 2026: about 4.1 MB, top-level `{"leaderboards": [...]}` with names Multilingual, Test, Verified, Lite, Multimodal; Verified had 180 results. |
| Fields used | Per result: `name`, `agent`, `agent_org`, `model_display`, `model_org`, `reasoning_effort` (null, `medium`, or `high` observed), `resolved` (percent), `date`, `folder`, `checked`, `tags` (contains `Model: <id>` and `System: Attempts - N`), `cost`. |
| Maps to | One measurement per result row whose `Model:` tag maps through the overlay: `metric` `swebench.verified.resolve_rate`, `metric_version` `verified-2024-08` (fixed until SWE-bench versions the split), `value` `resolved/100`, `unit` `fraction`, `higher_is_better` true, `task_family` `software_change`, `effort` from `reasoning_effort` (empty when null), `harness` the SWE-bench `agent` string, `provider` empty, `observed_at` from `date`, `source` `swebench:<folder>`. Rows whose `System: Attempts - N` tag is not 1 are not imported as measurements at all (the router matches on metric and version and cannot see attempts); they remain in the recorded fixture and are listed in the diff as skipped. |
| Refresh cadence | Irregular; weekly refresh is enough. |
| License | Leaderboard JSON is public; per-submission artifacts are not imported. Store folder IDs, not logs or trajectories. |
| Failure | Same partial semantics as models.dev; previous SWE-bench measurements are retained with their original `observed_at`. |

Only the Verified board is imported in slice 1a. Lite, Test, Multimodal, and Multilingual are recorded in the fixture but not normalized until a rule needs them.

### Artificial Analysis (optional, third)

| Item | Value |
| --- | --- |
| Endpoint | `GET https://artificialanalysis.ai/api/v2/language/models/free` with header `x-api-key` from `ARTIFICIAL_ANALYSIS_API_KEY`. Free tier: 100 requests per 24 h, internal use with attribution, no redistribution. The exact response shape was not exercised in research; the connector is written against a recorded payload the operator captures with `fetch --record` on first use. |
| Fields used | `id`, `slug`, `name`, headline indices (intelligence, coding, agentic) with `intelligence_index_version`, median output speed, median TTFT, input and output prices. |
| Maps to | Measurements with metrics `aa.intelligence_index`, `aa.coding_index`, `aa.agentic_index` (unit `index`, `metric_version` from the API's major.minor plus the methodology URL in `source`), `aa.output_tokens_per_second`, `aa.ttft_seconds_median`, `price.input_usd_per_mtok` / `price.output_usd_per_mtok` with `provider` `artificialanalysis-median`. Task family empty: AA indices are not task-family evidence in slice 1a. |
| Refresh cadence | Daily at most, well under the request budget. |
| License | Written only to `--restricted-out`. `refresh` refuses to write AA measurements to `--out`. Fixtures under `testdata/` for AA are synthetic. |
| Failure | Missing key means the source is skipped with a Note, not an error. |

### OpenRouter (deferred)

Not built in slice 1a. Its endpoint listing is the right source for per-endpoint price and recent latency when the operator uses OpenRouter, and its relayed AA scores must never be counted as a second observation. Add it as a connector when an OpenRouter-backed profile exists.

## Identity resolution

`overlay.json` is reviewed by a person and committed:

```json
{
  "schema_version": 1,
  "models": {
    "opus-5": {
      "label": "Opus 5",
      "sources": {
        "models.dev": ["anthropic/claude-opus-5"],
        "swebench": ["claude-opus-5", "claude-opus-5-20260401"],
        "artificialanalysis": ["claude-opus-5"]
      },
      "aliases": ["claude-opus-5"],
      "notes": "SWE-bench dated revision maps to the same Frost model; effort stays on the measurement."
    }
  },
  "ignore": { "swebench": ["System: Attempts - 3"] }
}
```

Rules:

- A source record with no overlay mapping contributes nothing and appears in `propose-aliases` and in the diff under `unmapped`, with ranked suggestions from normalized label similarity. Suggestions are never applied.
- Effort is never part of the model ID. It lives on the measurement (`effort`) and is matched by `findMeasurement` against the profile's fixed native effort when the rule uses `profile_match = "exact"`.
- Quantization, serving provider, and hosting tier live in `provider` on price and latency measurements; they never create a new model ID.
- A dated revision (`claude-opus-5-20260401`) maps to the base Frost ID unless the overlay declares it a distinct model. The source string keeps the exact revision, so a later split is possible without losing evidence.
- Two sources disagreeing on a fact (context limit, modality) produce a Note and the models.dev value wins for facts; measurements are never merged, so performance disagreements stay separately attributable.

## Measurement normalization

| Field | Convention |
| --- | --- |
| `metric` | `<source-family>.<benchmark-or-quantity>.<statistic>`, lowercase, dots only: `swebench.verified.resolve_rate`, `price.input_usd_per_mtok`, `aa.coding_index`. Documented in `tools/catalog-build/METRICS.md`, which is the list adequacy rules may reference. |
| `metric_version` | Benchmark split or methodology version, never the fetch date. Changing it is a deliberate edit that invalidates rules referencing the old version. |
| `unit`, `higher_is_better` | Required for every metric in METRICS.md. Prices and latency are lower-is-better. |
| `task_family` | Set only when the benchmark is a task-family measurement (`software_change` for SWE-bench). Empty for prices, latency, and indices. |
| `effort`, `harness`, `provider` | Set when the source states them; otherwise empty, which `findMeasurement` treats as unconstrained. |
| `observed_at` | The source's own date (SWE-bench `date`, AA publication date, models.dev `last_updated`). `fetched_at` and the payload digest live in the catalog `version` string and the run report, not on each measurement. |
| Dependence | A value relayed by a second source (AA scores inside OpenRouter or models.dev) is dropped when the primary source is configured, and otherwise imported once with `source` naming the relay. |

Catalog `version` is `<date>-<short digest of concatenated payload digests>`; `generated_at` is the publish time. Both appear in every Frost decision's provenance, so a replayed eval can name the exact catalog.

## First adequacy rules

The evidence above enables one rule set, shipped enabled in `config/frost.example.toml` because the alternative floor is an unmeasured impression; an operator can loosen or disable it locally:

```toml
[[policy.adequacy_rules]]
id = "swebench-software-change-l2"
task_family = "software_change"
reasoning_levels = [1, 2]
metric = "swebench.verified.resolve_rate"
metric_version = "verified-2024-08"
minimum = 0.60      # provisional
profile_match = "any"
on_missing = "provisional_only"

[[policy.adequacy_rules]]
id = "swebench-software-change-l3"
task_family = "software_change"
reasoning_levels = [3, 4]
metric = "swebench.verified.resolve_rate"
metric_version = "verified-2024-08"
minimum = 0.72      # provisional
profile_match = "any"
on_missing = "provisional_only"
```

The thresholds are provisional placeholders chosen so that, on the September 2026 board, the top tier passes level 3 and mid-tier models pass level 2. They are not calibrated. `profile_match = "any"` is deliberate for the first rule: SWE-bench harnesses are research agents, not the operator's CLI, so an exact harness match would never fire. The rule therefore says "this model, in some agent, resolves this fraction," which the plan treats as a prior with a measured basis, and results still show `basis: measured` with the rule ID so the reader can judge. Review procedure: after the third evaluation in the controlling plan produces paired outcomes for software-change tasks, compare pass/fail under each threshold with actual acceptance and adjust or retire the rule; record the comparison in this plan's changelog.

## Latency classes

The producer does not write latency classes into the catalog. `latency_class` lives on the operator's profile because it depends on harness and account, and the catalog contract has no such field. Instead:

- The producer emits `latency.suggestions.json` next to the catalog: per Frost model ID, the coarse class derived from public medians, the evidence used, and the source date. Derivation, labeled a prior: AA median TTFT under 0.5 s and output speed above 150 tok/s → `extra_fast`; TTFT under 1 s and above 60 tok/s → `fast`; TTFT under 3 s → `medium`; otherwise `slow`. Without AA data the file lists the model with `unknown`.
- `frost config check` gains a warning (not in this slice's producer, but in the same delivery) when a profile's `latency_class` is more than one class faster than the suggestion for its model.
- Measured end-to-end latency for the operator's own profiles is a slice 3 concern and overrides both.

## Restricted and distributable data

| Data | Where it may go |
| --- | --- |
| models.dev facts and prices | Distributable catalog, fixtures, examples (MIT, notice retained). |
| SWE-bench leaderboard rows | Distributable catalog and fixtures (folder IDs only). |
| AA measurements | `--restricted-out` only. Never fixtures, never `config/`, never a release archive. |
| Recorded payloads under `testdata/` | models.dev and SWE-bench payloads may be committed once trimmed to the models in the overlay; AA payloads are replaced by a synthetic file with the same shape. |

`frost` reads a single catalog file. An operator using AA points `catalog_file` at the restricted catalog, which the producer builds as the distributable catalog plus AA measurements. Two files, one contract.

## Tests and acceptance

Unit tests per connector run on recorded fixtures under `tools/catalog-build/testdata/2026-09-16/` and assert the exact normalized contribution. Integration tests run `refresh --from-fixtures` end to end and compare the published catalog with a golden file; `catalog.Parse` must accept it, and `frost route --replay` over `experiments/results/pilot-v2.jsonl`-derived records must still produce a decision for every record.

Acceptance cases from the controlling plan, each an offline test:

- A public model with no operator profile appears in the catalog and is never selected.
- Two sources disagreeing on one model version produce a Note and separately attributed measurements.
- A SWE-bench row at effort `high` does not satisfy an `exact` rule for a profile fixed at `medium`.
- Stale measurements keep their original `observed_at` after a refresh that fails for that source.
- AA data is absent from `--out` even when the key is set.
- A dropped source retains last-known-good data and the run exits 4 with the source named.
- An unmapped source ID is listed as a proposal and contributes nothing.
- A refresh with unchanged payloads produces an empty diff and the same catalog `version`.
- Removing a model from the overlay removes it from the catalog and the diff says so; `frost config check` then reports profiles referencing it.

## Work sequence

1. **Thread 1: identity only.** `tools/catalog-build` with the models.dev connector, overlay, validate, diff, atomic publish, and `--from-fixtures`. Output: a catalog with the operator's 13 models and zero benchmark measurements. Evidence: `frost route` against the produced catalog behaves exactly as against `config/catalog.example.json`; fixture-based tests; `make check` green.
2. **Thread 2: first measured rule.** SWE-bench connector, METRICS.md, the two provisional rules in the example config (commented out), and a recorded live run. Evidence: a software-change task routed with `basis: measured` under the enabled rule on a local config; the acceptance cases above.
3. **Thread 3: optional enrichment.** AA connector with `--restricted-out`, latency suggestions file, config-check warning. Evidence: synthetic AA fixture test; live run only after Marcus supplies a key and tier.
4. Update `docs/plans/README.md`, the Fractal model (`catalog-build` moves from proposed to current), and this plan's status to implemented.

## Decisions from review

- Artificial Analysis: the key is `ARTIFICIAL_ANALYSIS_API_KEY` in the operator's environment; thread 3 proceeds.
- OpenRouter stays deferred.
- Trimmed public fixtures from models.dev and SWE-bench are committed under `testdata/`.
- The two SWE-bench rules ship enabled; thresholds remain labeled provisional.
- Source-derived values must be overridable locally without being clobbered by a refresh (the overrides file above).

## Implementation status

Delivered September 16, 2026 (td-b850c7): `tools/catalog-build` with the models.dev, SWE-bench Verified, and Artificial Analysis connectors, `overlay.json` mapping every example model, operator overrides (`catalog.overrides.json`), atomic publish with `catalog.previous.json`, `latency.suggestions.json`, recorded fixtures under `testdata/2026-09-16/`, a golden end-to-end test, and the two enabled provisional rules in `config/frost.example.toml`. Live findings from the first refresh:

- models.dev maps every operator model except Haiku 4.6 (only Haiku 4.5 is listed) and Jev; both take their facts from overlay defaults. Gemini 3.8 maps to `google/gemini-3.8-flash`, the only 3.8 record, pending confirmation that Antigravity runs that model. DeepSeek 4.1 Flash has no first-party price on models.dev (only resellers list it), so it carries no price measurement.
- SWE-bench Verified's newest rows date from February 2026 and carry none of the operator's current models, so the two rules fall through to the operator prior for every profile today. models.dev records do carry vendor-reported SWE-Bench Pro, Terminal-Bench, and DeepSWE scores for the current models; importing those is a possible follow-up outside this slice.
- Artificial Analysis is paginated (four pages of 200 on the free tier) and publishes one record per effort variant; the connector reads the effort from the record name. Grok 4.6 and Gemini 3.8 had no AA slug on this date.
- AA's median time-to-first-token for reasoning models includes thinking time and grows steeply with effort (Sol: 3.1 s at low, 10 s at high, 103 s at max; Fable 5.1: 4.9 s at low, 282 s at max). Under the plan's thresholds every frontier model classifies as `slow` at its default effort while DeepSeek 4.1 Flash is `medium`. `latency.suggestions.json` therefore carries a `by_effort` map per model alongside the primary class, so the operator picks the class for the effort their profile runs at. The thresholds themselves are untested priors; measured end-to-end latency (slice 3) supersedes them.

## Changelog

- 2026-09-16: Approved with decisions recorded; implementation started.
- 2026-09-16: All three threads delivered; first live refresh published to `~/.config/frost/catalog.json` and `catalog.local.json`. Remaining from the sequence: the `config check` latency warning (belongs with `internal/config`) and the Fractal model update.
- 2026-09-16: Drafted after slice 1 landed; source facts re-verified against live models.dev and SWE-bench payloads on this date.
