# Kubernetes admission gate

`sop-k8s-gate` is a Kubernetes validating admission webhook. It puts the same barrier the MCP server uses in front of destructive cluster operations: a protected object is deleted only if the steps the runbook requires first have been recorded.

Source: [`tools/k8sgate`](../tools/k8sgate), [`cmd/sop-k8s-gate`](../cmd/sop-k8s-gate), manifests in [`deploy/k8s-gate`](../deploy/k8s-gate).

## What it does

A policy file maps a Kubernetes operation to a runbook step:

```json
{"rules": [{"resource": "persistentvolumeclaims", "operation": "DELETE", "workflow": "db-maintenance", "step": "drop_prod_db"}]}
```

When a `DELETE` for a PersistentVolumeClaim reaches the API server, the webhook reads the `joltrin.dev/trace-id` annotation from the object and asks the runbook whether `drop_prod_db` may run in that trace. In `db-maintenance` it may not until `take_backup` and `validate_backup` are recorded. If it may not, the API server refuses the delete and returns the reason:

```
Error from server: admission webhook "gate.joltrin.dev" denied the request: step "drop_prod_db" requires state "backup_validated", which has not been established in this trace
```

Steps are recorded with `POST /commit`, which needs a bearer token:

```bash
curl -sk -X POST https://joltrin-gate.joltrin-system.svc/commit \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"workflow":"db-maintenance","trace_id":"run-1","step":"take_backup"}'
```

The commit endpoint runs the same check as `execute_step`, so `validate_backup` is refused until `take_backup` is recorded. Requests the policy does not cover are allowed.

## Run it

Requires cert-manager. The manifests take the webhook's CA bundle from a cert-manager Certificate, and the gate reloads the certificate when it rotates.

```bash
docker build -f deploy/k8s-gate/Dockerfile -t joltrin-k8s-gate .
kubectl create namespace joltrin-system
kubectl -n joltrin-system create secret generic joltrin-gate-commit --from-literal=token="$(openssl rand -hex 32)"
kubectl apply -k deploy/k8s-gate
```

Edit the image in `deploy/k8s-gate/deployment.yaml` to point at the image you built or pushed. Then label what you want protected:

```bash
kubectl -n prod label pvc orders-db-data joltrin.dev/gate=enabled
kubectl -n prod annotate pvc orders-db-data joltrin.dev/trace-id=run-1
```

Flags: `--policy`, `--runbooks` (your own runbook file, same format as the MCP server), `--tls-cert`, `--tls-key`, `--commit-token-file`, `--addr`, and `--warn-only`. With `--warn-only` every request is allowed and what would have been blocked comes back as an admission warning, so you can see the effect of a policy before it enforces.

## Metrics and audit log

`GET /metrics` serves Prometheus text on the same TLS port as the webhook:

- `joltrin_k8sgate_admission_total{operation, resource, result}` where result is `allowed`, `denied`, `warned` (warn-only mode) or `unmatched` (no rule covers the request). Past 256 distinct label sets, new ones are counted under `other`, so request content cannot grow the series without limit.
- `joltrin_k8sgate_commit_total{result}` where result is `ok`, `unauthorized`, `conflict`, `rejected` or `disabled`.

Every admission decision also writes one JSON line to stderr (`msg: "admission"`, with `result`, `operation`, `resource`, `trace_id`, `uid` and `reason`). A denied delete therefore leaves a record of which run it belonged to and which state was missing. Alert on a rise in `denied`, and on `commit_total{result="unauthorized"}`, which means something is calling the commit endpoint with the wrong token.

Prometheus has to scrape over HTTPS. With the Prometheus Operator that is a `ServiceMonitor` with a `tlsConfig` for the gate's certificate authority. This repository does not ship one.

`deploy/k8s-gate/networkpolicy.yaml` allows ingress on 8443 only and denies all egress, since the gate reads its runbooks and policy from mounted files. The manifest builds with `kubectl kustomize`. Enforcement was not tested, because it depends on the cluster's network plugin.

## What was checked

The package has unit tests for each behavior above. The manifests and the webhook were also run against a real API server (kind, with cert-manager):

- a delete of a labelled PVC with nothing recorded was refused with the message above
- a delete of an unlabelled PVC went through
- `/commit` with a wrong token returned 401, and `validate_backup` before `take_backup` returned 409
- after `take_backup` and `validate_backup` were recorded, the same delete succeeded
- with the gate scaled to zero, a delete of a labelled PVC failed, because the webhook uses `failurePolicy: Fail`

## What it does not do

- **Whoever holds the commit token can record a step.** The gate does not check that a backup happened. It checks that something with the token said so. Give the token to the job that takes and validates backups, and not to the agent or workload being controlled. That separation is the control.
- **Removing the label or the trace id is refused, but only through this webhook.** The webhook also receives `UPDATE` for protected objects and denies any update that removes or changes the `joltrin.dev/gate` label or changes or clears the trace id annotation. That closes the path of patching the label off and then deleting. It does not stop anyone who can edit or delete the `ValidatingWebhookConfiguration` itself, so restrict that with RBAC. This was covered by unit tests only. It has not been run against a real API server yet, and because `UPDATE` now reaches the gate, a protected object cannot be updated while the gate is down.
- **The trace id is an annotation, so it is only as trustworthy as who can write it.** The gate cannot stop someone who can edit the object from pointing it at a trace that is complete. Use RBAC to limit who can set the annotation.
- **It covers what the policy lists and what the webhook configuration routes to it.** Only objects with the `joltrin.dev/gate` label reach it, and only for the operations and resources in `webhook.yaml`. Anything else is not gated.
- **State is in memory and the gate runs as one replica.** A restart empties the recorded steps, so steps have to be recorded again before a protected delete is allowed. Two replicas would each hold their own state.
- **A denied or allowed admission is not an execution.** The gate does not record `drop_prod_db` when it allows the delete, because another admission webhook or the API server can still refuse it.
- **If the gate is down, protected deletes fail.** That is `failurePolicy: Fail`, chosen so an outage cannot be used to skip the check. The label selector keeps the effect to protected objects.
