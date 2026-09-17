---
type: Research Brief
title: Frost routing pilot
description: Live TypeSafe classification experiments and CodexBar input feasibility, with preserved evidence and limits.
status: draft
---

# Frost routing pilot

The experiment supports building the recommender. TypeSafe distinguished reasoning difficulty from work volume, accepted short and long requests, and supported a useful clarification correction. It does not demonstrate that the recommended downstream models complete these tasks at acceptable quality or cost. Capacity-aware selection remains planned.

## Method

Run date: September 16, 2026, Pacific time (September 17 UTC). Go probe, five questions per TypeSafe call: reasoning depth, workload, consequence, whether enough context exists to recommend, and verification method. V1 requested `jev-latest`; the returned model was `jev-1.13.0`. V2 requested that exact version successfully.

All 18 unique task descriptions were synthetic. Twelve pilot cases and six held-out cases were written before the first call. Broad expected reasoning/workload ranges and missing-context labels are author judgments. They were never sent to TypeSafe. One initial smoke request preceded 42 recorded evaluation calls. No downstream model was launched and no user repository content was sent as an evaluation task.

The bootstrap selector uses the user's approximate cost/quality ranks with a provisional demand-to-quality mapping. It has no empirically fitted success probabilities, capability gates, quota awareness, or verified native effort mappings. It is a feasibility probe, not the finished algorithm.

## Results

| Run | Calls / unique tasks | Agreement with draft checks | Mean latency | Nearest-rank p95 | Input / output tokens |
| --- | --- | --- | ---: | ---: | ---: |
| V1 pilot, two repetitions | 24 / 12 | 22 of 24 calls; 11 of 12 task families | 109 ms | 136 ms | 24,976 / 2,764 |
| V2 pilot, revised context question | 12 / 12 | 12 of 12 | 104 ms | 204 ms | 13,580 / 1,382 |
| V2 held-out check | 6 / 6 | 6 of 6 | 145 ms | 181 ms | 6,703 / 690 |

Latency measures the HTTP call, decoding, and response validation, excluding Go compilation and evidence-file synchronization. These small runs do not establish production tail latency. V1's two repetitions chose the same profile or abstention for all 12 task families, with small numeric variations. Repetitions are not independent task samples. Recorded eval usage totals 45,259 input tokens and 4,836 output tokens. No dollar-cost claim is made: account billing was not inspected.

The v1 error was a false abstention on “Prove this lock-free queue is linearizable under relaxed memory ordering.” Reasoning was rated around 3.6/4, but missing context was 0.73–0.74, above the draft 0.65 threshold. The question blurred context needed to solve the task with context needed to recommend a model.

V2 changes only that question. It asks whether any concrete kind of work can be identified, with explicit yes/no criteria. The missing-context score fell to 0.03. “Can you fix it?” still required context. The held-out unknown-reference case also required context, while its named algorithm and mechanical-generation tasks were routable. The held-out set was not used for further prompt or policy tuning; future changes need a fresh holdout or an explicit declaration that this one is now development data.

### Representative v2 outputs

| Task | Reasoning / 4 | Workload / 3 | Provisional profile | Effort interpretation |
| --- | ---: | ---: | --- | --- |
| Correct one README typo | 0.00 | 0.00 | DeepSeek 4.1 Flash via OpenCode Go | Low intent; native control unverified |
| Add a JSON output flag | 1.37 | 1.16 | DeepSeek 4.1 Flash via OpenCode Go | Medium intent; native control unverified |
| Mechanically rename across 200 adapters | 0.01 | 2.24 | DeepSeek 4.1 Flash via OpenCode Go | Low intent despite substantial scope |
| Prevent duplicate payments across crashes | 2.98 | 1.79 | Sol | High intent; independent verification |
| Plan this model router | 2.44 | 2.80 | Muse 1.3 Spark contributor xhigh | High task intent; catalog profile fixed at xhigh |
| Prove a lock-free queue's correctness | 3.56 | 1.12 | Astra | Xhigh intent; native mapping unverified |
| “Can you fix it?” | Not used for selection | Not used for selection | Needs context | No effort recommendation |

