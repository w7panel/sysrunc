#!/usr/bin/env bash
set -euo pipefail

for name in L0_KUBECONFIG CKM_NAMESPACE CKM_SERVER_POD L2_NAMESPACE L2_POD; do
  if [[ -z "${!name:-}" ]]; then
    printf 'required: %s\n' "$name" >&2
    exit 2
  fi
done

command -v kubectl >/dev/null || { echo 'kubectl is required' >&2; exit 2; }
command -v python3 >/dev/null || { echo 'python3 is required' >&2; exit 2; }

ckm_container=${CKM_SERVER_CONTAINER:-k3s-server}
cri_endpoint=unix:///run/k3s/containerd/containerd.sock

l0() { kubectl --kubeconfig "$L0_KUBECONFIG" -n "$CKM_NAMESPACE" "$@"; }
l1() { l0 exec "$CKM_SERVER_POD" -c "$ckm_container" -- /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"; }
cri() { l0 exec "$CKM_SERVER_POD" -c "$ckm_container" -- /bin/crictl --runtime-endpoint "$cri_endpoint" "$@"; }

outer_userns=$(l0 get pod "$CKM_SERVER_POD" -o jsonpath='{.spec.hostUsers}')
outer_runtime=$(l0 get pod "$CKM_SERVER_POD" -o jsonpath='{.spec.runtimeClassName}')
outer_phase=$(l0 get pod "$CKM_SERVER_POD" -o jsonpath='{.status.phase}')
printf 'L1 K3s Server Pod on L0: %s/%s phase=%s runtimeClass=%s hostUsers=%s\n' \
  "$CKM_NAMESPACE" "$CKM_SERVER_POD" "$outer_phase" "$outer_runtime" "$outer_userns"
[[ "$outer_userns" == false ]] || { echo 'FAIL: L1 K3s Server Pod hostUsers must be false' >&2; exit 1; }
[[ "$outer_phase" == Running ]] || { echo 'FAIL: L1 K3s Server Pod is not Running' >&2; exit 1; }

l1_handler=$(l1 get runtimeclass sysrunc -o jsonpath='{.handler}')
printf 'L1 RuntimeClass: sysrunc handler=%s\n' "$l1_handler"
[[ "$l1_handler" == sysrunc ]] || { echo 'FAIL: L1 RuntimeClass handler is not sysrunc' >&2; exit 1; }

cri info | python3 -c '
import json, sys
data = json.load(sys.stdin)
runtime = data["config"]["containerd"]["runtimes"].get("sysrunc", {})
binary = runtime.get("options", {}).get("BinaryName", "")
snapshotter = runtime.get("snapshotter", "")
print(f"L1 CRI: BinaryName={binary} snapshotter={snapshotter}")
if not binary.endswith("/sysrunc") or "sysrunc-inner/releases/" not in binary:
    sys.exit("FAIL: L1 CRI does not point to the installed Sysrunc binary")
if snapshotter != "sysrunc":
    sys.exit("FAIL: L1 CRI sysrunc handler does not use the sysrunc snapshotter")
'

l2_runtime=$(l1 -n "$L2_NAMESPACE" get pod "$L2_POD" -o jsonpath='{.spec.runtimeClassName}')
l2_phase=$(l1 -n "$L2_NAMESPACE" get pod "$L2_POD" -o jsonpath='{.status.phase}')
l2_uid=$(l1 -n "$L2_NAMESPACE" get pod "$L2_POD" -o jsonpath='{.metadata.uid}')
l2_userns=$(l1 -n "$L2_NAMESPACE" get pod "$L2_POD" -o jsonpath='{.spec.hostUsers}')
printf 'L2 Pod: %s/%s uid=%s phase=%s runtimeClass=%s hostUsers=%s\n' \
  "$L2_NAMESPACE" "$L2_POD" "$l2_uid" "$l2_phase" "$l2_runtime" "${l2_userns:-unset}"
[[ "$l2_runtime" == sysrunc ]] || { echo 'FAIL: L2 Pod does not request sysrunc' >&2; exit 1; }
[[ "$l2_phase" == Running ]] || { echo 'FAIL: L2 Pod is not Running' >&2; exit 1; }

cri pods --name "$L2_POD" -o json | python3 -c '
import json, sys
name, namespace, uid = sys.argv[1:]
items = json.load(sys.stdin).get("items", [])
matches = [item for item in items if item.get("metadata", {}).get("name") == name
           and item.get("metadata", {}).get("namespace") == namespace
           and item.get("metadata", {}).get("uid") == uid]
for item in matches:
    print("L2 CRI sandbox: id={} handler={} state={}".format(
        item.get("id"), item.get("runtimeHandler"), item.get("state")))
if not any(item.get("runtimeHandler") == "sysrunc" and item.get("state") == "SANDBOX_READY"
           for item in matches):
    sys.exit("FAIL: current L2 Pod has no ready Sysrunc CRI sandbox")
' "$L2_POD" "$L2_NAMESPACE" "$l2_uid"

echo 'Status checks passed. Binary hash, HTTP, PVC, and FS gates require the acceptance matrix.'
