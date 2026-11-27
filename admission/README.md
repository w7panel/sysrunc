# sysrunc-admission

The webhook handles Pods using the `sysrunc` RuntimeClass and leaves
`hostUsers` unchanged.

`sysrunc-admission` validates the `sysrunc.io/rootfs-rw-layer` Pod annotation
and injects a canonical `sysrunc-rootfs` sidecar so `sysrunc-snapshotter` can
resolve PVC-backed rootfs upper/work directories safely. Application containers
never receive the rootfs PVC mount directly.

Empty writable CSI and K3s local-path PVCs are initialized by `sysrunc` from
their kubelet mount paths. Existing data is never overwritten, and unrecognized
storage paths are skipped.

The webhook handles Pod `CREATE` requests when either of these conditions applies:

- `spec.runtimeClassName` is `sysrunc`.
- The `sysrunc.io/rootfs-rw-layer` annotation is present, including an empty value.

The webhook never selects a RuntimeClass. A nonempty, enabled rootfs annotation
requires the `sysrunc` RuntimeClass. An empty value or `false` disables
the rootfs rw-layer and does not create persistent special mounts.

PVC initialization works without a rootfs annotation. The sidecar is injected
only when that annotation contains a valid, enabled entry list.

It does not mutate workload resources such as Deployments directly. Put the annotation on the Pod template for controllers.

## Rootfs Rw-Layer Annotation

Put the annotation under a Pod's `metadata.annotations`. For Deployment, StatefulSet, and other controllers, put it under `spec.template.metadata.annotations`.

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    sysrunc.io/rootfs-rw-layer: |
      [
        {
          "name": "c1",
          "volumeName": "rootfs",
          "path": "containers/c1",
          "persistentSpecialMounts": true,
          "specialPath": ["/srv/data"]
        }
      ]
spec:
  runtimeClassName: sysrunc
  containers:
    - name: c1
      image: alpine:3.23.5
  volumes:
    - name: rootfs
      persistentVolumeClaim:
        claimName: sysrunc-rootfs-pvc
```

Each annotation entry has this meaning:

| Field | Meaning |
| --- | --- |
| `name` | Target app container name. It must exist in `spec.containers`. |
| `volumeName` | Name of a PVC-backed `spec.volumes[]` entry. |
| `path` | Persistent rootfs rw-layer directory inside that PVC. It must be relative and must not escape the PVC root. |
| `persistentSpecialMounts` | Optional per-container switch for PVC-backed special directories. Defaults to `false`. |
| `specialPath` | Optional extra absolute, non-root paths. Requires `persistentSpecialMounts: true` and must not overlap built-in or custom paths. |

Validation is fail-closed. The webhook denies the request when the annotation is invalid, a referenced container is missing, a referenced volume is not a PVC volume, a PVC claim name is empty, a container is configured more than once, or `path` is absolute or escapes through `..`.

## Sidecar Injection

When validation succeeds, `sysrunc-admission` injects or replaces a container named `sysrunc-rootfs`.

The injected sidecar is canonical:

- `name`: `sysrunc-rootfs`
- `image`: the sandbox image detected from containerd config, or containerd's default sandbox image
- `volumeMounts`: one mount for each unique `volumeName` in the annotation
- `env`: empty
- `command`: empty

The sidecar is appended after all app containers. If the incoming Pod already
contains a `sysrunc-rootfs` container, it is removed and replaced with the
canonical sidecar. This prevents users from tampering with sidecar image,
command, environment, or mounts.

The sidecar does not carry rootfs rw-layer intent in environment variables. Rootfs configuration remains in the Pod annotation, which containerd forwards into each container OCI spec when the runtime is configured with:

```toml
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc]
  pod_annotations = ["sysrunc.io/rootfs-rw-layer"]
