# Migration status

## Latest 218 result, 2026-11-27

The L0 node rollback path has now passed an uninstall and reinstall on 218.
The cleanup DaemonSet removed the host runtime, install marker, K3s runtime
template entry, and node label; reinstall restored them and the node stayed
Ready. L0 Helm revision 14 and L1 revision 44 run the same published
`w7panel-sysrunc:218-20261127-host-rollback2` image digest for installer and
admission. Both installer DaemonSets are named `w7panel-sysrunc-installer`;
admission Deployments retain `w7panel-sysrunc-admission`.
After the L0 reinstall, two L1 one-shot test Pods were recreated without
deleting their PVCs. The FS, ordinary nginx, and rootfs Pods are Ready;
`/proc/meminfo` is readable and the rootfs marker remains intact. See
`INTEGRATION_218.md` for the exact digest and checks.
The subsequent `218-20261127-relative-proc` image ran in L0 revision 15 and
L1 revision 45 with one shared digest for all four installer and admission
containers. L1 confirmed `./meminfo` and `./hostname` memfd opens from their
virtual directories, while a traversal path retained its FUSE fd. L0 image
rollout recreated the CKM Server; all three L2 probes recovered with PVCs
intact and the rootfs marker unchanged.
The next `218-20261127-statx-tracee` image corrected intercepted `statx`
metadata: the earlier host lookup returned the wrong inode, device, and owner
for FUSE-mounted paths. On 218, a direct syscall probe now matches `stat()`
for four representative virtual paths, including a file absent on the nested
host. L0 revision 16 and L1 revision 47 run its shared digest; the three L2
probes recovered after the outer CKM Server recreation.
The `218-20261127-sysinfo-worker-decision` image also moved `sysinfo` caller
selection into the per-container proc worker. A 256Mi L1 test Pod returned its
cgroup limit for a process named `free`, while the same binary under another
name received the host kernel value. L0 revision 17 and L1 revision 48 share
one digest; the three L2 probes recovered after the outer runtime update.

TAPD `1162789787001016333` now has one repository and OCI runtime binary,
annotation-controlled FS and ticker, and a manager with separate proc, FUSE
repair, and ticker worker processes. Registered containers have cancellable
goroutines in the relevant workers; a container with the ticker annotation
absent gets no ticker task. The proc worker owns container-cached virtual
sysctl state as well as the previously migrated proc resource and syscall
calculations. The manager retains seccomp interception, registration, write
validation, required host operations, and ordinary FUSE callbacks as intended
fallbacks. L0 revision 19 and L1 revision 52 run image
`218-20261127-proc-sysctl-worker` on 218; worker restart and nested Pod checks
are recorded in `INTEGRATION_218.md`.

Separate acceptance work remains for TAPD `1162789787001016334`: the
exact-path read and directory fast paths cover selected resources, but not
every mounted virtual path or write operation. The `0.1.3` source Chart and
image have not yet been published as a matched final release artifact.

