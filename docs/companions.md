# Brain and Graph integration

CLI owns captured history. Brain derives searchable memories from sessions and
turns. Graph indexes the current checkout. Platform synchronizes CLI history
and blobs; companion databases are rebuilt locally.

## Build and use

With the three source repositories next to one another:

```sh
cd trace-cli
make companions
export PATH="$PWD/bin:$PATH"
```

In an initialized project:

```sh
trace companions refresh
trace companions status --json
trace context --path main.go "authentication" --json
trace brain remember "Use bounded authentication requests"
trace graph context --path main.go
```

CLI resolves `--repo` and runs companion plugins in that Git root, including
when started in a nested directory or another working directory. An explicit
companion `--db` still selects a custom database.

## Automatic refresh

With compatible executables on the daemon's `PATH`, startup catches up derived
state, and successfully processed turn/session completion events queue refresh.
This includes hook and transcript events. Notifications are nonblocking and
bursts coalesce. Brain imports history without invoking an extraction agent;
Graph scans local source. Neither refresh performs network synchronization.

Use `trace companions disable-auto` and `enable-auto` for per-project control.
`TRACE_COMPANIONS=0` disables automatic work in that daemon process; manual
refresh remains available. Restart an existing daemon after changing its
`PATH` or installing/upgrading companions.

Refresh subprocesses each have a one-minute deadline. Shutdown cancels their
process groups and waits for the worker. Failures do not fail capture. Safe
operation metadata, without memory/source content, is saved under
`.trace/companions/status.json`. Saving status does not recreate a removed
worktree.

## Storage and recovery

New integrated state uses `.trace/brain/memory.db` and `.trace/graph/graph.db`.
A pre-existing `.trace-brain/memory.db` or `.trace-graph/graph.db` takes
precedence, preserving local data. Standalone use without Trace history
retains the old default; explicit `--db` overrides the default.

Standalone state folders and all `.trace` state are excluded from future CLI
snapshots, including accidentally tracked database/WAL files. Historical
checkpoints are preserved. Graph respects Git ignores and excludes Trace state,
dependency trees, and secret paths from its source inventory.

After `trace sync pull --into CHECKOUT`, CLI invokes the same refresh commands
and includes their status in the result. Missing companions or rebuild failure
leave recovered history available. Run `trace companions refresh` after fixing
the problem. Graph describes the current checkout; recovery does not rewrite
Git history or provide a historical graph for every turn.

Manual memories remain local and survive repeated imports. They are not
uploaded or copied to another checkout. Derived memories rebuilt there retain
the original session/turn IDs. Recovery uses existing Platform history/blob
sync and credentials; raw companion SQLite files are not synchronized.

## Context contract

`trace context` and read-only MCP `get_context` return `steps`, `brain`, and
`graph`. Brain results contain memory source/session/turn links. Graph results
contain the canonical repository, index timestamp, symbols, and relationships.
Memory content and source signatures pass through CLI's pattern-based redaction before being
returned to an agent.

Lookup does not create databases, refresh indexes, or run extraction. Stores
refresh independently, so context immediately after an edit may describe the
previous generation. Graph exposes `indexed_at`; `trace companions status`
reports the last completed refresh.

The optional executables implement local JSON protocol **1**:

```sh
trace-brain --db DB refresh --trace-db TRACE_DB
trace-brain --db DB context --trace-db TRACE_DB --query WORDS --limit 20
trace-graph --db DB --repo ROOT refresh
trace-graph --db DB --repo ROOT context --path FILE --query WORDS --limit 20
```

Responses include `protocol: 1` and `status`. Results cap memories/symbols at 50
and relations at 50; content/signatures are truncated. CLI caps stdout at 1 MiB
and stderr at 4 KiB, bounds each lookup to two seconds, validates the protocol
and repository scope, and discards invalid component data. Statuses distinguish
`ready`, `not_installed`, `missing_index`, `needs_refresh`,
`unsupported_protocol`, `invalid_response`, `failed`, `timeout`, `cancelled`,
and `output_limit`. Missing companions leave history tools usable. This local
protocol is separate from cloud sync protocol 2.

CLI scopes imported memories to the current Trace source, including when a
legacy memory database contains imports from multiple projects. Manual notes
in that local database remain available. Graph queries are repository-scoped.

## Verification

`make test-companions` builds the three real binaries into a temporary
directory, captures a turn, checks automatic refresh, combined context,
redaction, project routing, and a derived rebuild in a second checkout using a
consistent history/blob fixture. Cleanup stops the daemon before removing
temporary directories. Existing sync integration tests separately verify
client/Platform transfer.
