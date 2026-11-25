# Migration status

This repository contains the Kubernetes runtime, FS service, IPC,
admission webhook, snapshotter, chart, and deployment scripts in one tree. The
root `go.work` resolves the local modules. `make build` produces one OCI runtime
binary plus the FS, admission, and snapshotter binaries. The standalone
`sysbox-mgr` service and its IPC protocol have been removed.
The FS Makefile resolves IPC, library, and runc nsenter dependencies from this
repository; `make -C fs sysrunc-fs` passes.

## Current 218 source integration, 2026-11-25

The isolated CKM `k3k-console-164315/ckm-sysrunc-it-1123` now runs its L0
Server on `sysrunc` with `hostUsers:false` and without an L0 FS annotation.
L1 loads the new `sysrunc` binary and snapshotter; the inner agent is Ready.
Fresh L2 ordinary, FS, and two-PVC nginx Pods all pass. The FS Pod has one
seccomp filter, reads `/proc/sys`, and writes files. PVC image-file
initialization and markers survive Pod and L0 Server recreation. The
cert-manager CSI driver is 3/3 Ready after the persistent special mounts were
made shared and duplicate volume mounts were removed. An unchanged L1 Chart
upgrade to revision 11 did not restart K3s or the agent. Evidence and hashes
are in `INTEGRATION_218.md`.

This is a local-image source integration result. L0 still has the older
Sysbox installation for other workloads. The final image has not been
published or accepted as a release artifact. The L0 test runtime is installed
by isolated Helm release `sysrunc-l0-it`; L1 currently runs the complete,
locally built `sysrunc-it:218-20261125-getdents-observe` image at Helm revision 53.
An installer DaemonSet rollout now preserves FS and snapshotter in an
independent L1 cgroup. The first full-image rollout at revision 30 disconnected
existing FUSE mounts; the corrected revision 33/34 rollout kept both daemon
PIDs and the L2 FS Pod unchanged. Revision 35 installed the meminfo worker
image and restarted K3s because the release binaries changed; three named L2
Pods were recreated with their PVCs intact. Reapplying the same image at
revision 36 preserved K3s, FS, snapshotter, and the FS Pod. A K3s Server
restart still ends the daemons in that PID namespace. Revision 37 added
container uptime rendering to the proc worker; the new binary restarted K3s
once, while a same-image revision 38 upgrade kept the daemon and FS Pod IDs.
Revision 39 moves opted-in loadavg cgroup sampling into the ticker worker;
revisions 40–42 verified stable same-image and real agent rollouts. See
`INTEGRATION_218.md` for exact hashes and checks.
Revision 43 moved the container-aware `/proc/stat` calculation into the proc
worker. Four L2 Pods passed the status checks; the FS Pod's direct probe
confirmed an eight-CPU memfd view, FUSE fallback, and monotonic CPU counters.
The same-image revision 44 upgrade preserved the K3s, FS, snapshotter, agent,
and L2 Pod identities.
The FS protobuf source and generated Go file were renamed to `sysrunc` while
retaining the wire service name. IPC tests and a full build passed. The new
full image passed the four-Pod 218 checks and same-image revision 47 stability
check. Revision 45 was interrupted by the expected K3s restart; rolling back
to the same revision 45 manifest recovered revision 46 as `deployed`.
Revision 48 added the `protobuf.sysruncStateChannel` service and switched the
client to it, with fallback only when the old daemon does not know that
service. The server still registers `protobuf.sysboxStateChannel` for rolling
compatibility. Both services responded on the live L1 FS socket. An in-CKM
Helm upgrade completed before the K3s restart, and revision 49 preserved all
daemon and L2 Pod identities on same-image reapply.
Revision 51 added an exact `/proc/sys/kernel/random` `getdents64` seccomp
notify fast path. The live FS log recorded a successful fast-path response,
and the directory probe checked shared offsets, rewind, and relative-path
containment. Four L2 Pods, the seven-file probe, HTTP, and persistent PVC
markers passed. A same-image revision 52 upgrade preserved the K3s, FS,
snapshotter, agent, FS Pod, and ticker Pod identities.
The current source Chart package also passed a same-image revision 53 upgrade
without changing those identities; it remains an unpublished local artifact.
The old `FsState` route only synthesized a `/lib/modules/<kernel-release>`
link; no current sysrunc runtime path calls it, so no replacement mount is
created.

## Remaining TAPD acceptance gaps

- The proc worker serves UUID, cpuinfo, diskstats, swaps, meminfo, uptime, and stat.
  The meminfo and uptime paths passed source tests and isolated 218 L2 probes.
  The ticker worker now owns cgroup sampling and loadavg calculation for
  containers with `sysrunc.io/ticker=true`; default-off containers still use
  the local handler. Writable proc sysctls and most FUSE handling
  remain in the manager. The FUSE worker handles repair requests, while normal FUSE
  requests remain in the manager.