```

`sysrunc-snapshotter` reads that OCI annotation for container intent and reads
the `sysrunc-rootfs` sidecar OCI mounts only to resolve each PVC's node-side
mount path.

## Persistent Special Directories

When the matching rootfs entry has `persistentSpecialMounts: true`,
`sysrunc-snapshotter` writes the resolved PVC source to a root-only handoff under:

```text
/run/sysrunc/rootfs-pvc-handoff/<container-id-hash>.json
```

`sysrunc` validates the handoff against the container ID, Pod UID, container
name, volume name, and kubelet volume path, resolves the configured rw-layer
directory, and bind-mounts special directories back onto the corresponding
container paths. When a special directory is first created, the runtime seeds
it once from the matching directory in the merged image rootfs before
installing the bind mount. The handoff is removed with the container snapshot.

Without per-entry opt-in, that container still supports persistent rootfs
`upper/work`; no special-directory mount is created. Another container or
sidecar cannot inherit the setting.

The resulting PVC layout is:

```text
<PVC>/<path>/
├── upper/
├── work/
└── special/
    ├── var/lib/docker/
    ├── var/lib/kubelet/
    ├── var/lib/k0s/
    ├── var/lib/rancher/k3s/
    ├── var/lib/rancher/rke2/
    ├── var/lib/buildkit/
    ├── var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/
    └── srv/data/
```

There is no `meta.json`. The `special/` directories are bind-mounted explicitly so inner Docker and K3s see the PVC's real filesystem rather than overlay-on-FUSE. A missing directory is initialized once from the image directory (or created empty if absent), then atomically published. Existing PVC ownership, ACLs, and xattrs are never rewritten. Persistent special mounts require idmapped mounts; startup fails if the source filesystem cannot provide them.

## Webhook Server

The server exposes:

- `GET /healthz`: readiness/liveness endpoint
- `POST /mutate`: Kubernetes AdmissionReview endpoint

Only Pod resources are supported. Other resources are denied with `unsupported resource <name>`.

Admission request bodies are limited to 1 MiB.

## TLS And Webhook Bootstrap

`sysrunc-admission` can run with explicit TLS files or bootstrap its own Kubernetes webhook resources.

Important flags:

| Flag | Purpose |
| --- | --- |
| `--addr` | Listen address, default `:9443`. |
| `--tls-cert`, `--tls-key` | Explicit serving certificate and key. Must be set together. |
| `--allow-insecure-http` | Allow plaintext HTTP. Development only. |
| `--bootstrap-webhook` | Create/refresh CA Secret, TLS Secret, Lease, and MutatingWebhookConfiguration. |
| `--namespace` | Namespace for webhook-owned runtime resources. Defaults to `POD_NAMESPACE`, then `sysrunc-system`. |
| `--service-name`, `--service-port` | Kubernetes Service used by the webhook client config. |
| `--webhook-name` | MutatingWebhookConfiguration name. Default `sysrunc-webhook-mutator`. |
| `--ca-secret-name`, `--tls-secret-name` | Secret names used for bootstrap TLS. |
| `--lease-name` | Lease name for certificate/bootstrap coordination. |
| `--containerd-config-dir` | Directory used to detect the sandbox image from containerd config. |

When `--bootstrap-webhook=true`, one elected replica manages certificate resources and webhook configuration. All replicas wait for and refresh the serving TLS certificate from the configured Secret.

The generated webhook matches Pod create requests that use a managed
RuntimeClass or carry either rootfs rw-layer annotation. It uses
`failurePolicy: Fail`, `sideEffects: None`, `reinvocationPolicy: IfNeeded`, and
a 10 second timeout.

## Sandbox Image

The sidecar image must match the cluster sandbox image. `sysrunc-admission`
detects it from containerd config:

- With `--containerd-config-dir`, it reads from that directory.
- Without it, it uses the default containerd config lookup.
- If no sandbox image is configured, the containerd default is used.

## Build And Test

```bash
cd admission && go test ./...
```

From the repository root:

```bash
make build
```
