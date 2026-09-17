# Portable configuration examples

These files are examples. Copy them to `~/.config/frost/` (or point `--config` or `FROST_CONFIG` at a copy) and edit for your own access.

- `catalog.example.json`: neutral model records with identity, output contracts, input modalities, and generation mechanism. It carries no measurements. `catalog-build` can replace it with a versioned, source-attributed catalog.
- `frost.example.toml`: operator configuration. It encodes Marcus's ordinal quality and cost impressions as of September 2026 so the pilot can be reproduced. Every such number is an **operator prior** and is labeled that way in results; nothing in the file is measured performance, pricing, or verified native mapping. Latency classes are coarse priors as well.
- `questions-v3.json`: the analyzer question specification the config references. Question IDs are a contract with the policy; changing the file changes its hash and invalidates recorded replays.

The strict parsers reject unknown keys. Provenance lives in `version` and in this file.

## Produced catalogs

`catalog-build` (see `tools/catalog-build/`) replaces the example catalog with one assembled from separately licensed sources: models.dev for identity, capabilities, context limits, and prices; SWE-bench Verified for `software_change` measurements; and Artificial Analysis, when `ARTIFICIAL_ANALYSIS_API_KEY` is set, for indices, speed, and prices written only to a restricted catalog. Identity mapping is the reviewed `tools/catalog-build/overlay.json`; unmatched IDs are reported for review and receive no automatic mapping.

```sh
# Offline and reproducible from the checked-in fixtures.
catalog-build refresh --from-fixtures tools/catalog-build/testdata/2026-09-16 \
  --out /tmp/frost-catalog.json

# Live refresh; add --dry-run to review without publishing.
catalog-build refresh --out ~/.config/frost/catalog.json

# Optional local-only enrichment when ARTIFICIAL_ANALYSIS_API_KEY is set.
catalog-build refresh --out ~/.config/frost/catalog.json \
  --restricted-out ~/.config/frost/catalog.local.json
```

Run those commands from the source checkout, or pass `--overlay /absolute/path/to/tools/catalog-build/overlay.json`. `make install` installs the two executables but leaves source assets in the checkout. Release archives include the overlay, metrics registry, notices, configuration, and capacity example. The Homebrew package places them under `$(brew --prefix frost)/share/frost/`.

Local changes that must survive a refresh go in `~/.config/frost/catalog.overrides.json` or the path passed to `--overrides`. Per-model `set`, `add_measurements`, and `remove_measurements` operations are applied last and labeled in the diff. A successful publish validates and atomically replaces the destination, keeps the prior file as `catalog.previous.json`, and writes `latency.suggestions.json` beside the restricted catalog when `--restricted-out` is set, or beside the public catalog otherwise.

Source-data terms remain separate from Frost's MIT license. models.dev retains its MIT notice; the SWE-bench website data repository is CC BY-NC 4.0; Artificial Analysis free-tier data is internal-use and attribution-restricted and never belongs in the distributable catalog or committed fixtures. Review `tools/catalog-build/NOTICES.md` before redistributing generated data.

### Latency suggestions

`latency.suggestions.json` is a configuration-review aid, not a runtime measurement or evidence that a response-time target will be met. Without `--restricted-out`, it is labeled `data_usage: public` and contains only `unknown` classes, with no Artificial Analysis values, source metadata, or effort classes. With `--restricted-out`, it is written beside that restricted catalog, labeled `data_usage: restricted_local_only`, and describes its basis as restricted local AA data. Keep that file local; if both catalogs share a directory, do not redistribute the directory as an artifact set. A failed or skipped AA refresh retains the last-good restricted suggestions. `frost config check` looks for the file next to the resolved `catalog_file`.

- A missing suggestions file is optional: the check stays successful and reports that the file was not found.
- Invalid JSON, schema versions, or classes produce warnings but do not make the configuration invalid.
- A profile warning appears only when its configured class is at least two tiers faster than the applicable suggestion in the order `extra_fast`, `fast`, `medium`, `slow`.
- Fixed-effort profiles use the exact `by_effort` suggestion when present. Configurable profiles check their declared effort options; profiles with no effort setting use the model-level class. An unknown or one-tier difference does not warn.

The example config ships two enabled `software_change` adequacy rules keyed on `swebench.verified.resolve_rate`. Their thresholds are provisional and the SWE-bench board has no rows for the current models as of September 2026, so those rules fall through to the operator prior (`on_missing = "provisional_only"`) until measurements arrive.

## Capacity configuration

The example config points `capacity_file` at `capacity.json` in the same directory. A missing configured file produces a diagnostic while the check and route continue successfully, so a producer can populate it later. An explicitly supplied `--capacity` file must parse and match the configured pool/window topology or the route exits 2.

`[policy.capacity]` controls freshness, reserve, whether estimated observations count, expiry preference, and whether a fresh exhausted pool excludes or demotes a profile. `--availability exclude|demote|ignore` overrides the enforcement choice for one route. Profiles and pools are configured statically here; a snapshot can report observations for those IDs but cannot add bindings or redefine which windows limit a profile. Use `frost capacity check [PATH] --json` to inspect pool and profile availability without making a TypeSafe request.

The included CodexBar producer is optional and lives under `examples/capacity/`. Its checked-in bindings distinguish verified main Codex, Claude, Fable, and OpenCode Go pools from the unverified Spark pool; Antigravity and Grok remain unbound until their window IDs are confirmed. Capacity affects only candidates that already meet the quality floor and never promotes a result from `provisional` to `recommended`.
