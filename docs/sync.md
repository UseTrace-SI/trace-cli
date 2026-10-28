# Optional sync client

Sync is off by default. The CLI never contacts a sync server until `trace login` has completed and you run `trace sync`. The companion Cloudflare server is maintained in a separate private `trace-app` repository.

The GitHub OAuth app must have device flow enabled. Set its client ID and a Trace sync HTTPS endpoint, then authenticate for a repository:

```sh
export TRACE_GITHUB_CLIENT_ID=YOUR_PUBLIC_CLIENT_ID
export TRACE_SYNC_URL=https://sync.example.com
trace login --remote owner/repo --mode pro
```

`trace login` shows a GitHub verification code and stores the resulting token in the operating system credential store (Keychain on macOS, Secret Service through `secret-tool` on Linux). The `.trace/login.json` file contains only the endpoint, repository, and mode. The OAuth token is never written to Trace's database. The OAuth app requests `read:user` and `repo` scopes so the server can check repository access.

For Pro, supply a passphrase of at least 12 characters when syncing:

```sh
export TRACE_SYNC_PASSPHRASE='a long passphrase kept outside the repo'
trace sync
trace sync --watch
```

Trace derives a 256-bit key with scrypt using a random 32-byte salt in `.trace/sync-salt`. Rows and blobs are encrypted with XChaCha20-Poly1305 before upload. The key and passphrase are never uploaded. Losing the passphrase prevents Pro recovery. Team mode uses HTTPS and sends plaintext to the server for its server-side encryption and repository access control.

To recover uploaded history, sign in from an initialized Trace checkout and pull into a separate, existing Git checkout that does not have a `.trace` directory:

```sh
trace sync pull --into /path/to/fresh-checkout
trace --repo /path/to/fresh-checkout log
```

For Pro, set `TRACE_SYNC_PASSPHRASE` to the original passphrase before pulling. The server returns each row's salt, so a separate salt file transfer is unnecessary. Trace verifies decryption and blob hashes, then publishes the recovered `.trace` history only when the download is complete. It does not change working-tree files; use `trace rewind <step>` in the recovered checkout after inspecting the timeline. Team recovery requires current GitHub read access. Existing `.trace` history is never overwritten.

## Server protocol

The client sends `Authorization: Bearer <GitHub OAuth token>` to the configured HTTPS endpoint. The server must validate that token and repository access. All requests and responses use JSON and HTTP 200 for success.

- `POST /v1/sync/blobs/have`: `{"repo":"owner/repo","mode":"pro|team","hashes":["sha256",...]}`. Up to 500 hashes; response `{"need":["sha256",...]}`. The response may only name hashes from the request.
- `POST /v1/sync/blobs`: `{"repo":"owner/repo","mode":"pro|team","hash":"sha256","payload":"base64","salt":"base64"}`. For Pro, payload is a random 24-byte nonce followed by XChaCha20-Poly1305 ciphertext, with `blob:<hash>` as associated data. Salt is present only for Pro. For Team, payload is base64 plaintext. The server should make this operation idempotent by hash.
- `POST /v1/sync/rows`: `{"repo":"owner/repo","mode":"pro|team","rows":[{"table":"sessions|turns|keyframes|ops|commits","id":"...","data":{...}}],"salt":"base64"}`. Up to 500 rows. Pro replaces `data` with `ciphertext`, using `<table>:<id>` as associated data. The server should make this operation idempotent by table and ID.
- `GET /v1/sync/rows?repo=owner/repo&mode=pro|team&table=sessions&after=ID`: up to 20 rows and a `next` cursor. Team returns decrypted row data after checking read access; Pro returns client ciphertext and its salt.
- `GET /v1/sync/blobs/list?repo=owner/repo&mode=pro|team&after=HASH`: up to 100 blob hashes and a `next` cursor.
- `GET /v1/sync/blobs?repo=owner/repo&mode=pro|team&hash=HASH`: base64 blob payload. Team returns decrypted bytes after checking read access; Pro returns client ciphertext and its salt.

Only blobs referenced by retained keyframes or operations are offered to the server. Blobs are uploaded before rows. The client keeps row requests below 7 MiB, within the Worker's 8 MiB request limit, and records a separate cursor for each table only after an acknowledged row batch. Switching endpoint, repository, or mode resets those cursors. Uploads are append-only. Recovery downloads rows and blobs into a fresh Trace history.

No public sync endpoint or OAuth app registration is configured yet. Live sync requires deploying the companion app and registering a GitHub OAuth app with device flow enabled.
