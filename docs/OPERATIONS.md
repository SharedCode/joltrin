# Operations Guide

This guide covers the operational aspects of running SOP in production, including failover handling, connection management, and backup strategies.

## Connection Management

SOP relies on long-lived (pooled) connections to Redis and Cassandra (for `incfs`).

> **Note**: If running in **Standalone Mode** (using `sop.InMemory` cache), Redis is not required, and this section can be ignored.

> **Note**: SOP does **not** require Redis data persistence (RDB/AOF). Redis is used for ephemeral locking and caching. If Redis restarts, SOP detects the change and recovers safely.

### Redis
*   **Pool**: SOP uses the standard `go-redis` client.
*   **Configuration**: Ensure your `redis.Options` includes:
    *   `PoolSize`: Set according to your concurrency needs (e.g., 10 * CPU cores).
    *   `MinIdleConns`: Keep some connections warm to avoid latency spikes.
    *   `ReadTimeout` / `WriteTimeout`: Tune these to avoid premature timeouts during heavy load.

### Cassandra (`incfs`)
*   **Consistency**: SOP typically uses `LOCAL_QUORUM` for strong consistency.
*   **Keyspace**: Ensure the keyspace is created with an appropriate replication strategy (e.g., `NetworkTopologyStrategy`) for your cluster.

## Failover Logic

SOP includes sophisticated logic to handle storage failures transparently.

### "Failover Qualified" I/O Errors

Not all errors trigger a failover. SOP distinguishes between transient errors (retryable) and permanent hardware/filesystem failures.

**Triggers for Failover:**
*   `syscall.EIO` (Input/output error)
*   `syscall.EROFS` (Read-only file system)
*   `syscall.ENOSPC` (No space left on device)
*   Specific Linux error codes for media failure (e.g., `EUCLEAN`).

**Behavior:**
1.  **Detection**: When a "Qualified" error occurs during a write.
2.  **Switch**: The system automatically marks the current storage path as "Passive" and switches to the configured "Active" standby path.
3.  **Recovery**: Operations continue on the new path. The failed path requires manual intervention or auto-repair (if configured).

### Monitoring

*   **Logs**: Watch for `[Failover]` tagged logs.
*   **Metrics**: Monitor the `sop_failover_count` metric (if you have instrumented your application).

## Backup & Restore

### Hybrid Backend (`incfs`)

Backing up a hybrid system requires coordination.

1.  **Snapshot Registry (Cassandra)**:
    *   Use `nodetool snapshot` to capture the state of the Cassandra keyspace.
2.  **Snapshot Blob Store (Filesystem)**:
    *   Use filesystem snapshots (e.g., ZFS, LVM, or cloud volume snapshots) to capture the data directory.
3.  **Consistency**:
    *   If possible, pause writes during the snapshot window to ensure the Registry and Blob Store are aligned.
    *   If zero-downtime is required, snapshot Cassandra *first*, then the Filesystem. SOP's Copy-On-Write nature means old blobs (referenced by the older Cassandra snapshot) will still exist on disk, ensuring a consistent point-in-time restore.

### Restore Procedure

1.  Stop the application.
2.  Restore the Cassandra keyspace.
3.  Restore the Filesystem data.
4.  Start the application.
5.  **Run Integrity Check**: Use SOP's internal tools to verify that all Registry entries point to valid blobs.

## Data Management Suite (SOP Web UI)

SOP includes an HTTP Server and Web UI that functions as a database management suite. It allows you to:
*   **Perform Full CRUD**: Create, Read, Update, and Delete records in any B-Tree.
*   **Manage Any Database**: Works with any SOP store, regardless of the language or data type used.
*   **Leverage Rich Indexing**: Utilize `IndexSpecification` to define and search on compound indexes with multiple fields and custom sort orders, giving you RDBMS-like power.
*   **Debug & Inspect**: Visually verify data integrity and structure.

> **Note on Architecture**: This tool is **not** a central database server. In SOP's masterless architecture, this UI is another client node. You can run it locally to manage a remote production cluster, or deploy it as a sidecar. It connects directly to the storage layer and respects the same ACID guarantees as other SOP clients.

### Running the Management Suite

You can run the tool directly from the source:

```bash
# Point to your SOP registry folder
go run ./tools/httpserver -registry /path/to/your/sop/data
```

Access the UI at `http://localhost:8080`.

### Key Features
*   **Store Listing**: See all B-Trees in your registry.
*   **Rich Search**: Search by key prefix or complex multi-field queries (if using `IndexSpecification`).
*   **JSON Editor**: View and edit complex value objects as formatted JSON.
*   **Bulk Friendly**: Pagination and efficient loading for large datasets.
*   **Safe Schema Updates**: Admins can securely unlock and modify Index/CEL expressions on live stores.

For more details, see the [SOP Data Manager Documentation](../tools/httpserver/README.md).

