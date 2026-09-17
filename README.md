# Frost

Frost recommends a model and execution profile for a task written in ordinary language. Give it the task you would give an agent, in a sentence or several pages, and it returns a model, an access surface (a CLI harness or an API), an effort intent when the profile supports one, a short explanation, and useful alternatives. An agent consumes the same result as JSON. Asking for a recommendation never launches a model, changes a project, or grants execution permissions.

**Status: slice 1, first usable CLI.** Task analysis runs through TypeSafe; selection is a deterministic policy over operator-configured profiles. Quality floors currently rest on labeled operator priors, so every result is `provisional` until measured task-family evidence and paired outcomes exist. Capacity-aware selection (subscription quota snapshots) and the public-catalog producer are the next slices. Read the [implementation plan](docs/plans/active/model-router.md) for the design and the [pilot findings](docs/research/router-pilot.md) for the evidence behind it.

## Install and configure

Requirements: Go 1.27 and a TypeSafe API key exported as `TYPESAFE_API_KEY`.

```sh
make build                      # bin/frost
make install                    # ~/.local/bin/frost
mkdir -p ~/.config/frost
cp config/frost.example.toml ~/.config/frost/config.toml
cp config/catalog.example.json config/questions-v3.json ~/.config/frost/
frost config check              # validates config, catalog, and question spec
```

Frost resolves its operator config from `--config`, then `FROST_CONFIG`, then `~/.config/frost/config.toml`. That file references the catalog and question spec. The [config README](config/README.md) explains what each example encodes; the example priors are Marcus's ordinal impressions, kept for reproducibility, and nothing in them is measured.

## Use

```sh
frost route "Find why our retries sometimes duplicate a payment."
frost route --file task.md --json
frost route --stdin --json < task.md
frost route --request request.json --json
frost route --policy quality "Prove this lock-free queue is linearizable."
frost route --allow sol,astra --effort high "…"
frost route --latency-ms 2000 --latency-mode prefer "…"
frost profiles list --json
frost config check --json --verify-model
frost explain saved-decision.json
```

Each `route` makes one TypeSafe request. Policy modes are `adequate` (default: the cheapest profile meeting the floor), `quality`, `fast`, and `relaxed` (an explicitly lowered floor). Explicit flags override a `--request` object, which overrides the config. Exit codes: 0 for a recommendation or provisional result, 2 for input or config errors, 3 for `needs_context`, `no_match`, or `conflict`, 4 for analyzer failures. JSON mode writes one result or error object to stdout and diagnostics to stderr.

## Record and replay

```sh
frost route --record .local/run.jsonl "…"        # stores task text, answers, and the decision
frost route --replay .local/run.jsonl --json      # recomputes decisions with the current config, no provider call
frost route --replay .local/run.jsonl --policy quality
```

Records carry the question spec hash; replay refuses records answered under a different spec, because changed questions require fresh judgments. Recorded files contain task text and stay local unless deliberately reviewed for sharing.

## Develop

```sh
make check      # fmt-check, build, test, vet, lint
make test
```

The selection core in `internal/router` is deterministic and has no I/O; its tests are the plan's offline acceptance matrix. The TypeSafe adapter in `internal/analyzer/typesafe` is the only network seam. `experiments/probe` remains the original feasibility probe with its recorded pilot answers.

The [CodexBar converter example](examples/capacity/README.md) demonstrates the planned neutral capacity input; the router does not consume it yet.
