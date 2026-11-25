# L0 → L1 → L2 acceptance matrix

Use explicit kubeconfigs, a dedicated CKM, a known image build, and a unique
test prefix. Run each applicable storage case with `local-path` and with a real
CSI StorageClass when one is available. Record **pass / fail / not run** for
each row; a partial result is not an overall pass.

| Gate | Action | Evidence for pass |
| --- | --- | --- |
| L1 K3s Server Pod on L0 | Inspect the named CKM Server Pod and its runtime class. | `spec.hostUsers=false`, expected outer RuntimeClass, CKM Ready, `/dev/fuse` usable without a Pod FUSE volume. Apply the same rule to any further nested K3s Server Pod. This may still be the old `sysbox-runc` during isolated migration testing; record that fact. |
| L1 installation | Install the `sysrunc` Chart in the dedicated CKM; wait through any K3s restart. | Helm release `w7panel-sysrunc` deployed; agent and admission Ready; FS and snapshotter sockets respond; `crictl info` contains handler `sysrunc` with `BinaryName` under the installed Sysrunc release and snapshotter `sysrunc`. Compare binary SHA256 with the build under test. |
| L1 isolation | Inspect loaded containerd config. | Native `runc` and `sysrunc` handlers exist; no accidental legacy `sysbox-runc-nested` or `sysrunc.real` handler is used. `images.runtime_platforms.sysrunc` maps to the `sysrunc` snapshotter. The K3s Server Pod above has `hostUsers:false`; ordinary agent and admission Pods do not require it. |
| L2 ordinary workload | Create a named nginx Pod or Deployment with `runtimeClassName: sysrunc`, without FS annotation or `hostUsers:false`. | L2 does not require `hostUsers:false`. Pod Ready; CRI sandbox reports `runtimeHandler: sysrunc` and `SANDBOX_READY`; HTTP on nginx returns 200 and expected page. Inspect the sandbox, rather than relying on the Pod field alone. |
| L2 PVC initialization | Mount a fresh, empty PVC at nginx webroot using the same Pod. | Image files such as `index.html` and `50x.html` appear in the PVC; no legacy `sysbox/volume-init` annotation is needed. |
| L2 persistence | Write unique markers to the webroot PVC and `/srv/data`, recreate only that named test Pod with the same PVC. | Both markers and nginx HTTP content survive; capture the new Pod UID to prove recreation occurred. Do not delete the PVC. |
| L2 FS / seccomp notify | Create a separate, named FS-enabled Pod using `sysrunc.io/fs`; inspect admission result, kubelet events, daemon logs, and functional filesystem operations. | Pod Ready and filesystem operations work through the intended seccomp notify path. A socket alone is insufficient. If kubelet rejects `hostUsers:false` due to handler user namespace support, this gate fails. |
| Reinstall stability | Reapply the same Chart and image to the test CKM. | Agent Ready, handler unchanged, L2 case still passes, and unchanged config does not restart K3s again. |

## Evidence commands

Run commands in the relevant cluster. The L1 `crictl` command runs inside the
CKM Server's K3s environment, through a controlled shell or `kubectl exec`:

```bash
kubectl --kubeconfig "$L0_KUBECONFIG" -n "$CKM_NAMESPACE" get pod "$CKM_SERVER_POD" -o yaml
kubectl --kubeconfig "$L0_KUBECONFIG" -n "$CKM_NAMESPACE" exec "$CKM_SERVER_POD" -c k3s-server -- /bin/kubectl get runtimeclass sysrunc -o yaml
kubectl --kubeconfig "$L0_KUBECONFIG" -n "$CKM_NAMESPACE" exec "$CKM_SERVER_POD" -c k3s-server -- /bin/crictl --runtime-endpoint unix:///run/k3s/containerd/containerd.sock info
kubectl --kubeconfig "$L0_KUBECONFIG" -n "$CKM_NAMESPACE" exec "$CKM_SERVER_POD" -c k3s-server -- /bin/crictl --runtime-endpoint unix:///run/k3s/containerd/containerd.sock pods -o json
kubectl --kubeconfig "$L0_KUBECONFIG" -n "$CKM_NAMESPACE" exec "$CKM_SERVER_POD" -c k3s-server -- /bin/kubectl -n "$L2_NAMESPACE" describe pod "$L2_POD"
```

