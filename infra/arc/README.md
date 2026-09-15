# ARC runner scale set for bindings builds

bindings/Dockerfile.build installs mingw-w64 and pulls the Zig toolchain
fresh on every GitHub-hosted runner that cross-compiles the Python, Java,
C#, and Rust bindings. That setup cost repeats on every job.

This wires the same image into a self-hosted GitHub Actions Runner
Controller (ARC) scale set on Kubernetes instead, so the toolchain and Go
module cache live on the runner's volume across jobs instead of getting
rebuilt from scratch each time.

## Install order

1. Controller, once per cluster:

   ```
   helm install arc oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller \
     --namespace arc-systems --create-namespace -f controller-values.yaml
   ```

2. Scale set, scoped to bindings builds:

   ```
   helm install joltrin-bindings-runners oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set \
     --namespace arc-runners --create-namespace -f bindings-runner-scaleset-values.yaml
   ```

`joltrin-arc-github-token` is a Kubernetes secret created out of band
(`kubectl create secret generic joltrin-arc-github-token --from-literal=github_token=...`),
not committed here. Needs repo-scoped `actions:read`/`actions:write`.

## Status

Not installed on a live cluster yet, this is the IaC only. `arc-smoke.yml`
is `workflow_dispatch`-only so it can queue against the scale set label
without ever touching a real push or PR run. Once the scale set is
actually up, `ci.yml`'s bindings cross-compile leg can point at
`joltrin-bindings-runners` the same way.
