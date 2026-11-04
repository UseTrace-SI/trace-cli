# Release setup

This guide is for maintainers. Users install the coordinated release from [the hosted installer](../README.md#install).

## Packaging

The legacy standalone release workflow builds pure-Go archives for macOS and Linux on amd64 and arm64, writes checksums, signs the checksum file with keyless cosign, and generates a Homebrew formula. When a tap token is configured, it publishes the formula to `UseTrace-SI/homebrew-tap`. The formula installs `trace` and the `trc` alias.

## Credentials

The release workflow's `GITHUB_TOKEN` publishes archives in the source repository, and its `id-token: write` permission supports keyless signing. Homebrew publishing needs a repository secret named `TAP_GITHUB_TOKEN` with permission to write contents in `UseTrace-SI/homebrew-tap`. Without that secret, the formula publish step skips and the signed binary archives remain available; publish the generated formula to the tap by hand.

## Verify and tag

Run `make lint test test-structure test-sync build e2e perf` before release. CI runs the lint, race, Tree-sitter, sync client, end-to-end, and performance checks on pull requests. On macOS and Ubuntu it also builds all four GoReleaser snapshot archives, installs the current platform archive through `scripts/install.sh`, verifies its checksum, and runs the end-to-end harness with the installed binary. With the private app checked out beside the CLI, `make test-sync-integration` checks the client and Worker together. A maintainer can then create a `vX.Y.Z` tag in the Git repository; the tag triggers `.github/workflows/release.yml`. Trace itself never writes commits, branches, or tags.

`goreleaser release --snapshot --clean` can validate packaging in a real checkout. It cannot run in a directory without Git history. The Homebrew tap and companion sync Worker are separate repositories.

The connected solo release is built by trace-platform from its pinned stack.lock.json and includes CLI, Brain and Graph. The public installer at https://usetrace.si/install.sh installs that bundle. The Homebrew generator and standalone workflow above are retained for legacy release tooling; do not advertise them as the coordinated release.

## Production acceptance for the coordinated release

The candidate `1.1.0-solo.3` adds import security, complete portable-history field
coverage, bounded capture and upload previews. The existing user working tree
may contain unrelated deleted plans or generated output; never discard or commit
those to make a release clean. Commit only the reviewed change set and build from
isolated clean checkouts of those commits. Pin companion revisions in the platform
lock after committing them, and retain the previous coordinated release.

Production deployment also requires current source-matched acceptance evidence:
security scans, real browser coverage, successful Claude/Codex capture, a complete
staging backup recovery, and delivered alert verification. A source build or a
failed model turn is not that evidence. See platform `docs/operations.md` for the
receipt schema and the outstanding operator steps. Release qualification must
include hook p95, idle daemon RSS, failure/restart cases and macOS/Linux CI.
