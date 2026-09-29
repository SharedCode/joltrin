# Contributing to SOP

Thank you for your interest in contributing to Joltrin (formerly SOP, Scalable Objects Persistence)! We welcome contributions from the community.

## Getting Started

1.  **Read the Documentation**:
    *   **[README.md](README.md)**: High-level overview and quick start.
    *   **[ARCHITECTURE.md](docs/ARCHITECTURE.md)**: Understanding the codebase structure, specifically the Public vs. Internal package split.

2.  **Explore the Code**:
    *   SOP V2 is written in Go. We prioritize simplicity and readability.
    *   Check out the unit tests to understand the interfaces.

## Development Workflow

1.  **Communication**:
    *   Before starting a major feature, please open an issue or discussion to coordinate with the authors. This helps avoid duplication of effort.

2.  **Branching Strategy**:
    *   Fork the repository.
    *   Create a feature branch for your changes.
    *   Submit a Pull Request (PR) to the `master` branch.

3.  **Testing**:
    *   **Unit Tests**: Run `run_all_tests.sh` bash shell script for quick feedback.
    *   **Stress & Integration Tests**: Critical for verifying backend interactions.
        *   For `infs` (In File System), you must run:
            ```bash
            go test -v -tags=stress -count=1 ./infs/stresstests/...
            ```
        *   For `incfs` (Hybrid Cassandra & File System), you must run:
            ```bash
            export SOP_RUN_INCFS_IT=1
            go test -v -tags=integration -count=1 ./incfs/integrationtests/...
            ```
    *   Ensure all tests pass before submitting your PR.

## Code Structure & Guidelines

*   **Public vs. Internal**:
    *   **Primary Public Packages**: `infs` (In File System), `jsondb` (JSON Document Store), `database` (Helpers) & `tools/httpserver` Data Manager RESTful web service & SPA WebUI. These represent the core user-facing APIs & Tools.
    *   **Secondary**: `incfs` (In Cassandra & File System) is now considered secondary.
    *   **Internal Packages**: Packages under `internal/` (e.g., `internal/inredck`) contain implementation details that should not be exposed.
    *   See [ARCHITECTURE.md](docs/ARCHITECTURE.md) for more details on the architecture.

*   **Style**: Follow standard Go idioms and formatting (`gofmt`).

## Questions?

Don't be shy to ask questions in the [Discussions](https://github.com/SharedCode/joltrin/discussions) tab. We are happy to help!