- `openat`/`openat2` and `statx` are intercepted for selected proc resources.
  `getdents64` is served directly for the exact `/proc/sys/kernel/random`
  directory while the original FUSE fd remains in the tracee. Other
  directories retain the kernel/FUSE path. Injecting a host directory fd was
  rejected after a relative `../hostname` escape test.
- The FS protocol source, generated file, and primary gRPC service now use
  `sysrunc` names. The legacy gRPC service and compatibility executable names
  remain available for rolling socket clients and existing L0 Sysbox workloads.
- The isolated 218 test uses local images. A complete Dockerfile build passed;
  published image/Chart acceptance is outstanding. The k0s persistence
  fixture passed, but a full k0s service boot and node rollback have not.

The `sysrunc` runtime selects FS with `sysrunc.io/fs=true` and ticker with
`sysrunc.io/ticker=true`. Both default to false. Ticker requires FS. The
`sysbox-runc` executable name defaults FS on, and `sysbox-runc-lite` defaults
FS off. Both names point to the same binary in the build output.

The FS process starts supervised proc, FUSE repair, and ticker worker processes.
It tracks container registration and cancellation and starts a ticker task only
for containers that opt in. The ticker worker samples cgroup tasks, computes
load averages, and releases its samplers when a container unregisters. The proc worker handles UUID reads through one
goroutine per registered container and rejects reads after unregister. It also
computes `/proc/cpuinfo`, `/proc/diskstats`, `/proc/swaps`, `/proc/meminfo`,
`/proc/uptime`, and `/proc/stat` for the seccomp fast path using
that container's cgroup view. The FUSE repair worker handles mount repair
requests. The FS process still owns
most virtual proc and FUSE request handling, the FUSE descriptor store, and
seccomp listener handling. These paths have not yet been isolated into their
respective workers.

FS handles `openat` and `openat2` notifications for the worker-backed
`/proc/sys/kernel/random/uuid` file. It injects a memfd with `NOTIF_ADDFD`,
so reads of that fd avoid FUSE. Directory opens retain the FUSE fd, but the
exact random directory's `getdents64` uses the same byte-offset cookies and
shared cursor through seccomp notify. Injecting a host proc directory fd would
allow subsequent relative paths such as `../hostname` to escape the virtual
view, so the fast path never replaces it. Relative `uuid` opens from
the FUSE directory fd still receive the worker memfd. `statx` notifications
for the two exact paths write proc metadata into caller memory, preserving
the virtual UUID file's size and mode.
`NOTIF_ADDFD` cannot supply buffers for `statx` or `getdents64`.

Seven exact read-only proc mounts (`cpuinfo`, `diskstats`, `meminfo`, `stat`,
`swaps`, `uptime`, and `loadavg`) now reuse their existing container-aware
handlers directly from the seccomp notification path. Each open receives a
sealed, read-only memfd, and `statx` returns the virtual mode and size without
entering FUSE. Other flags and paths retain the FUSE fallback. In a nested user
namespace, the host's `/proc/slabinfo` cannot be read; it remains on the FUSE
path and reports `EACCES` instead of an opaque I/O error.

Other paths and unsupported flags continue to the kernel and retain FUSE
behavior. Masked proc paths, invalid `openat2` modes or structure sizes, and
injection failures also fall back. Read-path notifications are installed only
where they preserve the OCI seccomp profile's existing allow rules. The
remaining mounted proc resources, especially writable `/proc/sys` files and
their directories, still need direct backing providers before their FUSE path
can be bypassed safely.

The new runtime bridges registration and seccomp notifications into FS, but
the original full `sysbox-runc` `syscont.ConvertSpec` and other system-container
behavior have not been ported. The admission webhook leaves `hostUsers`
unchanged for FS-enabled Pods using the new `sysrunc` RuntimeClass; its legacy
`sysbox-runc` path still requires `hostUsers: false`. Persistent special directories are handled only by the
snapshotter and runc when a rootfs rw-layer is configured. FS-enabled system
container behavior is therefore incomplete. The isolated 218 CKM has passed
FS-enabled workload tests and proc worker recovery, including the current
`/proc/swaps` worker output comparison. A legacy rootfs annotation Pod also
passed with a persistent `/var/lib/k0s` marker across Pod recreation. A full
k0s service startup, node rollback, remaining virtual proc paths, and
published artifacts still need acceptance. The exact cluster evidence is in
`INTEGRATION_218.md`.

## Historical 218 checkpoints

