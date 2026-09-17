# Catalog metrics

Adequacy rules in operator config may reference only metrics listed here. Each metric names its unit and direction; `metric_version` is the benchmark split or methodology version, never the fetch date.

| Metric | Version | Unit | Direction | Task family | Source connector |
| --- | --- | --- | --- | --- | --- |
| `swebench.verified.resolve_rate` | `verified-2024-08` | fraction 0-1 | higher is better | `software_change` | swebench (one row per Verified submission with one attempt; `effort` from `reasoning_effort`, `harness` from `agent`, `observed_at` from `date`) |
| `price.input_usd_per_mtok` | `1` | USD per million tokens | lower is better | none | models.dev (`provider` = the model's own organization), artificialanalysis (`provider` = `artificialanalysis-median`) |
| `price.output_usd_per_mtok` | `1` | USD per million tokens | lower is better | none | same as above |
| `aa.intelligence_index` | AA `intelligence_index_version` (for example `4.3`) | index | higher is better | none | artificialanalysis, restricted |
| `aa.coding_index` | same | index | higher is better | none | artificialanalysis, restricted |
| `aa.agentic_index` | same | index | higher is better | none | artificialanalysis, restricted |
| `aa.output_tokens_per_second` | same | tokens per second | higher is better | none | artificialanalysis, restricted |
| `aa.ttft_seconds_median` | same | seconds | lower is better | none | artificialanalysis, restricted |

Conventions:

- `source` on every measurement is `<connector>:<source record id>` so a value relayed by a second site is never counted twice and a failed source can retain its previous rows.
- Artificial Analysis publishes one record per effort variant. The connector reads the effort from the record name ("(high)", "High Effort") into `effort`; the base record (max effort) has `effort` set to `max`, which no Frost profile declares, so it never satisfies an `exact` rule by accident.
- Restricted metrics are written only to the `--restricted-out` catalog.
