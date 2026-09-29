# SOP Core Engine (Golang API Guide)

The SOP Core Engine is a native, low-level B-Tree storage engine written in Go. Unlike traditional databases that run as separate network services, SOP embeds directly into your application. This effectively turns your distributed microservices into a unified storage engine capable of coordinating massive datasets across nodes.

By leveraging **direct I/O** rather than delegating to third-party database engines, SOP provides raw storage execution directly at your fingertips. It pairs this native B-Tree structure with **Redis** for distributed locking, active caching, and real-time transaction orchestration. This eliminates heavy across-the-wire data transfers and enables lightning-fast, ACID-compliant transactions across your cluster.

<h3> Key Architectural Benefits</h3>

*   **Embedded B-Tree Muscle**: Built entirely from the ground up, SOP gives Go developers the raw, efficient disk mechanics previously hidden deep inside monolithic DBMS products.
*   **True Multi-Modal Execution**: Handles arbitrary data sizes effortlessly - from tiny metadata keys to massive streaming blobs (like video files) - all within the same storage interface.
*   **Hybrid Storage Backends (`incfs`)**: Choose between standard local file systems, or utilize a hybrid backend that pairs **Cassandra** (for registry and metadata) with your **File System** (for heavy data blobs).

> Terminology: In this document, “B-tree” refers to the balanced M-ary (multiway) search tree (per Bayer & McCreight). A trie (prefix tree) is a different structure; SOP uses a B-tree, not a trie.

Code coverage: https://app.codecov.io/github/sharedcode/sop

## Table of contents

