# Verified offline plugins

Managed bundles contain `manifest.json`, `manifest.sig` and one executable named
`plugin`. Obtain the publisher's Ed25519 **public** key through a trusted channel,
then explicitly pin its PEM `PUBLIC KEY` file:

```sh
trace plugin trust --publisher example-publisher --key publisher.pem
trace plugin install /absolute/path/to/bundle
trace plugin list --json
trace plugin run example -- --option literal-value
```

The trust command reports the SHA-256 fingerprint of the raw public key. Private
key PEM files are rejected. Publisher names are local aliases for pinned keys.
Signature verification proves the pinned publisher and exact bytes; a plugin runs
as a trusted host executable with your environment and permissions. Managed
bundles are explicitly run through `plugin run`; PATH-discovered commands use the
existing separate discovery mechanism.

Example signed manifest:

```json
{
  "format": 1,
  "name": "example",
  "publisher": "example-publisher",
  "version": "1.2.3",
  "os": "linux",
  "arch": "amd64",
  "sha256": "EXACT_LOWERCASE_SHA256_OF_EXECUTABLE",
  "bytes": 12345
}
```

`manifest.sig` contains the base64-encoded Ed25519 signature over the exact raw
`manifest.json` bytes, including whitespace. Supported versions are three numeric
components without leading zeros. Platform values must match the running CLI:
`linux`/`darwin` and `amd64`/`arm64`. Unknown or duplicate manifest fields,
symlinked input files, platform mismatches and executables larger than 32 MiB are
rejected. Installation verifies before activating and never executes the bundle.

Install a newer signed bundle to update. The publisher cannot change during an
update, and a version cannot be republished with different signed manifest or
executable bytes. An explicit `--allow-downgrade` permits an older signed bundle.
`plugin trust --replace` explicitly revokes a different previous publisher key;
installed plugins then require reinstallation under the new key. Identical
version/manifest/binary bytes can be re-signed under that newly pinned key.

Before each run, Trace rechecks the publisher key, signature, manifest, executable
hash, ownership and permissions. Private state lives in `~/.trace/plugins` (or
`$TRACE_HOME/plugins`), with immutable version directories and atomic activation
receipts. Management writes use a nonblocking lock. Keep enough free disk space
for staging; installation retains at most ten releases per plugin.

```sh
trace plugin untrust example-publisher
trace plugin remove example
trace plugin prune example
```

Revocation prevents future verified runs. Removal deactivates a plugin while
retaining releases. Pruning keeps the active and previous releases; after removal
it removes complete marked releases. Unexpected content is preserved and reported.
Managed state is bounded to 50 active plugins, 64 publisher keys and 100 retained
plugin names. An online catalog, network downloader and automatic update service
are outside this offline implementation.
