# Sysrunc workspace notes

## L2 rootfs startup

For a Pod using `sysrunc.io/rootfs-rw-layer`, the first app container start can
report `sysrunc sidecar oci spec unavailable` while the injected
`sysrunc-rootfs` sidecar is being created. This first error can be ignored **if
the app container retries and the Pod becomes Ready**. Check the final Pod
status and exercise the rootfs layer; do not mark a Pod that remains in
`StartError` as passed. A disposable test Pod with `restartPolicy: Never` can
remain stuck after the first error, so use the normal `Always` policy when
testing this behavior.

On 218, the dedicated CKM `ckm-sysrunc-fresh-1125` passed this check with the
published `w7panel-sysrunc:v0.1.0` image: nginx recovered after one restart,
and a file written to `/root` survived Pod recreation with the same rootfs
PVC. Keep the PVC when recreating the named test Pod.
