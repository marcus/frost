# Frost

Frost will recommend a model and execution profile for a task written in ordinary language. It will combine the task's quality and speed needs with a refreshable model catalog, optional personal preferences, and available subscription usage. Profiles can use a CLI harness or a typed API; effort applies when supported.

**Status: planning and experiments.** The production CLI is not implemented. Start with the [implementation plan](docs/plans/planning/model-router.md) and [pilot findings](docs/research/router-pilot.md).

## Try the experiment

Requirements: Go 1.27 and a TypeSafe API key exported as `TYPESAFE_API_KEY`.

```sh
go run ./experiments/probe --task "Add a --json flag to this CLI's list command."
go run ./experiments/probe --task "Investigate lost writes during leader election." --json
go run ./experiments/probe --cases experiments/cases.jsonl
```

Piped stdin accepts longer task descriptions. Run from the repository root, or supply absolute `--questions` and `--catalog` paths. `--help` lists the experimental options. Each task makes one paid TypeSafe request; it does not execute the task. The probe does not read shell startup files or load secrets for you.

The experiment's catalog contains Marcus's approximate rankings for reproducibility. The production plan uses [source-attributed public evidence](docs/plans/planning/catalog-and-profile-evidence.md) with optional personal preferences. Probe recommendations remain provisional; it does not yet use public-source refresh, latency-aware selection, or quota snapshots.

## Reproduce and inspect

```sh
mkdir -p .local
go run ./experiments/probe --cases experiments/cases.jsonl --repeat 2 --out .local/my-pilot.jsonl
go run ./experiments/probe --replay .local/my-pilot.jsonl --json
```

Replay uses saved answers and the current catalog without calling TypeSafe. It requires the original question specification. Evidence files include task text and should stay local unless deliberately reviewed for sharing. The checked-in pilot uses synthetic tasks only.

The [CodexBar converter example](examples/capacity/README.md) demonstrates an optional source of quota data. Frost's planned input format can also be produced by scripts, other usage tools, or a hand-edited file.
