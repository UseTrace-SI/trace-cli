# Privacy and redaction

## Local history and network access

Trace stores data locally in `.trace/` by default. It contacts the sync server only after you run `trace sync` or explicitly opt in with `trace login --auto-sync`. Automatic sync sends existing and new history from the local daemon in the background; stop it with `trace sync disable-auto`. Pro sync encrypts rows and blobs on the client, with its derived key held in the OS credential store for unattended sync. Team sync sends rows and blobs over HTTPS for server-side encryption. See [sync](sync.md) for the protocol and setup.

## Text redaction

Prompt text is redacted before it reaches the event spool or SQLite. Trace masks common API keys, tokens, private-key blocks, credentialed URLs, high-entropy values near secret labels, and environment variable values with secret-like names. Optional PII masking covers email addresses and phone numbers.

For Claude Code and Antigravity CLI, the daemon reads newly appended assistant transcript records after a turn ends and redacts text before storing it. Tool call records keep names and safe repository paths; they do not store raw tool inputs or tool results.

Codex assistant output and reasoning summaries, Cursor Agent CLI response and thought hooks, Windsurf Cascade response summaries, and Gemini CLI and Qwen Code final responses pass through the same redactor before storage. Copilot CLI hooks record prompts, tool names, and safe paths without raw tool arguments or results. Windsurf's full transcript file is not read by the hook. The available context depends on what each agent actually emits.

## File exclusions and removal

Trace does not read `.env*`, `*.pem`, `*.key`, `id_rsa*`, `.aws/`, or `.ssh/`. File blobs for other paths are kept byte-exact so rewind can restore them. Do not put credentials in ordinary source files if you do not want those bytes in local snapshots.

`trace uninstall` removes integrations while keeping history. `trace uninstall --purge` removes `.trace/` data as well.
