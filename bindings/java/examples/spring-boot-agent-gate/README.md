# spring-boot-agent-gate

A Spring Boot service that runs an agent plan through the joltrin verification barrier. You give it a goal step. It asks the barrier to run it. When the barrier blocks the step, it runs the steps the barrier says establish the missing state, then retries. When nothing establishes it, it stops and returns the reason for a person.

It talks to `sop-mcp-server` over MCP streamable HTTP, so it needs no native library (unlike `spring-boot-checkout-store`). There is no LLM in the loop: the planner is deterministic, and the barrier is the part that says no.

## Run it

```bash
# terminal 1: the barrier
go run ./cmd/sop-mcp-server http --addr 127.0.0.1:8088

# terminal 2: the agent service
cd bindings/java/examples/spring-boot-agent-gate
mvn spring-boot:run          # or: ./gradlew bootRun

# terminal 3
curl -s localhost:8080/agent/runs -H 'Content-Type: application/json' \
  -d '{"workflow":"db-maintenance","traceId":"t1","goal":"drop_prod_db"}'
```

`drop_prod_db` is blocked first, so the service runs `take_backup`, then `validate_backup`, then `drop_prod_db`. The response lists every decision in order.

Status is `COMPLETED`, `NEEDS_HUMAN`, or `BUDGET_EXHAUSTED` (`JOLTRIN_MAX_CALLS`, default 20). An unknown workflow or an unreachable barrier returns 502.

## Config

| Variable | Default |
|---|---|
| `JOLTRIN_BARRIER_URL` | `http://127.0.0.1:8088/mcp` |
| `JOLTRIN_BARRIER_TOKEN` | empty (loopback only) |
| `JOLTRIN_MAX_CALLS` | `20` |

## Build and deploy targets

| Target | Files |
|---|---|
| Maven, Gradle | `pom.xml`, `build.gradle.kts`, `./gradlew` |
| Docker, Podman, Buildah | `Dockerfile`, `scripts/build-image.sh` picks the engine that is installed |
| Docker Compose (two services) | `compose.yaml`, `Dockerfile.barrier` |
| Helm, OpenShift | `deploy/helm/agent-gate` (`--set route.enabled=true` adds an OpenShift Route) |
| Azure DevOps | `azure-pipelines.yml` (Maven and Gradle tests, chart lint, ACR push) |
| AWS CodeBuild, CloudFormation | `deploy/aws/buildspec.yml`, `deploy/aws/ci.yaml` (ECR repo, CodeBuild project, least-privilege role) |
| Smoke test | `scripts/smoke.py`, `scripts/smoke.ps1` |

## What is verified

- `mvn test` and `./gradlew test`: 5 tests pass in both.
- Ran the packaged jar against a real `sop-mcp-server`: the run above completed in the order shown, an unknown workflow returned 502, an empty body returned 400.
- `helm lint` and `helm template` pass, with and without the Route.
- `shellcheck` is clean on the build script. `scripts/smoke.py` passed against the running jar and a real barrier.

## What is not verified

- The Docker images and `compose.yaml` were not built here (no running Docker daemon). Podman and Buildah paths were not run.
- The Helm chart was rendered, not installed on an OpenShift or Kubernetes cluster.
- `azure-pipelines.yml`, `buildspec.yml`, and `ci.yaml` parse as YAML but were not run in Azure DevOps or AWS.
- `smoke.ps1` was not run (no PowerShell on this machine).
