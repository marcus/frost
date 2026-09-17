# External capacity snapshots

Frost's proposed quota input is independent of CodexBar. This optional converter turns a CodexBar usage array into the draft neutral schema in the [plan](../../docs/plans/planning/model-router.md#capacity-input).

```sh
codexbar usage --provider codex --source oauth --json |
  jq --slurpfile bindings examples/capacity/bindings.json \
     -f examples/capacity/codexbar-to-frost.jq
```

The example bindings require confirmation. They are not authoritative mappings of providers to models or quota pools. They deliberately omit Spark because its pool-to-model mapping has not been verified. A missing five-hour window remains `null`; it does not become 100% remaining. Multiple accounts for one provider produce an ambiguous-account result until the producer is given an explicit account mapping. Missing or failed providers retain unknown windows. Account identities, credit identifiers, credentials, and raw errors are not copied into the output.

The bindings file only tells this example producer which source fields to read. Frost's static configuration will own the authoritative profile-to-pool relationship, required window IDs, and which weekly/monthly windows drive expiry preference. A snapshot cannot change that topology. Match the IDs between the producer and the static configuration before using the output.

The validity period is an initial 15-minute policy choice, not a promise from CodexBar. Producers must preserve the source observation time: rewriting the file must not make old usage look new. Check the source command's exit status before publishing a snapshot; when using a shell pipeline, enable `pipefail`. A production producer should stage the complete snapshot and rename it atomically.

This is a format experiment. The routing probe does not yet consume it. Anyone can implement a producer; the Frost executable will accept normalized files and will never start CodexBar or run a configured shell command.
