# Portable configuration examples

These files are examples. Copy them to `~/.config/frost/` (or point `--config` or `FROST_CONFIG` at a copy) and edit for your own access.

- `catalog.example.json`: neutral model records with identity, output contracts, input modalities, and generation mechanism. It carries no measurements. A public-data producer replaces it with a versioned, source-attributed catalog.
- `frost.example.toml`: operator configuration. It encodes Marcus's ordinal quality and cost impressions as of September 2026 so the pilot can be reproduced. Every such number is an **operator prior** and is labeled that way in results; nothing in the file is measured performance, pricing, or verified native mapping. Latency classes are coarse priors as well.
- `questions-v3.json`: the analyzer question specification the config references. Question IDs are a contract with the policy; changing the file changes its hash and invalidates recorded replays.

The strict parsers reject unknown keys, so provenance lives in `version` and in this file rather than in ad hoc fields.

## Produced catalogs

`catalog-build` (see `tools/catalog-build/`) replaces the example catalog with one built from public sources: models.dev for identity, capabilities, context limits, and prices; SWE-bench Verified for `software_change` measurements; Artificial Analysis, when `ARTIFICIAL_ANALYSIS_API_KEY` is set, for indices, speed, and prices written only to a restricted catalog. Identity mapping is the reviewed `tools/catalog-build/overlay.json`; local tweaks that must survive a refresh go in `~/.config/frost/catalog.overrides.json` (per-model field `set`, `add_measurements`, `remove_measurements`). A refresh also writes `latency.suggestions.json` next to the catalog: a coarse class per model derived from public medians, a prior for each profile's `latency_class`.

The example config ships two enabled `software_change` adequacy rules keyed on `swebench.verified.resolve_rate`. Their thresholds are provisional and the SWE-bench board has no rows for the current models as of September 2026, so those rules fall through to the operator prior (`on_missing = "provisional_only"`) until measurements arrive.