## Troubleshooting & Best Practices

### Cluster reboot procedure
When rebooting an entire cluster running applications that use SOP, follow this order to avoid stale locks and ensure clean recovery:

1) Gracefully stop all apps that use SOP across the cluster.
2) Stop the Redis service(s) used by these SOP apps.
3) Reboot hosts if needed (or proceed directly if not).
4) Start the Redis service(s) first and verify they are healthy.
5) Start the apps that use SOP.

Notes:
- SOP relies on Redis for coordination (locks and recovery bookkeeping). Bringing Redis up before SOP apps prevents unnecessary failovers or stale-lock handling during app startup.
- If any node was force-killed, SOP’s stale-lock and rollback paths will repair on next write; starting Redis first ensures the required state is available.

### Clustered Mode: Data Deletion

When running SOP in **Clustered Mode** (using Redis + Disk Storage), it is critical to maintain synchronization between the persistent data on disk and the ephemeral locks/cache in Redis.

**Recommended Practice:**
*   Always use the SOP API (e.g., `RemoveBtree`, `DeleteDatabase`, or `RemoveStore`) to delete data.
*   These methods automatically handle the cleanup of both the physical files on disk and the corresponding entries in Redis (cache keys, locks, registry entries).

**Manual Deletion (Development Only):**
If you must delete the data files manually (e.g., during local development or a hard reset):
1.  **Stop all Applications**: Ensure no SOP processes (services, CLI, Data Manager) are running.
2.  **Delete Files**: Remove the store folders/files from the disk.
3.  **Flush Redis**: You **MUST** run `FLUSHALL` on your Redis instance immediately after deleting the files.
    ```bash
    redis-cli flushall
    ```

**Why this is necessary**:
Redis maintains locks and cached metadata with a default timeout (typically 15 minutes). If you delete the files but leave the Redis keys active, any new application instance you start will see "ghost" locks or stale metadata, preventing it from recreating the stores or acquiring locks until the timeout expires.

## Watching the barrier server and capping retries

Four settings, all off by default, for a server you leave running. The first three do not change what the barrier allows or blocks. `SOP_MODE=observe` does, and is covered after them.

- `SOP_METRICS_ADDR=127.0.0.1:9464` serves Prometheus metrics at `/metrics`: `joltrin_steps_total` by workflow and outcome (`allowed`, `replayed`, `blocked`, `observed`, `invalid`), `joltrin_blocks_total` by the rule that blocked, and a `joltrin_step_duration_seconds` histogram. It binds where you tell it to, so keep it on localhost or behind your own access control. Only workflows the server knows become labels.
- `SOP_DECISION_LOG=stderr` (or a file path) writes one JSON line per `execute_step`: time, workflow, trace, step, outcome, the rule and missing state for a block, and how long the check took. It never uses stdout, which carries the MCP protocol.
- `SOP_RETRY_LIMIT=3` is a guardrail against a retry loop. After a step has been blocked that many times for the same reason in one trace, the result says `next: stop_and_ask` with a hint to ask a person. The step stays blocked either way.

`SOP_MODE=observe` is a dry run, for trying a runbook on real agent traffic before you enforce it. A step that would be blocked runs anyway. The result has `executed: true`, `would_have_blocked` with the same missing state a block would give, and a hint saying nothing is being blocked. The decision log and `joltrin_steps_total` record it as `observed`. The server prints a warning to stderr at startup. Nothing is stopped in this mode, so do not leave it on for a database you care about. Unset it, or set `SOP_MODE=enforce`, to block again. Any other value is refused at startup. The step is still recorded, so later steps in the same trace see the state it establishes.

These cover `execute_step` on the MCP server. `validate_step` and the A2A server are not counted yet.

## Serving the barrier over HTTP, and in Kubernetes

`sop-mcp-server http` serves the same barrier over MCP streamable HTTP at `/mcp`, with `/healthz` for probes. It listens on `127.0.0.1:8088` unless you pass `--addr`. On any other address it will not start without `SOP_MCP_TOKEN` of at least 16 characters, and every call to `/mcp` must send `Authorization: Bearer <token>`. It stops cleanly on SIGINT or SIGTERM.

It is single tenant. Everyone who holds the token shares one set of runbooks and one trace namespace, so two callers that use the same `trace_id` are working in the same trace. Run one server per team. State is in memory, so a restart forgets traces, which makes the barrier stricter and never looser.

`docker/Dockerfile.barrier` builds a distroless, non-root image, and `deploy/barrier/k8s.yaml` is a one-replica Deployment and Service for it. Create the token secret first, as the comment at the top of the manifest says. Both were run once on a local `kind` cluster, by hand. No workflow builds or deploys them. [HOSTED_BARRIER_ASSESSMENT.md](HOSTED_BARRIER_ASSESSMENT.md) lists what is still missing before a hosted service.