For release acceptance, verify the published image digest and Chart checksum
before installation. A workspace image imported into L1 is a source integration
result only. If a case fails, save Pod description, events, container and agent
logs, PVC state, exact `crictl info`, and relevant build hashes. Do not mark
the release accepted until all required gates pass with release artifacts.

## Current 218 baseline, 2026-11-23

The isolated CKM `k3k-console-164315/ckm-sysrunc-it-1123` used the old outer
`sysbox-runc` with `hostUsers:false`. Its inner `sysrunc` handler and ordinary
nginx workload passed; PVC initialization and marker persistence after Pod
recreation passed. After removing L1's automatic `hostUsers:false`, the
FS-enabled Pod progressed past `/proc/sys` access after source fixes, but
loading the inner seccomp notify filter returned EBUSY while L0 still used
legacy `sysbox-runc` with a seccomp filter. The old L0 Sysbox installer and
RuntimeClasses remained installed. See the repository's `INTEGRATION_218.md`
for build identity and details. This is **not** an FS or full migration pass.

The later L0 debug Pod run installed a separate `sysrunc` handler and its
snapshotter on `server1`, with the old Sysbox handlers still present. CRI does
not report `user_namespaces` for `sysrunc`: its OCI feature output lacks
`linux.mountExtensions.idmap.enabled`. Do not switch a `hostUsers:false` L1
Server Pod to this handler until real idmapped mount support is implemented and
verified. The dedicated CKM remains on L0 `sysbox-runc`. See the incident and
recovery record in `INTEGRATION_218.md` before repeating host installation.

## 218 follow-up, 2026-11-23

L0 isolated Helm `default/sysrunc-l0-it` revision 6 now installs a Sysrunc
handler with `user_namespaces:true`. Its local test binary SHA256 is
`634c630df40241fdfe1afebf92f7ac9aecc39aadf3001c63adc1c6a18d7d7e7d`.
The dedicated CKM's new L0 Server Pod had `hostUsers:false`, used Sysrunc,
and received `/dev/fuse` without a Pod hostPath. Its L1 K3s failed at
`/dev/kmsg` and kube-proxy iptables permissions after switching to the native
K3s snapshotter. FS mode also lacks the K3s sandbox sysctl path under its
virtual `/proc/sys`. The CKM was restored to `sysbox-runc` and is Ready;
the production CKM controller image was restored. No L0-to-L2 or FS pass has
been recorded. See `INTEGRATION_218.md` for the attempted images and events.

Revision 7 then added a `/dev/kmsg` shim and CKM Server capabilities.
The former kubelet device and kube-proxy iptables failures cleared, but
K3s still failed on read-only `route_localnet` and denied
`/var/lib/kubelet` bind mount. The dedicated CKM and production controller
were restored again. No later gate has passed.

The next isolated test set the Sysrunc CKM Server to AppArmor `Unconfined`
and `procMount: Unmasked`; kubelet and kube-proxy passed their earlier
mount and sysctl errors, then kubelet failed to create
`/sys/fs/cgroup/kubepods` on a read-only cgroup mount. The CKM and
production controller were restored again. L1/L2 and FS remain failed.

The 2026-11-24 isolated follow-up made cgroup v2 writable and delegated its
root, then moved the K3s init process into `init.scope`. It also cleared the
nested OOM adjustment and mount namespace access failures. L1 system Pod
sandboxes now fail when native runc mounts `sysfs` (`operation not permitted`).
The CKM and controller were restored; L1/L2 and FS still fail.

The later independent L0 FS Pod passed its sysctl read and a `sysfs` mount
after correcting the FS nsenter cgroup namespace ordering. This is an isolated
L0 source integration result: the new FS binary was installed only on the node,
and the dedicated CKM remained on outer `sysbox-runc`. The L1/L2 FS gate and
published release gate remain unverified.

## 218 source integration result, 2026-11-24

The dedicated CKM now uses outer `sysrunc` with `hostUsers:false` and no L0
FS annotation. L1 agent and snapshotter are Ready with a verified `sysrunc`
binary path. Fresh L2 ordinary, FS, and two-PVC Pods passed CRI handler,
functional FS, HTTP, image-file initialization, and persistence checks. CSI
driver recovered after shared persistent special mounts replaced duplicate
private mounts. Reapplying the same L1 Chart and image to revision 11 left
K3s and the agent unchanged. The exact Pods, hashes, and assertions are in
`INTEGRATION_218.md`. Only `local-path` storage was available inside L1.
The image is local and L0's binary was installed through a debug Pod, so the
published release gate remains **not run**. FUSE still backs some `/proc/sys`
paths; the seccomp notification filter and FS functionality passed, while
complete FUSE bypass is not established by this integration test.