The 2026-11-26 Chart renders the same `w7panel-sysrunc-installer` DaemonSet
and `installer` container in L0 and L1. Installer, admission, and the L1
uninstall Job use one `installer.image` value; admission keeps its own workload
name. On 218, L1 Helm revision 33 removed the obsolete admission image override
and ran installer and admission with the same local image ID
`sha256:20470ae06b35e732e931da78bd2e41b071a62b2657fe39abdeb2e6322e3a47c7`.
Both were Ready, the ordinary and FS Pods were Ready, and the rootfs marker
survived the K3s restart. This was a local image test; the later published
recovery image completed the L1 non-systemd FS crash recovery path. The
nested installer starts the FD broker and FS session recovery, and an actual
FS SIGKILL on 218 restored two FUSE mounts while preserving the FS Pod
identity. A later CKM Server recreation validated stale checkpoint cleanup
when its old PID namespace had no surviving containers.
The 2026-11-27 integration moved L0 and L1 to one public
`w7panel-sysrunc:218-20261127-unified-installer` image; their installer and
admission Pods all run digest
`sha256:a914eec08d632265613c3554ea9706efa4407c7bdee08177588dd0dd7304420f`.
The L0 Helm release is revision 6 and L1 is revision 38, both deployed.
The dedicated CKM Server and L2 probes recovered after the runtime update.
The newer `w7panel-sysrunc:218-20261127-sysinfo-worker` image is now
running in both layers. L0 Helm revision 10 and L1 revision 43 are deployed;
both installer and admission Pods in both layers use digest
`sha256:30ee838b6be267e3e906c62c55df13184e34d855094bfdb2cd4b609a453fdb4c`.
The dedicated CKM Server and FS, ordinary, and rootfs L2 probes are Ready;
the rootfs PVC marker persisted through the L0 upgrade.
This image continues virtual backing-file reads after short reads and reports
short proc/sys writes as errors. It also moves the FS checkpoint to `/dev/shm`:
the previous `/run` checkpoint could fail atomic rename on the CKM Server's
fuse-overlayfs rootfs. A live L1 FS SIGKILL restored two FUSE mounts with the
FS Pod UID unchanged, and the L0 upgrade plus CKM Server recreation recovered
the three named L2 probes while retaining the rootfs PVC marker.
The new image also delegates `sysinfo` cgroup memory calculation to the proc
worker. A 218 FS Pod limited to 256Mi returned `268435456` bytes from
`sysinfo`, matching `memory.max`; the disposable probe Pod was removed.
The L1 revision 34 local image adds a registered proc worker read for
`/proc/slabinfo`. The 218 nested user namespace cannot read its backing file,
so the live check verified the unchanged `EACCES` fallback and other Pod
health, while successful worker reads still need an accessible environment.
L1 revision 35 adds plain-read snapshots for four IPVS virtual sysctls only
when the backing IPVS directory exists. The 218 kernel lacks that directory;
the live check proved it stayed absent and all ordinary, FS, and rootfs Pods
recovered after the upgrade. IPVS read hits still require a module-enabled
environment, and writes remain on FUSE.
L1 revision 36 routes FUSE reads of registered read-only proc and CPU resource
snapshots to the proc worker, retaining local handler fallback. A nonblocking
open probe in 218 proved the FUSE-mounted meminfo and CPU online paths reached
the worker through the actual FUSE callback; their values were readable and
the three named L2 Pods were Ready after the upgrade. Writable sysctl state
and ordinary FUSE callbacks still live in the manager. The later recovery2
image passed the L1 FS SIGKILL test described above.
On 2026-11-27, the old 218 CKM controller reconciled the dedicated Sysrunc
Server back to an unusable template without its rootfs annotation and native
snapshotter. The already committed CKM source passed focused tests; its CCR
test image `w7panel-ckm:218-20261127-sysrunc-reconcile` was deployed to the
218 controller. Reconciliation restored the intended template, the Server
became Ready, and the FS, ordinary, and rootfs probes recovered with their
PVCs intact. The renewed meminfo probe confirmed memfd versus FUSE FD targets
and a worker-backed FUSE read. See `INTEGRATION_218.md` for digest and details.
The follow-up CKM controller image
`w7panel-ckm:218-20261127-sysrunc-snapshotter-reconcile` repairs
`K3S_SNAPSHOTTER=native` on every Sysrunc reconcile, even without a runtime
class change. A live 218 overlayfs drift injection was corrected; the CKM
Server, FS and ordinary probes, and rootfs probe returned Ready with the
rootfs PVC marker intact.

This repository contains the Kubernetes runtime, FS service, IPC,
admission webhook, snapshotter, chart, and deployment scripts in one tree. The
root `go.work` resolves the local modules. `make build` produces one OCI runtime
binary plus the FS, admission, and snapshotter binaries. The standalone
`sysbox-mgr` service and its IPC protocol have been removed.
The FS Makefile resolves IPC, library, and runc nsenter dependencies from this
repository; `make -C fs sysrunc-fs` passes.

## Current 218 source integration, 2026-11-25

