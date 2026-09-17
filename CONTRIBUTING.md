# Contributing to Frost

Use the Go version declared in `go.mod`. Start with `make build`, then run `make check` before submitting a change. The checks use committed fixtures and do not need API credentials. Live task analysis needs `TYPESAFE_API_KEY` in the environment; do not put credentials in config files or source control.

Keep selection policy in `internal/router`, which has no I/O. CLI behavior belongs in `internal/cli`; external task analysis, catalog sources, and capacity producers stay behind their existing adapters. Asking for a recommendation must never launch the recommended model. Changes to the JSON contract need focused tests and documentation.

Describe the behavior that changes, why it changes, and the verification performed in your pull request. Use synthetic tasks and fixtures for reproductions. Task recordings contain the submitted task text; real recordings, account usage snapshots, and Artificial Analysis payloads should stay outside commits. `.local/` is ignored for local experiments.

Model facts, operator profiles, and transient capacity have different owners. Do not turn vendor claims into independent benchmark results or remove provisional labels without supporting evidence. See the [source notices](tools/catalog-build/NOTICES.md) before redistributing data; Frost's code license does not replace source-data terms.

The [plan index](docs/plans/README.md) records the current scope and deferred work. Repository-maintainer agents also follow [AGENTS.md](AGENTS.md).

## Release checks

`make release-verify` builds snapshot archives for macOS and Linux on amd64 and arm64, checks their contents and checksums, and smoke-tests the host archive. It publishes nothing. GoReleaser is required in addition to Go.

For an authorized release, write the changelog entry, choose `RELEASE_VERSION=vX.Y.Z`, and inspect `make release-dry-run`. The release scripts require a clean, synchronized main branch, passing checks, and an annotated tag targeting live main. `make release` publishes the GitHub release and source-built Homebrew formula through the existing scripts; `make release-tap` resumes the tap step for an existing verified release. Do not use snapshot archives as release assets.
