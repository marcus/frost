# Recorded source fixtures

`2026-09-16/` holds payloads recorded on September 16, 2026 (UTC September 17) and trimmed to the models named in `../overlay.json` plus a few deliberately unmapped records used by the alias-proposal tests.

- `models.dev/`: `models.json` and `api.json` trimmed to the overlay's models and their own organization providers; the `benchmarks` arrays were removed because the producer does not import them.
- `swebench/`: the Verified board reduced to eight real rows (recent single-attempt submissions, one multi-attempt row, one row without a reasoning effort) and one Lite row that must be ignored. None of the operator's current models appears on the board; the tests map older model tags through a test overlay.
- `artificialanalysis/`: synthetic. Same response shape as the free-tier endpoint, invented numbers, two pages. Real Artificial Analysis payloads are restricted and are never committed.

Re-record with `catalog-build fetch --source <name> --record <dir>` and trim by hand or with the same `jq` filters; each source directory carries a `meta.json` with URL, fetch time, and digest per file.
