# Getting Started with Joltrin

This guide takes you from downloading the software to building your first application. (Joltrin was called SOP before v5, and some package and binary names still say SOP.)

## 1. Download and run the server

**Note for Go developers:** you do not need the server to use the library. Joltrin is a native Go library, so you can `go get` it (see [Developing with SOP](#3-developing-with-sop) below) and compile your application.

The Data Manager server and its UI are one file. Pick the one for your machine from the [latest release](https://github.com/SharedCode/joltrin/releases/latest), then make it executable and run it:

```bash
curl -fsSLO https://github.com/SharedCode/joltrin/releases/latest/download/sop-httpserver-darwin-arm64   # or darwin-amd64, linux-amd64, linux-arm64
chmod +x sop-httpserver-darwin-arm64
./sop-httpserver-darwin-arm64
```

On Windows, download `sop-httpserver-windows-amd64.exe` (or `-arm64.exe`) from the same page and run it from PowerShell. The download is about 50 MB, and the server was ready in under a second on a MacBook Air.

> **AI usage note:** to use the AI Copilot features, you must supply your own LLM API key (for example from Google AI Studio or OpenAI). The system does not come with a trial key. You can enter your key in the "Environment Configuration" after starting the server.

**Only for Python, C#, Java, or Rust.** Those packages are also in the platform bundle on the release page, `sop-bundle-<os>-<arch>-<version>.tar.gz` (`.zip` on Windows). It is about 120 MB and unpacks to a folder of the same name:

```text
sop-bundle-<os>-<arch>-<version>/
├── sop-httpserver       # The Database Server & UI
├── libs/                # Shared libraries (for C/Rust)
├── python/              # Python package (.whl)
├── java/                # Java library (.jar)
├── dotnet/              # C# package (.nupkg)
├── rust/
└── docs/
```

---

## 2. First Run & Setup

Once the server is running, open your browser to: **[http://localhost:8080](http://localhost:8080)**

### Quick Start (Demo)
For a step-by-step walkthrough of the **Setup Wizard** and features, please see the **[SOP Demo Walkthrough](DEMO_GUIDE.md)**.

### The Setup Wizard
On your first visit, you will see the **Setup Wizard**.

1.  **Database Engine**:
    *   **Standalone**: Best for local development. Data is stored in a local folder.
    *   **Clustered**: For distributed environments. Requires a Redis connection string.

2.  **Initialize Database**:
    *   **Populate Demo Data**: Check this box! It will create a sample E-commerce database (Users, Products, Orders) so you can explore the features immediately.

3.  Click **"Initialize Database"**.

### Explore the Data
*   **Browse Stores**: Click on "user", "product", or "order" in the sidebar to see the data.
*   **AI Copilot**: Click the chat icon (bottom-left). Try asking:
    *   *"Show me the top 5 most expensive products"*
    *   *"Find all users who live in New York"*

---

## 3. Developing with SOP

Now that your server is running, you can write code to interact with it. SOP is **polyglot**, meaning you can access the same data from Go, Python, C#, or Java.

### Go (Native)
1.  **Install**:
    ```bash
    go get github.com/sharedcode/joltrin/v5
    ```
2.  **Code**:
    ```go
    import (
        "context"
        "github.com/sharedcode/joltrin/v5"
        "github.com/sharedcode/joltrin/v5/database"
    )

    // Open Database (Standalone)
    db, _ := database.Open(sop.DatabaseOptions{
        Type: sop.Standalone, 
        StoresFolders: []string{"./data"},
    })

    // Transaction & Store
    tx, _ := db.BeginTransaction(ctx, sop.ForWriting)
    store, _ := tx.GetStore("users")

    // Add Item
    store.Add(ctx, "user_999", "Alice")
    tx.Commit(ctx)
    ```

### Python
1.  **Install**:
    ```bash
    pip install python/sop-*.whl
    ```
2.  **Code**:
    ```python
    from sop.store import StoreFactory

    # Connect to the local server
    factory = StoreFactory()
    store = factory.get_store("user")

    # Add a user
    store.add("user_999", {"name": "Alice", "age": 30})
    
    # Get a user
    user = store.get("user_999")
    print(user)
    ```

### C# / .NET
1.  **Install**:
    Add the `.nupkg` to your project or local feed.
    ```bash
    dotnet add package Sop --source ./dotnet
    ```
2.  **Code**:
    ```csharp
    using Sop;

    var factory = new StoreFactory();
    var store = factory.GetStore<string, User>("user");

    store.Add("user_999", new User { Name = "Alice", Age = 30 });
    ```

### Java
1.  **Install**:
    Add the `.jar` to your classpath.
2.  **Code**:
    ```java
    import sop.StoreFactory;
    import sop.Store;

    StoreFactory factory = new StoreFactory();
    Store<String, User> store = factory.getStore("user");

    store.add("user_999", new User("Alice", 30));
    ```

---

## 4. Next Steps

*   **[Architecture Guide](ARCHITECTURE.md)**: Learn how SOP works under the hood.
*   **[Workflows](WORKFLOWS.md)**: Best practices for scaling from local dev to production swarms.
*   **[AI Copilot Guide](../ai/README.md)**: Learn how to build AI-powered applications with SOP.
