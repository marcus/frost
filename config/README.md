# Portable configuration examples

These files are examples. Copy them to `~/.config/frost/` (or point `--config` or `FROST_CONFIG` at a copy) and edit for your own access.

- `catalog.example.json`: neutral model records with identity, output contracts, input modalities, and generation mechanism. It carries no measurements. A public-data producer replaces it with a versioned, source-attributed catalog.
- `frost.example.toml`: operator configuration. It encodes Marcus's ordinal quality and cost impressions as of September 2026 so the pilot can be reproduced. Every such number is an **operator prior** and is labeled that way in results; nothing in the file is measured performance, pricing, or verified native mapping. Latency classes are coarse priors as well.
- `questions-v3.json`: the analyzer question specification the config references. Question IDs are a contract with the policy; changing the file changes its hash and invalidates recorded replays.

The strict parsers reject unknown keys, so provenance lives in `version` and in this file rather than in ad hoc fields.