## 218 full Dockerfile follow-up, 2026-11-24

The full Dockerfile now builds with a configurable CentOS mirror and static FUSE
helpers. L1 Helm revision 13 runs the complete local image and L0 Helm
revision 11 installs the final local image through its installer. L0 runtime,
FS, and snapshotter hashes match the final image; the dedicated CKM and three
L2 test Pods recovered after the L0 installer rebuilt the CKM Server. The two
PVC markers survived. Opening `/proc/sys/kernel/random/uuid` in the FS Pod
produced a `memfd:sysrunc-proc-uuid` descriptor, proving that narrow
`NOTIF_ADDFD` path. The TAPD `statx`/`getdents64` paths remain unimplemented,
and `/proc/sys` remains FUSE mounted. No image or Chart was published, so the
published artifact gate remains **not run**. See `INTEGRATION_218.md` for
identities, hashes, and the revision 12 interrupted Helm upgrade recovery.

Reapplying the same L0 Chart and final local image to revision 12 also passed:
the dedicated CKM Server Pod UID, container ID, and restart count were
unchanged; the installer Pod was unchanged; `check-status.sh` passed for all
three current L2 Pods. Ordinary, FS, and two-PVC HTTP checks returned 200, and
both PVC markers remained. This is a local integration result, not published
artifact acceptance.

## 218 proc fast path follow-up, 2026-11-24

L1 Helm revision 14 uses the full local Dockerfile image
`sysrunc-it:218-20261124-proc-fastpath` (ID
`sha256:f9d3e32e0f6623c4a91d0f6fb720bbb80a1ce00d77e4e54922d395fadf319588`).
The three recreated L2 Pods passed `check-status.sh`, HTTP, and PVC marker
checks. The direct syscall probe in `scripts/proc-fastpath-probe.c` confirmed
that the exact `/proc/sys/kernel/random` directory fd is procfs, its
`getdents64` returns `uuid`, and relative UUID opens still use a memfd. UUID
`statx` returned the virtual size and read-only mode. A nonmatching `/proc/sys`
directory fd remained FUSE backed. This verifies the narrow fast path; other
virtual proc resources and published artifacts remain outside the pass.

To repeat the direct syscall probe, build
`scripts/proc-fastpath-probe.c` with `gcc -O2 -static`, copy it into the named
FS-enabled L2 Pod, and execute it there. The program exits nonzero if an
expected syscall result or backing filesystem type differs.

The directory FD injection was removed after this run: a host proc directory
FD permits relative `../` traversal outside the container's FUSE view. The
current probe expects the random directory and a relative `../hostname` open
to remain FUSE backed while `getdents64` still lists `uuid` and relative UUID
opens use a memfd. L1 revision 22 with local image
`sysrunc-it:218-20261124-proc-dir-safe` passed this updated probe in the
FS Pod, plus the seven-file probe, all three `check-status.sh` runs, HTTP,
and two-PVC marker checks. The same-image reapply left K3s and the Pods
unchanged. The directory `getdents64` path still uses FUSE; published
artifact acceptance has not run. See `INTEGRATION_218.md` for hashes.

L1 revision 26 uses the later local `diskstats-worker` image. The updated
directory probe and seven-file probe passed again, as did all three status
checks, HTTP, PVC markers, and a same-image Helm reapply. The proc worker
also restarted after a forced exit in revision 24 while the original FS Pod
continued to serve UUID reads. Published artifact acceptance remains open.

## 218 read-only proc handlers, 2026-11-24

L1 Helm revision 17 uses full local image
`sysrunc-it:218-20261124-proc-handlers-ro` (ID
`sha256:fa09ee02aa24ffd69ac23476aed1c873b6b5f4c108af06e6dc4419e5ce82b300`).
`scripts/proc-files-probe.c` passed in the FS Pod for seven exact proc file
mounts: each opened as a sealed read-only memfd, `statx` returned virtual
metadata, and an unsupported open flag fell back to FUSE. Relative meminfo
access passed. The UUID and directory probe still passed. Reapplying the
same L1 Chart and image to revision 18 left the CKM container, L1 agent, and
L2 FS Pod unchanged. Three L2 Pods passed the status script, HTTP, and PVC
markers. The full FS Go test suite passed.

