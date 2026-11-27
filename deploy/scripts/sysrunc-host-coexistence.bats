#!/usr/bin/env bats

setup() {
  export SYSRUNC_VERSION=test
  source <(sed '$d' "$BATS_TEST_DIRNAME/sysrunc-deploy-k8s.sh")
  test_root="$BATS_TEST_TMPDIR/host"
  mkdir -p "$test_root/bin" "$test_root/var" "$test_root/artifacts" "$test_root/containerd" "$test_root/etc"
  host_bin="$test_root/bin"
  host_etc="$test_root/etc"
  host_var_lib_sysrunc_deploy_k8s="$test_root/var"
  host_k3s_containerd_conf_dir="$test_root/containerd"
  host_k3s_containerd_conf_template="$host_k3s_containerd_conf_dir/config-v3.toml.tmpl"
  host_k3s_containerd_conf_template_backup="$host_k3s_containerd_conf_template.orig"
  get_artifacts_dir() { echo "$test_root/artifacts"; }
  restart_k3s() { :; }
  for name in sysrunc sysrunc-fs sysrunc-admission sysrunc-snapshotter; do
    printf '#!/bin/sh\n' > "$test_root/artifacts/$name"
    chmod +x "$test_root/artifacts/$name"
  done
  printf 'old Sysbox runtime\n' > "$host_bin/sysbox-runc"
  printf 'old Sysbox lite runtime\n' > "$host_bin/sysbox-runc-lite"
  sysrunc_snapshotter_enabled=false
}

@test "host binary install and removal leave old Sysbox binaries intact" {
  copy_sysrunc_to_host
  [ "$(cat "$host_bin/sysbox-runc")" = 'old Sysbox runtime' ]
  [ "$(cat "$host_bin/sysbox-runc-lite")" = 'old Sysbox lite runtime' ]
  rm_sysrunc_from_host
  [ "$(cat "$host_bin/sysbox-runc")" = 'old Sysbox runtime' ]
  [ "$(cat "$host_bin/sysbox-runc-lite")" = 'old Sysbox lite runtime' ]
}

@test "host binary removal leaves existing subordinate ID files intact" {
  printf 'containers:100000:65536\n' > "$host_etc/subuid"
  printf 'containers:100000:65536\n' > "$host_etc/subgid"

  rm_sysrunc_from_host

  [ "$(cat "$host_etc/subuid")" = 'containers:100000:65536' ]
  [ "$(cat "$host_etc/subgid")" = 'containers:100000:65536' ]
}

@test "same-version rebuilt artifact triggers host update" {
  copy_sysrunc_to_host
  run is_sysrunc_artifact_changed
  [ "$status" -eq 1 ]

  printf 'rebuilt runtime\n' > "$test_root/artifacts/sysrunc"
  run is_sysrunc_artifact_changed
  [ "$status" -eq 0 ]
  [[ "$output" == *'artifact sysrunc changed'* ]]

  copy_sysrunc_to_host
  run is_sysrunc_artifact_changed
  [ "$status" -eq 1 ]
}

@test "K3s configuration preserves old handler and adds sysrunc only" {
  cat > "$host_k3s_containerd_conf_template" <<'EOF'
version = 3

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc-runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc-runc.options]
  BinaryName = "/usr/bin/sysbox-runc"

EOF
  config_k3s_containerd_for_sysrunc
  grep -q 'BinaryName = "/usr/bin/sysbox-runc"' "$host_k3s_containerd_conf_template"
  grep -q 'runtimes.sysrunc]' "$host_k3s_containerd_conf_template"
  unconfig_k3s_containerd_for_sysrunc
  grep -q 'BinaryName = "/usr/bin/sysbox-runc"' "$host_k3s_containerd_conf_template"
  ! grep -q 'runtimes.sysrunc]' "$host_k3s_containerd_conf_template"
}

@test "same K3s handler configuration does not restart shared K3s" {
  restart_k3s() { printf 'restart\n' >> "$test_root/restarts"; }
  cat > "$host_k3s_containerd_conf_template" <<'EOF'
version = 3

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

EOF
  config_k3s_containerd_for_sysrunc
  [ "$(wc -l < "$test_root/restarts")" -eq 1 ]
  config_k3s_containerd_for_sysrunc
  [ "$(wc -l < "$test_root/restarts")" -eq 1 ]
}

@test "runtime refresh deletes only sysrunc Pods on the target node" {
  NODE_NAME=server1
  cat > "$test_root/pods.json" <<'EOF'
{"items":[
  {"metadata":{"name":"old","namespace":"business"},"spec":{"nodeName":"server1","runtimeClassName":"sysbox-runc"}},
  {"metadata":{"name":"new","namespace":"integration"},"spec":{"nodeName":"server1","runtimeClassName":"sysrunc"}},
  {"metadata":{"name":"other-node","namespace":"integration"},"spec":{"nodeName":"server2","runtimeClassName":"sysrunc"}}
]}
EOF
  kubectl() {
    if [ "$1" = get ]; then
      cat "$test_root/pods.json"
    else
      printf '%s\n' "$*" >> "$test_root/deleted"
    fi
  }
  delete_sysrunc_pods
  [ "$(cat "$test_root/deleted")" = 'delete pod/new -n integration --grace-period=1 --wait=false' ]
}

@test "FS upgrade drain check ignores legacy Sysbox Pods" {
  NODE_NAME=server1
  kubectl() {
    cat <<'EOF'
{"items":[{"metadata":{"name":"business","namespace":"business"},"spec":{"nodeName":"server1","runtimeClassName":"sysbox-runc"}}]}
EOF
  }
  precheck_fs_upgrade() { fs_mounts_present=false; }
  systemctl() { return 0; }
  verify_legacy_fs_drained
}

@test "FS migration refuses to touch Sysbox Pods when no host sysrunc Pod exists" {
  NODE_NAME=server1
  host_run="$test_root/run"
  mkdir -p "$host_run"
  kubectl() {
    if [ "$1" = get ]; then
      cat <<'EOF'
{"items":[{"metadata":{"name":"business","namespace":"business"},"spec":{"nodeName":"server1","runtimeClassName":"sysbox-runc"}}]}
EOF
    else
      printf '%s\n' "$*" >> "$test_root/mutations"
    fi
  }
  run migrate_legacy_fs_pods
  [ "$status" -ne 0 ]
  [[ "$output" == *'refusing automatic migration'* ]]
  [ ! -e "$test_root/mutations" ]
}

@test "empty FS checkpoint does not trigger Pod migration" {
  host_run="$test_root/run"
  mkdir -p "$host_run/sysrunc"
  printf '%s\n' '{"Version":1,"Clean":false,"Containers":null}' > "$host_run/sysrunc/fs-restart.json"
  nsenter() { printf 'absent\n'; }
  systemctl() {
    if [ "$1" = show ]; then printf '0\n'; fi
    return 0
  }
  precheck_fs_upgrade
  [ "$legacy_fs_migration_required" = false ]
}