The 2026-11-26 follow-up tested registered virtual directory `getdents64`
fast paths and moved pressure plus CPU online/present rendering to the proc
worker. In the dedicated CKM, 21 directories recorded direct seccomp hits;
five resource files returned worker-backed memfds. The random-directory
cursor and relative-path probe, L2 status checks, HTTP, and same-image Helm
reapply passed. This remains a local image integration result; see
`INTEGRATION_218.md` for image and binary hashes.

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

This section records the earlier local-image source integration result. L0 still
has the older Sysbox installation for other workloads. A later `v0.1.2` image
and Chart were published and accepted for the rootfs copy and proc fast paths
listed in `INTEGRATION_218.md`. The L0 test runtime was installed
by isolated Helm release `sysrunc-l0-it`; at that point L1 ran the complete,
locally built `sysrunc-it:218-20261125-getdents-count` image at Helm revision 59.
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
An isolated L1 Helm rollback to the prior `getdents-safe` image produced
revision 54; the K3s Server, agent, and a new Sysrunc nginx sandbox recovered.
Rolling back to the then-current `getdents-observe` image produced revision 55.
After recreating four `restartPolicy: Never` test Pods, their CRI, HTTP, PVC,
and FS directory checks passed; the new FS log recorded a direct `getdents64`
hit. Each image change restarted K3s and left the old test Pods `Unknown`, so
this is a recoverable L1 installer rollback, not uninterrupted workloads.
The ticker worker now dispatches opted-in `read_loadavg` requests to each
container's cancellable goroutine. Local worker and race tests passed. The
full `ticker-goroutine` image ran in the isolated CKM at revision 56; the
running FS hash matched the build, the ticker Pod read `/proc/loadavg`, and
four L2 CRI, HTTP, FS, and PVC checks passed. Reapplying the same image and
Chart at revision 57 left K3s, the ticker worker, and four Pod identities
unchanged. This is still a local-image integration result.
The `getdents64` notifier now lets the kernel handle raw calls whose count
register has nonzero high 32 bits. A direct syscall probe in the FS Pod
previously returned 224 bytes for `0x100000010` although the kernel receives
only 16; the fixed image returned `EINVAL`, matching an ordinary count of 16.
Normal 224-byte directory reads and the direct-hit log still passed. The
fixed local image ran at revision 58, and same-image revision 59 preserved
K3s and all four L2 Pod identities. See `INTEGRATION_218.md` for hashes.
The old `FsState` route only synthesized a `/lib/modules/<kernel-release>`
link; no current sysrunc runtime path calls it, so no replacement mount is
created.

## Remaining TAPD acceptance gaps

- The proc worker serves UUID, cpuinfo, diskstats, swaps, meminfo, uptime, stat,
  slabinfo,
  pressure, CPU online/present, and five global read-only kernel values.
  The meminfo and uptime paths passed source tests and isolated 218 L2 probes.
  The ticker worker now owns cgroup sampling and loadavg calculation for
  containers with `sysrunc.io/ticker=true`; default-off containers still use
  the local handler. Writable proc sysctls and most FUSE handling
  remain in the manager. The FUSE worker handles repair requests, while normal FUSE
  requests remain in the manager.
  The proc worker now receives post-start creation-time updates, and graceful
  restart re-registers restored containers with their saved ticker opt-in.
  New FUSE mountpoint directories are created by the FUSE worker; a worker
  failure now fails pre-registration and rolls back the container and netns
  state. Removal still has a local fallback. The manager still owns the
  kernel FUSE connection and normal request callbacks.
  These lifecycle changes passed source tests and the isolated 218 revision 22
  image. Killing the FUSE worker restarted it, and a newly created FS Pod
  remained Ready. FUSE repair commands now run inside the registered
  container's cancellable goroutine. A later 218 outer-node systemd restart
  produced disconnected FUSE mounts; the worker repaired two container
  mounts after roughly 45–60 seconds, with two successes and zero failures.
  The disposable FS Pod kept its UID and zero restarts, and proc reads
  recovered after the mount device changed. Existing open descriptors still
  needed reopening. The disposable Pod was removed afterwards.
  TAPD story `1162789787001016333` specifically assigns FUSE *self-healing*
  to that worker, while normal FUSE fallback can remain in the manager.
  Remaining proc virtualization and lifecycle coverage still need auditing
  before calling the worker split complete.
  A later 218 fault injection paused the FUSE worker during a new FS Pod's
  pre-registration. The supervisor restarted that worker and the Pod became
  Ready; an existing FS Pod's ten `/proc/meminfo` reads completed in 4–7 ms.
  This verifies worker isolation and restart after a stalled worker, not a
  real FUSE mount repair fault.
  A real 218 manager SIGKILL disconnected existing L2 FUSE mounts. The nested
  installer restarted FS, but existing proc reads returned ENOTCONN and the
  restarted daemon did not recognize old seccomp notifications. Its nested
  launcher does not enable session recovery, which currently relies on a
  systemd FD store for listener continuity. A K3s restart and recreation of
  two Never-policy test Pods restored the isolated CKM; the rootfs PVC marker
  survived. This remains an unpassed manager-crash robustness case; the TAPD
  worker requirement concerns FUSE self-healing work, and does not explicitly
  require retaining existing mounts after a manager SIGKILL.
  The manager-to-worker protocol now matches replies by request ID; reads
  and FUSE repairs can run concurrently across registered containers within
  one worker process. A source test holds one FUSE container's repair while
  another completes, and the race test passes. The complete local image
  passed the isolated 218 revision 30 smoke checks. This does not resolve
  FS manager crash recovery or the remaining manager-owned FUSE callbacks.
  A follow-up closed-response-stream guard passed the race test and ran as
  the final local image in isolated 218 revision 31; installer and admission
  image IDs matched, FS and rootfs smoke checks passed.
