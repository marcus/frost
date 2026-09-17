---
type: Research Brief
title: Public model data for Frost
description: Verified catalog and benchmark sources for optional external configuration producers, with access, identity, freshness, and comparability limits.
status: draft
stale_after: 2026-10-16
---

# Public model data for Frost

Verified September 16, 2026 in America/Los_Angeles, September 17 UTC. This is design research for the [controlling plan](../plans/planning/model-router.md), not an implemented importer. Public JSON was fetched read-only from models.dev, OpenRouter, and SWE-bench. Artificial Analysis access and terms were checked in official documentation; no account, key, paid subscription, authenticated request, or scheduled job was created.

## Recommendation

Use external producers to generate a neutral catalog. Keep public model facts, benchmark observations, local execution profiles, personal preferences, and subscription capacity distinguishable. Frost should select from the resulting snapshot without knowing which benchmark service supplied it.

| Priority | Source | Useful first contribution | Important boundary |
| --- | --- | --- | --- |
| 1 | models.dev | Broad identity, capability, effort-option, and provider-price inventory | Community-maintained facts are candidates for verification; a listed provider is not proof of local account access |
| 2 | Artificial Analysis | Independent quality, price, and speed evidence | Optional bring-your-own-key enrichment; free API data is not a redistributable default catalog |
| 3 | OpenRouter | Provider and endpoint facts for models served through OpenRouter | Its API price, speed, and accepted effort values describe that service, not every CLI hosting the same model |
| 4 | SWE-bench and Terminal-Bench artifacts | Evidence for specific agent configurations and task families | Keep benchmark, split, harness, effort, attempt count, and version attached; never import a leaderboard rank as universal quality |

Start with one public producer and one small local overlay. Additional sources should earn their place by filling a real coverage gap. The producer can be a separate example command or script; the route command needs neither a network refresh step nor provider-specific benchmark logic.

## models.dev

