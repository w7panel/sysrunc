# sysrunc Helm Chart

This chart installs the Sysrunc Kubernetes resources, including the runtime
installer and admission backend.

## Publish

Set `ZPK_IDC_PASSWORD`, then run `make full` from the repository root. The
command validates host and nested renders, packages the `name` and `version`
from `Chart.yaml`, builds and pushes
`ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:v<version>`, and attaches
the Helm package to the `w7panel-sysrunc` application at `zpk.idc.w7.com`.
The package is written to `dist/charts/`. If `zpk` is absent, Make downloads
the Linux amd64 CLI to `dist/tools/zpk` from the same release URL used in
`w7panel-ckm/.cnb.yml`. To publish a new version, update `Chart.yaml` first;
`VERSION` must match that version.

Use the Helm release name `w7panel-sysrunc` in both the outer cluster and
each CKM inner cluster. The installer DaemonSet is named
`w7panel-sysrunc-installer` in both clusters, and both use `installer.image`.
Admission has a separate resource name and uses the same image.

## Values

### Installation mode

`installMode` defaults to `auto`; an empty value also selects automatic detection.
The chart queries DaemonSets in `default`, following CKM's `createAgent` identity:
an agent labeled `k3k-agent-pod=true` with `IS_CHILD=true` in its container environment
selects `nested`. Otherwise it selects `host`. Explicit `host` or `nested` skips
detection. The mode is resolved consistently for all chart resources.

Automatic detection requires API access to get the `kube-system` namespace and
list DaemonSets in `default`. Offline `helm template`/`helm lint` must use
`--set installMode=host` or `--set installMode=nested`; connected previews can use
`--dry-run=server`. A CKM without its panel agent yet must explicitly select
`nested`. Existing releases that saved `installMode=host` must change it to
`auto` to enable detection; `--reuse-values` retains the saved override.

### RuntimeClass

The chart creates the `sysrunc` RuntimeClass in host and nested modes. Its FS
and ticker features are selected with `sysrunc.io/fs` and `sysrunc.io/ticker`;
both default to off. Set `compatRuntimeClasses=true` only after the old chart
has relinquished ownership of its `sysbox-runc` and `sysbox-runc-lite`
RuntimeClasses. The nested installer installs the runtime and containerd handler
from the installer image, then restarts K3s when the template changes.
RuntimeClasses have no scheduling node selector.

### Node Selector

By default, the chart does not restrict Sysrunc resources to labeled nodes. To
install Sysrunc only on nodes with a specific label, set the shared node selector:

```yaml
nodeSelector:
  sysrunc-runtime: running
```

When set, the same label selector is applied to the installer DaemonSet, the
admission Deployment, and the nested installer DaemonSet. Label the target nodes
before using this option:

```bash
kubectl label node <node-name> sysrunc-runtime=running
```

To use a custom label, set `nodeSelector` to the desired key/value map.

| Value | Default | Description |
| --- | --- | --- |
| `nodeSelector` | `{}` | Shared node selector for the installer DaemonSet and admission Deployment. RuntimeClasses in both modes have no node selector. |

### Images

The installer and admission backend use the same image. When the
installer tag is empty, the chart defaults it to `v{{ .Chart.AppVersion }}`.

| Value | Default | Description |
| --- | --- | --- |
| `installer.image.repository` | `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc` | Installer image repository. |
| `installer.image.tag` | `""` | Installer image tag; defaults to `v{{ .Chart.AppVersion }}`. |

Images are pulled directly from the public CCR repository by default.

### Installer

For an explicit L0 node rollback, first run a Helm upgrade with
`--reuse-values --set installer.action=cleanup --wait`. The installer cleans
each selected host and becomes Ready only after node cleanup completes. Then
uninstall the Helm release or set `installer.action=install` to reinstall.
The cleanup action is valid only in host mode. A node selector using the
`sysrunc-runtime` label is rejected because cleanup removes that label.

In nested mode, the Chart runs the installer from `installer.image`.
The outer `sysrunc` supplies `/dev/fuse` to the CKM Server at runtime; its
Pod does not declare a FUSE volume. The Server also needs persistent
`/var/lib/rancher/k3s`. The agent stores release binaries there and reconciles
them after K3s or agent restarts. Uninstall checks for active sysrunc Pods, stops
the agent, and restores the previous containerd template. Restart the CKM
Server after Helm uninstall completes so containerd loads that template;
`w7panel-doc/sysrunc-in-sysrunc/07-uninstall-ckm-chart.sh` performs both steps.

| Value | Default | Description |
| --- | --- | --- |
| `installer.action` | `install` | Host mode action: `install` or explicit node-level `cleanup`. |
| `installer.podLabels` | `{}` | Extra labels for installer pods. |
| `installer.podAnnotations` | `{}` | Extra annotations for installer pods. |

### Admission

The admission backend is rendered by this chart and uses `installer.image`.
Set `admission.enabled=false` to
skip the admission Deployment, Service, and admission-only RBAC.

| Value | Default | Description |
| --- | --- | --- |
| `admission.enabled` | `false` | Render the admission backend Deployment, Service, and admission-only RBAC. |
| `admission.replicas` | `1` | Admission backend replica count. |
| `admission.containerdConfigPath` | `/var/lib/rancher/k3s/agent/etc/containerd` | Host containerd config directory mounted read-only into the admission backend for sandbox image detection. |
| `admission.podLabels` | `{}` | Extra labels for admission pods. |
| `admission.podAnnotations` | `{}` | Extra annotations for admission pods. |

For non-k3s clusters, set `admission.containerdConfigPath` to the host
containerd config directory containing `config.toml`:

```bash
helm upgrade --install w7panel-sysrunc ./charts/sysrunc \
  --set admission.enabled=true \
  --set admission.containerdConfigPath=/etc/containerd
```

### Snapshotter

The installer passes `snapshotter.enabled` to the deploy script inside the shared
image. Set it to `false` to install Sysrunc without the host-side
`sysrunc-snapshotter` service and containerd proxy snapshotter configuration.

| Value | Default | Description |
| --- | --- | --- |
| `snapshotter.enabled` | `false` | Install and configure the host-side sysrunc-snapshotter service. |
