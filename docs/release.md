# Release setup

Users install the coordinated CLI, Brain and Graph bundle from
[the hosted installer](../README.md#install). The current production version is
`1.1.0-solo.4`.

## Homebrew

The public CLI repository also contains its coordinated formula. Use an explicit
repository URL, as supported by [Homebrew custom taps](https://docs.brew.sh/Taps):

```sh
brew tap usetrace-si/trace https://github.com/UseTrace-SI/trace-cli
brew install usetrace-si/trace/trace
brew test usetrace-si/trace/trace
```

The formula downloads the exact hosted archives and installs all three executables
plus `trc`. The old `usetrace-si/tap` repository is unavailable; its legacy formula
installed only the standalone CLI. Existing users can keep using the hosted
installer under `~/.local/bin`; do not overwrite Homebrew-managed binaries with it.
Use the fully qualified formula name for a fresh Homebrew installation.

## Publish qualified artifacts

The private platform builds all four target bundles from committed source pins.
Qualify and deploy that coordinated version first. Review the resulting public
`stack.json` and checksum inventory, then generate the matching formula:

```sh
go run ./scripts/homebrew --version 1.1.0-solo.4 --checksums /private/path/checksums.txt --out Formula/trace.rb
```

Commit the formula and release tooling before creating a `vX.Y.Z` publication tag.
The tag workflow downloads the hosted archives with byte limits, verifies their
hashes, required binary members, compiler/source metadata and embedded manifests,
and refuses missing targets or ambiguous checksums. It also verifies that the
published CLI source is an ancestor of the publication tag and that `cmd`,
`internal`, `go.mod` and `go.sum` have not changed. Packaging/docs-only commits
can therefore publish the already-tested binaries without rebuilding them.

The publication tooling sends a `trace-cli-release` User-Agent because the hosted
origin's bot protection answered urllib's default `Python-urllib/3.x` agent with
HTTP 403 while curl, an empty agent and a descriptive agent were accepted. If a tag
run fails for a tooling reason, do not move or recreate the signed tag. Fix the
tooling on `main` (the manual trigger exists once the updated workflow is on the
default branch), then run
`gh workflow run release.yml --ref main -f tag=v<version>` (or Actions, Release,
Run workflow, using the workflow from `main`) and follow it with `gh run watch`.
The manual run refuses any branch other than `main`, requires the tag to be an
ancestor of `main`, checks the hosted source against the tag's own `cmd`,
`internal`, `go.mod` and `go.sum`, regenerates the formula with `main`'s tooling
and publishes with `--verify-tag`. It marks the release as Latest, so use it only
for the newest publication tag. Its cosign bundle carries the `refs/heads/main`
workflow identity rather than the tag's.

The workflow signs the checksum file with keyless cosign and publishes those
same archives, source manifest, checksums, signing bundle and formula to GitHub.
Its standard repository `GITHUB_TOKEN` and OIDC permission are sufficient. No
cross-repository tap token is required. The checked-in formula must match the
published version exactly. Existing release assets are never replaced by the
workflow; retries against an already-created release require review.

Tag the exact four source commits from `stack.json` separately as
`stack/<version>` in their respective repositories. Those provenance tags do not
trigger standalone companion publication, although they do run the CI workflow,
including its release-snapshot job (see Validation). A `v<version>` CLI publication tag
identifies the release tooling commit; the archived `stack.json` remains the
source of truth for the exact binary revisions.

## Validation

Run `make lint test test-structure test-sync build e2e perf` before runtime changes
are released. Run `python3 scripts/prepare_coordinated_release_test.py` for
publication validation. Linux/macOS CI checks race safety, optional parsing,
repeated companion integration, sync, end-to-end behavior, release snapshots and
performance. The macOS snapshot job also performs a fresh coordinated Homebrew
installation and tests all three executables. GoReleaser snapshots remain
standalone CLI fixtures; their installer test explicitly opts out of companions.
Snapshots built from `stack/<version>` provenance tags drop that prefix so archive
names stay flat and the installer's version check passes.
The normal installer requires the complete coordinated bundle.

The platform deployment gate requires current security scans, browser checks,
real Claude/Codex capture, application-level staging recovery and delivered alert
evidence. Preserve raw acceptance receipts and record any reviewed equivalence
of unchanged execution inputs rather than rewriting their source metadata.
Successful Codex interruption, daemon crash/restart, same-thread resume and exact
file restoration were verified on macOS for solo.4. Other provider/platform
lifecycle coverage remains explicit in the README.

Review user changes before committing them. Build production archives from clean
committed checkouts, pin companion revisions after committing them, and retain
previous release archives. Private backup files, recovery vaults, credentials and
owner receipts belong outside these repositories.

Local object writes flush contents before atomic publication. Checkpoints do not
force every parent-directory metadata update to stable storage and do not promise
recovery from every sudden power loss. Maintain verified backups. The owner has
verified local encrypted backups and recovery-key escrow; an off-device copy is
still needed for loss of the Mac.
