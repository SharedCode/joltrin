# joltrin on AKS (demo)

Runs the same `tools/httpserver` image that `infra/azure` ships to Container Apps, but on AKS with Argo CD. Production stays on Container Apps. This is a short-lived environment for demos and recordings.

Git holds the desired state:

- `k8s/base` and `k8s/overlays/demo`: a one-replica StatefulSet on a 1Gi volume, a Service, and the image tag.
- `argocd/application.yaml`: the Argo CD Application that syncs the demo overlay from `master`.
- `.github/workflows/publish-image.yml`: builds, scans and pushes `ghcr.io/sharedcode/joltrin:sha-<commit>`.

The cluster and Argo CD come from [gitops-aks-demo](https://github.com/gerardrecinto/gitops-aks-demo) (`make up`, about $0.13 an hour, `make down` removes it).

## Run it

1. Run the `publish-image` workflow. The repository is public and the image holds only binaries built from it, with no secrets, so the GHCR package can be made public once, which lets the cluster pull without a pull secret.
2. Put the new tag in `k8s/overlays/demo/kustomization.yaml` through a pull request.
3. From gitops-aks-demo, `make up`, then apply the Application:

```
kubectl apply -f deploy/aks/argocd/application.yaml
kubectl -n argocd get applications
kubectl -n joltrin-demo port-forward svc/joltrin 8080:80
```

4. When done, `make down` in gitops-aks-demo. The volume is deleted with the resource group.

One replica is deliberate. The B-Tree engine has no guarantee of safe multi-process writes to one data path, so scaling out needs the Redis-backed lock cache first.