- [Summary](#summary)
- [Simple usage (transaction + B-tree + replication)](#simple-usage)
- [Replication: Active/Passive and Erasure Coding (EC)](#software-based-efficient-replication)
- [Lifecycle: Failures, Failover, Reinstate, and EC Auto-Repair](#lifecycle-failures-failover-reinstate-and-ec-auto-repair)
- [Store caching config guide](#store-caching-config-guide)
- [Data partitioning](#data-partitioning)
- [Usability](#usability)
- [Best practices](#best-practices)
- [SOP in Redis, Cassandra & File System (incfs)](#sop-in-redis-cassandra--file-system-incfs)
- [SOP in Redis & File System](#sop-in-redis--file-system)
- [Sample code (Go) and struct keys](#sample-code)
- [Streaming data (big data upload/download/partial updates)](#streaming-data)
- [Transaction batching](#transaction-batching)
- [Atomicity, Consistency, Isolation and Durability](#atomicity-consistency-isolation-and-durability)
- [Fine tuning](#fine-tuning)
- [Transaction logging](#transaction-logging)
- [Item Serialization](#item-serialization)
- [Two Phase Commit](#two-phase-commit)
- [Coordination model (OOA) and safety](#coordination-model-ooa-and-safety)
- [Optimistic Orchestration Algorithm (OOA)](#optimistic-orchestration-algorithm-ooa)
- [Concurrent/parallel commits](#concurrent-or-parallel-commits)
- [ACID vs Big Data](#acid-transactions-vs-big-data)
- [Another Big Data example](#another-big-data-example)
- [Tidbits](#tid-bits)
- [Brief Background](#brief-background)
- [SOP in-memory](#sop-in-memory)

## Simple Usage
In this tutorial, we will be showing how to configure and code with a transaction & a B-tree that has replication feature.
a. setup the Erasure Coding (EC) config in the module "init" function so it can be made available in all of the functions/code blocks
```
func init() {
	// Erasure Coding configuration lookup table (map).
	ec := make(map[string]fs.ErasureCodingConfig)

	// Erasure Coding config for "barstoreec" table uses three base folder paths across three disks.
	// Two data shards and one parity shard.
	ec["barstoreec"] = fs.ErasureCodingConfig{
		DataShardsCount:   2,
		ParityShardsCount: 1,
		BaseFolderPathsAcrossDrives: []string{
			fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
			fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
			fmt.Sprintf("//storage%cdisk3", os.PathSeparator),
		},
		RepairCorruptedShards: true,
	}

	// Assign the EC config as the global configuration that can be referenced in functions.
	fs.SetGlobalErasureConfig(ec)
}
```
The init function as shown above will create a map containing Erasure Coding information about the three disk drives & paths which will store the replicated data, EC based, i.e. - disk1, disk2 for data shards & disk3 for parity.

b. Instantiate a transaction and B-tree with replication feature, referencing the EC config specified in init
```
package main

import (
	"cmp"
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5/fs"
	"github.com/sharedcode/joltrin/infs"
)

func main() {
	ctx := context.Background()

	// Stores' home base folder w/ Active (1st) & Passive (2nd) folders specified.
	// I used disk1 & disk2 as stores' base folders (active & passive) but you can use another set of disks.
	storesFolders = []string{
		fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
		fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
	}

	// Specifying nil on "erasureConfig" (last) param will allow SOP to use the global Erasure Config.
	// You can speciy a different erasure config if you don't like the global EC config.
	to, _ := infs.NewTransactionOptionsWithReplication(sop.ForWriting, -1, fs.MinimumModValue, storesFolders, nil)

	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		fmt.Println(fmt.Sprintf("error got creating a transction, details: %v", err))
		return
	}

	trans.Begin()

	b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "barstoreec",
		SlotLength:               200,
		IsValueDataInNodeSegment: true,
	}, trans, cmp.Compare)

	b3.Add(ctx, 1, "hello world")
	b3.FindOne(ctx, 1, false)
	v, _ := b3.GetCurrentValue(ctx)
	fmt.Println(fmt.Sprintf("Fetched current value %s from backend", v))

	trans.Commit(ctx)
}
```
The code above:
* creates a transaction referencing the global EC config, specifying nil on last parameter of NewTransactionOptionsWithReplication function call does this.
* specify two disks/paths, one for Active & another for Passive, as stores home base folder (storesFolders array).
* creates a B-tree with name "barstoreec" specifying 200 items per node and using the EC config for backend data storage/disk details. It will use the entry with name "barstoreec" in EC config map. 
* adds an item to the b-tree, shows how to find an item using the FindOne function of the B-tree,
* and then commit (trans.Commit) the transaction.

## Software Based Efficient Replication
There are two types of replication in SOP, they are:
* Active/Passive replication for the metadata, i.e. - StoreRepository & Handles' (a.k.a. virtual IDs) Registry
* Erasure Coding (EC) based replication for the B-tree & large data nodes

## Active/Passive Replication
StoreRepository, the data files containing the list of all B-trees, a.k.a. store or data store, and the Registry, the data files (.reg) that contain B-tree nodes & large data nodes virtual IDs, a.k.a. handles, are replicated using SOP's Active/Passive replication feature. Each of the stores' data file set (store repository & registry files) have two copies, the active files' set stored in the currently categorized active drive/folder & the passive files' set stored in the currently categorized passive drive/folder.

So, if ever there is a drive failure in the active file set, SOP will automatically failover to the passive and make it the current active files. And vice versa, meaning, when the failed drives are reinstated, it will be marked as passive and then when another failure occurs in the future in the active drive, it will failover to this passive drive that got reinstated.

Within these replication events and life cycle, 100% data protection is provided at any given point in time. Assuming the IT administrators do their diligence in pro-actively managing the failing drives and reinstating replacement drives. SOP has automated facility that can do auto-failover, syncing replacement drives & reinstating them back to the replication rotation.

## Erasure Coding (EC) based Replication
For the B-tree nodes & large data file nodes, these files are replicated using EC based replication. SOP sports very efficient software based replication via Reed Solomon algorithm erasure coding, similar to MinIO S3's implementation. Based on a given EC configuration, e.g. - data shards & parity shards, data redundancy & thus, high data protection is achieved. Typically, 50% drive failure is tolerated and SOP will allow full read & write operations even in this degraded redundancy state. Admins are expected though to work on bringing back redundancy to the normal state of drive availability.

SOP's EC has auto-repair mode for detected missing or bitrot shards if the RepairCorruptedShards flag is turned on (see infs/NewTransactionWithReplication). Use-case: keep it off during failure, replace the drive, turn it on, restart, SOP reconstructs missing shards automatically.

If left untouched, SOP can operate even with drive(s) failures so long as data can be reconstructed from the available shards. The sample I made(see infs/integration_tests/basic_ec_test.go) uses 2 data shards and 1 parity shard. Yes, you can use minimal replication and it will work to your desire, if enough to support drive(s) failure.
See above "Sample Usage" section for EC configuration.

## Lifecycle: Failures, Failover, Reinstate, and EC Auto-Repair

This section describes what happens during drive failures, how SOP reacts, and how to safely reinstate drives after replacement.

For planned maintenance across a cluster, see also: [Cluster reboot procedure](#cluster-reboot-procedure).

Key terms
- StoreRepository and Registry: SOP’s metadata/state; covered by Active/Passive replication. Only I/O errors here can trigger a failover.
- Blob store: User data and B-tree node files; protected by Erasure Coding (EC). Blob I/O errors never cause a failover; they are handled via rollback or EC repair.

Failure scenarios and behavior
1) Blob drive failure (EC-protected)
	- Up to parity failures: Reads/writes continue. SOP reconstructs missing shards on read and writes to available shards. With RepairCorruptedShards=true, SOP will background-repair missing/bitrotted shards when a replacement drive is introduced.
	- Beyond parity: Write attempts fail and are rolled back; no failover event is generated. Reads may fail if data cannot be reconstructed. Replace drives to restore parity, then restart/continue for auto-repair.

2) Registry/StoreRepository drive failure (Active/Passive)
	- On qualified I/O errors during critical sections, SOP flips from active to passive (failover) and continues serving requests.
	- BlobStore I/O errors do not participate in failover decisions.
	- Observability: a failover event is recorded and a “failed to replicate” flag may be set if the passive side cannot be updated.

3) Reinstate workflow after drive replacement
	- Replace/format the failed drive and mount to the expected passive path.
	- Trigger reinstate (administratively or via API if exposed). SOP will fast-forward deltas from the active to the reinstated passive, clear failure flags, and return the pair to steady-state replication.
	- For EC (blob) data, enable RepairCorruptedShards and let SOP reconstruct missing shards on the replacement drive(s).

Operational guidance
- Monitoring: Track failover events and “failed to replicate” flags to identify replication gaps.
- Maintenance windows: Reinstate during low traffic to reduce fast-forward latency for large delta sets.
- Capacity planning: Ensure enough drives and parity to continue writes during failures; EC parity determines tolerated concurrent failures.

Developer contract
- Failover eligibility: Only sop.Error codes from Registry and StoreRepository I/O paths that are classified as failover-qualified can trigger failover. BlobStore errors must not.
- Transaction outcomes: If a BlobStore write fails beyond EC tolerance, Upsert/Commit returns an error and the transaction rolls back. No failover is raised; your code should surface a retriable error and/or backoff until replacement/repair completes.

Related tests (deterministic integration)
- Active-side failover flip, reinstate, and fast-forward
- Passive-side replication failure then reinstate
- BlobStore EC failures beyond parity cause rollback without failover

### Try it locally (optional)
You can exercise the lifecycle behaviors using the infs integration tests. Ensure Redis is running locally and your data path is writable (see README.md prerequisites).

Optional commands
- Run active-side failover flip, reinstate, and fast-forward
	- go test -tags=integration ./infs/integrationtests -run Test_ActiveSide_FailoverFlip_Then_Reinstate_FastForward -count=1 -v
- Run passive-side replication failure then reinstate/fast-forward
	- go test -tags=integration ./infs/integrationtests -run Test_EC_Failover_Reinstate_FastForward_Short -count=1 -v
- Run EC beyond parity: rollback without failover (BlobStore)
	- go test -tags=integration ./infs/integrationtests -run Test_EC_BlobStore_ShardsExceedParity_Rollback_NoFailover -count=1 -v

What to look for
- Logs like “failover event occurred” indicate an Active/Passive flip on Registry/StoreRepository errors.
- After reinstate, tests verify “fast-forward” (deltas applied to the reinstated side) and that failure flags are cleared.
- For EC beyond parity, the test asserts rollback and the absence of a failover event.


## Operational caveats

- Replication disabled (Active/Passive off): Metadata I/O errors will not trigger a failover. Commits to the single active path may fail depending on the error; recovery is manual. Prefer enabling replication to benefit from automatic failover and reinstate/fast‑forward.

- Passive replication failures: When writes to the passive side fail, SOP sets FailedToReplicate. Active-side commits typically continue because the active write succeeded. Plan to run ReinstateFailedDrives to resync the passive, then fast‑forward recent deltas; monitor the FailedToReplicate flag until it clears.

- After a flip: Immediately after an Active/Passive flip, some in‑flight or lingering operations aimed at the old active can encounter errors. Design clients for idempotent retry: start a fresh transaction and retry the operation.


## Store Caching Config Guide
Below examples illustrate how to configure the Store caching config feature. This feature provides automatic Redis based caching of data store's different data sets, both internal, for use to accelerate IO on internal needs of the B-trees and external, the enduser large data.

Sample code for customization of store level caching:
* Store data cache is "sliding window"
  
  NOTE: Setting 2nd param(isCacheTTL) true of sop.NewStoreCacheConfig(..) sets the store so each operation including fetch(get) will instruct Redis to extend the caching for the target data, a.k.a. "sliding time" or TTL
  ```
	b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "barstoreec",
		SlotLength:               200,
		IsValueDataInNodeSegment: true,
		CacheConfig:              sop.NewStoreCacheConfig(time.Duration(5*time.Hour), true),
	}, trans, cmp.Compare)
  ```
* Store data cache has absolute expiration(default)
  
  NOTE: This is the default mode and is also achieved in the sop.NewStoreCacheConfig(..) call by passing false to the 2nd param(isCacheTTL) & a > 0 duration.
  ```
  	b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "storecaching",
		SlotLength:               200,
		IsValueDataInNodeSegment: true,
		CacheConfig:              sop.NewStoreCacheConfig(time.Duration(5*time.Hour), false),
	}, trans)
  ```
* B-Tree Node & Application data is "sliding window"
  
  NOTE: You can set app data to get stored in B-Tree Node & make the Node caching as "sliding window", thus, your app data also gets such caching behavior. Here is how:
  ```
  	b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "storecaching",
		SlotLength:               200,
		IsValueDataInNodeSegment: true,		// true means application data is store in B-tree node!
		CacheConfig: &sop.StoreCacheConfig{
			RegistryCacheDuration:  time.Duration(5 * time.Hour),
			StoreInfoCacheDuration: time.Duration(5 * time.Hour),
			NodeCacheDuration:      time.Duration(5 * time.Hour),
			IsNodeCacheTTL   :      true,	// B-tree Node cache is TTL!
		},
  	}, trans)
  ```
* Application data cache is "sliding window"
  
  NOTE: When you would like to conserve Redis cache but still provide great level of caching of your application data, you can set the application data to do "sliding window"(TTL) and set store meta data to absolute expiration. Here is how to do it:
  ```
  b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
	Name:                     "storecaching",
	SlotLength:               200,
	IsValueDataInNodeSegment: false,		// false specifies Application data to be stored in separate node than the B-tree node!
	CacheConfig: &sop.StoreCacheConfig{
		RegistryCacheDuration:  time.Duration(2 * time.Hour),
		NodeCacheDuration:      time.Duration(2 * time.Hour),
		StoreInfoCacheDuration: time.Duration(2 * time.Hour),
		ValueDataCacheDuration: time.Duration(7 * time.Hour),
		IsValueDataCacheTTL   : true,		// true here says Value Data (node) will be cached (7hrs as specified above) & using TTL mode!
	},
  }, trans)
  ```
After 2 hours, Registry, Node & StoreInfo meta data for this "storecaching" SOP store will expire and thus, reduce the data cached in Redis. BUT since the application data(ValueData) is set to do "sliding window" or TTL, then those that got accessed(including fetch!) within the set duration will be retained, so future access to them will take it from Redis cache instead of reading from backend storage, and each access extending cache retention, a.k.a. - "sliding window". Keeping the frequently accessed data in the cache.
The StoreOption field (**IsValueDataInNodeSegment**) defaults to false, which is not set as shown above, will cause application data to be stored in the data segments and not in the B-Tree Nodes themselves.

Of course, you have to do fine tuning as there are tradeoffs :), determine what works best in your particular situation. As there are quite a few "knobs" you can tweak in SOP to achieve what you want. See below discussions for more details in this area.

## Data Partitioning
* SOP in File System has the following to address data partitioning:
  - Vertical partitioning (per B-tree/table) is built-in, you can take advantage of this by specifying different drives set in the "storesFolders" & "EC config" of the TransactionOptionsWithReplication. For example, use a different "storesFolders" value & an entry in EC config params (map) for B-tree "store1" and another set for "store2".
  EC config is a map that can have a different set of disk drives/paths for each key. E.g. - "store1" key w/ different drives & "store2" key with another set.
  - Horizontal partitioning, this is also built-in, EC based replication divides into shards each B-tree node & large data node and thus, spreading out to different disks storage. And causing optimal IO as data writers & readers use multi-threads to efficiently save or read data from these files across drives. So, nothing to do here as it is built-in, 'just specify correctly the EC config you wanted based on your Stores vs. Disk drives data allocations & replication needs.

## Usability
SOP can be used in a wide, diverse storage usability scenarios. Ranging from general purpose data storage - search & management, to highly scalable and performant version of the same, to domain specific use-cases. As SOP has many feature knobs you can turn on or off, it can be used and get customized with very little to no coding required. Some examples bundled out of the box are:
  * A. General purpose data/object storage management system
  * B. Large data storage and management, where your data is stored in its own data segment. See StoreInfo.IsValueDataInNodeSegment = false (default) flag
  * C. Streaming Data application domain enabling very large data storage - search and management, supporting multi-GBs record or item, limited only by your storage drive/sub-system. See sop/infs/NewStreamingDataStore or OpenStreamingDataStore for code & sample usage in test
  * D. High Performance Search Engine, alternative to ElasticSearch/SOLR but also has attributes of a real database engine, with ACID/two phase commit transactions
  * E. Standalone Embedded Database: Run SOP without any external dependencies (no Redis, no Cassandra). Ideal for desktop applications, CLI tools, or local AI models where you need the power of a B-Tree on the local filesystem.
  * F. Full Data Management Suite: Use the built-in HTTP Server to perform full CRUD operations, manage any B-Tree, and leverage RDBMS-like compound indexing (`IndexSpecification`) via a rich Web UI.

**The Anti-Monolith Philosophy**:
SOP breaks the traditional database monolith. It is designed as a **flat, masterless architecture** where every application node is a "master" capable of direct storage I/O.
- **No Central Server**: There is no single "SOP Server" process to install or manage.
- **Decentralized**: The "Data Management Suite" (Item F above) is just another client in the swarm. You can run it locally or deploy it as a sidecar, and it interacts with the storage layer directly, just like your application code.
- **Scalability**: This allows you to scale your "database" simply by adding more application nodes, without being bottlenecked by a central coordinator.
- **Swarm Computing**: Each user managing data via the SOP management app (REST and WebUI) actively participates in the "swarm". Their changes are efficiently merged or rejected (if conflicting) by SOP's engine, ensuring data integrity with full **ACID transaction guarantees**.

Above list already covers most data storage scenarios one can think of. Traditionally, (R)DBMS systems including NoSqls can't support storage - search & management of these three different data size use-cases. It is typically one of them and up to two, e.g. - A and/or B(SQL server) or just C(AWS S3 & a DBMS like Postgres for indexing). But SOP supports all six of them out of the box.

In all of these, ACID transactions, high speed, scalable searches and management comes built-in. As SOP turned the B-tree (an M-ary, multiway search tree) into a commodity available in all of its usage scenarios. Horizontally scalable in the cluster, meaning, there is no single point of failure. SOP offers a decentralized approach in searching & management of your data. It works with optimal efficiency in the cluster. It fully parallelize I/O in the cluster, not needing any communication for "orchestration"(see new "communication free" OOA algorithm section below) to detect conflict and auto-merging of changes across transactions occuring simultaneously or in time.

## Best Practices
Following are the best practices using SOP outlined so you can get a good understanding of best outcome from SOP for your implementation use-cases:

## As a general purpose DB engine
  * Single Writer, many Readers - a dedicated background worker populating your SOP DB doing management operations such as: adds, updates and/or deletes. And having many readers across the cluster.
  * Many Writers, many Readers - this setup "can be" slow as you are exposed to having conflicting transactions and data merges. BUT if your use-case or organized the transactions in a way that there is minimal or not excessive conflicts per data submitted, then you can achieve a very decent/great performance considering you are benefiting from ACID transactions, thus achieving higher data quality in high volume mgmt, sorting & searching.
  * No check mode - Transaction mode ```NoCheck``` allows your code to do reader transaction that will not do any check on commit. Useful for cases you are sure there will be no changes to the items & their Nodes(by other transactions in the cluster) you will be reading(or you don't care) in this transaction and thus, can tell SOP not to do any item version check during commit. This is the leanest & most performant mode if such guarantee is met.

Still, you have to bear in mind that these use-cases are geared for achieving higher data quality. Comparing the solution with other ACID transactions data providers, you will find that what SOP provides will match or, most likely, surpass whatever is available in the market. Because the solution provides a sustained throughput as there is no bottleneck and the entire data processing/mgmt solution is as parallelized as possible. The OOA algorithm for orchestration for example, provides decentralized & sustained throughput performance.

## As a large or very large Search &/or DB engine - 2nd, 3rd & 4th SOP use-case
For these three use-cases, there is not much competition for what SOP has to offer here, considering SOP is addressing being able to provide better data quality on top of supporting these use-cases.

Please feel free to file a request/discussion entry if you have a special domain-use in mind, as perhaps we can further optimize. Today, SOP piggy backs on the global cache(Redis) re-seeding the local cache of each transaction. It has a lot of advantages including solving data synchronization requirements among different instances running in the cluster without requiring to communicate & "orchestrate" with one another thus, maintaining a fully parallelized execution model with sustained throughput for each instance.

## SOP in Redis, Cassandra & File System (incfs)
This package (`incfs`) offers a hybrid storage approach:
- **Registry (Cassandra)**: Stored in Cassandra. This provides robust, scalable management for B-tree node virtual IDs (handles).
- **Data Blobs (Nodes & Values)**: Stored in the File System (local disk or network mount). This retains the raw performance and cost benefits of filesystem storage for bulk data.
- **Caching & Locking**: Redis is used for L2 caching and the OOA coordination/locking mechanism.

This hybrid model is available for environments that prefer Cassandra for metadata. However, note that the pure **`infs` backend is now the recommended model for distributed, high-scale environments**. In stress tests simulating heavy workloads across machines, `infs` (using its proprietary on-disk registry hashmap) performed **25% faster** than this hybrid model on commodity hardware.

Usage is very similar to `infs`, but you import `github.com/sharedcode/joltrin/incfs` and provide Cassandra configuration in `Initialize`.

## SOP in Redis & File System
**The Recommended Backend for Distributed & Local Workloads**

B-tree–based object persistence (balanced M-ary, multiway search tree), File System as backend storage & Redis for caching, orchestration & node/data merging. Sporting ACID transactions and two phase commit for seamless 3rd party database integration. SOP uses a new, unique algorithm(see OOA) for orchestration where it uses Redis I/O for attaining locks. NOT the `Redis Lock API`, but just simple Redis "fetch and set" operations. That is it. Ultra high speed algorithm brought by in-memory database for locking, and thus, not constrained by any client/server communication limits.

**Distributed Scale**: Contrary to earlier assumptions, `infs` is not just for single nodes. It is the **most performant choice for distributed clusters**. Its proprietary registry hashmap on disk, combined with Redis coordination, scales better than the Cassandra hybrid model (`incfs`), offering ~25% higher throughput in stress tests. The registry uses a segmented file structure (configurable via `RegistryHashModValue`) to optimize for both small and massive datasets. See [Configuration Guide](CONFIGURATION.md#registry-partitioning--tuning) for details.

**Standalone Mode**: SOP can also run in a pure standalone mode without Redis. By configuring the `CacheFactory` to `InMemory`, SOP uses internal memory for caching and locking. This is ideal for single-node applications, embedded databases, or local development where distributed coordination is not required.

SOP has all the bits required to be used like a golang map but which, has the features of a B-tree, which is, manage & fetch data in your desired sort order (as driven by your item key type & its Comparer implementation), and do other nifty features such as "range query" & "range updates", turning "go" into a very powerful data management language, imagine the power of "go channels" & "go routines" mixed in to your (otherwise) DML scripts, but instead, write it in "go", the same language you write your application. No need to have impedance mismatch.

Requirements:
  * Storage Drive(s) or sub-system (paths) for Blobs. Can be **Local Disk** (for standalone/dev) or **Network Mounts** (NAS/S3/Cloud) for distributed clusters.
  * User that you will use for the process which has Read/Write permissions(e.g. - 0777) to the drive(s)/storage path(s)
  * Golang that supports generics, SOP package (go.mod) currently is set to use 1.26.8 and higher
  * (Optional) Redis - required only for distributed/cluster mode or if using Redis-backed caching.

## Sample Code
Another sample code, edited for brevity and to show the important parts.

```
import (
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5/redis"
)

var redisConfig = redis.Options{
	Address:                  "localhost:6379",
	Password:                 "", // no password set
	DB:                       0,  // use default DB
	DefaultDurationInSeconds: 24 * 60 * 60,
}

// Initialize Redis.
func init() {
	infs.Initialize(redisConfig)
}

var ctx = context.Background()
...

func main() {
	// See above top example on how to setup "ec" or erasureConfig in "init" function. That is required
	// for this code example to work.

	// Stores' home base folder w/ Active (1st) & Passive (2nd) folders specified.
	// I used //storage/disk1 & ../disk2 as stores' base folders (active & passive) but you can use another set of disks.
	storesFolders = []string{
		fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
		fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
	}

	to, _ := infs.NewTransactionOptionsWithReplication(sop.ForWriting, -1, fs.MinimumModValue, storesFolders, nil)
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	trans.Begin()

	b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		Name:                     "barstoreec",
		SlotLength:               200,
	}, trans)

	b3.Add(ctx, 1, "hello world")

	...

	// Once you are done with the management, call transaction commit to finalize changes, save to backend.
	trans.Commit(ctx)
}
```

And, yet another example showing user-defined structs both as Key & Value pair. Other bits were omitted for brevity.
```
// Sample Key struct.
type PersonKey struct {
	Firstname string
	Lastname  string
}

// Sample Value struct.
type Person struct {
	Gender string
	Email  string
	Phone  string
	SSN    string
}

// Helper function to create Key & Value pair.
func newPerson(fname string, lname string, gender string, email string, phone string, ssn string) (PersonKey, Person) {
	return PersonKey{fname, lname}, Person{gender, email, phone, ssn}
}

// The Comparer function that defines sort order.
func (x PersonKey) Compare(other interface{}) int {
	y := other.(PersonKey)

	// Sort by Lastname followed by Firstname.
	i := cmp.Compare[string](x.Lastname, y.Lastname)
	if i != 0 {
		return i
	}
	return cmp.Compare[string](x.Firstname, y.Firstname)
}

const nodeSlotLength = 500

func main() {

	// Create and start a transaction session.

	// Stores' home base folder w/ Active (1st) & Passive (2nd) folders specified.
	// I used disk1 & disk2 as stores' base folders (active & passive) but you can use another set of disks.
	storesFolders = []string{
		fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
		fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
	}

	to, _ := infs.NewTransactionOptionsWithReplication(sop.ForWriting, -1, fs.MinimumModValue, storesFolders, nil)
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	trans.Begin()

	// Create the B-Tree (store) instance. ValueDataSize can be SmallData or MediumData in this case.
	// Let's choose MediumData as the person record can get set with medium sized data, that storing it in
	// separate segment than the Btree node could be beneficial or more optimal per I/O than storing it
	// in the node itself(as in SmallData case).
	so := sop.ConfigureStore("persondb", false, nodeSlotLength, "", sop.MediumData, "")
	b3, err := infs.NewBtreeWithReplication[PersonKey, Person](ctx, so, trans)

	// Add a person record w/ details.
	pk, p := newPerson("joe", "krueger", "male", "email", "phone", "mySSN123")
	b3.Add(ctx, pk, p)

	...
	// To illustrate the Find & Get Value methods.
	if ok, _ := b3.FindOne(ctx, pk, false); ok {
		v, _ := b3.GetCurrentValue(ctx)
		// Do whatever with the fetched value, "v".
		...
	}

	// And lastly, to commit the changes done within the transaction.
	trans.Commit(ctx)
}
```
You can store or manage any data type in Golang. From native types like int, string, long, etc... to custom structs for either or both Key & Value pair. For custom structs as Key, all you need to do is to implement the "Compare" function. This is required by SOP so then you can specify how the items will be sorted. You can define however you like the sorting to happen. Compare has int return type which follows standard "comparable" interface. The return int value is as follows:
  * Returns ```0``` means both keys being compared are equal
  * ```> 1``` means that the current key(x) is greater than the other key(y) being compared
  * ```< 1``` means that the current key(x) is lesser than the other key(y) being compared

You can also create or open one or many B-trees within a transaction. And you can have/or manage one or many transactions within your application.
Import path for SOP V2 is: "github.com/sharedcode/joltrin/infs". "infs" is an acronym that stands for:
SOP in Redis & File System(infs).

V2 is in Release Candidate 1 (RC1) status and there is no known issue. If things go well, RC1 will be declared the Released version of V2.

But yeah, V2 is showing very good results. ACID, two phase commit transaction, and impressive performance as Redis is baked in. SOP V2 actually succeeded in turning the B-tree a native "resident" of the cluster. Each of the host running SOP, be it an application or a micro-service, is turned into a high performance database & rich search server. Each, a master, or shall I say, master-less. And, of course, it is objects persistence, thus, you just author your golang struct and SOP takes care of fast storage & ultra fast searches and in the order you specified. No need to worry whether you are hitting an index, because each SOP "store" (or B-tree) is the index itself! :)

## Streaming Data
As discussed above, the third usability scenario of SOP is support for very large data. Sample code to use this ```StreamingDataStore```:
```
import (
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/infs"
)

// ...
	// To create and populate a "streaming data" store.

	// Stores' home base folder w/ Active (1st) & Passive (2nd) folders specified.
	// I used disk1 & disk2 as stores' base folders (active & passive) but you can use another set of disks.
	storesFolders = []string{
		fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
		fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
	}

	to, _ := infs.NewTransactionOptionsWithReplication(sop.ForWriting, -1, fs.MinimumModValue, storesFolders, nil)
	trans, _ := infs.NewTransactionWithReplication(ctx, to)
	trans.Begin()
	so := sop.ConfigureStore("videoStoreD", true, 100, "", sop.BigData, "")
	sds := infs.NewStreamingDataStoreWithReplication[string](ctx, so, trans, nil)
	// Add accepts a string parameter, for naming the item, e.g. - "fooVideo".
	// It returns an "encoder" object which your code can use to upload chunks
	// of the data.
	encoder, _ := sds.Add(ctx, "fooVideo")
	for i := 0; i < 10; i++ {
		encoder.Encode(fmt.Sprintf("%d. a huge chunk, about 15MB.", i))
	}
	trans.Commit(ctx)

	// Read back the data.
	trans, _ = infs.NewTransactionWithReplication(ctx, to)
	trans.Begin()
	sds, _ = infs.OpenStreamingDataStoreWithReplication[string](ctx, "videoStoreD", trans, nil)

	// Find the video we uploaded.
	sds.FindOne(ctx, "fooVideo")
	decoder, _ := sds.GetCurrentValue(ctx)
	var chunk string
	for {
		if err := decoder.Decode(&chunk); err == io.EOF {
			// Stop when we have consumed all data(reached EOF) of the uploaded video.
			break
		}
		// Do something with the downloaded data chunk.
		fmt.Println(chunk)
	}
	// End the reader transaction.
	trans.Commit(ctx)
```
### Upload
The Streaming Data Store's methods like ```Add```, ```AddIfNotExists``` and ```Update``` all return an ```Encoder``` object that allows your code to upload(via ```Encode``` method) chunks or segments of data belonging to the item, e.g. - a video if it is a video, or anything that is huge data. Divide your large data into decent chunk size, e.g. - 20MB chunk, 500 of them will allow you to store a 10GB data/content. Upon completion, calling transaction ```Commit``` will finalize the upload.

### Download
On downloading, code can call ```FindOne``` to find the item and position the built-in cursor to it, then call ```GetCurrentValue``` will return a ```Decoder``` object that allows your code to download the chunks or segments of the uploaded data(via ```Decode``` method). And like usual, calling the transaction ```Commit``` will finalize the reading transaction. If you pass a buffer to ```Decode``` that matches your uploaded chunk size(recommended) then the number of times you call ```Decoder.Decode``` will match the number of times you invoked ```Encoder.Encode``` during upload.

### Fragment(s) Download
Streaming Data store supports ability to skip chunk(s) and start downloading to a given desired chunk #. Btree store's navigation method ```Next``` is very appropriate for this. Sample code to show how to position to the fragment or chunk #:
```
	// FindChunk will find & position the "cursor" to the item with a given key and chunk index(1). Chunk index is 0 based, so, 1 is actually the 2nd chunk.
	sds.FindChunk(ctx, "fooVideo", 1)

	// Calling GetCurrentValue will return a decoder that will start downloading from chunk #2 and beyond, 'til EOF.
	decoder, _ := sds.GetCurrentValue(ctx)
	// decoder.Decode method will behave just the same, but starts with the current fragment or chunk #.
```
Alternately, instead of using ```FindOne``` & ```Next``` to skip and position to the chunk #, you can use the ```FindChunk``` method and specify the chunk # your code wants to start downloading from.

If you think about it, this is a very useful feature. For example, you can skip and start downloading (or streaming your movie!) from a given segment. Or if you use SOP to manage/store and to download your big data, e.g. - a software update, a data graph, etc... you can easily support inteligent download, e.g. - "resume and continue" without coding at all.

### Direct Chunks Management
Streaming Data Store has these three APIs to allow direct management of chunks. It means you get to interact indirectly with the B-tree to manage the entries' chunks, which is very useful for things like video editing, etc... Imagine, you upload video chunks that each chunk index aligns w/ the chapters of the video. Then you would like to manage a given set of chapter(s) retaining most of the chapters (chunks!) untouched. It is very powerful if used that way.
```
* AddChunk(ctx context.Context, key TK, chunkIndex int, chunkValue []byte)
* UpdateChunk(ctx context.Context, key TK, chunkIndex int, newChunkValue []byte)
* RemoveChunk(ctx context.Context, key TK, chunkIndex int)

Sample snippet to do direct update of a given video file's 1st chunk record:
	sds, _ = infs.OpenStreamingDataStoreWithReplication[string](ctx, "videoStoreD", trans, nil)
	sds.UpdateChunk(ctx, "fooVideo", 1, []byte{1,2,3})
	trans.Commit(ctx)
```
See the code for more details in: sop/streaming_data/streaming_data_store.go

## Transaction Batching
All your actions within a transaction becomes the batch that gets submitted to the backend. Thus, you can just focus on your data mining and/or application logic and let the SOP transaction to take care of submitting all your changes for commit. Even items you've fetched (GetCurrentValue or GetCurrentItem API call) are checked for consistency during commit. There is a "reader" transaction where you just do fetches or item reads, then on commit, SOP will ensure the items you read did not change while in the middle or up to the time you submitted or committed the transaction.

Recommended size of a transaction is about 500 items(and should typically match the "slot length" of the node, That is, you can fetch(Read) and/or do management actions such as Create, Update, Delete for around 500 items more or less and do commit to finalize the transaction.

## Atomicity, Consistency, Isolation and Durability
SOP transaction achieves each of these ACID transaction attributes by using a B-tree (an M-ary, multiway search tree) within the SOP code library. B-tree is the heart of database systems. It enables fast storage and searches, a.k.a. - indexing engine. But more than that, by SOP's design, the B-tree is used as part of the "controller logic" to provide two phase commit, ACID transactions.

It has nifty algorithms controlling/talking to Redis in order to ensure each ACID attribute is enforced by the transaction. If ACID attributes spells mission critical for your system, then look no further. SOP provides all that and a whole lot more, e.g. built-in data caching via Redis. So, your data are "cached" in Redis and since SOP transaction also caches your data within the host memory, then you get a L1/L2 caching for free, just by using SOP code library.

## Fine Tuning
There are four primary ingredients affecting performance and I/O via SOP. They are:
  * Slot Length - typical values are 100, 500, 1,000 and so on... up to 10,000, depends on your application data requirements & usage scenario.
  * Batch Size - typically aligns with Slot Length, i.e. - set the batch size to the same amount/value as the Slot Length.
  * Cache Duration - see respective section above for details about cache duration.

Base on your data structure size and the amount you intend to store using SOP, there is an opportunity to optimize for I/O and performance. Small to medium size data, will typically fit well with a bigger node size. For typical structure size scenarios, slot length anywhere from 100 to 5,000 may be ideal. You can match the batch size with the slot length. In this case, it means that you are potentially filling in a node with your entire batch's items. This is faster for example, as compared to your batch requiring multiple nodes, which will require more "virtual Ids" (or handles) in the registry table, thus, will (potentially) require more reads from registry & the node blob table. And more importantly, during commit, the lesser the number of nodes(thus, lesser "virtual Ids") used, the leaner & faster the "logged transaction" performs, which is the deciding step in the commit process, the one that makes your changes available to other transactions/machines, or triggers rollback due to conflict. It is best to keep that (virtual Ids) volume as minimal as possible.

But of course, you have to consider memory requirements, i.e. - how many bytes of data per Key/Value pair(item) that you will store. (SmallData) If you configure for the Key & Value pair to be persisted together with the other data including meta data of the node then it is a straight up one node that will contain your entire batch's items. Not bad really, but of course, you may have to do fine tuning, try a combination of "slot length"(and batch size) and see how that affects the I/O throughput. Fetches will always be very very fast, and the bigger node size(bigger slot length!), the better for fetches(reads). BUT in trade off with memory. As one node will occupy bigger memory, thus, you will have to checkout the Redis caching and your application cluster, to see how the overall setup performs.
You can also consider storing the Value part to a dedicated partition(MediumData), this will keep your Nodes' memory footprint small in exchange of an extra read when fetching the Value data part. And lastly, you can also consider "data streaming"(BigData), which is similar to MediumData, but with global caching turned off, and such... fitted for the "very large data, data streaming" use-case.

Reduce or increase the "slot length" and see what is fit with your application data requirements scenario.
In the tests that comes with SOP, the node slot length is set to 500 with matching batch size. This proves decent enough. I tried using 1,000 and it even looks better in my laptop. :)
But 500 is decent, so, it was used as the test's slot length.

## Transaction Logging
SOP does transaction logging which is used for automated cleanup of un-referenced resoures of dead in-flight transactions.

## Item Serialization
By default, uses Golang's built-in JSON marshaller for serialization for simplicity and support for "streaming". There is some future feature to allow code to override serialization, see sop/encoding package for the serialization related objects SOP uses.

## Two Phase Commit
Two phase commit is required so SOP can offer "seamless" integration with your App's other DB backend(s)' transactions. On Phase 1 commit, SOP will commit all transaction session changes onto respective new (but geared for permanence) Btree transaction nodes. Your App will then be allowed to commit any other DB(s) transactions it use. Your app is allowed to Rollback any of these transactions and just relay the Rollback to SOP ongoing transaction if needed.

On successful commit on Phase 1, SOP will then commit Phase 2, which is, to tell all Btrees affected in the transaction to finalize the committed Nodes and make them available on succeeding Btree I/O.
Phase 2 commit is a very fast, quick action as changes and Nodes are already resident on the Btree storage, it is just a matter of finalizing the Virtual ID registry with the new Nodes' physical addresses to swap the old with the new ones.

See [transaction.go](../transaction.go) for more details on two phase commit & how to access it for your application transaction integration.

## Coordination model (OOA) and safety

### Coordination model: Redis-assisted, storage-anchored

SOP uses Redis for fast, ephemeral coordination and the filesystem for durable sector claims. Redis locks provide low-latency contention detection; per-sector claim markers on storage enforce exclusive access for CUD operations. This hybrid keeps coordination responsive without coupling correctness to Redis durability.

### Why this is safe (despite Redis tail loss/failover)

- Locks are advisory; correctness is anchored in storage-sector claims and idempotent commit/rollback.
- On Redis restart, SOP detects it and performs cleanup sweeps (clearing stale sector claims) before resuming.
- Time-bounded lock TTLs, takeover checks, and rollback paths ensure progress without split-brain.
- Priority logs and deterministic rollback let workers resume or repair safely after interruptions.

### Operational properties

- Decentralized: no leader or quorum; any node can coordinate on a sector independently.
- Horizontally scalable: sharded by registry sectors; no global hot spots.
- No single point of failure: loss of Redis state slows coordination briefly but doesn't corrupt data.
- Low latency: lock checks and claim writes are O(1) on hot path; no multi-round consensus.

### When Redis is unavailable

- Writes that need exclusivity will wait/fail fast; storage remains consistent.
- On recovery, restart sweeps clear stale sector claims; workers resume.

### Comparison to Paxos-style consensus

- SOP avoids global consensus, leader election, and replicated logs - lower coordination latency and cost.
- Better horizontal scaling for partitioned workloads (per-sector independence).
- No SPOF in the coordination layer; failover is trivial and stateless.
- If you need a globally ordered, cross-region commit log, consensus is still the right tool; SOP targets high-throughput, partition-aligned coordination. But then again, SOP is not a coordination engine, it is a storage engine. Its internal piece for coordination, e.g. - of handle (virtual ID) Registry, is what was described here.

### TL;DR

SOP builds a fast, decentralized coordination layer using Redis only for ephemeral locks and relies on storage-anchored sector claims for correctness. It scales out naturally and avoids consensus overhead while remaining safe under failover.

See also: README.md section “Coordination model (OOA) and safety”.

## Optimistic Orchestration Algorithm (OOA)
SOP uses a new, proprietary & open sourced, thus MIT licensed, unique algorithm using Redis I/O for orchestration, which aids in decentralized, highly parallelized operations. It uses simple Redis I/O ```fetch-set-fetch``` (not the Redis lock API!) for conflict detection/resolution and data merging across transactions whether in same machine or across different machines.
Here is a brief description of the algorithm for illustration:
  * Create a globally unique ID(UUID) for the item
  * Issue a Redis ```get``` on target item key to check whether this item is locked

A. If Item exists in Redis...
  * Check whether the fetched item key has the item ID, if yes then it means the item is locked by this client and can do whatever operations on it
  * If fetched item has a different item ID then it means the item was locked by another transaction
    - if the fetched item lock is compatible for the request, e.g. - both are "read lock" then proceed or treat as if "read lock" was attained
    - otherwise, rollback and abort/fail the transaction

B. If Item does not exist in Redis...
  * Update the item key in Redis with the ID using ```set``` Redis API
  * Fetch again to check whether this session "won" in attempting to attain a lock
  * If fetched ID is not the same as the item ID then another session won and apply the same logic check for compatible "read lock" and roll back and abort/fail the transaction if incompatible lock is determined
  * If fetched ID is the same then we can proceed and treat as if "lock" was attained...

Now at this point, the "lock" attained only works for about 99% of the cases, thus, another Redis "fetch" for the in-flight item(s) version check is done right before the final step of commit.
Then, as a "final final" step(after doing the mentioned Redis ```fetch``` for in-flight item(s)' version check), SOP uses the backend storage's feature to ensure only one management action for the target item(s) in-flight is done.

The entire multi-step & multi-data locks, e.g. ```lock keys``` & in-flight item(s)' version checks, "lock attainment" process is called OOA and ensures highly scalable data conflict resolution and merging. Definitely not the Redis "lock" API. :)
The estimated time complexity is: O(3r) + O(r) or simply: O(4r)
where:
  * r represents the number of items needing lock and doing a single Redis fetch or set operation, a very quick, global cache/in-memory I/O. I stayed away from using "n" and used "r" to denote that it is a very very quick Redis I/O, not a database I/O.

OOA algorithm was specially cooked by yours truly to make hot-spot free, "decentralized", distributed processing to be practical and easily "efficiently" done. This is the first use-case, but in time, I believe we can turn this into another "commodity". :)
If you are or you know of an investor, perhaps this is the time you dial that number and get them to know SOP project. Hehe.

## Concurrent or Parallel Commits
SOP is designed to be friendly to transaction commits occurring concurrently or in parallel. In most cases, it will be able to "merge" properly the records from successful transaction commit(s), record or row level "locking". If not then it means your transaction has conflicting change with another transaction commit elsewhere in the  cluster, and thus, it will be rolled back, or the other one, depends on who got to the final commit step first. SOP uses a combination of algorithmic ingredients like "optimistic locking", intelligent "merging", etc... doing its magic with the B-tree and Redis.

The magic will start to happen after you have created the Btree(s) (& transaction committed them) you will be using. Having such enables a lot of the "cool commits merging" features. Typically, you should have "initializer" code block or function somewhere in your app/microservice where you instantiate the B-Tree stores analogous to creating your tables in RDBMS. You can run DDL scripts to create the tables, or create them dynamically within your application logic.

Sample code to illustrate this:
```

// Stores' home base folder w/ Active (1st) & Passive (2nd) folders specified.
// I used disk1 & disk2 as stores' base folders (active & passive) but you can use another set of disks.
storesFolders = []string{
	fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
	fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
}

to, _ := infs.NewTransactionOptionsWithReplication(sop.ForWriting, -1, fs.MinimumModValue, storesFolders, nil)
t1, _ := infs.NewTransactionWithReplication(ctx, to)
t1.Begin()
b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.ConfigureStore("twoPhase2", true, 50, "", sop.SmallData, ""), t1)

t1.Commit(ctx)
// ***

eg, ctx2 := errgroup.WithContext(ctx)

f1 := func() error {
	t1, _ := infs.NewTransactionWithReplication(ctx, to)
	t1.Begin()
	b3, _ := infs.OpenBtreeWithReplication[int, string](ctx2, "twophase2", t1)
	b3.Add(ctx2, 5000, "I am the value with 5000 key.")
	b3.Add(ctx2, 5001, "I am the value with 5001 key.")
	b3.Add(ctx2, 5002, "I am the value with 5002 key.")
	return t1.Commit(ctx2)
}

f2 := func() error {
	t2, _ := infs.NewTransactionWithReplication(ctx, to)
	t2.Begin()
	b32, _ := infs.OpenBtreeWithReplication[int, string](ctx2, "twophase2", t2)
	b32.Add(ctx2, 5500, "I am the value with 5500 key.")
	b32.Add(ctx2, 5501, "I am the value with 5501 key.")
	b32.Add(ctx2, 5502, "I am the value with 5502 key.")
	return t2.Commit(ctx2)
}

eg.Go(f1)
eg.Go(f2)

if err := eg.Wait(); err != nil {
	t.Error(err)
	return
}
```

One thing to note, is that there is no resource locking in above code & it is able to merge just fine those records added across different transaction commits that ran concurrently.

Check out the integration test that demonstrate this, here: https://github.com/sharedcode/joltrin/blob/493fba2d6d1ed810bfb4edc9ce568a1c98e159ff/infs/integration_tests/transaction_edge_cases_test.go#L315C6-L315C41
(the sample adds one record but it is not needed, empty Btree will work just fine)

## Transaction Commit Merging & Swarm Computing
SOP features a unique "commit merging" capability that acts as a technological moat - something not found in traditional RDBMS like SQL Server, Oracle, or Postgres.

When multiple transactions across different machines or threads commit concurrently, SOP's storage engine automatically detects if they are compatible (non-conflicting). Instead of locking out or failing these concurrent operations, SOP **merges** them.

*   **Swarm Intelligence**: This allows your cluster to operate like a swarm, with independent nodes working in parallel and the storage layer unifying their work.
*   **Simplified Concurrency**: Developers don't need complex locking logic. SOP transactions are natively thread and machine safe. It rolls back only true conflicts and merges everything else.
*   **Proven at Scale**: These capabilities are demonstrated in the `infs/stresstests`, simulating distributed parallel transaction commits.

SOP encourages applications to use the B-tree store and its derivatives in a simple, synchronization-free manner. The library automatically handles data merging or rolling back in case of conflicts, ensuring data integrity without complex application-level locking. This approach simplifies development while providing fine-grained, key/value-pair level isolation and resolution - a unique capability in distributed storage.

It operates like swarm computing: SOP manages coordination across threads and machines in a peer-to-peer, masterless architecture (where every node acts as a master), delivering robust consistency and scalability effortlessly.

## ACID Transactions vs. Big Data
It is well known to the database world that data engines are written to support being transactional or not. Transactions work best for non-big data management. And Big Data support typically has no support for transactions, specifically, ACID type of transactions. These perception change with SOP V2+. That is, SOP V2 supports ACID transactions and Big Data, together with "partial updates". Yes, full fidelity Big Data management protected by ACID transactions.

How?
Streaming Data Store was written for this. And if you are looking for Big Data partial updates, then you can use this Btree store. Remember, in Streaming Data Store, you are given the chance to upload/download big data in streaming fashion, like using the Encoder for upload & Decoder for download. If you have noticed, using this store you can do partial download. That is, go straight to a given chunk # then download that fragment or fragments 'til the end. Similarly, you can update a given set of chunk(s). Yes, you can also update one or more chunks part of a transaction and upon commit, these updated chunk(s) will replace the one stored in the backend.

So, essentially, we have partial update support even for the Big Data with ACID transaction protection. :)
Without exchanging anything or causing any weakness on any feature we have. So, all you have to do to take advantage of this feature is, to be able to design & organize your big data set into chunks that which, you can have option to update any part(s) of them.

This is what the StreamingDataStore does. See code samples above, specifically, the infs.NewStreamingDataStore(..) API call for details how to use it.

## Another Big Data Example
Sample Project: Upload 1TB of big data
```
package big_data
import(
	github.com/sharedcode/joltrin/infs
)

type BigKey struct {
	filename string
	chunkIndex int
}

// The Comparer function that defines sort order.
func (x BigKey) Compare(other interface{}) int {
	y := other.(BigKey)

	// Sort by filename followed by chunk index.
	i := cmp.Compare[string](x.filename, y.filename)
	if i != 0 {
		return i
	}
	return cmp.Compare[int](x.chunkIndex, y.chunkIndex)
}

func uploader() {
	// Stores' home base folder w/ Active (1st) & Passive (2nd) folders specified.
	// I used disk1 & disk2 as stores' base folders (active & passive) but you can use another set of disks.
	storesFolders = []string{
		fmt.Sprintf("//storage%cdisk1", os.PathSeparator),
		fmt.Sprintf("//storage%cdisk2", os.PathSeparator),
	}

	to, _ := infs.NewTransactionOptionsWithReplication(sop.ForWriting, -1, fs.MinimumModValue, storesFolders, nil)
	t1, _ := infs.NewTransactionWithReplication(ctx, to)
	t1.Begin()
	b3, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.ConfigureStore("twoPhase2", true, 50, "", sop.SmallData, ""), 	t, _ := infs.NewTransactionWithReplication(sop.ForWriting, -1, true)
	t.Begin()
	b3, _ := infs.NewBtreeWithReplication[bigKey, []byte](ctx, sop.StoreOptions{
		Name:                     "bigstore",
		SlotLength:               500,
		IsUnique:                 true,
		IsValueDataActivelyPersisted: true,
		CacheConfig:              sop.NewStoreCacheConfig(time.Duration(5*time.Hour), true),
	}, t)

	// Add byte array of 50 MB chunk size.
	b3.Add(ctx, BigKey{filename: "bigfile", chunkIndex: 0}, []byte{..})

	// ...
	// Commit transaction every 500 inserts, then begin a new one...

	// Add upp to 20,000 will store 1TB of data. :)
	b3.Add(ctx, BigKey{filename: "bigfile", chunkIndex: 20000}, []byte{..})
	t.Commit(ctx)
}
```

Above is an example how to upload using a single thread of execution. Of course, since Golang supports highly concurrent programming, you can instead write a Micro Service that has endpoint for upload and allows client to submit data files in similar fashion above, but now, you can put this Micro Service in a load balancer, and wala, suddenly, you can support a cluster of services that can do parallel uploads of big data files. Secured and surpassing anything on the market in efficiency!

Why? Because the SOP transaction underneath manages the data chunks in the most efficient way possible. It pro-actively persist each of the added chunks to the backend storage right when you invoke b-tree.Add(..) method. Then
at commit time, it only needs to persist the B-tree node as the chunks (large data) are already saved in the storage.
This very subtle improvement in B-tree data management spells huge performance increase because the large data chunks are not persisted at the same time, preventing very huge spikes in resource utilization. Together with other SOP B-tree backend storage improvements allows SOP to flexibly manage & aligns its operational efficiency for managing small to huge data sizes.

All "ACID transaction" guarded, "richly searchable", "partially updateable" encouraging readable code & having great concurrency model/control under your fingertips, like using Go channels and Go routines.

## Big Data Partial Updates
Updating any part(s) of the Big Data file is of no special case, SOP Btree.Update(..) method (or the AddChunk, UpdateChunk & RemoveChunk methods in StreamingDataStore), will take care of updating the target part of the file needing modification. Sample code snippet is shown below for illustration.
```
package big_data
import(
	github.com/sharedcode/joltrin/infs
)

//...


t, _ := infs.NewTransactionWithReplication(sop.ForWriting, -1, true)
t.Begin()
b3, _ := infs.OpenBtreeWithReplication[bigKey, []byte](ctx, "bigstore", t)

// Update chunk index # 100, with your new byte array of a given size.
b3.Update(ctx, BigKey{filename: "bigfile", chunkIndex: 100}, []byte{..})

// Commit the change.
t.Commit(ctx)

```

## Tid Bits

SOP is an object persistence based, modern database engine within a code library. Portability & integration is one of SOP's primary strengths. Code uses the Store API to store & manage key/value pairs of data.

Internal Store implementation uses an enhanced, modernized B-tree implementation that virtualizes RAM & Disk storage. Few key enhancements to this B-tree as compared to traditional implementations are:

* node load optimization keeps it at around 62%-75+% full average load of inner & leaf nodes. Traditional B-trees only achieve about half-full (50%) at most, average load. This translates to a more compressed or more dense data Stores saving IT shops from costly storage hardware.
* leaf nodes' height in a particular case is tolerated not to be perfectly balanced to favor speed of deletion at zero/minimal cost in exchange. Also, the height disparity due to deletion tends to get repaired during inserts due to the node load optimization feature discussed above.
* virtualization of RAM and Disk due to the seamless-ness & effectivity of handling B-tree Nodes and their app data. There is no context switch, thus no unnecessary latency, between handling a Node in RAM and on disk.
* etc... a lot more enhancements waiting to be documented/cited as time permits.

Via usage of SOP API, your application will experience low latency, very high performance scalability.

## Brief Background
SOP is written in Go and is a full re-implementation of the c# version. A lot of key technical features of SOP got carried over and a lot more added. V2 support ACID transactions and turn any application using it into a high performance database server itself. If deployed in a cluster, turns the entire cluster into a well oiled application & database server combo cluster that is masterless and thus, hot-spot free & horizontally scalable.

V1 written in c# dotnet was designed to be a data server. It can be used to create a server app where clients submit requests for data storage and mgmt. BUT realizing that this is not horizontally scalable, I designed V2(this current version in Golang!) to address the horizontal scale, without sacrificing much of the vertical scaleability. I think I succeeded. :)

A design where I broke apart the "server" data mgmt and introduced horizontal scale design without losing much of the scaleability and acceleration inherent for a "server" piece. It is not an outcome of luck, it is as designed from the ground up, leaving the legacy or traditional form and out with a new one! :)

## SOP in Memory
SOP in-memory was created in order to model the structural bits of SOP and allowed us to author the same B-tree algorithms that will work irrespective of backend, be it in-memory or others, such as the "in Redis & File System" implementation, as discussed above.

SOP in-memory is a full implementation and you can use it if it fits the needs, i.e. - no persistence, map + sorted "range" queries/updates.

Sample Basic Usage:
  * Import the sop/inmemory, e.g. `import sop "github.com/sharedcode/joltrin/v5/inmemory"`
  * Instantiate the b-tree manager, e.g. - `sop.NewBtree[int, string](false)`. The single parameter specifies whether you would want to manage unique keys.
  * Populate the b-tree, e.g. - `b3.Add(<key>, <value>)`
  * Do a range query, e.g. `b3.FindOne(<key>, true),... b3.Next(), b3.GetCurrentKey or b3.GetCurrentValue` will return either the key or the value currently selected by the built-in "cursor".
  * Let the b-tree go out of scope.

Here is the complete example:

```
package hello_world

import (
	"fmt"
	"testing"

	sop "github.com/sharedcode/joltrin/v5/inmemory"
)

func TestBtree_HelloWorld(t *testing.T) {
	fmt.Printf("Btree hello world.\n")
	b3 := sop.NewBtree[int, string](false)
	b3.Add(5000, "I am the value with 5000 key.")
	b3.Add(5001, "I am the value with 5001 key.")
	b3.Add(5000, "I am also a value with 5000 key.")

	if !b3.FindOne(5000, true) || b3.GetCurrentKey() != 5000 {
		t.Errorf("FindOne(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey())
	}
	fmt.Printf("Hello, %s.\n", b3.GetCurrentValue())

	if !b3.Next() || b3.GetCurrentKey() != 5000 {
		t.Errorf("Next() failed, got = %v, want = 5000", b3.GetCurrentKey())
	}
	fmt.Printf("Hello, %s.\n", b3.GetCurrentValue())

	if !b3.Next() || b3.GetCurrentKey() != 5001 {
		t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey())
	}
	fmt.Printf("Hello, %s.\n", b3.GetCurrentValue())
	fmt.Printf("Btree hello world ended.\n\n")
}

Here is the output of the sample code above:
Btree hello world.
Hello, I am also a value with 5000 key..
Hello, I am the value with 5000 key..
Hello, I am the value with 5001 key..
Btree hello world ended.
```
