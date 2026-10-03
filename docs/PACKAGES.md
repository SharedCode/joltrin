# Packages and releases

## Language Packages & Tooling

| Language | Installation | Description |
| :--- | :--- | :--- |
| **Go** | `go get github.com/sharedcode/joltrin/v5` | Native high-performance core engine. |
| **Python** | `pip install sop4py` | Python bindings with Data Manager and AI scripts. |
| **C#** | `dotnet add package Sop` | Complete .NET Core integration. |
| **WebAssembly** | `GOOS=js GOARCH=wasm go build` | Browser-sandboxed zero-server execution. |
| **HTTP Data Manager** | `sop-httpserver` | Standalone UI console and AI Copilot interface. |
| **Java** *(in progress)* | source in `bindings/java`, not yet on Maven Central | `sop4j` bindings and tests are complete; publishing is blocked on Central Portal credential setup, tracked in [`docs/RELEASE_PROCESS_JAVA_STATUS.md`](RELEASE_PROCESS_JAVA_STATUS.md). |
| **Rust** *(in progress)* | source in `bindings/rust`, not yet on crates.io | `sop4rs` bindings, tests, and examples exist in-repo but are not yet published as a crate. |

**Naming note:** Joltrin was called SOP until v5. The old names are unchanged so existing code keeps working: the Go package is still `sop`, the published packages are still `sop4py` and `Sop`, the binaries are still `sop-httpserver`, `sop-mcp-server`, `sop-a2a-agent` and `sop-a2a-bridge`, and the default data directory is still `/tmp/sop_data`.

### How to Consume Joltrin: Releases vs. In-Repo Source

When integrating Joltrin into your stack, choose between official versioned releases and in-repo source consumption based on your development and operational needs:

| Dimension | Official Tagged Releases (Recommended for Production) | In-Repo Source / Submodule (Active Prototyping & Contribution) |
| :--- | :--- | :--- |
| **Artifacts** | `go get github.com/sharedcode/joltrin/v5@vX.Y.Z`<br>PyPI: `pip install sop4py`<br>NuGet: `dotnet add package Sop` | Git clone or submodule linked directly to `HEAD` or a feature branch |
| **Best For** | Production services, reproducible CI/CD builds, audited dependencies | Modifying engine internals, local benchmarking, custom protocol servers |
| **Stability** | Semantic versioning, tagged releases, audited dependency graph | Bleeding-edge features, experimental branches, unreleased protocol bridges |
| **Maintenance** | Handled by standard language package managers | Requires manual git fetch/rebase and local workspace management |

#### 1. Official Tagged Releases (Recommended for Production)
For production deployments, pin your dependency to a tagged release. This guarantees reproducible builds, backward-compatible API guarantees, and security-scanned transitive dependencies:
- **Go**: `go get github.com/sharedcode/joltrin/v5@v5.7.0` (see [tags](https://github.com/sharedcode/joltrin/tags) for the latest)
- **Python**: `pip install sop4py==2.3.3`
- **C# / .NET**: `dotnet add package Sop --version 4.5.0`

#### 2. In-Repo Source / Submodule (Prototyping & Contribution)
If you are extending storage engine internals (`btree/`, `fs/`), modifying protocol servers (`verify`, `cmd/sop-mcp-server`, `cmd/sop-a2a-agent`), or benchmarking performance enhancements, consuming from source is recommended:
```bash
# Add as a git submodule in your project
git submodule add https://github.com/sharedcode/joltrin.git vendor/joltrin

# Or configure a Go workspace (go.work) for local development
go work use ./vendor/joltrin
```

### Cutting a Release (Maintainers)

Releases are cut from this repo with the scripts in `scripts/`, then tagged and pushed to GitHub. The full step-by-step for building native bindings and publishing to PyPI/NuGet/Maven is in [`RELEASE_PROCESS.md`](../RELEASE_PROCESS.md); the short version:

```bash
# 1. Bump the version everywhere (VERSION file, go.mod-adjacent metadata, bindings)
./scripts/update_version.sh 5.8.0

# 2. Review the diff, then commit the bump
git add -A && git commit -m "bumped version to 5.8.0"

# 3. Build release artifacts (native libs for Python/Java/C# bindings)
./scripts/build_release.sh

# 4. Verify checksums, archive integrity, and SBOM before publishing
./scripts/verify_release.sh release

# 5. Tag and push. This is what makes `go get github.com/sharedcode/joltrin/v5@v5.8.0` resolve.
git tag v5.8.0
git push origin master v5.8.0

# 6. Create the GitHub Release from the tag (attaches release notes + artifacts)
gh release create v5.8.0 --generate-notes
```

Go's package proxy needs no separate publish step: once the tag is pushed, `go get ...@v5.8.0` works immediately. Python, C#, and Java bindings still require the explicit `twine upload` / `dotnet nuget push` / `mvn deploy` steps in `RELEASE_PROCESS.md`.
