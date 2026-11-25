# sysrunc

Kubernetes-focused system-container runtime based on Sysbox. The repository keeps the OCI runtime, FS service, IPC, shared libraries, admission webhook, snapshotter, Helm chart, and Kubernetes deployment scripts together.

The runtime starts from `sysbox-runc-lite`. A single `sysrunc` binary accepts
`sysrunc.io/fs` and `sysrunc.io/ticker` annotations; both default to `false`.
The legacy `sysbox-runc` and `sysbox-runc-lite` executable names retain their
FS defaults. Go modules are wired through the root `go.work` file. Use Go 1.25
or newer and run `make build` to create the four binaries in `dist/bin/generic`.

The FS-enabled path is still under migration. Read [MIGRATION_STATUS.md](MIGRATION_STATUS.md)
before using it for system containers or deploying it to a cluster.

| Directory | Component |
| --- | --- |
| `runc` | OCI runtime |
| `fs`, `ipc`, `libs` | FS service and shared code |
| `admission`, `snapshotter` | Kubernetes admission and rootfs snapshotter |
| `charts`, `deploy` | Helm chart and Kubernetes installer files |

See [NOTICE](NOTICE) for source revisions and license information.
