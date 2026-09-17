# Working in Frost

Frost recommends a model and execution profile for a natural-language task. Profiles use a harness or API interface, with effort when supported. Slice 1 (the first usable CLI) is implemented: `cmd/frost` over `internal/cli`, with the deterministic selection core in `internal/router`, the TypeSafe adapter in `internal/analyzer/typesafe`, operator config in `internal/config`, and the catalog in `internal/catalog`. The controlling plan is `docs/plans/active/model-router.md`; `experiments/probe` is the original feasibility probe and its recorded evidence, not the CLI. Run `make check` before handing off.

Follow the project-standards skill when available. Use Go, a small shared core, and replaceable adapters for external judgment providers. Keep the CLI non-interactive with structured output. Source-attributed model evidence, operator profiles, and transient capacity have separate owners. Personal rankings are optional. Public-catalog and usage producers stay outside the router. Do not launch a recommended model as a side effect of asking for a recommendation.

Read `td usage --new-session -q` in a new context. Track substantive work in this repository's td workspace, and record review honestly. Preserve unrelated changes. Never operate on the default tmux server in tests.

Use the TypeSafe skill when working on its adapter. The experimental local copy is `/Users/marcus/code/clara-home/skills/typesafe-ai/SKILL.md` on Marcus's machine; the portable upstream is https://github.com/typesafe-ai/skills/tree/main/skills/typesafe-ai. Treat live vendor documentation as API authority, and retain exact question sets and responses for reproducible experiments.

Keep credentials in the environment. Do not print keys, commit secrets, or automatically source a user's shell files. Synthetic eval fixtures are shareable; real tasks and run artifacts are local by default. Keep Markdown paragraphs unwrapped.
