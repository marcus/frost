---
type: Design Specification
title: Frost catalog, profiles, and performance evidence
description: Source-independent model evidence, optional operator preferences, and task-specific comparison across generative and typed-decision models.
status: draft
---

# Catalog, profiles, and performance evidence

This supports the [controlling model-router plan](model-router.md). It defines the changing inputs to the router. Public-data refreshers and production evidence-based selection are proposed; the personal ranking in `experiments/catalog.json` exists only to reproduce the pilot.

## Three inputs with distinct owners

| Input | Owns | Typical producer |
| --- | --- | --- |
| Model catalog | Identity, documented capabilities, source-attributed quality/speed/price observations | External public-data builder; portable JSON |
| Operator configuration | Available accounts/harnesses, native profiles, quota-pool topology, policy, optional personal priors | Human-maintained TOML or generated equivalent |
| Capacity snapshot | Fresh remaining-usage and reset-time observations | CodexBar wrapper, another tool, or manual JSON |

The Frost executable reads these inputs. Catalog connectors and usage collectors run externally; route-time network access is limited to the configured task analyzer. The operator config references the other two files. Explicit field ownership avoids a recursive merge framework. An external wrapper may generate the complete config for each run.

Personal preferences are optional. They can define tradeoffs, preferred providers, or a clearly labeled prior where evidence is missing. They cannot overwrite source measurements or make Marcus's names, ranking, or number of quality tiers part of the core schema. A public-derived catalog with no personal ranking must be sufficient to operate, with uncertainty exposed.

## Public-data sources and refresh

Use the [source research brief](../../research/model-data-sources.md) to select concrete connectors. Start with one registry for identity/capability metadata and one optional benchmark source. Artificial Analysis is a candidate for measured quality, speed, and price; other sources may supply better evidence for a particular task or operating configuration. A public leaderboard does not establish which accounts or native harnesses the operator can access.

Every normalized observation retains source ID/URL, metric name and version, raw value, unit and direction, evaluation time when available, fetch time, exact model revision, serving provider, effort, harness, and sample information when supplied. Preserve source licensing and redistribution constraints. Distinguish reported vendor claims, benchmark measurements, local paired outcomes, and personal priors.

Use a compact typed measurement record. Keep the initial useful fields predictable: task family, benchmark score, source-grounded correctness, context limit, effective long-context performance when measured, first-response latency, throughput, price basis, and native capabilities. Unsupported or missing data is `unknown`. Extra source metadata may remain in an attached record without becoming a runtime policy dimension.

Do not average unrelated benchmarks into a universal intelligence score. Compare within a relevant benchmark/version and operating configuration. Any derived quality band or percentile is a labeled bootstrap estimate, not a probability of successful task completion; changing the candidate population can change a percentile without changing a model. Public evidence can guide a first shortlist, while local paired outcomes establish suitability for the actual journey.

Model identity uses explicit aliases. Fuzzy matching may propose a mapping for review; it cannot automatically equate a marketing alias, effort setting, quantization, serving provider, or dated revision. Discovery adds a model to the catalog but does not enable a runnable profile. A benchmark at high effort does not establish low-effort quality. Conflicting measurements remain separately attributable; field-level authority resolves factual conflicts without discarding dissenting performance evidence.

The first external builder fetches permitted sources, normalizes IDs/units, validates the result, shows a change summary, and atomically publishes a versioned catalog. It accepts recorded source snapshots for offline tests. Retain the last usable catalog when a source fails; mark stale or partial evidence. Do not delete working profiles or assign zero price because an upstream field disappeared. A new fetch must preserve an old evaluation date.

Start with a documented one-shot refresh. A user-owned scheduler can later run it daily for catalog/pricing metadata and at each benchmark's actual publishing cadence. Honor source limits, cache headers, and terms; this planning task installs no automation. Review identity changes and replay evals before promoting changed quality policy. Ordinary source observations can be refreshed deterministically. No LLM needs to browse the entire model landscape on every invocation.

Source-code licenses and data licenses may differ. Restricted or internal-use benchmark data stays in local ignored catalogs. The open-source repository can distribute connector code and synthetic fixtures without redistributing restricted data. Keep provenance and required attribution in local display/export paths. Missing credentials for one source must not prevent use of other sources.

## What a profile describes

A model record describes capabilities and evidence. An execution profile adds actual access: a CLI harness or API interface, exact native model ID, supported quality/effort mode, serving tier, and operator availability. A model can have several profiles with different tools, price, context, or effort. Measurements match the profile configuration explicitly; mismatched evidence remains conditional.

Required declarations are stable IDs, enabled state, model reference, access surface, output contract, native setting mode (`fixed`, `configurable`, or `none`), documented capabilities, and verification status. Numeric quality and cost evidence may be missing; missing values must affect recommendation status. Personal rank fields are optional annotations. Validate duplicate IDs, unknown references, incompatible output contracts, unsupported native settings, and ambiguous aliases.

