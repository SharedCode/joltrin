# Configuration & Tuning Guide

This guide details the configuration options available in SOP and provides recommendations for tuning your stores for different workloads.

## StoreOptions Reference

The `StoreOptions` struct is the primary way to configure a B-Tree store.

| Field | Type | Description | Default / Recommendation |
| :--- | :--- | :--- | :--- |
| `Name` | `string` | Short name of the store. Must be unique within the repository. | Required. |
| `SlotLength` | `int` | Number of items stored in a single B-Tree node. | **Default: 2000**. **Max: 10,000**. Higher values improve density. **Trade-off**: Larger nodes increase memory usage (L1/L2 cache). Read & Write latency is generally unaffected due to EC striping (parallel I/O). **4,000** is a recommended balance for high scale. |
| `IsUnique` | `bool` | Enforces uniqueness of keys. | `true` for primary keys, `false` for non-unique indexes. |
| `IsValueDataInNodeSegment` | `bool` | Stores the Value directly inside the B-Tree node. | **Best for Small Data** (< 1KB). Improves locality. If `false`, stores Value in a separate file/blob. |
| `IsValueDataActivelyPersisted` | `bool` | If `true`, persists the Value to a separate file immediately upon `Add`. | **Best for Big Data & Streaming**. Prevents large values from bloating the B-Tree structure. Makes commit faster as data is already persisted. |

### Active Persistence Optimization

SOP supports an optimization for handling large data (e.g., media files or other large payloads) called **Active Persistence**.

*   **Configuration**: Set `IsValueDataInNodeSegment = false` and `IsValueDataActivelyPersisted = true`.
*   **Mechanism**: When an item is added to the B-Tree, the value (blob) is persisted to a separate file **immediately** during the `Add` call, rather than waiting for the transaction commit.
*   **Benefit**: The final `Commit` operation becomes faster because the large data blobs have already been written. This is useful for streaming scenarios where data chunks are saved incrementally.

| `IsValueDataGloballyCached` | `bool` | Caches the Value in Redis. | `true` for read-heavy workloads. `false` for write-heavy or very large data. |
| `LeafLoadBalancing` | `bool` | Checks siblings for space before splitting a node. | `false` (default). Set to `true` to save space at the cost of insert latency. |
| `BlobStoreBaseFolderPath` | `string` | Base path for the filesystem blob store. | Required for `infs` / `incfs`. |
| `CELexpression` | `string` | CEL expression used for custom key comparison/sorting. | Optional. |
| `MapKeyIndexSpecification` | `string` | JSON specification for compound indexes on Map keys. | Optional. |

## Performance Tuning

### Workload: Read-Heavy (e.g., User Profiles, Product Catalog)

*   **Goal**: Maximize cache hit rate and minimize I/O.
*   **Configuration**:
    *   `SlotLength`: **High** (e.g., 2000-5000). Fewer nodes to traverse.
    *   `IsValueDataGloballyCached`: **True**.
    *   `CacheConfig`: Increase `ValueDataCacheDuration` (e.g., 1 hour).

### Workload: Write-Heavy (e.g., Event Logging, IoT Telemetry)

*   **Goal**: Minimize locking contention and write amplification.
*   **Configuration**:
    *   `SlotLength`: **Moderate** (e.g., 500-1000). Reduces the cost of rewriting nodes during updates.
    *   `LeafLoadBalancing`: **False**. Avoids extra locking during inserts.
    *   `IsValueDataGloballyCached`: **False**. Don't pollute Redis with data that won't be read immediately.

### Workload: Large Objects (e.g., Images, Documents)

*   **Goal**: Keep the B-Tree lean.
*   **Configuration**:
    *   `IsValueDataInNodeSegment`: **False**.
    *   `IsValueDataActivelyPersisted`: **True**.
    *   Use the `streamingdata` package for objects > 1MB.

## Physical Storage & Redundancy

SOP utilizes a dual-layer approach to storage configuration to distinctively handle system-critical metadata vs. high-volume data.