The official repository documents three unauthenticated JSON exports: [provider catalog](https://models.dev/api.json), [model-only metadata](https://models.dev/models.json), and [combined catalog](https://models.dev/catalog.json). The combined response separates `providers` and `models`; the repository holds editable TOML and a shared schema. Preserve provider ID plus provider model ID, and use explicit underlying-model references when supplied. [Repository and API documentation](https://github.com/anomalyco/models.dev)

The schema contains modalities, context/input/output limits, tool calling, structured output, reasoning, native reasoning options, pricing tiers, and release/update dates. Optional benchmark observations can carry source, date, metric, version, dataset, harness, and variant; this is not a uniformly independently measured benchmark service. [Schema](https://github.com/anomalyco/models.dev/blob/dev/packages/core/src/schema.ts)

The repository is [MIT licensed](https://github.com/anomalyco/models.dev/blob/dev/LICENSE); preserve its copyright/license notice for reused material. Check the original source terms before redistributing third-party benchmark observations carried within it. A repository license is not proof that every underlying external dataset permits republication.

Provider sync runs hourly and proposes updates through pull requests. That does not promise that every model is verified hourly. Record the fetched payload digest and, for repository-based generation, the exact commit. The inspected `dev` revision was `6092333750ba7d422ad45e52ed1ad0c3e8451fa8`; it is an observation, not a claim that the served JSON was built from that revision. [Sync workflow](https://github.com/anomalyco/models.dev/blob/dev/sync.md)

Live inspection found `reasoning_options` on a provider record while model-only metadata included benchmark records with explicit versions and harnesses. This supports separate model facts and execution profiles. Do not copy a provider's effort list into a different harness without checking that harness.

## Artificial Analysis

The current documented Free endpoint is `GET https://artificialanalysis.ai/api/v2/language/models/free`, authenticated with `x-api-key`. It returns paginated JSON with model/creator identity, headline intelligence/coding/agentic indices, median performance, and input/output prices. Free is 100 requests per fixed 24-hour window. Pro is 500 and adds detailed model-level data; `GET /api/v2/language/models` is Pro+. Provider-level data and time series require Commercial access. Keep `id` as identity and retain slug/name for display. Missing measurements remain null. The response's numeric `intelligence_index_version` excludes patch versions. [Current API reference](https://artificialanalysis.ai/data-api/docs)

The older [API reference](https://artificialanalysis.ai/api-reference) still documents `/api/v2/data/llms/models` and 1,000 requests/day. Do not mix that older contract with the current endpoints. Before building an authenticated producer, verify its exact response and tier; this research did not test authenticated access.

AA says its data updates as findings are published. Its access page describes Free as internal use with attribution and no redistribution; Pro has restricted external use; Commercial offers negotiated redistribution. An open-source producer can let users create their own local snapshots without shipping AA data in Frost releases. Check the applicable terms before publishing derived ratings as well as raw scores. No precise Pro subscription price was verified. [Access and redistribution](https://artificialanalysis.ai/data-api)

The methodology currently reports version 4.3.1, while the API exposes major/minor. Store the methodology URL and observation date as well as the API version; do not invent a patch version for historical records. AA's current index includes different task families and evaluation harnesses, so it is an external prior rather than a direct probability that a local agent will finish a task. [Methodology and version history](https://artificialanalysis.ai/methodology/intelligence-benchmarking)

## OpenRouter

Unauthenticated `GET https://openrouter.ai/api/v1/models` succeeded during this research. The official overview shows unauthenticated examples and describes the catalog as freely available; the generated endpoint reference nevertheless marks a bearer token required. Treat unauthenticated access as observed and explicitly document the discrepancy. [Models overview](https://openrouter.ai/docs/guides/overview/models), [endpoint reference](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties)

Retain `id`, permanent `canonical_slug`, provider, modalities, context limits, accepted parameters, and pricing conditions. `created` is the date added to OpenRouter, not a measurement-refresh timestamp. Prices are USD per token/request/unit, so preserve their units and conditional overrides. Default listings select text outputs; `output_modalities=all` includes other model types. The catalog is edge-cached; no fixed freshness SLA was established. [Schema and semantics](https://openrouter.ai/docs/guides/overview/models)

The public [model endpoint listing](https://openrouter.ai/api/v1/models/openai/gpt-6-astra/endpoints) also succeeded. It contained provider-specific limits, parameter support, prices, and nullable recent latency/throughput fields. A flex endpoint had a different price from the model-level record. The exact hosting endpoint and tier therefore belong in price evidence.

The live model response additionally contained `reasoning.supported_efforts` and AA headline scores. Those scores lacked the underlying AA index version, measurement date, and tested effort in that response. Store them as attributed, incomplete observations if needed; do not automatically combine them with AA's current scores or treat the relay as independent corroboration. OpenRouter's published terms reserve rights in its data; an open-data redistribution license for the catalog was not established. [Terms, section 12](https://openrouter.ai/terms)

## Latency and live-meeting requests

AA distinguishes time to first token, time to first answer token after reasoning, output speed, and complete response time including input processing, reasoning, and answer generation. First token can be a reasoning token. Its ordinary performance summaries are medians over 72 hours; 100k-token results use 14 days. Standard workloads run about every three hours, while 100k runs weekly. Input length and testing location affect latency. These are useful priors, not guarantees that a two-second live request will finish in time. [Performance methodology](https://artificialanalysis.ai/methodology/performance-benchmarking)

OpenRouter describes TTFT as network, queue, and prefill delay, distinct from generation throughput. Its routing preferences can use recent percentile measurements, but those preferences reorder providers rather than guarantee a deadline. Warm caches, fallback attempts, and hosting endpoints also change latency. [Latency guidance](https://openrouter.ai/docs/guides/best-practices/latency-and-performance)

For Frost, measure the user's actual target: time from submitting the transcript request to the first useful grounded answer, and time to the complete short answer. Include the router/analyzer call itself in the end-to-end budget. A two-second target may make even a cheap remote classifier unsuitable unless its delay is measured or routing can be precomputed. Compare profiles on matched transcript lengths, cold/warm state, effort, endpoint, and output length; report medians and tail behavior. First token alone is insufficient, particularly if text can be provisional or revised before completion.

Store context limits separately from demonstrated retrieval or grounded-answer accuracy at that length. A model's generation mechanism does not establish long-context quality, hallucination rate, or overall intelligence. Claims that diffusion models systematically win these dimensions are hypotheses for per-model evals; no architecture-based quality bonus belongs in the initial policy.

## Task-specific evidence

SWE-bench publishes [leaderboard JSON](https://raw.githubusercontent.com/SWE-bench/swe-bench.github.io/master/data/leaderboards.json), documented by its [website repository](https://github.com/SWE-bench/swe-bench.github.io). A public fetch succeeded. Rows can include model, agent, effort, date, score, cost, agent version, and per-instance outcomes. These are stronger context for software-agent routing than a bare global rank, but fields and coverage differ between submissions.

Its [experiments repository](https://github.com/SWE-bench/experiments) holds submission metadata and result summaries, with raw artifacts linked to submitter repositories or older public S3 paths. The [submission checklist](https://github.com/SWE-bench/experiments/blob/main/checklist.md) specifies model and agent identities and optional reasoning effort. Pin the repository commit and submission folder; preserve the split and attempt policy. No guaranteed update schedule or blanket redistribution license for every contributor's artifacts was established.

Terminal-Bench's [official historical run-log repository](https://github.com/laude-institute/terminal-bench-leaderboard) explicitly records agent plus model and versioned datasets, with repeated runs. It demonstrates a useful evidence shape, but its documented submission flow targets an older benchmark release. This research did not establish a current, stable machine-readable API for the latest [Terminal-Bench leaderboard](https://www.tbench.ai/). Resolve the current export and artifact permissions before implementing that producer; do not quietly import the historical dataset as current evidence.

## Normalization and refresh contract

The following are proposed producer requirements, not statements about guarantees made by any source:

- Join identities explicitly. Use a local stable model ID with source-specific IDs, provider endpoint IDs, and verified aliases. A fuzzy label match may propose a join for review; it must not establish one automatically. Versions, effort variants, and hosting tiers can represent different observations.
- Preserve observations before computing suitability. Keep metric name, value, units, direction, task family, benchmark version/split, harness and effort when known, observation date, source URL, sample count when supplied, and source revision/digest. An unknown dimension is not zero or false.
- Preserve dependence. AA data relayed by OpenRouter or models.dev is the same underlying evidence, not two extra votes. Vendor self-reports, independent benchmarks, local run outcomes, and user preferences have different provenance.
- Publish one complete validated snapshot atomically. Keep the last valid snapshot when a source fails, but retain its original observation time and expose staleness. A failed refresh is not evidence that a model disappeared. Surface additions, removals, identity conflicts, and material capability/price changes in a diff.
- Start with manual refresh, then optional daily catalog refresh after the path works. This is a suggested policy, not a source guarantee. Capacity snapshots have a different, much shorter freshness window. No refresh schedule is created by this plan.
- The local overlay owns enabled profiles, harness mappings, account availability, quality policy, personal tie-breaks, and subscription-pool bindings. Public catalog updates must not overwrite it. Public API token prices cannot determine subscription resource consumption.

A benchmark observation could normalize into this illustrative record. Every ID and measurement below is synthetic; this is not an adopted schema or a reported result:

```json
{
  "model_id": "example/model-revision-1",
  "kind": "benchmark",
  "metric": "example-repo-fix.resolve_rate",
  "value": 0.72,
  "unit": "fraction",
  "higher_is_better": true,
  "task_family": "software_change",
  "evaluation": {
    "version": "1.0",
    "split": "heldout",
    "harness": "example-agent@2.1",
    "native_effort": "high",
    "attempts_per_task": 1,
    "sample_count": 100
  },
  "provenance": {
    "source": "synthetic-example",
    "observed_at": "2026-09-17T02:00:00Z",
    "source_revision": "example-revision"
  }
}
```

Do not average raw Elo, percentage scores, intelligence indices, tokens per second, and subjective ranks. A versioned policy can select a relevant evidence set and derive provisional task-family suitability; the raw observations stay inspectable. Local completion evals then test whether that suitability transfers to the user's actual tasks and harness.

## Questions that remain before adoption

AA needs a user-provided key and verification of the selected tier and permitted use. Native model identities and effort mappings still need local harness checks. Broad public sources cannot establish account entitlement, subscription cost per task, personal writing taste, or local completion quality. Cross-source comparisons need compatible benchmark versions and execution conditions; missing compatibility remains visible. Those are bounded calibration and producer tasks, not reasons to bake this week's personal model order into Frost.
