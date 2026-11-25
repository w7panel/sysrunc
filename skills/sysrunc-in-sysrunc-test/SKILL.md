---
name: sysrunc-in-sysrunc-test
description: Validate Sysrunc on a nested CKM K3s cluster, from L0 host through L1 runtime installation to L2 workload and PVC persistence.
---

# Sysrunc nested integration test

Use this skill when testing or accepting Sysrunc in an L0 Kubernetes → L1 CKM
K3s → L2 workload topology. Read [the acceptance matrix](references/acceptance-matrix.md)
before changing the cluster. Record the image digest or binary hash, chart
revision, CKM identity, storage class, Pod names, and failed assertions.

## Scope and namespace rules

- L0 is the host Kubernetes cluster. The **L1 K3s Server Pod scheduled on L0**
  must have `spec.hostUsers: false`. Verify its outer runtime class and
  `/dev/fuse` access. Apply the same rule to any Pod that runs another nested
  K3s server.
- L1 is K3s inside that Server Pod. Its agent and admission Pods do not need
  `hostUsers: false` merely because the K3s Server Pod has it.
- L1 must load a `sysrunc` CRI handler with `BinaryName` pointing to the new
  Sysrunc binary and `snapshotter = "sysrunc"`. A RuntimeClass object or a
  Running Pod alone does not prove which binary handled the sandbox.
- L2 is an ordinary workload Pod created in L1 with `runtimeClassName: sysrunc`.
  It does not need `hostUsers: false`. Confirm its CRI sandbox uses
  `runtimeHandler: sysrunc`, then check HTTP and PVC behavior.
- FS-enabled Pods are a separate gate. If admission sets `hostUsers: false`
  and kubelet reports that the handler lacks user namespace support, mark FS,
  seccomp notify, and FS-dependent rootfs cases **failed**. Do not infer their
  success from the ordinary nginx case or from a live FS socket.

## Safe execution

Use a named, disposable CKM and unique test resource names. Pass the L0
kubeconfig and the exact CKM Server Pod explicitly. Never select a business CKM by discovery or run the
old `sysbox-in-sysbox` scripts: those scripts hardcode Sysbox handlers, Charts,
and cleanup targets. Do not uninstall or replace L0 Sysbox while validating
an isolated CKM. A test image imported only into L1 containerd proves that
local build, not a published release.

The read-only status checker can collect the baseline for an existing CKM and
L2 Pod:

```bash
L0_KUBECONFIG=/path/to/host.config \
CKM_NAMESPACE=<outer-namespace> CKM_SERVER_POD=<outer-server-pod> \
L2_NAMESPACE=default L2_POD=<test-pod> \
  bash skills/sysrunc-in-sysrunc-test/scripts/check-status.sh
```

The checker runs `/bin/kubectl` and `/bin/crictl` inside the CKM Server's
`k3s-server` container. Set `CKM_SERVER_CONTAINER` only if the container has a
different name. No external L1 kubeconfig or L1 Helm binary is needed.

Then follow the functional cases in the matrix. A failed case remains failed
until the cause is fixed and the same assertion is rerun. Keep the test PVCs
and their markers for review; delete only named disposable resources when
cleanup is requested, and require explicit storage cleanup authorization before
deleting PVCs.
