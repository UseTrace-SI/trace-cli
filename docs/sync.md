# Optional sync client

## Connect a repository

Sync is off by default. `trace login` connects one repository but does not upload history unless you choose automatic sync or run `trace sync`. The companion Cloudflare service is maintained in the separate `trace-platform` repository.

From an initialized repository with a GitHub `origin`, run:

```sh
trace login
```

To opt in to background uploads after sign-in, add `--auto-sync`:

```sh
trace login --auto-sync
```

The repository daemon starts after login and uploads existing history as well as new turns in the background. It wakes after completed turns, backs off while idle, and retries network or authorization errors with increasing delays. Hook delivery remains local and does not wait for uploads. Stop future background uploads with `trace sync disable-auto`; one-shot `trace sync` remains available. Automatic sync starts only after the repository is explicitly connected with `--auto-sync`.

Trace discovers the repository and public sign-in settings from `https://usetrace.si`, then displays a GitHub device code. Pro mode is the default and keeps history encrypted on your device. For Team mode, which allows browsing history on the web, run `trace login --mode team` instead. Add `--auto-sync` to either command to upload local history in the background; without it, upload manually with `trace sync`.

For custom setups, `--remote owner/repo`, `--endpoint`, `--client-id`, `--github-app`, and `--auto-sync` are available. `TRACE_SYNC_URL` and `TRACE_GITHUB_CLIENT_ID` can also supply the endpoint and client ID. The server's GitHub App must have device flow enabled and access to the repository.

`trace login` shows a GitHub verification code and stores the resulting access and refresh credentials in the operating system credential store (Keychain on macOS, Secret Service through `secret-tool` on Linux). The `.trace/login.json` file contains the endpoint, repository, mode, public client ID, GitHub App mode, and automatic-sync choice. Tokens and the optional Pro encryption key are never written to Trace's database. GitHub App mode requests `offline_access` and renews expiring user tokens before sync and recovery. For an existing OAuth App, omit `--github-app`; that mode still requests `read:user` and `repo` scopes.

## Encryption modes

For Pro, supply a passphrase of at least 12 characters during login. Trace saves the derived key in the OS credential store for background use; the passphrase itself is not saved:

```sh
export TRACE_SYNC_PASSPHRASE='a long passphrase kept outside the repo'
trace login --auto-sync
```

Trace derives a 256-bit key with scrypt using a random 32-byte salt in `.trace/sync-salt`. Rows and blobs are encrypted with XChaCha20-Poly1305 before upload. The key and passphrase are never uploaded. Losing the passphrase prevents Pro recovery. Team mode uses HTTPS and sends plaintext to the server for its server-side encryption and repository access control.

## Recover history

To recover uploaded history, sign in from an initialized Trace checkout and pull into a separate, existing Git checkout that does not have a `.trace` directory:

```sh
trace sync pull --into /path/to/fresh-checkout
trace --repo /path/to/fresh-checkout log
```

For Pro, set `TRACE_SYNC_PASSPHRASE` to the original passphrase before pulling. The server returns each row's salt, so a separate salt file transfer is unnecessary. Trace verifies decryption and blob hashes, then publishes the recovered `.trace` history only when the download is complete. It does not change working-tree files; use `trace rewind <step>` in the recovered checkout after inspecting the timeline. Team recovery requires current GitHub read access. Existing `.trace` history is never overwritten.

## Server protocol

The client sends `Authorization: Bearer <GitHub user access token>` to the configured HTTPS endpoint. The server must validate that token and repository access. All requests and responses use JSON and HTTP 200 for success.

- `POST /v1/sync/blobs/have`: `{"repo":"owner/repo","mode":"pro|team","hashes":["sha256",...]}`. Up to 500 hashes; response `{"need":["sha256",...]}`. The response may only name hashes from the request.
- `POST /v1/sync/blobs`: `{"repo":"owner/repo","mode":"pro|team","hash":"sha256","payload":"base64","salt":"base64"}`. For Pro, payload is a random 24-byte nonce followed by XChaCha20-Poly1305 ciphertext, with `blob:<hash>` as associated data. Salt is present only for Pro. For Team, payload is base64 plaintext. The server should make this operation idempotent by hash.
- `POST /v1/sync/rows`: `{"repo":"owner/repo","mode":"pro|team","rows":[{"table":"sessions|turns|keyframes|ops|commits|sync_updates","id":"...","data":{...}}],"salt":"base64"}`. Up to 40 rows per request. Pro replaces `data` with `ciphertext`, using `<table>:<id>` as associated data. The server should make this operation idempotent by table and ID. A `sync_updates` row carries a completed snapshot of a previously uploaded session, turn, or operation; its own ID is a new ULID and its payload names the original table and row ID.
- `GET /v1/sync/rows?repo=owner/repo&mode=pro|team&table=sessions&after=ID`: up to 20 rows and a `next` cursor. Team returns decrypted row data after checking read access; Pro returns client ciphertext and its salt.
- `GET /v1/sync/blobs/list?repo=owner/repo&mode=pro|team&after=HASH`: up to 100 blob hashes and a `next` cursor.
- `GET /v1/sync/blobs?repo=owner/repo&mode=pro|team&hash=HASH`: base64 blob payload. Team returns decrypted bytes after checking read access; Pro returns client ciphertext and its salt.

Only blobs referenced by retained keyframes or operations are offered to the server. Blobs are uploaded before rows. The client targets 128 KiB and 40 rows per request to fit Worker CPU and D1 query budgets; a single large row can use up to 7 MiB. It records a separate cursor for each table only after an acknowledged row batch. Switching endpoint, repository, or mode resets those cursors. Uploads are append-only, including corrections made after an early upload. Recovery applies those corrections after the original rows and blobs are downloaded.

`trace login` uses GitHub's device authorization flow: it opens the verification page, shows the one-time code, and saves the resulting token in the operating system credential store. Use `--no-browser` on remote or headless machines. Live sync requires the companion app to expose its GitHub client ID and have device flow enabled for its GitHub App. Users do not need to create or configure an OAuth application themselves.
