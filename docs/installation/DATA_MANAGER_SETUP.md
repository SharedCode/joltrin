# SOP Data Manager Setup Guide

Welcome to the **SOP Data Manager**. If this is your first time here, you might be wondering: *"What exactly am I building?"*

This guide is designed to walk you through the **Setup Wizard** step-by-step, explaining every concept, field, and feature so you can configure your environment with confidence.

---

## 1. What are we building?

When you run the setup wizard, you are essentially initializing a **SOP Cluster** (even if it's just on your laptop). A functioning SOP environment consists of two main components that the wizard will ask you to configure:

### The System Database ("The Brain")
The **System Database** is a special, internal database that SOP uses to manage itself. Think of it as the operating system's registry. It stores:
*   **Database Registry**: A list of all other databases (User DBs) in the environment.
*   **Configuration**: Settings for timeouts, defaults, and security.
*   **AI Knowledge**: If you use the AI Copilot, its learned behaviors and scripts are stored here.

### The User Database ("The Warehouse")
The **User Database** is where your actual application data lives. When you write a Go, C#, or Python application to store "Customers" or "Orders", they go here. You can have many User Databases, but the wizard helps you create your first one.

---

## 2. Deployment Modes: Standalone vs. Clustered

One of the first choices you'll face is the **Database Type**.

### 🏠 Standalone
*   **What it is**: SOP runs locally on a single machine. It accesses files directly on your hard drive.
*   **Best for**: Local development, notebook testing, single-server applications.
*   **Pros**: Simplest setup, zero network overhead, fastest for single-user.
*   **Cons**: If this machine dies, the service stops.

### 🏢 Clustered
*   **What it is**: SOP is designed to run across multiple servers. It uses **Redis** to coordinate locks and cache frequent data.
*   **Best for**: Production environments, Kubernetes deployments, high-availability scenarios.
*   **How it works**: Multiple SOP instances (HTTP Servers or Application Nodes) share access to the same storage (e.g., a shared network drive or cloud volume) and use Redis to ensure they don't overwrite each other's changes.
*   **Requirement**: You will need a Redis connection string (e.g., `localhost:6379`).

---

## 3. Storage Configuration

SOP is a high-performance database engine that interacts directly with the filesystem. The wizard gives you granular control over *where* and *how* data is stored.

### 📂 Stores Folders (Registry Redundancy)
You will see a field for **Stores Folders**, often allowing two paths.

*   **Concept**: Active/Passive Redundancy.
*   **Why use two?** To provide a failover mechanism (Active/Passive) for the Registry files.
*   **Explanation**: If the Active drive fails, SOP automatically fails over to the Passive drive to ensure system continuity.
*   **Default**: A single folder is fine for basic setups.

### 🛡️ Erasure Coding (Software RAID)
This is SOP's "superpower" for data striping and reliability. **Erasure Coding (EC)** splits your files into chunks and calculates "parity" data, allowing you to reconstruct a file even if parts of it are lost. It also enables **striping**, which boosts IOPS by spreading large data files across drives for parallel I/O.

You will see configuration fields for:

*   **Data Shards**: The number of segments to split the original file into.
*   **Parity Shards**: The number of redundant "repair" segments to generate.
    *   *Example*: **2 Data + 1 Parity**. This means the file is split into 2 parts, plus 1 math-based backup. You can lose **any 1** of these 3 parts and still recover the file.
*   **Drive Paths**: The distinct folders where these shards should live.
    *   *Critical*: For EC to work, these paths should theoretically be on **different physical drives**. If Drive A fails, the parity data on Drive B saves you.

> **Analogy**: It's like RAID 5 or RAID 6, but configured in software for specific tables (Stores), rather than for the whole hard drive.

---

## 4. Other Setup Options

### Populate Demo Data
*   **What it is**: A checkbox to pre-fill your database.
*   **Content**: Creates an "E-commerce" style dataset with Users, Products, and Orders.
*   **Why use it?**: It gives you instant data to play with. You can open the **AI Copilot** immediately after setup and ask: *"Show me the top 5 users by order volume"* and get real results.

---

## 5. What happens next?

Once you click **"Initialize"**:
1.  SOP creates the directory structures on your disk.
2.  It writes the `dboptions.json` configuration file.
3.  The **System Database** registers itself.
4.  The **Data Manager UI** launches, connected to your new environment.

You are now ready to start coding! Check the [Language Bindings](LANGUAGE_BINDINGS.md) to see how to connect your code to this new database.
