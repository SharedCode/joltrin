# SOP Language Bindings

## Integration Guide

After successfully setting up the SOP Data Manager and configuring your storage environment, the next step is to integrate SOP into your applications.

Because you have already completed the **Setup Wizard**, your environment is fully configured (Path locations, Erasure Coding rules, Partitioning, etc.).

## 1. Connecting Your Application

To connect your code to the database you just created, you can load the settings directly from the disk. This ensures your application always uses the exact same configuration as the Data Manager.

### 🐹 Go (Golang)
```go
package main

import (
    "github.com/sharedcode/joltrin/v5/database"
    "github.com/sharedcode/joltrin/v5"
    "context"
)

func main() {
    ctx := context.Background()
    
    // 1. Load configuration from disk (setup by Wizard)
    opts, _ := database.GetOptions(ctx, "/path/to/my/db")

    // 2. Begin Transaction (SOP Go is transaction-based)
    trans, _ := database.BeginTransaction(ctx, opts, sop.ReadWrite)
    defer trans.Commit(ctx)
}
```
*   [**Go Library Tutorial**](../../README.md)

### #️⃣ C# (.NET)
```csharp
using Sop;
using var ctx = new Context();

// 1. Load configuration (Helper method available)
var opts = Database.GetOptions(ctx, "/path/to/my/db");

// 2. Open Database Handle
var db = new Database(opts);
```
*   [**C# Binding Tutorial**](../../bindings/csharp/README.md)

### ☕ Java
```java
import com.sharedcode.sop.*;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.File;

try (Context ctx = new Context()) {
    // 1. Load configuration (JSON deserialization)
    ObjectMapper mapper = new ObjectMapper();
    DatabaseOptions opts = mapper.readValue(new File("/path/to/my/db/dboptions.json"), DatabaseOptions.class);

    // 2. Open Database Handle
    Database db = new Database(opts);
}
```
*   [**Java Binding Tutorial**](../../bindings/java/README.md)

### 🐍 Python
```python
from sop import Context, Database

with Context() as ctx:
    # 1. Load configuration (Helper method available)
    opts = Database.get_options(ctx, "/path/to/my/db")
    
    # 2. Open Database Handle
    db = Database(opts)
```
*   [**Python Binding Tutorial**](../../bindings/python/README.md)

### 🦀 Rust
```rust
use sop::{Context, Database, DatabaseOptions};
use std::fs::File;

let ctx = Context::new();

// 1. Load configuration (using serde_json)
let file = File::open("/path/to/my/db/dboptions.json").unwrap();
let opts: DatabaseOptions = serde_json::from_reader(file).unwrap();

// 2. Open Database Handle
let db = Database::new(&ctx, opts).unwrap();
```
*   [**Rust Binding Tutorial**](../../bindings/rust/README.md)

---

## 2. Understanding the Workflow: Data-First vs. Code-First

What you just did is called the **Data-First Workflow**. You used a GUI tool (the Wizard) to define your schema and infrastructure, and then your code simply consumed it.

However, SOP is flexible. As a developer, you might prefer to define everything in code, especially for automated testing or CI/CD pipelines.

### The Code-First Alternative
In this approach, you ignore the wizard. You define your `DatabaseOptions` (paths, redundancy rules) directly in your source code and call `Database.Setup()`.

**Example (Python)**:
```python
# Define config in code
opts = DatabaseOptions(
    type=DatabaseType.Standalone,
    stores_folders=["/data/new_feature_db"],
    erasure_config={
        "": ErasureCodingConfig(data_shards=2, parity_shards=1)
    }
)
# Initialize environment
Database.setup(ctx, opts)
```

### The "Full Circle" Benefit
The beauty of SOP is that these workflows communicate.
*   If you setup via **Wizard** $\rightarrow$ You can load it in Code.
*   If you setup via **Code** $\rightarrow$ You can open the **Data Manager**, point it to your new folder, and immediately visualize/debug the data your code generated.

This allows you to verify your application logic visually, without writing custom inspection tools.


## Next Steps

Now that you have your code connecting to the database, you can:
1.  Run the **SOP Data Manager** to see the "Users" table you just created.
2.  Check out the **Language Tutorials** linked above for deep-dives into your preferred language.
3.  Visit the **[SOP GitHub Repository](https://github.com/sharedcode/joltrin)** for the latest updates, issues, and discussions.