- The existing container-aware `/proc/pressure/{cpu,io,memory}` and
  `/sys/devices/system/cpu/{online,present}` handlers, plus the two read-only
  `/proc/sys/kernel/{cap_last_cap,ngroups_max}` entries, now have exact-path,
  plain-read seccomp notify fast paths. They use read-only memfd snapshots;
  masked paths and unsupported flags retain their original kernel or FUSE
  handling. A local image passed 218 L2 repeated-read and fallback probes.
- Twenty-two world-readable writable sysctls under `/proc/sys/{kernel,fs,net,vm}`
  now have plain-read snapshots from their existing container handlers. Write
  opens still use FUSE. Owner-only files remain on FUSE. Hostname and
  domainname now resolve in the requesting process's namespace before being
  cached; the previous FUSE hostname incorrectly exposed the inner-agent
  hostname. The namespace fix and direct read path passed dedicated 218 L2
  probes. The earlier twenty paths also returned
  `memfd:sysrunc-*`. In the nested agent the host
  `/proc/sys/net/core/default_qdisc` file is absent. Its FUSE handler now
  keeps a per-container `fq_codel` default and accepts validated container-only
  writes in that case; a source test covers read, write, and invalid values.
  Three legacy `/proc/sys/net/ipv4/neigh/default/gc_thresh{1,2,3}` files
  are likewise absent in the nested kernel; their virtual handlers now keep
  per-container defaults of 128, 512, and 1024. Revision 24 on 218 confirmed
  read-only seccomp memfds for all three. The owner-only
  `/sys/module/nf_conntrack/parameters/hashsize` could not read its host
  backing from the nested user namespace. Revision 25 confirmed its FUSE
  handler reads the equivalent `nf_conntrack_buckets` value into a container
  cache, while a non-root process remains denied by its `0600` mode.
  The virtual neighbor directory previously labeled its three threshold
  files as directories in `getdents64`; revision 27 on 218 confirmed their
  `d_type` changed from `DT_DIR` to `DT_REG` after correcting handler metadata.
  Four owner-only virtual files now have plain-read memfd fast paths gated
  on the tracee filesystem UID matching the FUSE root owner. The injected
  descriptors preserve modes 0600 or 0400; other users remain on the FUSE
  permission path. Revision 32 on 218 confirmed memfd reads by root and
  denial for `nobody` on all four. The L2 `/proc/sys` mount is read-only, so
  write-open behavior was not exercised there.