### 1. Registry Redundancy (StoresFolders)
The `StoresFolders` option (found in `DatabaseOptions` or global config) defines the root partitions for the **Database Registry** and **System Tables**.
*   **Purpose**: Ensures the "Brain" of the system survives a drive failure.
*   **Mechanism**: Active/Passive Failover.
*   **Configuration**: Provide 2 paths (e.g., `["/disk1/sop_reg", "/disk2/sop_reg"]`).
    *   **Active**: `/disk1/sop_reg` accepts all writes.
    *   **Passive**: `/disk2/sop_reg` takes over if the Active path becomes inaccessible.

### 2. Data Striping & Reliability (Erasure Coding)
For the actual B-Trees and BLOB data (User Data), SOP uses Erasure Coding.
*   **Purpose**: providing **Data Striping** (High IOPS via parallel I/O) and **Reliability** (Software RAID).
*   **Mechanism**: Reed-Solomon Erasure Coding.
*   **Config**: `ErasureCodingConfig` (passed during Store creation or via global Map).
*   **Recommendation**:
    *   **Production**: Use at least 2 Data + 1 Parity Shard across 3 physical drives.
    *   **Benefits**: Survived single-drive failure; reads/writes are parallelized across drives for speed.

## Batch Size

When performing bulk operations (e.g., `UpdateMany`, `RemoveMany`), SOP processes items in batches.

*   **Default**: 500 items.
*   **Tuning**:
    *   Increase to **1000+** for high-throughput bulk loads.
    *   Decrease if you encounter transaction timeouts or memory pressure.

## Cache Configuration

SOP supports pluggable caching backends.

### Cache Factory

You can configure the global cache factory to switch between Redis (distributed) and In-Memory (standalone) modes.

```go
import "github.com/sharedcode/joltrin/v5"

func init() {
    // Use Redis (Default) - Requires a running Redis server
    sop.SetCacheFactory(sop.Redis)

    // OR

    // Use In-Memory - No external dependencies
    sop.SetCacheFactory(sop.InMemory)
}
```

### Store Cache Config

The `StoreCacheConfig` struct controls how data is cached *within* the chosen backend.

| Field | Type | Description |
| :--- | :--- | :--- |
| `RegistryCacheDuration` | `time.Duration` | TTL for registry entries (Virtual ID -> Physical Location). |
| `StoreInfoCacheDuration` | `time.Duration` | TTL for store metadata. |
| `NodeCacheDuration` | `time.Duration` | TTL for B-Tree nodes. |
| `ValueDataCacheDuration` | `time.Duration` | TTL for value data (if stored separately). |
| `IsNodeCacheTTL` | `bool` | If `true`, accessing a node extends its cache TTL (Sliding Window). |
| `IsValueDataCacheTTL` | `bool` | If `true`, accessing a value extends its cache TTL. |

## Registry Partitioning & Tuning

SOP uses a "Registry" to map logical IDs (UUIDs) to physical file locations. This registry is partitioned into multiple "Segment Files" to manage file sizes and concurrency.

### Registry Hash Mod

The `RegistryHashModValue` determines the granularity of this partitioning. This value is configured **per database** (per `StoresBaseFolder`) via `TransactionOptions`.

*   **Flexibility**: You can configure different databases with different hash mod values based on their expected size.
    *   *Example*: Database A (User Profiles) uses `250` (Default) for efficiency.
    *   *Example*: Database B (IoT Logs) uses `1000` or `5000` to handle billions of records with fewer files.

*   **Formula**: `Segment File Size = RegistryHashModValue * 4096 bytes`
*   **Default**: `250` (Minimum).
    *   Segment Size: `250 * 4KB = 1MB` (approx).
    *   Capacity: **~10.7 Million items** per segment file (assuming `SlotLength` of 1000 and 65% node utilization).
        *   Calculation: `250 (Blocks) * 66 (Handles/Block) * 1000 (Items/Node) * 0.65 (Load) = 10,725,000`
*   **Maximum**: `750,000` (Creates ~3GB segment files).

**Scaling & File Handles**:
SOP automatically allocates additional segment files (e.g., `registry-1.reg`, `registry-2.reg`) as needed.
*   **Example**: Storing **1 Billion items** with the default hashmod (250) will result in approximately **100 segment files** (1B / 10.7M).
*   **Optimization**: For very large datasets, increasing `RegistryHashModValue` reduces the total file count, conserving OS file handles and simplifying backup operations.

### Capacity Planning Table

The following table estimates the storage capacity for a single Registry Segment File based on the `RegistryHashModValue`.

