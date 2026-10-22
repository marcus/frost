# Portable configuration examples

These files are examples. Copy them to `~/.config/frost/` (or point `--config` or `FROST_CONFIG` at a copy) and edit for your own access.

- `catalog.example.json`: neutral model records with identity, output contracts, input modalities, and generation mechanism. It carries no measurements. `catalog-build` can replace it with a versioned, source-attributed catalog.
- `frost.example.toml`: operator configuration. It encodes Marcus's ordinal quality and cost impressions as of September 2026 so the pilot can be reproduced. Every such number is an **operator prior** and is labeled that way in results; nothing in the file is measured performance, pricing, or verified native mapping. Latency classes are coarse priors as well.
- `generated-profiles.example.json`: the shape a producer such as rook writes for `generated_profiles_file` (see [Generated profiles](#generated-profiles)). The example config mentions it in a comment only; copy it beside your config and uncomment the key to try it.
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

### Several capacity producers

Each producer writes its own snapshot file. List them all; `capacity_file` keeps working and, when set, is the first entry:

```toml
capacity_file  = "capacity.json"        # codexbar wrapper, unchanged
capacity_files = ["rook-capacity.json"] # rook: local fleet pools
```

Every file is read, parsed, and checked against the declared topology on its own. A producer whose file is missing, unparseable, or names a pool or window the config does not declare contributes nothing, and only the pools it would have reported are unknown; a note on stderr names the file. Unknown never means exhausted, so a dead producer cannot exclude a profile, and the other producers' pools keep deciding. Freshness is per pool and so per producer: each pool is judged by its own `observed_at`, by its `valid_until` when the producer sets one, and by `max_snapshot_age_minutes`, whichever expires first. A fast-moving producer such as the local fleet should set a short `valid_until` (a minute or two); codexbar's fifteen minutes stays as it is.

Each pool should have one owner. If two files report the same pool, the file earlier in the list wins for that pool and the other is ignored for it, with a note. The merged snapshot's hash in provenance is the file's own hash when one producer contributed and a digest of the contributing hashes in list order otherwise.

`--capacity PATH` may be repeated; explicit paths replace the configured list and each must be valid or the route exits 2. `frost capacity check [PATH...]` reports every producer (`ok`, `missing`, or `invalid`) and then the merged pools and profiles; it exits 2 if any listed producer is unusable, since it is a check, while `frost route` carries on with the rest. Its JSON gains a `producers` array; `snapshot`, `snapshot_hash`, `generated_at` and `producer` describe the merged view.

### Local fleet pools

A machine or fleet is a pool of `kind = "local"`, `marginal_cost = "included"`, and its health is expressed as windows whose `remaining_percent` is a headroom percentage, with no `resets_at`:

```toml
[[pools]]
id = "host-5090"
kind = "local"
marginal_cost = "included"
mapping_verified = true
required_window_ids = ["online", "vram_free", "queue_slots"]
```

| Window | `remaining_percent` |
| --- | --- |
| `online` | 100 when the host answers its health check, 0 when it does not |
| `vram_free` | free VRAM as a percentage of what the placed models need or the card holds |
| `queue_slots` | 100 × (1 − queued ÷ allowed) |

A window below `reserve_percent` or at 0 marks the pool exhausted, so an offline or saturated host excludes the profiles bound to it (or demotes them, under `--availability demote`). A producer that cannot reach the host says `source_status: error` or omits the pool, which is unknown, not exhausted. Local windows never reset, so they never earn the expiry preference. [examples/capacity/README.md](../examples/capacity/README.md) has the producer-side contract and a sample snapshot.

### Expiring API credit

A monthly API credit that refreshes each billing cycle with no rollover is an expiring included-usage pool. Declare it `prepaid`, mark its one window non-rollover and expiry-eligible, and give it a horizon long enough to matter:

```toml
[[pools]]
id = "anthropic-credit"
kind = "prepaid"
marginal_cost = "included"          # spending it costs nothing extra until it runs out
mapping_verified = true
required_window_ids = ["monthly"]
expiry_preference_window_ids = ["monthly"]
non_rollover_window_ids = ["monthly"]
expiry_horizon_hours = 168          # optional; default is policy.capacity.expiry_horizon_hours
```

Bind it by listing it in `pool_ids` on the profile that spends it, normally a gateway profile whose alias the gateway routes to the Anthropic API key funded by the credit. With `prefer_expiring_included_usage = true`, once the window's `resets_at` is inside the horizon and it still has headroom above `reserve_percent`, that profile is preferred over otherwise-tied profiles: the preference sits where it always has, after the quality floor and the policy objective and before cost, so it never admits an inadequate model. `expiry_horizon_hours` on a pool overrides the policy horizon for that pool only, because 24 hours of lead is too little for a monthly allowance to be worth spending down. The snapshot reports the window as percent remaining of the grant (`examples/capacity/credit-snapshot.sh` computes it from a grant, month-to-date spend, and the cycle's end). When the grant is exhausted, or the snapshot is missing or stale, the profile is excluded or unknown by the usual rules.

## Gateway profiles

A profile whose `access_surface` is `gateway` is executed by calling an OpenAI-compatible gateway, such as a LiteLLM proxy, with a model alias. It names the alias and which gateway serves it:

```toml
[[gateways]]
id = "litellm"
base_url = "http://aerie.local:4000/v1"

[[profiles]]
id = "gw-code"
model = "deepseek-4.1-flash"     # still a catalog model: evidence and capabilities come from it
access_surface = "gateway"
alias = "code"                    # sent as `model` to the gateway
gateway = "litellm"               # a [[gateways]] id ...
# gateway_url = "http://aerie.local:4000/v1"   # ... or a direct URL; exactly one of the two
mapping_verified = true
effort_mode = "none"
pool_ids = ["host-5090"]
```

Rules, all checked by `frost config check`: a gateway profile requires `alias` (1–128 characters, no whitespace) and exactly one of `gateway` and `gateway_url`; `alias`, `gateway`, and `gateway_url` are errors on any other surface; a named gateway must exist; `base_url` must be an absolute http or https URL with a host and no credentials, query, or fragment. Gateway IDs are unique.

Selection treats a gateway profile like any other: the quality floor, effort, capabilities, pools, and the policy order are unchanged. What changes is the output. A recommended or alternative gateway profile carries a `gateway` object, and no other surface does:

```json
"recommendation": {
  "profile_id": "gw-code",
  "access_surface": "gateway",
  "mapping_verified": true,
  "gateway": {"id": "litellm", "base_url": "http://aerie.local:4000/v1", "alias": "code"}
}
```

`recommendation.gateway.alias` and `recommendation.gateway.base_url` are the stable fields a caller reads: send the request to `base_url` with `model` set to `alias`. `id` is present only for a named gateway. Check `mapping_verified` before trusting an alias the operator has not confirmed.

A caller that can only execute gateway aliases must say so, or frost may recommend a CLI profile it cannot run. `--surface gateway` (or `"access_surfaces": ["gateway"]` in a `--request` object, or `frost route --replay --surface gateway`) restricts candidates to those surfaces; excluded profiles are listed with the reason, and when none qualifies the result is `no_match` with exit 3. A LiteLLM pre-call hook therefore runs:

```sh
frost route --json --stdin --surface gateway --availability exclude < prompt.txt | jq -r '.recommendation.gateway.alias'
```

and rewrites `model: "auto"` to the printed alias, falling back to its own default on a non-zero exit.

## Generated profiles

An inventory tool can write the profiles it knows how to execute, so the config never drifts from what is installed. Point the config at the file:

```toml
generated_profiles_file = "profiles.generated.json"   # relative to this file; optional
```

The file is JSON, written atomically by the producer (rook), with the same fields as the TOML tables:

```json
{
  "schema_version": 1,
  "generated_at": "2026-10-21T20:00:00Z",
  "producer": "rook",
  "gateways": [{"id": "litellm", "base_url": "http://aerie.local:4000/v1"}],
  "pools": [{"id": "host-5090", "kind": "local", "marginal_cost": "included", "mapping_verified": true,
             "required_window_ids": ["online", "vram_free", "queue_slots"]}],
  "profiles": [{"id": "rook-local-code", "model": "deepseek-4.1-flash", "access_surface": "gateway",
                "alias": "code", "gateway": "litellm", "pool_ids": ["host-5090"],
                "mapping_verified": true, "effort_mode": "none", "latency_class": "fast",
                "capabilities": ["repository_tools", "web_research"],
                "prior": {"quality_rank": 4, "cost_rank": 13}}]
}
```

Parsing is strict: unknown fields, trailing data, a different `schema_version`, a malformed `generated_at`, or a file over 1 MiB reject the whole file. `gateways`, `pools`, and `profiles` take every field their TOML counterparts do. Because `model` must resolve in the catalog and a pool's windows are operator-visible topology, a producer that emits a local model also needs that model in `catalog_file`.

**Precedence.** Operator declarations always win, per kind (profile, pool, gateway) and by `id`. A generated entry with an operator's ID is ignored, which is also how you retire or tune one: declare a profile with that ID, for instance with `enabled = false`. An ID that appears more than once in the generated file is ambiguous, so every entry with it is ignored, and an entry with no ID is ignored. Surviving generated entries are appended after the operator's, and generated profiles are marked `"source": "generated"` in `frost profiles list --json`.

**A producer bug never stops routing.** The file is optional input. A missing file, an unreadable one, or a generated entry that fails validation (unknown model, missing alias, bad URL, a pool or gateway that does not exist, an invalid pool) is dropped and reported as a warning with `Source: "generated"`; routing continues over the operator's profiles and the generated ones that are valid. Dropped entries are printed to stderr by `frost route` and listed by `frost config check`, which still exits 0 for them. Deliberate overrides are reported at `info` severity. The file's hash is recorded in each decision's `provenance.generated_profiles_hash`.

`frost config check` shows the generated file's path, producer, and how many of its profiles are in use; `--json` adds `generated_profiles` and `capacity_files`.

`[policy.capacity]` controls freshness, reserve, whether estimated observations count, expiry preference, and whether a fresh exhausted pool excludes or demotes a profile. `--availability exclude|demote|ignore` overrides the enforcement choice for one route. Profiles and pools are configured statically here; a snapshot can report observations for those IDs but cannot add bindings or redefine which windows limit a profile. Use `frost capacity check [PATH] --json` to inspect pool and profile availability without making a TypeSafe request.

The included CodexBar producer is optional and lives under `examples/capacity/`. Its checked-in bindings distinguish verified main Codex, Claude, Fable, and OpenCode Go pools from the unverified Spark pool; Antigravity and Grok remain unbound until their window IDs are confirmed. Capacity affects only candidates that already meet the quality floor and never promotes a result from `provisional` to `recommended`.