`/proc/slabinfo` is not accelerated because the nested user namespace cannot
read its host source; FUSE now reports `EACCES`. Other `/proc/sys` directories
and writable virtual files still require backing providers. This remains a
local source integration result, not acceptance of published artifacts or
complete proc FUSE bypass.

## 218 stat worker follow-up, 2026-11-24

L1 Helm revision 44 uses local image `sysrunc-it:218-20261124-stat-worker`.
The four named L2 Pods passed `check-status.sh`; ordinary, FS, PVC, and ticker
HTTP returned 200, and the three PVC markers remained. The updated
`scripts/proc-files-probe.c` confirmed `/proc/stat` is a read-only memfd on the
plain open path, `O_NONBLOCK` falls back to FUSE, both paths report eight CPU
lines, and worker counters do not decrease. The directory/UUID escape probe
also passed. Reapplying the same Chart and image left K3s, FS, snapshotter,
agent, and L2 Pod identities unchanged. See `INTEGRATION_218.md` for hashes.
The image and Chart have not been published; this is a source integration
result only.

## 218 gRPC service follow-up, 2026-11-24

The local `protocol-service` image ran at L1 Helm revision 48, and the same
image reapply to revision 49 left K3s, FS, snapshotter, agent, FS Pod, and
ticker Pod identities unchanged. Direct calls to both the new
`protobuf.sysruncStateChannel` and legacy `protobuf.sysboxStateChannel` on
the live FS socket returned `/var/lib/sysruncfs-inner`. All four L2 status
checks, HTTP responses, PVC markers, and FS probes passed. This verifies the
rolling protocol compatibility in the isolated CKM; at that revision the
`getdents64`, remaining FUSE, and published artifact gates were still open.

## 218 getdents64 follow-up, 2026-11-25

The local `getdents-observe` image ran at L1 Helm revision 51. Its running FS
binary matched the built SHA256, and the FS log recorded a successful
`/proc/sys/kernel/random` `getdents64` seccomp notify fast path during the
direct syscall probe. The tracee retained its FUSE directory fd, shared
offsets and rewind worked, and relative `../hostname` remained FUSE backed.
Four L2 status checks, the seven-file probe, nginx HTTP, and three persistent
PVC markers passed. Same-image revision 52 preserved K3s, daemon, agent, and
L2 Pod identities. This closes the exact random-directory fast-path gate for
the local build; other FUSE resources and published artifact acceptance remain
open. The current source Chart package also passed a same-image revision 53
upgrade without changing these identities. See `INTEGRATION_218.md` for hashes
and identities.

## 218 ticker goroutine follow-up, 2026-11-25

The isolated CKM rolled L1 installation back to the earlier `getdents-safe`
image at Helm revision 54 and recovered a new Sysrunc nginx sandbox. Revision
55 restored `getdents-observe`. Both binary changes restarted K3s and left
existing `restartPolicy: Never` test Pods `Unknown`; recreating only the named
test Pods restored them without deleting PVCs. This verifies a recoverable L1
installer rollback, not uninterrupted workloads or L0 node rollback.

Revision 56 installed the local `ticker-goroutine` image. The running FS hash
matched the build; the ticker Pod read `/proc/loadavg`. Four L2 status checks,
nginx HTTP, PVC markers, the seven-file probe, and the random-directory
`getdents64` probe passed. Reapplying the same image and Chart at revision 57
left K3s restartCount 25, the ticker worker PID, and the four L2 Pod UIDs
unchanged. This remains local-source integration; published artifact acceptance
and L0 node-level rollback remain open.

## 218 getdents64 count-width follow-up, 2026-11-25

A raw syscall probe found that the local revision 57 FS fast path accepted a
64-bit count whose low 32 bits were 16 and returned 224 bytes, while a plain
count of 16 returned `EINVAL`. The kernel treats the argument as unsigned int.
The fixed notifier defers requests with nonzero high bits to the kernel/FUSE
path. The same probe returned `EINVAL` for both cases on the revision 58 local
image; a normal 224-byte directory read still hit seccomp notify. Four L2
status, HTTP, ticker, and PVC checks passed. Same-image revision 59 preserved
the K3s restart count and all four L2 Pod UIDs. The image remains unpublished.