**Assumptions**:
*   **Block Size**: 4096 bytes
*   **Items per Sector**: 62 (conservative estimate)
*   **Slot Length**: 5,000 (High-density configuration)

| Hash Mod Value | Segment File Size (Disk) | Estimated Capacity (Key/Value Pairs) |
| :--- | :--- | :--- |
| **250** (Default) | ~1 MB (`250 * 4096`) | **77,500,000** (77.5 Million) |
| **500** | ~2 MB | **155,000,000** (155 Million) |
| **10,000** | ~41 MB | **3,100,000,000** (3.1 Billion) |
| **100,000** | ~410 MB | **31,000,000,000** (31 Billion) |
| **400,000** | ~1.6 GB | **124,000,000,000** (124 Billion) |

### Capacity Planning (Max Density)

The following table portrays the theoretical maximums using a **Slot Length of 20,000** and a typical B-Tree **Load Factor of 68%**.

**Assumptions**:
*   **Items per Sector**: 66 (Max handles per sector)
*   **Slot Length**: 20,000
*   **Load Factor**: 68% (0.68)

| Hash Mod Value | Segment File Size (Disk) | Estimated Capacity (Key/Value Pairs) |
| :--- | :--- | :--- |
| **250** (Default) | ~1 MB | **224,400,000** (224.4 Million) |
| **500** | ~2 MB | **448,800,000** (448.8 Million) |
| **10,000** | ~41 MB | **8,976,000,000** (8.97 Billion) |
| **100,000** | ~410 MB | **89,760,000,000** (89.7 Billion) |
| **400,000** | ~1.6 GB | **359,040,000,000** (359 Billion) |
| **750,000** (Max) | ~3 GB | **673,200,000,000** (673.2 Billion) |

> **Note on Horizontal Scaling**: The capacity figures above apply to a **single** registry segment file. When a "sector" (which serves as a hash bucket) within a segment file becomes full, SOP automatically allocates a new segment file (e.g., `registry-2.reg`). The total capacity scales linearly with the number of files.
> *   *Example*: If your usage requires 5 segment files, your total capacity is **5x** the figures shown in the table.
>
> **Performance Constraint**: It is recommended to limit the number of segment files to **5-10 at most**.
> *   **Reasoning**: Segment files are traversed sequentially (like a linked list) when searching for a Virtual ID. Searching for an ID could require visiting up to N files in the worst case (where N is the number of segments).
> *   **Warning**: If you use a small `RegistryHashModValue` for billions of items, the system will generate many segment files, causing registry lookups to consume excessive IOPS.
> *   **Best Practice**: Fine-tune the `RegistryHashModValue` and B-Tree `SlotLength` to accommodate your target capacity within a minimal number of segment files.

### Alternative Optimization: Slot Length

Instead of increasing `RegistryHashModValue`, you can also optimize for large datasets by increasing the B-Tree `SlotLength`.

*   **Strategy**: Keep `RegistryHashModValue` at default (`250`) but increase `SlotLength` to `5000` (or up to the max of `10,000`).
*   **Effect**: Each B-Tree node becomes larger (acting like a "mini-table" of 5,000 items), which drastically increases the number of items managed per registry handle.
*   **Capacity Boost**:
    *   Calculation: `250 * 66 * 5000 * 0.65 = ~53.6 Million items` per segment file.
    *   **Result**: Storing **1 Billion items** would only require **~19 segment files** (vs 100 with `SlotLength` 1000).

### Tuning Guidelines

| Scenario | Recommendation | Rationale |
| :--- | :--- | :--- |
| **Small to Medium Datasets** (< 100M items) | **Default (250)** | Keeps segment files small (~1MB), minimizing I/O overhead for partial updates. |
| **Large Datasets** (> 1B items) | **Increase (e.g., 1000 - 5000)** | Creates larger segment files (4MB - 20MB). Reduces the total number of files on disk, which is better for filesystem performance and backup operations. |
| **High Concurrency** | **Moderate (500)** | Balances file size with lock contention (though SOP uses row-level locking, file handles are still a resource). |

**Note**: Changing `RegistryHashModValue` after a store has been created is **not supported** and will result in data inaccessibility. This value must be set once during the initial creation of the repository.

