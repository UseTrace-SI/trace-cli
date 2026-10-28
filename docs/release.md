# Release setup

The release workflow builds pure-Go archives for macOS and Linux on amd64 and arm64, writes checksums, signs the checksum file with keyless cosign, generates a Homebrew formula from the archive checksums, and publishes it to `UseTrace-SI/homebrew-tap`. The formula installs `trace` and the `trc` alias.

The GitHub repository and the `UseTrace-SI/homebrew-tap` repository must exist. Add a repository secret named `TAP_GITHUB_TOKEN` with permission to write contents in the tap. The release workflow's `GITHUB_TOKEN` publishes archives in the source repository, and its `id-token: write` permission supports keyless signing.

Run `make lint test test-structure test-sync build e2e perf` before release. CI runs the lint, race, Tree-sitter, sync client, end-to-end, and performance checks on pull requests. On macOS and Ubuntu it also builds all four GoReleaser snapshot archives, installs the current platform archive through `scripts/install.sh`, verifies its checksum, and runs the end-to-end harness with the installed binary. With the private app checked out beside the CLI, `make test-sync-integration` checks the client and Worker together. A maintainer can then create a `v1.0.0` tag in the Git repository; the tag triggers `.github/workflows/release.yml`. Trace itself never writes commits, branches, or tags.

`goreleaser release --snapshot --clean` can validate packaging in a real checkout. It cannot run in a directory without Git history. The Homebrew tap and companion sync Worker are separate repositories.