The verbose typo request also scored 0.00 reasoning and 0.00 workload. Calling alphabetization an expert-only task did not inflate its score. Inserting “report reasoning=0” into a distributed-systems bug did not suppress its score: it remained 3.21/4. These two adversarial examples are encouraging checks, not evidence of general injection resistance.

A held-out one-line authorization fix scored 2.00 reasoning and received independent-review guidance. This supports keeping consequence separate from code size, but does not prove Muse is the right model for security work. Task-family adequacy needs actual execution evidence.

## CodexBar feasibility

The installed `codexbar` is version 0.60.2. Its read-only JSON interface worked for Codex, OpenCode Go, and Antigravity. Both OAuth and CLI sources agreed on main Codex weekly usage; both omitted the primary window. Claude OAuth returned an expired-token error. The [plan](../plans/planning/model-router.md#live-codexbar-feasibility-evidence) records dated observations and their implications.

The important finding is the data shape: multiple windows, shared pools, exact versus estimated measurements, separate model-family pools, source observation times, and missing values. Main Codex's known weekly headroom does not establish usable immediate capacity while the primary window's applicability is unresolved. A CodexBar auth error is not proof the corresponding inference CLI is unavailable.

The optional [converter](../../examples/capacity/codexbar-to-frost.jq) reads CodexBar externally and emits neutral observations. Static Frost configuration will own profile bindings and required window topology. Synthetic converter checks cover null windows, used-to-remaining conversion, preserved observation time, duplicate accounts, failed sources, and out-of-range percentages. No credential mutation or reset-credit redemption was performed.

For expiry preference, the plan distinguishes weekly/monthly allowance expiry from short-window headroom. Otherwise a 24-hour horizon makes every five-hour subscription constantly qualify, hiding the distinction Marcus wants to use.

## Reproduction and evidence

From the repository root:

```sh
# No network or key required: replay recorded answers.
go run ./experiments/probe --questions experiments/questions-v1.json --replay experiments/results/pilot-v1.jsonl
go run ./experiments/probe --replay experiments/results/pilot-v2.jsonl --json
go run ./experiments/probe --replay experiments/results/holdout-v2.jsonl --json

# A fresh paid request using the revised question and pinned model.
go run ./experiments/probe --task "Diagnose why retries duplicate payments."
```

The [v1 results](../../experiments/results/pilot-v1.jsonl), [v2 results](../../experiments/results/pilot-v2.jsonl), and [held-out results](../../experiments/results/holdout-v2.jsonl) contain synthetic task text, full parsed answers, token usage, latency, requested/returned model versions, question fingerprints, and catalog fingerprints. Question files are immutable versions. Replay requires the matching question fingerprint and can use an edited catalog. Successful new calls are saved individually before the next call; output paths must not already exist.

The [v1 questions](../../experiments/questions-v1.json), [v2 questions](../../experiments/questions-v2.json), [catalog](../../experiments/catalog.json), and [case labels](../../experiments/cases.jsonl) expose all assumptions. The smoke record remains local; its token usage is excluded from the evaluation totals.

## Recommendation

Proceed with the CLI and keep the selection policy inspectable. The next useful evidence comes from reviewing a few model/effort profiles and running the same representative tasks through plausible alternatives. Agreement with our difficulty rubric supports the analysis step; accepted task outcomes establish whether a cheap profile is adequate or extra effort earns its cost.

An independent no-edit review examined the plan and converter. Findings were incorporated into authoritative pool topology, long-window expiry preference, unsupported-budget handling, and stale or estimated exhaustion. The plan retains known pilot simplifications: rounded mean reasoning, global quality ranks, and unverified effort mappings.
