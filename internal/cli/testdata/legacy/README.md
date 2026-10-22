# Legacy regression fixtures

A frozen copy of the pre-gateway configuration shape: `config.toml` (the September 2026 example), its catalog and question specification, and a snapshot with an expiry-eligible pool (`codex-main`) and an exhausted one (`claude-fable`). `TestLegacyDecisionsGolden` routes a fixed matrix through these files with a pinned clock and compares the decisions to `golden.json`. Features added later must leave these decisions byte-identical; regenerate the golden only for an intentional behavior change (`go test ./internal/cli -run Golden -update`).
