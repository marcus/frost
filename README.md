# Frost

Frost recommends a model and execution profile for a task written in ordinary language. Give it the task you would give an agent, in a sentence or several pages, and it returns a model, an access surface (a CLI harness or an API), an effort intent when the profile supports one, a short explanation, and useful alternatives. An agent consumes the same result as JSON. Asking for a recommendation never launches a model, changes a project, or grants execution permissions.

**Status: slices 1, 1a, and 2 are implemented.** Task analysis runs through TypeSafe; selection is a deterministic policy over operator-configured profiles, source-attributed catalog evidence, and an optional neutral capacity snapshot. The separate `catalog-build` command refreshes model facts and benchmark observations, while `frost` remains offline except for the configured task analyzer. Results remain `provisional`: the September 2026 SWE-bench source has no applicable rows for the current configured models, the latency classes are unmeasured priors, and paired completion outcomes have not been collected. Read the [implementation plan](docs/plans/active/model-router.md) for the design and the [pilot findings](docs/research/router-pilot.md) for the evidence behind it.

## Install and configure

Requirements: Go 1.27 and a TypeSafe API key exported as `TYPESAFE_API_KEY`.

```sh
make build                      # bin/frost and bin/catalog-build
make install                    # ~/.local/bin/frost and ~/.local/bin/catalog-build
mkdir -p ~/.config/frost
cp config/frost.example.toml ~/.config/frost/config.toml
cp config/catalog.example.json config/questions-v3.json ~/.config/frost/
frost config check              # validates config, catalog, and question spec
```

Frost resolves its operator config from `--config`, then `FROST_CONFIG`, then `~/.config/frost/config.toml`. That file references the catalog, question spec, and optional capacity snapshot. The [config README](config/README.md) explains what each example encodes; the example rankings and latency classes are operator priors kept for reproducibility and contain no measured performance.

## Use

```sh
frost route "Find why our retries sometimes duplicate a payment."
frost route --file task.md --json
frost route --stdin --json < task.md
frost route --request request.json --json
frost route --policy quality "Prove this lock-free queue is linearizable."
frost route --allow sol,astra --effort high "…"
frost route --latency-ms 2000 --latency-mode prefer "…"
frost route --capacity ~/.config/frost/capacity.json --availability exclude "…"
frost profiles list --json
frost config check --json --verify-model
frost capacity check ~/.config/frost/capacity.json --json
frost explain saved-decision.json
```

Each live `route` makes one TypeSafe request. Policy modes are `adequate` (default: the cheapest profile meeting the floor), `quality`, `fast`, and `relaxed` (an explicitly lowered floor). Explicit flags override a `--request` object, which overrides the config. Exit codes: 0 for a recommendation or provisional result, 2 for input or config errors, 3 for `needs_context`, `no_match`, or `conflict`, 4 for analyzer failures. JSON mode writes one result or error object to stdout and diagnostics to stderr.

## Refresh the catalog

`catalog-build` is a separate producer. It imports model identity, capabilities, context limits, and prices from models.dev; SWE-bench Verified measurements where an explicitly reviewed identity mapping exists; and optional Artificial Analysis data into a restricted local catalog when `ARTIFICIAL_ANALYSIS_API_KEY` is set. It prints a reviewable diff and atomically publishes the new catalog while retaining `catalog.previous.json`. A failed or skipped Artificial Analysis refresh retains last-good restricted measurements only in the restricted file and reports a partial exit; public, restricted, and latency output paths must be distinct.

```sh
# Reproduce the checked-in source fixtures without network access.
catalog-build refresh --from-fixtures tools/catalog-build/testdata/2026-09-16 \
  --out /tmp/frost-catalog.json
catalog-build validate /tmp/frost-catalog.json

# Refresh the repository catalog from live sources; review their data terms.
catalog-build refresh --out ~/.config/frost/catalog.json

# Add non-redistributable Artificial Analysis observations to a local catalog.
catalog-build refresh --out ~/.config/frost/catalog.json \
  --restricted-out ~/.config/frost/catalog.local.json
```

Point `catalog_file` at the published file you intend to use. Source IDs become Frost model IDs only through the reviewed `tools/catalog-build/overlay.json`; unmatched IDs are reported for review and receive no automatic mapping. Run from the source checkout or pass an absolute `--overlay` path, because source installation copies the executables but not repository assets. Release archives retain the required assets; a future Homebrew package will place them under `$(brew --prefix frost)/share/frost/`, but no Homebrew release is published yet. The produced `latency.suggestions.json` contains coarse priors for reviewing profile configuration and carries no runtime latency measurements. See [Portable configuration examples](config/README.md) for source ownership, licensing, overrides, and the current evidence gap.

## Use capacity snapshots

Frost reads capacity from a neutral JSON file and never invokes a usage collector. The optional [CodexBar example](examples/capacity/README.md) converts provider observations, publishes the file atomically, and can schedule refreshes with a user LaunchAgent on macOS.

The wrapper treats empty, malformed, provider-error, or wrong-source payloads as collection failures and passes each configured source explicitly. If every selected request fails, it exits 4 and leaves the previous snapshot untouched. Partial success publishes the usable observations plus explicit failed-pool states.

```sh
cp examples/capacity/bindings.json ~/.config/frost/capacity-bindings.json
examples/capacity/refresh.sh --bindings ~/.config/frost/capacity-bindings.json --dry-run
examples/capacity/refresh.sh --bindings ~/.config/frost/capacity-bindings.json \
  --out ~/.config/frost/capacity.json
frost capacity check ~/.config/frost/capacity.json
```

A fresh, applicable snapshot affects only profiles that already meet the quality floor. `--availability exclude` rejects profiles with exhausted required pools, `demote` keeps them behind available or unknown peers within the same objective tier, and `ignore` skips capacity for that call. Missing, stale, estimated-without-opt-in, failed, or unverified observations stay visible as unknown; they do not become evidence that a harness is unavailable. The example config uses `capacity_file = "capacity.json"`, so a missing first snapshot produces a diagnostic and routing continues without capacity.

## Record and replay

```sh
frost route --record .local/run.jsonl "…"        # stores task text, answers, and the decision
frost route --replay .local/run.jsonl --json      # recomputes decisions with the current config, no provider call
frost route --replay .local/run.jsonl --policy quality
```

Records carry the question spec hash; replay refuses records answered under a different spec, because changed questions require fresh judgments. Recorded files contain task text and stay local unless deliberately reviewed for sharing.

Recording is part of the command's success contract. An invalid `--record` path fails before the analyzer call. If append or sync fails after analysis, Frost exits 2: JSON output uses error kind `recording` and includes the complete recoverable record, while human output keeps the decision visible and adds a warning.

## Develop

```sh
make check      # fmt-check, build, test, vet, lint
make test
```

The selection core in `internal/router` is deterministic and has no I/O; its tests are the plan's offline acceptance matrix. The TypeSafe adapter in `internal/analyzer/typesafe` is the route-time network seam. Public-data and capacity producers remain separate adapters outside the router. `experiments/probe` remains the original feasibility probe with its recorded pilot answers.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and source-data boundaries. Frost code is available under the [MIT License](LICENSE); imported or generated source data retains its own terms as documented in `tools/catalog-build/NOTICES.md`.