The 2026-11-23 isolated 218 CKM test in [INTEGRATION_218.md](INTEGRATION_218.md)
confirmed the `sysrunc` CRI handler and an nginx Pod, including PVC
initialization and persistence after recreation. The FS-enabled Pod now passes
kubelet admission without `hostUsers: false`. Source fixes for the nsenter
protocol, same-user-namespace entry, recursive capability lookup, and FUSE
ownership allowed the FS Pod to reach seccomp filter installation. The kernel
then returned EBUSY while the outer CKM Server still used legacy `sysbox-runc`
with an inherited seccomp filter. FS and seccomp-notify end-to-end requirements
remain unverified. The revised Dockerfile builds a static `fusermount3`;
the helper was checked inside the local image. The subsequent local image
`sysrunc-it:218-20261123k` was imported into 218 through a node debug Pod,
but has not been published. L0 now has a separate `sysrunc` handler and active
FS and snapshotter services. The dedicated CKM has not been moved onto it:
containerd reports no `user_namespaces` support for this handler because the
new OCI binary does not yet implement and report idmapped mounts. Its L1
Server Pod must retain `hostUsers: false`, so L0 migration and FS acceptance
remain blocked. Host installation exposed two legacy Pod selectors that
deleted old Sysbox Pods; these are corrected and protected by Bats tests.
Controllers rebuilt the affected Pods; see the 218 report for impact and
current state.

The later 218 L0 build implements idmapped mounts and now reports
`user_namespaces:true`; a `hostUsers:false` nginx Pod passed. The dedicated
CKM still cannot remain on L0 Sysrunc. CKM's controller needed a
`sysrunc.io/rootfs-rw-layer` annotation for its Server Pod; that source fix
passed tests in the adjacent `w7panel-ckm` repository, but is not published.
The Sysrunc runtime also needed idmapped binds for persistent special paths;
that source fix passed a focused test and was copied to the test node through
its debug Pod, but was not yet in the Helm image at that point. With those fixes, inner K3s
reached datastore startup, then its overlayfs mount failed with EPERM because
the new outer runtime has not yet supplied the legacy Sysbox system-container
behavior or `/dev/fuse`. The isolated CKM was restored to L0 `sysbox-runc` and
its Server is Ready. See the chronological evidence in the 218 report.

The next 218 L0 build, `sysrunc-it:218-20261123p`, is installed as isolated
`default/sysrunc-l0-it` Helm revision 6. The host Sysrunc binary matches
that build. Rootfs-enabled Sysrunc Pods now receive `/dev/fuse` without a Pod
hostPath, and the installer permits the actual `sysboxfs` FUSE source under
`/var/lib/sysruncfs`. The dedicated CKM reached L1 K3s startup with
`hostUsers:false` and the `native` K3s snapshotter, then failed because
kubelet needs `/dev/kmsg` and kube-proxy cannot use iptables in this user
namespace. FS mode separately still lacks the K3s sandbox sysctl path in its
virtual `/proc/sys`. L0-to-L2 Sysrunc and FS acceptance remain failed. The
CKM and production controller were restored to their original Sysbox runtime
and controller image; source controller changes and local test images remain
unpublished. See `INTEGRATION_218.md` for hashes and failures.

Follow-up revision 7 supplies a safe `/dev/kmsg` shim to the nested Server
and adds `NET_ADMIN` plus `SYS_ADMIN` only to the CKM Sysrunc Server. An
isolated Sysrunc Pod proved iptables works with those capabilities. The
dedicated CKM then progressed beyond its earlier `/dev/kmsg` and iptables
failures, but K3s still stopped on a read-only `route_localnet` sysctl and a
denied `/var/lib/kubelet` bind mount. It was again restored to outer
`sysbox-runc`; the production CKM controller image was restored. L1/L2 and
FS release acceptance still fail.

A further isolated test set AppArmor `Unconfined` and `procMount: Unmasked`
only on the Sysrunc CKM Server after independent Pods proved those settings
allow bind mounts and namespaced `route_localnet` writes. Kubelet and
kube-proxy progressed, but kubelet could not create
`/sys/fs/cgroup/kubepods` because the nested cgroup filesystem is read-only.
The dedicated CKM and production controller were restored again. Full
acceptance remains blocked on nested cgroup support or a verified rootless
kubelet configuration.

Verified locally: `make build`, `make chart`, shell syntax checks, and Go tests
for IPC, admission, and snapshotter. Focused runc feature and seccomp tests
pass. Full runc and FS test suites require mount and namespace
permissions, writable host mount or cgroup paths, ownership changes, or a
specific `/proc` layout unavailable in this workspace.

The 2026-11-24 isolated follow-up reached a writable, delegated cgroup v2
hierarchy with K3s in `init.scope`. The nested OOM adjustment and mount
namespace access errors were also cleared. L1 native runc now fails to mount
`sysfs` for system Pod sandboxes with `operation not permitted`, so the L1
Server has not reached Ready and L2/FS/reinstall gates remain failed. L0
`sysrunc-l0-it` is revision 10 with a local-only image; the dedicated CKM and
production controller were returned to their old Ready baseline.