- `openat`/`openat2` and `statx` are intercepted for selected proc resources.
  `getdents64` is served directly for registered virtual `/proc` and `/sys`
  directories while the original FUSE fd remains in the tracee. Unregistered
  directories retain the kernel/FUSE path. Injecting a host directory fd was
  rejected after a relative `../hostname` escape test.
- The FS protocol source, generated file, and primary gRPC service now use
  `sysrunc` names. The legacy gRPC service and compatibility executable names
  remain available for rolling socket clients and existing L0 Sysbox workloads.
- The older isolated 218 source test used local images. The later published
  `v0.1.2` image and Chart passed rootfs copy and selected proc path checks.
  L0 node-level runtime rollback remains open.
  The `/var/lib/k0s` fixture only verifies persistent special-directory
  handling; running a k0s service is outside this CKM K3s acceptance scope.

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
`/proc/uptime`, `/proc/stat`, `/proc/slabinfo`, `/proc/pressure/{cpu,io,memory}`, and
`/sys/devices/system/cpu/{online,present}` for the seccomp fast path using
that container's cgroup view. It also calculates the memory fields for
intercepted `sysinfo` calls; seccomp notification and tracee memory writing
stay in the manager. The FUSE repair worker handles mount repair
requests. The FS process still owns
most virtual proc and FUSE request handling, the FUSE descriptor store, and
seccomp listener handling. These paths have not yet been isolated into their
respective workers.

FS handles `openat` and `openat2` notifications for the worker-backed
`/proc/sys/kernel/random/uuid` file. It injects a memfd with `NOTIF_ADDFD`,
so reads of that fd avoid FUSE. Directory opens retain the FUSE fd, but the
registered virtual directories' `getdents64` use the same byte-offset cookies and
shared cursor through seccomp notify. Injecting a host proc directory fd would
allow subsequent relative paths such as `../hostname` to escape the virtual
view, so the fast path never replaces it. Relative `uuid` opens from
the FUSE directory fd still receive the worker memfd. `statx` notifications
for the two exact paths write proc metadata into caller memory, preserving
the virtual UUID file's size and mode.
`NOTIF_ADDFD` cannot supply buffers for `statx` or `getdents64`.

Eight exact read-only proc mounts (`cpuinfo`, `diskstats`, `meminfo`, `stat`,
`slabinfo`, `swaps`, `uptime`, and `loadavg`) now reuse their existing container-aware
handlers directly from the seccomp notification path. Each open receives a
sealed, read-only memfd, and `statx` returns the virtual mode and size without
entering FUSE. Other flags and paths retain the FUSE fallback. In a nested user
namespace, the host's `/proc/slabinfo` cannot be read; its worker attempt
falls back to FUSE and reports `EACCES` instead of an opaque I/O error.

Other paths and unsupported flags continue to the kernel and retain their
original FUSE behavior where mounted. Masked proc and sys paths, invalid
`openat2` modes or structure sizes, and
injection failures also fall back. Read-path notifications are installed only
where they preserve the OCI seccomp profile's existing allow rules. The
remaining mounted proc resources, especially writes to virtual `/proc/sys`
files and owner-only virtual sysfs files, still need direct backing and access
checks before their FUSE path can be bypassed safely.

The new runtime bridges registration and seccomp notifications into FS.
System-container capability, AppArmor, and `/proc` OCI spec conversion have
been restored for nested containers, including exec processes; full Sysbox
behavior parity remains to be audited. The admission webhook leaves `hostUsers`
unchanged for FS-enabled Pods using the new `sysrunc` RuntimeClass; its legacy
`sysbox-runc` path still requires `hostUsers: false`. Persistent special directories are handled only by the
snapshotter and runc when a rootfs rw-layer is configured. FS-enabled system
container behavior is therefore incomplete. The isolated 218 CKM has passed
FS-enabled workload tests and proc worker recovery, including the current
`/proc/swaps` worker output comparison. A legacy rootfs annotation Pod also
passed with a persistent `/var/lib/k0s` marker across Pod recreation. CKM K3s
L0 node-level runtime rollback, remaining virtual proc paths, and published artifacts still
need acceptance. The exact cluster evidence is in
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
