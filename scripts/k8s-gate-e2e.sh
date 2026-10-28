#!/usr/bin/env bash
# Runs the Kubernetes admission gate against a real API server in a disposable kind cluster
# and checks each behavior described in docs/KUBERNETES_GATE.md. Needs docker, kind and kubectl,
# and network access to fetch cert-manager. It touches only the kind cluster it creates and
# deletes it when it finishes. Run from the repository root:
#
#   scripts/k8s-gate-e2e.sh
#
# Set KEEP_CLUSTER=1 to leave the cluster up for inspection (delete it with: kind delete cluster --name joltrin-gate-e2e).
set -euo pipefail

CLUSTER=joltrin-gate-e2e
CTX=kind-$CLUSTER
CERT_MANAGER=v1.15.3
IMAGE=joltrin-k8s-gate:e2e
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
PF_PID=""

k() { kubectl --context "$CTX" "$@"; }
pass=0
fail=0
# check NAME EXPECT COMMAND...  where EXPECT is "ok" or "denied". A denial must come from the gate.
check() {
  local name=$1 want=$2; shift 2
  local out rc=0
  out=$("$@" 2>&1) || rc=$?
  if [ "$want" = ok ] && [ $rc -eq 0 ]; then echo "PASS  $name"; pass=$((pass+1))
  elif [ "$want" = denied ] && [ $rc -ne 0 ] && grep -q 'gate.joltrin.dev' <<<"$out"; then echo "PASS  $name"; pass=$((pass+1))
  elif [ "$want" = failed ] && [ $rc -ne 0 ]; then echo "PASS  $name"; pass=$((pass+1))
  else echo "FAIL  $name (wanted $want, exit $rc)"; echo "$out" | sed 's/^/      /'; fail=$((fail+1)); fi
}

cleanup() {
  if [ -n "$PF_PID" ]; then kill "$PF_PID" 2>/dev/null || true; wait "$PF_PID" 2>/dev/null || true; fi
  rm -rf "$WORK"
  if [ "${KEEP_CLUSTER:-}" != 1 ]; then kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

for tool in docker kind kubectl openssl curl; do command -v "$tool" >/dev/null || { echo "missing: $tool" >&2; exit 2; }; done
docker info >/dev/null 2>&1 || { echo "docker is not running" >&2; exit 2; }

echo "== cluster"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --wait 120s >/dev/null
docker build -q -f "$ROOT/deploy/k8s-gate/Dockerfile" -t "$IMAGE" "$ROOT" >/dev/null
kind load docker-image "$IMAGE" --name "$CLUSTER" >/dev/null
k apply -f "https://github.com/cert-manager/cert-manager/releases/download/$CERT_MANAGER/cert-manager.yaml" >/dev/null
k -n cert-manager wait --for=condition=Available deploy --all --timeout=240s >/dev/null

echo "== gate"
k create namespace joltrin-system >/dev/null
TOKEN=$(openssl rand -hex 32)
k -n joltrin-system create secret generic joltrin-gate-commit --from-literal=token="$TOKEN" >/dev/null
cp -r "$ROOT/deploy/k8s-gate" "$WORK/gate"
sed -i.bak "s#image: ghcr.io/sharedcode/joltrin-k8s-gate.*#image: $IMAGE\\
          imagePullPolicy: Never#" "$WORK/gate/deployment.yaml"
k apply -k "$WORK/gate" >/dev/null
k -n joltrin-system rollout status deploy/joltrin-gate --timeout=180s >/dev/null

echo "== checks"
k create namespace prod >/dev/null
claim() {
  k -n prod apply -f - >/dev/null <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: $1}
spec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Mi}}}
EOF
}
claim orders-db-data
claim unprotected

check "unlabelled claim can be deleted"                    ok     k -n prod delete pvc unprotected
check "label and trace id can be added"                    ok     k -n prod label pvc orders-db-data joltrin.dev/gate=enabled
check "trace id can be added"                              ok     k -n prod annotate pvc orders-db-data joltrin.dev/trace-id=run-1
check "unrelated annotation is accepted"                   ok     k -n prod annotate pvc orders-db-data note=hello
check "removing the gate label is refused"                 denied k -n prod label pvc orders-db-data joltrin.dev/gate-
check "changing the trace id is refused"                   denied k -n prod annotate pvc orders-db-data joltrin.dev/trace-id=run-2 --overwrite
check "removing the trace id is refused"                   denied k -n prod annotate pvc orders-db-data joltrin.dev/trace-id-
check "delete with nothing recorded is refused"            denied k -n prod delete pvc orders-db-data

k -n joltrin-system port-forward svc/joltrin-gate 18443:443 >/dev/null 2>&1 &
PF_PID=$!
for _ in $(seq 1 20); do curl -sk -o /dev/null https://127.0.0.1:18443/metrics && break; sleep 1; done
commit() { curl -sk -o /dev/null -w '%{http_code}' -X POST https://127.0.0.1:18443/commit -H "Authorization: Bearer $1" \
  -d "{\"workflow\":\"db-maintenance\",\"trace_id\":\"run-1\",\"step\":\"$2\"}"; }
expect_code() { local name=$1 want=$2 got=$3; if [ "$got" = "$want" ]; then echo "PASS  $name"; pass=$((pass+1)); else echo "FAIL  $name (wanted $want, got $got)"; fail=$((fail+1)); fi; }
expect_code "commit with a wrong token returns 401"        401 "$(commit wrong take_backup)"
expect_code "validate before take_backup returns 409"      409 "$(commit "$TOKEN" validate_backup)"
expect_code "take_backup is recorded"                      200 "$(commit "$TOKEN" take_backup)"
expect_code "validate_backup is recorded"                  200 "$(commit "$TOKEN" validate_backup)"
check "delete after the steps are recorded is allowed"     ok     k -n prod delete pvc orders-db-data

claim p2
k -n prod label pvc p2 joltrin.dev/gate=enabled >/dev/null
k -n prod annotate pvc p2 joltrin.dev/trace-id=run-9 >/dev/null
k -n joltrin-system scale deploy/joltrin-gate --replicas=0 >/dev/null
sleep 5
check "with the gate down, a protected claim cannot be updated" failed k -n prod annotate pvc p2 note=x

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
