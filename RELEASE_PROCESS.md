# SOP Unified Release Process

This document outlines the build and release lifecycle for the Joltrin (formerly SOP) ecosystem.

## 1. Core Architecture

### The Artifacts: "One Core, Many Bindings"
SOP follows a **Unified Native Core** architecture.
1.  **The Engine**: The core storage and transaction engine is written in **Go**.
2.  **The Build**: We compile this Go code into **Native Shared Libraries** (`.so`, `.dll`, `.dylib`) using `build_release.sh`.
    *   `libjsondb_linux_amd64.so`
    *   `libjsondb_windows_amd64.dll`
    *   `libjsondb_darwin_amd64.dylib`
    *   (and ARM64 variants)
3.  **The Bindings**: Language libraries (Python, Java, C#) act as high-level wrappers that load this shared library into their process memory using **FFI** (Foreign Function Interface).
    *   Python: Uses `ctypes` to load the `.so`/`.dll`.
    *   Java: Uses **JNA** to load the `.so`/`.dll`.
    *   C#: Uses `DllImport` / `NativeLibrary` to load the `.so`/`.dll`.

There is **NO** separate server process for standard usage. The database engine runs *embedded* inside your Python/Java/C# process for maximum performance (zero network latency).

### The Tool: `sop-httpserver`
Separately, we build `sop-httpserver`, which is a standalone executable wrapping the same engine. This is used for:
*   **The Data Manager UI**: For visual inspection.
*   **Remote Access**: If you want to run SOP as a server (e.g., in a cluster).

## 2. Release Workflow (Step-by-Step)

This checklist covers the lifecycle from version bump to public availability.

### Step 1: Automated Version Bump
Use the helper script to update version numbers across all 5 languages simultaneously.

```bash
# 1. Bump version (e.g., to 2.2.5)
./update_version.sh 2.2.5

# 2. Check changes (optional verify)
git diff

# 3. Commit the version bump (Do NOT tag yet)
git add .
git commit -m "chore: bump version to 2.2.5"
```

### Step 2: Build Native Core for Bindings
Before we can package Python/C#/Java libraries, we must compile the Go core into shared libraries (`.so`/`.dll`/`.dylib`) and place them in the correct binding folders.

```bash
# 1. Build Linux & Windows libraries (runs inside Docker for consistency)
./bindings/build_in_docker.sh

# 2. (macOS Only) Build macOS libraries (runs locally due to CGO limitations)
./bindings/build_local_macos.sh
```
*Result*: The `bindings/python/sop/`, `bindings/csharp/runtimes/`, and `bindings/java/src/` folders are now populated with the native engine binaries for version 2.2.5.

### Step 3: Package & Publish Language Bindings
With native libs in place, we package and publish the wrappers.

**A. Python (PyPI)**
```bash
cd bindings/python
./build_wheels.sh
twine upload dist/*
```

**B. C# (.NET / NuGet)**
```bash
cd bindings/csharp
dotnet pack -c Release
dotnet nuget push Sop4CS.2.2.5.nupkg --source https://api.nuget.org/v3/index.json
```

**C. Java (Maven Central)**
```bash
cd bindings/java
mvn clean deploy -DperformRelease=true
```

### Step 4: GitHub Release (The Server Binary)
Finally, we tag the release. This triggers the GitHub Action (or manual build) to check out the tag, compile the standalone `sop-httpserver` executable, and attach it to the Release page.

**Why this is crucial**: The language bindings (published in Step 3) contain logic to download this `sop-httpserver` binary from the GitHub Release page if the user requests the UI.

```bash
# 1. Tag and Push
git tag v2.2.5
git push origin v2.2.5
```

**CI/CD Action**:
1.  Detects `v*` tag.
2.  Runs `./build_release.sh` to generate `sop-httpserver-linux-amd64`, `sop-httpserver-windows.exe`, etc.
3.  Creates a GitHub Release `v2.2.5`.
4.  Uploads the server binaries as assets.

**Completion**:
*   Users `pip install sop4py` -> Get native performance immediately.
*   Users run `python -m sop.httpserver` -> Script finds `v2.2.5` tag on GitHub -> Downloads matching server binary -> Launches UI.

## 3. Alternative Workflow: CI-Driven Publishing (Recommended)

Instead of building wheels and nuget packages locally (which requires setting up complex cross-compilation toolchains for Linux/Windows/macOS), you can leverage the GitHub Actions pipeline to build "perfect" artifacts for you.

**Why this is better:**
1.  **Guaranteed Clean Build**: The CI environment builds the native engine from scratch for all platforms (Linux, Windows, macOS).
2.  **Identical Artifacts**: The wheels and NuGet packages on PyPI/NuGet.org will exactly match the ones attached to the GitHub Release.
3.  **Full Metadata**: The CI build process ensures READMEs, licenses, and auxiliary scripts (like `sop-httpserver` auto-downloader) are correctly packed.

### The CI-First Steps:

1.  **Bump Version**: Run `./update_version.sh <version>` and commit the changes locally.
2.  **Tag & Push**: Run `git tag v<version>` and `git push origin v<version>`.
    *   This triggers the `release.yml` workflow on GitHub.
3.  **Wait for CI**: Go to the Actions tab on GitHub and wait for the "Release SOP Data Manager" workflow to complete.
4.  **Download Artifacts**:
    *   Go to the new **Release** page on GitHub.
    *   Download the `.whl` (Python) and `.nupkg` (C#) files from the "Assets" section.
5.  **Publish**:
    *   **Python**: `twine upload sop4py-*.whl`
    *   **C# (Library)**: `dotnet nuget push Sop.*.nupkg --source https://api.nuget.org/v3/index.json`
    *   **C# (CLI Tool)**: The CI currently only produces the `Sop4CS` library. To release the CLI tool (`Sop4CS.CLI`), you must build/pack it locally.
        *   *Prerequisite*: Run `./bindings/build_in_docker.sh` and `./bindings/build_local_macos.sh` to generate the native libraries.
        *   Run the `./bindings/csharp/build.sh` to build the dotnet nuget packages for Sop4CS & Sop4CS.CLI. Or do below:
        *   Then pack and push:
            ```bash
            cd bindings/csharp/Sop.CLI
            dotnet pack -c Release
            dotnet nuget push bin/Release/Sop4CS.CLI.*.nupkg --source ...
            ```

*Note: The `sop-httpserver` logic works exactly the same way. When a user installs these packages and runs the server command, it will automatically download the correct standalone server binary from the GitHub Release version that matches the package version.*

## 3. Versioning Strategy

We adhere to **Strict Version Parity**. All artifacts (Shared Libs, EXE, Python Wheel, Java JAR, NuGet) must share the exact same version number (e.g., `v2.2.4`) as the Git Tag.

### Automated Versioning
To ensure consistency across this polyglot codebase, use the `update_version.sh` script. **Do not manually edit version files.**

```bash
./update_version.sh 2.2.5
```

This script automatically updates:
*   Root `VERSION` file
*   `bindings/python/pyproject.toml` and `bindings/python/sop/__init__.py`
*   `bindings/java/pom.xml`
*   `bindings/csharp/Sop/*.csproj` and other C# projects
*   `bindings/rust/Cargo.toml`
*   Bindings version constant files (e.g. `bindings/csharp/VERSION`)

## 4. Manual Verification (Pre-Release)

**Note on `build_release.sh`**:
This script is primarily designed to **simulate the CI/CD packaging process locally**. It allows you to verify that the release tagging and bundling logic works correctly before pushing to GitHub. Running it locally ensures that the `sop-httpserver` bundle (which includes the server binary and language libraries) is generated correctly, just as the GitHub Action will do.

To verify the build locally:
1.  Run `./build_release.sh`.
2.  Inspect `release/` to ensure all artifacts (archives, binaries, shared libraries) are created with the correct structure.
3.  (Optional) Extract a generated bundle and try running the `install.sh` or the `sop-httpserver` binary to confirm it works on your machine.
4.  Run `bindings/python/sanity_check.py` (ensure you have the local `.so` adjacent to it) to verify the FFI bridge works.