Effort exists only where supported. A typed-decision endpoint can declare no effort setting. A diffusion model can expose ordinary reasoning effort or another documented quality mode. Preserve the actual control; do not map every parameter onto a universal high/Xhigh ladder. Compare supported profiles by measured outcome/resource tradeoffs. A large mechanical workload need not receive deeper reasoning.

## Output contract and generation mechanism

Two separate facts matter: the output a model can deliver, and how it produces that output. Diffusion is a generation mechanism. Diffusion language models can still generate text/code and call tools; see Inception's [Mercury description](https://www.inceptionlabs.ai/blog/introducing-mercury) and [current model interface](https://docs.inceptionlabs.ai/get-started/models).

TypeSafe calls Jev a [System One model](https://docs.typesafe.ai/concepts/system-one). It evaluates state against defined choices, scores, and yes/no questions and returns typed decisions/probabilities. That is TypeSafe's product category; Frost's neutral output contract is `typed-decision`. Its internal generation mechanism can remain undisclosed.

| Declaration | Examples | Routing effect |
| --- | --- | --- |
| Output contract | generated-text, structured-object, typed-decision, code-edit | Match the requested deliverable |
| Generation mechanism, optional | autoregressive, diffusion, undisclosed | Informational; no automatic quality or speed bonus |
| Interface prerequisites | chat messages, state plus criteria, prefix/suffix | Establish whether the input is sufficient |
| Capabilities | tool calling, input modalities, schema constraints | Enforce functional requirements |
| Native controls | none/fixed, documented effort options | Restrict recommendations to valid settings |

“Classify these tickets into these four categories” can admit a generative structured-output model and a typed-decision model. Compare both on the same classification accuracy, calibration where relevant, latency, and cost. “Build an application that classifies tickets” requires a code-producing profile; the application may use Jev internally, but Jev alone cannot author it. “Write a migration and explain its tradeoffs” likewise requires generated code/text.

Represent these contracts in the first schema. The production CLI may conditionally recommend a typed-decision profile when the required state/criteria are missing, naming the prerequisites. It does not synthesize schemas, compile workflows, or execute them. Diffusion profiles can participate immediately through supported interfaces once their native access and evidence are configured; no architecture-specific execution engine is needed.

TypeSafe may both analyze the task and be eligible to answer suitable decision tasks. That dual role does not grant it a preference: maintain independent outcome evals and a generative baseline. Automated cascades, image/audio generation, and multi-stage model composition wait for a concrete later journey.

## Multiple dimensions without a giant score

Frost should manage the comparison the user cannot conveniently keep in their head. TypeSafe judges the task's meaning and relevant tradeoffs; source data supplies measurable model properties; ordinary code enforces constraints and makes the comparison.

| Dimension | Question Frost must answer | Evidence or input |
| --- | --- | --- |
| Functional suitability | Can this profile produce the required output and use the necessary tools? | Output contract, capability declarations, operator verification |
| Task quality | Is it adequate for this reasoning/writing/coding/decision task? | Relevant public evals and local paired outcomes; priors when labeled |
| Factual reliability | How often does it give unsupported or incorrect answers under relevant conditions? | Grounding/correctness evals and their abstention behavior |
| Context | Can the input fit, and can the model use it effectively? | Documented limit, tokenizer where known, long-context evals |
| Responsiveness | Will useful information arrive quickly enough? | Request objective and comparable end-to-end latency measurements |
| Resource cost | What spend or subscription resource would this profile consume? | Price basis, workload estimates when supported, actual consumption |
| Capacity and access | Is the profile usable now, and does included usage expire soon? | Operator config and fresh neutral quota snapshots |

Use only dimensions supported by the request and available evidence. Do not invent missing scores to complete a matrix. A lower hallucination benchmark may come from frequent abstention, so compare answer coverage as well as error rate. A large advertised context window does not prove reliable retrieval or reasoning across that window. Architecture alone establishes neither property.

The user's impressions about diffusion speed, context, hallucination rates, general ability, and cost are hypotheses for individual profiles. Preserve measurable differences as data. Some diffusion profiles may be attractive for fast responses; others may not meet the required tools or quality. A typed-decision model can be excellent for a constrained judgment without being suitable for free-form explanation.

Selection remains constrained comparison: reject functional incompatibilities, establish a task-relevant adequacy target, apply any explicit latency requirement, then compare remaining quality/resource/expiry tradeoffs. Return a winner and a meaningful alternative when the tradeoff is material. A small Pareto shortlist can remove candidates clearly worse on every relevant known dimension, but unknown dimensions cannot prove dominance. Avoid a universal weighted sum and avoid pretending unlike quality metrics are interchangeable.

## Worked adequacy rule without personal ranks

Start with a small explicit rule table. Each rule matches a task family and reasoning band, selects one comparable metric/version, and declares a minimum result and missing-evidence behavior. The following configuration and observations are synthetic acceptance fixtures. They demonstrate policy mechanics; a 70% benchmark threshold is not a calibrated production recommendation or a 70% chance of completing the user's task.

```toml
[[policy.adequacy_rules]]
id = "example-software-change"
task_family = "software_change"
reasoning_levels = [1, 2]
metric = "example.repo_edit.resolve_rate"
metric_version = "1.0"
minimum = 0.70
profile_match = "exact"
on_missing = "provisional_only"
```

An analysis of a moderate CLI change selects reasoning level 2 after the configured uncertainty rule. All four enabled example profiles can write code and use the required tools. Their observations use the same benchmark version/split, attempt policy, and task conditions, and match each profile's native model, harness, and effort. Latency describes matched end-to-end first-useful-response p95, including Frost overhead, for a comparable input. Expiry facts are fresh, verified, and satisfy every required limiting window.

| Profile | Resolve rate | End-to-end p95 | Qualifying included usage expires soon | Adequacy |
| --- | --- | --- | --- | --- |
| A | 0.90 | 1,800 ms | Yes | Meets floor |
| B | 0.78 | 800 ms | No | Meets floor |
| C | 0.64 | 500 ms | Yes | Below floor |
| D | Unknown | 100 ms | No | Unverified; conditional alternative |

For a two-second preference, `fast` selects B. `quality` without a conflicting speed preference selects A. `adequate` with expiry preference selects A. A required one-second target admits B, while a required 300 ms target returns `no_match`: C fails quality and D lacks it. Capacity cannot rescue C, and missing evidence cannot make D the winner. No personal model ordering participates.

The fixture's comparison uses raw resolve rate for quality and p95 milliseconds for speed; exact ties proceed to expiry, cost, and stable ID. Production rules must state how they treat uncertainty and indistinguishable measurements rather than implying that tiny benchmark differences are meaningful. A public metric with different effort, split, or version does not satisfy this rule silently.

When no matching rule or measurement exists, mark adequacy unknown. Permit a separately labeled provisional suggestion only when no hard constraint requires the missing evidence; include the missing rule or measurement in the reason. Do not treat a missing value as a passing floor. Add real rules one task family at a time, with thresholds reviewed against paired completion outcomes before making stronger quality claims.

## Live meeting response journey

A user supplying a live transcript may value a useful answer in one or two seconds more than deeper reasoning delivered much later. This belongs in v1's request and decision model. A proposed explicit field is `response_time_target_ms`, paired with `latency_mode = prefer | require` and the desired milestone `first_useful_response | complete_response`. Natural language can establish a speed preference; an explicit numeric target takes precedence.

For `prefer`, optimize observed responsiveness among adequate candidates and show the quality tradeoff. For `require`, use only relevant latency evidence, accounting for uncertainty. If no candidate supports the target, return `no_match` with no selected recommendation; conditional alternatives may explain what evidence or constraint relaxation would make a profile eligible. A recommendation is a prediction, not an enforceable runtime SLA. A user-authorized fast policy can lower the desired quality target explicitly; it cannot remove functional or hard correctness requirements silently.

Define latency before using it. Provider time-to-first-token may precede internal reasoning, tool calls, or useful visible text. Tokens per second measures generation throughput, not response delay. Retain measurement milestone, percentile, prompt/context length, output size, serving provider/tier, tools/harness, region when known, and observation time. Prefer end-to-end p95 first-useful-response measurements for the meeting journey; public TTFT and throughput are proxies and must be labeled as such.

The budget includes Frost's analysis and process overhead, prompt prefill, provider queueing, reasoning, tool calls, and rendering through the chosen harness. The pilot's roughly 0.1-second API average excludes cold Go compilation and is not a two-second end-to-end guarantee. A long live transcript can change prefill cost and responsiveness even when the user asks a short question.

When repeated transcript updates make analysis overhead significant, measure that journey first. A later session-level route or cache can reuse a result while task intent, context demands, catalog, and capacity remain compatible. V1 needs a single-request budget and evidence contract; it does not need a streaming transcript server or anticipatory cache framework.

## Acceptance and staged delivery

The first schema supports optional evidence, task family, output contract, generation metadata, and latency preference. It works with the personal pilot fixture or a generated catalog. The first external producer imports one registry and optional quality measurements; adding a source changes a connector and mapping, not the routing core. Refresh scheduling remains outside the runtime.

Offline acceptance cases include: no personal ranking; a new public model without access; two sources disagreeing on one version; unmatched effort; stale performance measurements; restricted data excluded from distributable artifacts; a dropped source retaining last-known-good data; typed-decision versus code-generation requests; an unknown generation mechanism with known capabilities; a fast adequate profile beating a slower higher-scoring one for an explicit meeting objective; unsupported latency deadlines; long-context prefill; different price units; and error-versus-abstention tradeoffs.

The outcome eval should cross task families, output contracts, native efforts, and interactive/batch contexts. Compare personal-prior-only, public-evidence-only, and combined evidence policies alongside simple fixed baselines. Include measured latency and actual cost per accepted result. Broad performance claims require task outcomes, not success on the task-classification pilot.

Do not block the first recommendation CLI on comprehensive source coverage, an ideal taxonomy, automatic training, or every model interface. Unknowns are visible. Add the next dimension or connector when it changes a concrete routing decision and has enough evidence to test.
