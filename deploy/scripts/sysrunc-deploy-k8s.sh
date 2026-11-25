#!/bin/bash

#
# Copyright 2019-2023 Nestybox, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

#
# Script to install or remove Sysrunc (CE) and Sysrunc-EE on a Kubernetes node.
# The script assumes it will run inside the sysrunc deploy daemonset container,
# and that several host directories are mounted onto the container. The script
# requires full root privileges on the host (e.g., CAP_SYS_ADMIN + write access
# to /proc) in order to install Sysrunc on it.
#
# Note: inspired by kata-deploy (github.com/kata-containers/packaging/tree/master/kata-deploy)
#

set -o errexit
set -o pipefail
set -o nounset

# The Sysrunc edition to install: Sysrunc (CE) or Sysrunc-EE.
sysrunc_edition=""
# The Sysrunc version to install; append "-0" if it doesn't have a patch number.
sysrunc_version=$(echo "$SYSRUNC_VERSION" | sed '/-[0-9]/!s/.*/&-0/')

# Optional extra arguments for the Sysrunc systemd units.
SYSRUNC_FS_CONFIG="${SYSRUNC_FS_CONFIG:-}"
SYSRUNC_ADMISSION_NAMESPACE="${SYSRUNC_ADMISSION_NAMESPACE:-${POD_NAMESPACE:-kube-system}}"
SYSRUNC_ADMISSION_WEBHOOK_NAME="${SYSRUNC_ADMISSION_WEBHOOK_NAME:-sysrunc-webhook-mutator}"
SYSRUNC_ADMISSION_CA_SECRET_NAME="${SYSRUNC_ADMISSION_CA_SECRET_NAME:-sysrunc-admission-webhook-ca}"
SYSRUNC_ADMISSION_TLS_SECRET_NAME="${SYSRUNC_ADMISSION_TLS_SECRET_NAME:-sysrunc-admission-webhook-tls}"
SYSRUNC_ADMISSION_LEASE_NAME="${SYSRUNC_ADMISSION_LEASE_NAME:-sysrunc-admission-webhook-init}"

# The daemonset Dockerfile places sysrunc artifacts here
sysrunc_artifacts="/opt/sysrunc"
crio_artifacts="/opt/crio-deploy"

# The daemonset spec will set up these mounts.
host_systemd="/mnt/host/lib/systemd/system"
host_sysctl="/mnt/host/lib/sysctl.d"
host_bin="/mnt/host/usr/bin"
host_lib_mod="/mnt/host/usr/lib/modules-load.d"
host_local_bin="/mnt/host/usr/local/bin"
host_etc="/mnt/host/etc"
host_os_release="/mnt/host/os-release"
host_crio_conf_file="${host_etc}/crio/crio.conf"
host_crio_conf_file_backup="${host_crio_conf_file}.orig"
host_containerd_conf_file="${host_etc}/containerd/config.toml"
host_containerd_conf_file_backup="${host_containerd_conf_file}.orig"
host_run="/mnt/host/run"
host_var_lib="/mnt/host/var/lib"
host_k3s_containerd_conf_dir="${host_var_lib}/rancher/k3s/agent/etc/containerd"
host_k3s_containerd_conf_template="${host_k3s_containerd_conf_dir}/config-v3.toml.tmpl"
host_k3s_containerd_conf_template_backup="${host_k3s_containerd_conf_template}.orig"
host_var_lib_sysrunc_deploy_k8s="${host_var_lib}/sysrunc-deploy-k8s"

#
# Subid default values.
#
# Sysrunc supports up 4K sys containers per K8s node, each with 64K subids.
#
# Historical note: prior to Docker's acquisition of Nesytbox, Sysrunc-CE was
# limited to 16-pods-per-node via variable subid_alloc_min_range below, whereas
# Sysrunc-EE was limited to 4K-pods-per-node. After Docker's acquisition of
# Nestybox (05/22) Sysrunc-EE is no longer being offered and therefore Docker has
# decided to lift the Sysrunc-CE limit to encourage adoption of Sysrunc on K8s
# clusters (the limit will now be 4K-pods-per-node as it was in Sysrunc-EE).
#
subid_alloc_min_start=100000
subid_alloc_min_range=268435456
subid_alloc_max_end=4294967295

# We use CRI-O's default user "containers" for the sub-id range (rather than
# user "sysrunc").
subid_user="containers"
subid_def_file="${host_etc}/login.defs"
subuid_file="${host_etc}/subuid"
subgid_file="${host_etc}/subgid"

# Current OS distro and kernel release
os_distro_release=""
os_kernel_release=""

# System platform architecture
sys_arch=""

# Installation flags
do_sysrunc_install="true"
do_sysrunc_update="false"
do_crio_install="false"  # only set to true if containerd does not support user-namespaces with alternative runtimes.
do_kubelet_use_crio="false"
sysrunc_install_in_progress="false"
sysrunc_snapshotter_enabled="true"
legacy_fs_migration_required="false"
legacy_fs_cordoned="false"
legacy_fs_migrated="false"
fs_mounts_present="false"

#
# CRI-O Installation Functions
#

function deploy_crio_installer_service() {
	echo "Deploying CRI-O installer agent on the host ($k8s_version) ..."

	cp ${crio_artifacts}/bin/${k8s_version}/cri-o.${sys_arch}.tar.gz ${host_local_bin}/cri-o.${sys_arch}.tar.gz
	cp ${crio_artifacts}/bin/${k8s_version}/crio-patched ${host_local_bin}/crio-patched
	cp ${crio_artifacts}/bin/${k8s_version}/pinns-patched ${host_local_bin}/pinns-patched

	cp ${crio_artifacts}/scripts/crio-installer.sh ${host_local_bin}/crio-installer.sh
	cp ${crio_artifacts}/scripts/crio-extractor.sh ${host_local_bin}/crio-extractor.sh
	cp ${crio_artifacts}/systemd/crio-installer.service ${host_systemd}/crio-installer.service

	mkdir -p ${host_var_lib_sysrunc_deploy_k8s}
	cp ${crio_artifacts}/config/etc_cni_net.d_200-loopback.conf ${host_var_lib_sysrunc_deploy_k8s}/etc_cni_net.d_200-loopback.conf
	cp ${crio_artifacts}/config/etc_containers_registries.conf.d_000-shortnames.conf ${host_var_lib_sysrunc_deploy_k8s}/etc_containers_registries.conf.d_000-shortnames.conf
	cp ${crio_artifacts}/config/etc_containers_storage.conf ${host_var_lib_sysrunc_deploy_k8s}/etc_containers_storage.conf
	cp ${crio_artifacts}/config/etc_containers_registries.conf ${host_var_lib_sysrunc_deploy_k8s}/etc_containers_registries.conf
	cp ${crio_artifacts}/config/etc_containers_registries.d_default.yaml ${host_var_lib_sysrunc_deploy_k8s}/etc_containers_registries.d_default.yaml
	cp ${crio_artifacts}/config/etc_containers_policy.json ${host_var_lib_sysrunc_deploy_k8s}/etc_containers_policy.json

	systemctl daemon-reload
	echo "Running CRI-O installer agent on the host (may take several seconds) ..."
	systemctl restart crio-installer.service
}

function remove_crio_installer_service() {
	echo "Removing CRI-O installer agent from the host ..."
	systemctl stop crio-installer.service
	systemctl disable crio-installer.service
	rm -f ${host_local_bin}/crio-installer.sh
	rm -f ${host_local_bin}/crio-extractor.sh
	rm -f ${host_systemd}/crio-installer.service

	systemctl daemon-reload
}

function deploy_crio_removal_service() {
	echo "Deploying CRI-O uninstaller ..."
	cp ${crio_artifacts}/scripts/crio-removal.sh ${host_local_bin}/crio-removal.sh
	cp ${crio_artifacts}/scripts/crio-extractor.sh ${host_local_bin}/crio-extractor.sh
	cp ${crio_artifacts}/systemd/crio-removal.service ${host_systemd}/crio-removal.service

	systemctl daemon-reload
	systemctl restart crio-removal.service
}

function remove_crio_removal_service() {
	echo "Removing the CRI-O uninstaller ..."
	systemctl stop crio-removal.service
	systemctl disable crio-removal.service
	rm -f ${host_local_bin}/crio-removal.sh
	rm -f ${host_local_bin}/crio-extractor.sh
	rm -f ${host_systemd}/crio-removal.service

	systemctl daemon-reload
}

function deploy_kubelet_config_service() {
	echo "Deploying Kubelet config agent on the host ..."
	mkdir -p ${host_var_lib_sysrunc_deploy_k8s}
	cp ${crio_artifacts}/scripts/kubelet-config-helper.sh ${host_local_bin}/kubelet-config-helper.sh
	cp ${crio_artifacts}/systemd/kubelet-config-helper.service ${host_systemd}/kubelet-config-helper.service
	cp ${crio_artifacts}/config/crio-kubelet-options ${host_var_lib_sysrunc_deploy_k8s}/crio-kubelet-options

	cp /usr/local/bin/crictl ${host_local_bin}/sysrunc-deploy-k8s-crictl

	echo "Running Kubelet config agent on the host (will restart Kubelet and temporary bring down all pods on this node for ~1 min) ..."
	systemctl daemon-reload
	systemctl restart kubelet-config-helper.service
}

function remove_kubelet_config_service() {
	echo "Stopping the Kubelet config agent on the host ..."
	systemctl stop kubelet-config-helper.service
	systemctl disable kubelet-config-helper.service

	echo "Removing Kubelet config agent from the host ..."
	rm -f ${host_local_bin}/kubelet-config-helper.sh
	rm -f ${host_systemd}/kubelet-config-helper.service
	rm -f ${host_local_bin}/sysrunc-deploy-k8s-crictl
	systemctl daemon-reload
}

function deploy_kubelet_unconfig_service() {
	echo "Deploying Kubelet unconfig agent on the host ..."

	cp ${crio_artifacts}/scripts/kubelet-unconfig-helper.sh ${host_local_bin}/kubelet-unconfig-helper.sh
	cp ${crio_artifacts}/systemd/kubelet-unconfig-helper.service ${host_systemd}/kubelet-unconfig-helper.service
	cp /usr/local/bin/crictl ${host_local_bin}/sysrunc-deploy-k8s-crictl

	echo "Running Kubelet unconfig agent on the host (will restart Kubelet and temporary bring down all pods on this node for ~1 min) ..."
	systemctl daemon-reload
	systemctl restart kubelet-unconfig-helper.service
}

function remove_kubelet_unconfig_service() {
	echo "Stopping the Kubelet unconfig agent on the host ..."
	systemctl stop kubelet-unconfig-helper.service
	systemctl disable kubelet-unconfig-helper.service

	echo "Removing Kubelet unconfig agent from the host ..."
	rm -f ${host_local_bin}/kubelet-unconfig-helper.sh
	rm -f ${host_systemd}/kubelet-unconfig-helper.service
	rm -f ${host_local_bin}/sysrunc-deploy-k8s-crictl
	systemctl daemon-reload
}

function config_crio() {
	echo "Configuring CRI-O ..."

	touch ${host_crio_conf_file}
	if [ ! -f ${host_crio_conf_file_backup} ]; then
		cp ${host_crio_conf_file} ${host_crio_conf_file_backup}
	fi

	# Disable selinux for now.
	dasel put bool -f ${host_crio_conf_file} -p toml -m "crio.runtime.selinux" false

	# Add user "containers" to the /etc/subuid and /etc/subgid files
	get_subid_limits
	config_subid_range "$subuid_file" "$subid_alloc_min_range" "$subuid_min" "$subuid_max"
	config_subid_range "$subgid_file" "$subid_alloc_min_range" "$subgid_min" "$subgid_max"

	# Set capabilities to match default caps in containerd/docker
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "CHOWN"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "DAC_OVERRIDE"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "FSETID"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "FOWNER"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "SETUID"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "SETGID"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "SETPCAP"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "SETFCAP"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "NET_BIND_SERVICE"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "KILL"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "AUDIT_WRITE"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "NET_RAW"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "SYS_CHROOT"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.runtime.default_capabilities.[]' "MKNOD"

	# Set the default runtime to crio-runc.
	dasel put string -f ${host_crio_conf_file} -p toml -m "crio.runtime.default_runtime" "crio-runc"

	# Add crio-runc runtime and its monitor-path.
	dasel put object -f "${host_crio_conf_file}" -p toml -t string -t string -t string "crio.runtime.runtimes.crio-runc" \
		"runtime_path=/usr/local/bin/crio-runc" "runtime_type=oci" "monitor_path=/usr/local/bin/crio-conmon"

	# Create 'crio.image' table (required for 'pause_image' settings).
	dasel put document -f ${host_crio_conf_file} -p toml -m '.crio.image'

	# Create 'crio.network' table (required for 'network_dir' settings).
	dasel put document -f ${host_crio_conf_file} -p toml -m '.crio.network'

	# CRI-O puts a default limit of 1024 processes per pod; this is too small for
	# Sysrunc pods, since these run sometimes complex software such as Docker,
	# K8s, etc. Thus we increase this to 16K processes per pod. Since the max
	# limit for Linux is 4M (see /proc/sys/kernel/pid_max), this allows up to
	# ~256 Sysrunc containers each consuming 16K processes on a given host. It
	# also constraints a malicious container executing a fork bomb to 16K
	# processes, well below the kernel's max pid limit.
	dasel put int -f ${host_crio_conf_file} -p toml -m "crio.runtime.pids_limit" 16384
}

function restart_crio() {
	echo "Restarting CRI-O ..."
	systemctl restart crio
}

#
# Sysrunc Installation Functions
#

function get_artifacts_dir() {

	local distro=$os_distro_release

	if [[ "$distro" == "ubuntu-26.04" ]] ||
		[[ "$distro" == "ubuntu-24.04" ]] ||
		[[ "$distro" == "ubuntu-22.04" ]] ||
		[[ "$distro" == "ubuntu-21.10" ]] ||
		[[ "$distro" == "ubuntu-20.04" ]] ||
		[[ "$distro" == "ubuntu-18.04" ]] ||
		[[ "$distro" == "centos-9" ]] ||
		[[ "$distro" =~ "debian" ]]; then
		artifacts_dir="${sysrunc_artifacts}/bin/generic"
	elif [[ "$distro" =~ "flatcar" ]]; then
		local release=$(echo $distro | cut -d"-" -f2)
		artifacts_dir="${sysrunc_artifacts}/bin/flatcar-${release}"
	fi

	echo $artifacts_dir
}

function copy_sysrunc_to_host() {

	local artifacts_dir=$(get_artifacts_dir)

	# Replace binaries atomically. A direct cp can fail with ETXTBSY while the
	# current executable is mapped by a running Sysrunc service.
	install_host_binary "${artifacts_dir}/sysrunc-fs" "${host_bin}/sysrunc-fs"
	install_host_binary "${artifacts_dir}/sysrunc" "${host_bin}/sysrunc"
	install_host_binary "${artifacts_dir}/sysrunc-admission" "${host_bin}/sysrunc-admission"
	if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
		install_host_binary "${artifacts_dir}/sysrunc-snapshotter" "${host_bin}/sysrunc-snapshotter"
	fi

	# Keep track of the sysrunc version installed on the host (upgrade purposes).
	echo "${sysrunc_version}" >${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed_version
	echo "${sysrunc_snapshotter_enabled}" >${host_var_lib_sysrunc_deploy_k8s}/sysrunc_snapshotter_enabled
}

function install_host_binary() {
	local source="$1"
	local destination="$2"
	local temporary="${destination}.new.$$"

	install -m 0755 "${source}" "${temporary}"
	mv -f "${temporary}" "${destination}"
}

function rm_sysrunc_from_host() {
	rm -f "${host_bin}/sysrunc-fs"
	rm -f "${host_bin}/sysrunc"
	rm -f "${host_bin}/sysrunc-snapshotter"
	rm -f "${host_bin}/sysrunc-admission"

	# Remove sysrunc from the /etc/subuid and /etc/subgid files
	sed -i '/sysrunc:/d' "${host_etc}/subuid"
	sed -i '/sysrunc:/d' "${host_etc}/subgid"

	rm -f "${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed_version"
	rm -f "${host_var_lib_sysrunc_deploy_k8s}/sysrunc_snapshotter_enabled"
}

function copy_sysrunc_env_config_to_host() {
	cp "${sysrunc_artifacts}/systemd/99-sysrunc-sysctl.conf" "${host_sysctl}/99-sysrunc-sysctl.conf"
	cp "${sysrunc_artifacts}/systemd/50-sysrunc-mod.conf" "${host_lib_mod}/50-sysrunc-mod.conf"
}

function rm_conf_from_host() {
	rm -f "${host_sysctl}/99-sysrunc-sysctl.conf"
	rm -f "${host_lib_mod}/50-sysrunc-mod.conf"
}

function config_sysrunc_env() {
	# Set the sysrunc edition in the FS unit.
	sed -i "/^Environment=/ s|SYSRUNC_EDITION=.*|SYSRUNC_EDITION=${sysrunc_edition}|" ${sysrunc_artifacts}/systemd/sysrunc-fs.service
}

# Update Sysrunc's systemd unit files with the received configMap configuration
# corresponding to the sysrunc-fs service.
function config_sysrunc() {
	if [[ "${sysrunc_snapshotter_enabled}" != "true" ]]; then
		sed -i 's/ sysrunc-snapshotter.service//g' ${sysrunc_artifacts}/systemd/sysrunc.service
		sed -i 's@ && /usr/bin/sysrunc-snapshotter --version@@' ${sysrunc_artifacts}/systemd/sysrunc.service
	fi

	if [ -n "$SYSRUNC_FS_CONFIG" ]; then
		sed -i "/^ExecStart=/ s|/usr/bin/sysrunc-fs|/usr/bin/sysrunc-fs ${SYSRUNC_FS_CONFIG}|" ${sysrunc_artifacts}/systemd/sysrunc-fs.service
	fi
}

function copy_sysrunc_config_to_host() {
	cp "${sysrunc_artifacts}/systemd/sysrunc.service" "${host_systemd}/sysrunc.service"
	cp "${sysrunc_artifacts}/systemd/sysrunc-fs.service" "${host_systemd}/sysrunc-fs.service"
	if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
		cp "${sysrunc_artifacts}/systemd/sysrunc-snapshotter.service" "${host_systemd}/sysrunc-snapshotter.service"
	else
		rm -f "${host_systemd}/sysrunc-snapshotter.service"
	fi
	systemctl daemon-reload
	systemctl enable sysrunc.service || true
	systemctl enable sysrunc-fs.service || true
	if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
		systemctl enable sysrunc-snapshotter.service || true
	fi
}

function rm_systemd_units_from_host() {
	rm -f "${host_systemd}/sysrunc.service"
	rm -f "${host_systemd}/sysrunc-fs.service"
	rm -f "${host_systemd}/sysrunc-snapshotter.service"
	systemctl daemon-reload
}

function apply_sysrunc_env_config() {
	# Note: this requires CAP_SYS_ADMIN on the host
	echo "Configuring host sysctls ..."
	if command -v sysctl >/dev/null 2>&1; then
		sysctl -p "${host_sysctl}/99-sysrunc-sysctl.conf" || true
	else
		echo "Warning: sysctl is unavailable in this installer environment; skipping host sysctl configuration"
	fi
}

function start_sysrunc() {
	echo "Starting $sysrunc_edition ..."
	systemctl restart sysrunc
	systemctl is-active --quiet sysrunc
}

function stop_sysrunc() {
	if systemctl is-active --quiet sysrunc || systemctl is-enabled --quiet sysrunc; then
		echo "Stopping $sysrunc_edition ..."
		systemctl stop sysrunc
	fi
}

# Newer distributions confine fusermount3 separately from sysrunc-fs.
function config_sysrunc_apparmor() {
	local profile="${host_etc}/apparmor.d/fusermount3"
	local local_rules="${host_etc}/apparmor.d/local/fusermount3"
	if [[ ! -f "$profile" || -e "${host_etc}/apparmor.d/disable/fusermount3" ]]; then
		return
	fi
	if ! nsenter -t 1 -m -u -i -n -p --root -- sh -c 'test "$(cat /sys/module/apparmor/parameters/enabled 2>/dev/null)" = Y'; then
		return
	fi

	echo "Configuring fusermount3 AppArmor rules for Sysrunc ..."
	mkdir -p "$(dirname "$local_rules")"
	local rule
	while IFS= read -r rule; do
		if ! grep -qxF -- "$rule" "$local_rules" 2>/dev/null; then
			printf '\n%s\n' "$rule" >> "$local_rules"
		fi
	done <<'EOF'
mount fstype=fuse options=(rw,nosuid,nodev) sysruncfs -> /var/lib/sysruncfs/*/,
mount fstype=fuse options=(rw,nosuid,nodev) sysboxfs -> /var/lib/sysruncfs/*/,
umount /var/lib/sysruncfs/*/,
EOF
	# Reload even on a retry after a previous parser failure.
	nsenter -t 1 -m -u -i -n -p --root -- apparmor_parser -r /etc/apparmor.d/fusermount3
}

function install_sysrunc() {
	# Sysrunc could potentially be already installed (during upgrades),
	# so stop it first to ensure that copy instructions below can
	# succeed.
	if [[ "$legacy_fs_migrated" == "true" ]]; then
		verify_legacy_fs_drained
	fi
	stop_sysrunc

	echo "Installing $sysrunc_edition on host ..."
	config_sysrunc_env
	copy_sysrunc_env_config_to_host
	apply_sysrunc_env_config
	config_sysrunc
	copy_sysrunc_config_to_host
	copy_sysrunc_to_host
	start_sysrunc
}

function precheck_fs_upgrade() {
	local mounts pid args store preserve stored_fds checkpoint_pending=false checkpoint
	legacy_fs_migration_required="false"
	checkpoint="${host_run}/sysrunc/fs-restart.json"
	if [[ -e "$checkpoint" ]]; then
		stored_fds=$(systemctl show sysrunc-fs -p NFileDescriptorStore --value 2>/dev/null) ||
			die "cannot inspect sysrunc-fs FD store"
		if ! jq -e '(.Containers // []) | length == 0' "$checkpoint" >/dev/null 2>&1 ||
			[[ "$stored_fds" != 0 ]]; then
			checkpoint_pending=true
		fi
	fi
	mounts=$(nsenter -t 1 -m -u -i -n -p -- sh -c '
		[ -r /proc/1/mountinfo ] || exit 1
		for file in /proc/[0-9]*/mountinfo; do
			grep -qF " - fuse sysruncfs " "$file" 2>/dev/null
			case $? in
				0) echo present; exit 0 ;;
				1) ;;
				# A zombie still has a /proc entry, but mountinfo is unreadable.
				# Ignore it; it has no mount namespace to preserve.
				*) [ ! -e "$file" ] || grep -q '^State:[[:space:]]*Z' "${file%/mountinfo}/status" 2>/dev/null || exit 1 ;;
			esac
		done
		echo absent
	') || die "cannot inspect host mounts before Sysrunc upgrade"
	fs_mounts_present="false"
	[[ "$mounts" == absent ]] || fs_mounts_present="true"
	if ! systemctl is-active --quiet sysrunc-fs; then
		if [[ "$mounts" == present ]] || [[ "$checkpoint_pending" == true ]]; then
			die "inactive sysrunc-fs has FUSE mounts or a checkpoint; preserve them and recover manually"
		fi
		return
	fi
	if [[ "$mounts" == absent && "$checkpoint_pending" == false ]]; then
		return
	fi
	pid=$(systemctl show sysrunc-fs -p MainPID --value) || die "cannot inspect running sysrunc-fs"
	[[ "$pid" =~ ^[1-9][0-9]*$ ]] || die "running sysrunc-fs has no valid MainPID"
	args=$(nsenter -t 1 -m -u -i -n -p -- cat "/proc/${pid}/cmdline" | tr '\0' ' ') ||
		die "cannot inspect running sysrunc-fs arguments"
	store=$(systemctl show sysrunc-fs -p FileDescriptorStoreMax --value) || die "cannot inspect sysrunc-fs FD store"
	preserve=$(systemctl show sysrunc-fs -p FileDescriptorStorePreserve --value) || die "cannot inspect sysrunc-fs FD store"
	stored_fds=$(systemctl show sysrunc-fs -p NFileDescriptorStore --value) || die "cannot inspect sysrunc-fs FD store"
	if [[ " $args" == *" --session-recovery "* && "$store" =~ ^[1-9][0-9]*$ &&
		"$preserve" == yes && "$stored_fds" =~ ^[1-9][0-9]*$ ]]; then
		return
	fi
	legacy_fs_migration_required="true"
}

function restore_legacy_fs_node() {
	if [[ "$legacy_fs_cordoned" == "true" ]]; then
		kubectl uncordon "$NODE_NAME" || echo "ERROR: cannot uncordon $NODE_NAME" >&2
	fi
}

function verify_legacy_fs_drained() {
	local pods rows
	pods=$(kubectl get pod -A -o json) || die "cannot verify Pods after legacy sysrunc-fs migration"
	rows=$(jq -r --arg node "$NODE_NAME" '.items[] |
		select(.spec.nodeName==$node and .spec.runtimeClassName=="sysrunc") |
		.metadata.namespace + "/" + .metadata.name' <<< "$pods") ||
		die "cannot inspect Pods after legacy sysrunc-fs migration"
	[[ -z "$rows" ]] || die "sysrunc Pods remain during legacy sysrunc-fs migration: $rows"
	precheck_fs_upgrade
	[[ "$fs_mounts_present" == "false" ]] ||
		die "Sysrunc FUSE mounts remain after Pod migration; old sysrunc-fs remains running"
	systemctl is-active --quiet sysrunc-fs || die "old sysrunc-fs stopped during Pod migration"
}

function migrate_legacy_fs_pods() {
	local pods rows namespace name uid owner owner_name owner_uid current remaining attempt unschedulable
	local checkpoint="${host_run}/sysrunc/fs-restart.json"
	pods=$(kubectl get pod -A -o json) || die "cannot list Pods before legacy sysrunc-fs migration"
	rows=$(jq -r --arg node "$NODE_NAME" '.items[] |
		select(.spec.nodeName==$node and .spec.runtimeClassName=="sysrunc") |
		[.metadata.namespace, .metadata.name, .metadata.uid,
		 (.metadata.ownerReferences // [] | map(select(.controller==true)) | .[0].kind // ""),
		 (.metadata.ownerReferences // [] | map(select(.controller==true)) | .[0].name // ""),
		 (.metadata.ownerReferences // [] | map(select(.controller==true)) | .[0].uid // "")] | @tsv' <<< "$pods") ||
		die "cannot inspect Pods before legacy sysrunc-fs migration"
	[[ -n "$rows" ]] ||
		die "sysrunc-fs has mounts or a checkpoint but no host sysrunc Pods; refusing automatic migration"
	while IFS=$'\t' read -r namespace name uid owner owner_name owner_uid; do
		[[ -z "$name" ]] && continue
		case "$owner" in
			ReplicaSet|ReplicationController|StatefulSet|Job) ;;
			*) die "sysrunc Pod $namespace/$name has no supported controller; migrate it manually" ;;
		esac
		[[ -n "$owner_name" && -n "$owner_uid" ]] ||
			die "cannot verify controller of sysrunc Pod $namespace/$name"
		current=$(kubectl get "$owner" "$owner_name" -n "$namespace" -o jsonpath='{.metadata.uid}') ||
			die "cannot inspect controller of sysrunc Pod $namespace/$name"
		[[ "$current" == "$owner_uid" ]] ||
			die "controller of sysrunc Pod $namespace/$name is missing or replaced"
	done <<< "$rows"
	unschedulable=$(kubectl get node "$NODE_NAME" -o jsonpath='{.spec.unschedulable}') ||
		die "cannot inspect scheduling state of $NODE_NAME"
	if [[ "$unschedulable" != true ]]; then
		kubectl cordon "$NODE_NAME" || die "cannot cordon $NODE_NAME before legacy sysrunc-fs migration"
		legacy_fs_cordoned="true"
		trap restore_legacy_fs_node EXIT
	fi
	while IFS=$'\t' read -r namespace name uid owner owner_name owner_uid; do
		[[ -z "$name" ]] && continue
		kubectl delete pod "$name" -n "$namespace" --wait=false ||
			die "cannot delete legacy sysrunc Pod $namespace/$name"
	done <<< "$rows"
	for ((attempt=0; attempt<120; attempt++)); do
		remaining="false"
		while IFS=$'\t' read -r namespace name uid owner owner_name owner_uid; do
			[[ -z "$name" ]] && continue
			current=$(kubectl get pod "$name" -n "$namespace" --ignore-not-found -o jsonpath='{.metadata.uid}') ||
				die "cannot confirm deletion of legacy sysrunc Pod $namespace/$name"
			[[ "$current" != "$uid" ]] || remaining="true"
		done <<< "$rows"
		[[ "$remaining" == "false" ]] && break
		sleep 1
	done
	[[ "$remaining" == "false" ]] || die "legacy sysrunc Pods did not terminate; old sysrunc-fs remains running"
	verify_legacy_fs_drained
	if [[ -e "$checkpoint" ]]; then
		mkdir -p "$host_var_lib_sysrunc_deploy_k8s"
		local backup
		backup=$(mktemp -d "${host_var_lib_sysrunc_deploy_k8s}/fs-legacy-checkpoint.XXXXXXXX") ||
			die "cannot preserve old sysrunc-fs checkpoint"
		mv "$checkpoint" "$backup/" || die "cannot preserve old sysrunc-fs checkpoint"
	fi
	legacy_fs_migration_required="false"
	legacy_fs_migrated="true"
}

function remove_sysrunc() {
	echo "Removing $sysrunc_edition from host ..."
	stop_sysrunc
	rm_systemd_units_from_host
	rm_conf_from_host
	rm_sysrunc_from_host
}

function deploy_sysrunc_installer_helper() {
	echo "Deploying $sysrunc_edition installer helper on the host ..."
	cp ${sysrunc_artifacts}/scripts/sysrunc-installer-helper.sh ${host_local_bin}/sysrunc-installer-helper.sh
	cp ${sysrunc_artifacts}/systemd/sysrunc-installer-helper.service ${host_systemd}/sysrunc-installer-helper.service
	systemctl daemon-reload
	echo "Running $sysrunc_edition installer helper on the host (may take several seconds) ..."
	systemctl restart sysrunc-installer-helper.service
}

function remove_sysrunc_installer_helper() {
	echo "Stopping the $sysrunc_edition installer helper on the host ..."
	systemctl stop sysrunc-installer-helper.service || true
	systemctl disable sysrunc-installer-helper.service || true
	echo "Removing $sysrunc_edition installer helper from the host ..."
	rm -f ${host_local_bin}/sysrunc-installer-helper.sh
	rm -f ${host_systemd}/sysrunc-installer-helper.service
	systemctl daemon-reload
}

function install_sysrunc_deps() {
	# Kubernetes user namespaces remove the shiftfs and subid allocator dependency.
	deploy_sysrunc_installer_helper
	remove_sysrunc_installer_helper
}

function remove_sysrunc_deps() {
	# Shared host packages are left in place for other workloads.
	true
}

function get_subid_limits() {

	# Get subid defaults from /etc/login.defs

	subuid_min=$subid_alloc_min_start
	subuid_max=$subid_alloc_max_end
	subgid_min=$subid_alloc_min_start
	subgid_max=$subid_alloc_max_end

	if [ ! -f $subid_def_file ]; then
		return
	fi

	set +e
	res=$(grep "^SUB_UID_MIN" $subid_def_file 2>/dev/null)
	if [ $? -eq 0 ]; then
		subuid_min=$(echo $res | cut -d " " -f2)
	fi

	res=$(grep "^SUB_UID_MAX" $subid_def_file 2>/dev/null)
	if [ $? -eq 0 ]; then
		subuid_max=$(echo $res | cut -d " " -f2)
	fi

	res=$(grep "^SUB_GID_MIN" $subid_def_file 2>/dev/null)
	if [ $? -eq 0 ]; then
		subgid_min=$(echo $res | cut -d " " -f2)
	fi

	res=$(grep "^SUB_GID_MAX" $subid_def_file 2>/dev/null)
	if [ $? -eq 0 ]; then
		subgid_max=$(echo $res | cut -d " " -f2)
	fi
	set -e
}

function config_subid_range() {
	local subid_file=$1
	local subid_size=$2
	local subid_min=$3
	local subid_max=$4

	if [ ! -f $subid_file ] || [ ! -s $subid_file ]; then
		echo "$subid_user:$subid_min:$subid_size" >"${subid_file}"
		return
	fi

	readarray -t subid_entries <"${subid_file}"

	# if a large enough subid config already exists for user $subid_user, there
	# is nothing to do.

	for entry in "${subid_entries[@]}"; do
		user=$(echo $entry | cut -d ":" -f1)
		start=$(echo $entry | cut -d ":" -f2)
		size=$(echo $entry | cut -d ":" -f3)

		if [[ "$user" == "$subid_user" ]] && [ "$size" -ge "$subid_size" ]; then
			return
		fi
	done

	# Sort subid entries by start range
	declare -a sorted_subids
	if [ ${#subid_entries[@]} -gt 0 ]; then
		readarray -t sorted_subids < <(echo "${subid_entries[@]}" | tr " " "\n" | tr ":" " " | sort -n -k 2)
	fi

	# allocate a range of subid_alloc_range size
	hole_start=$subid_min

	for entry in "${sorted_subids[@]}"; do
		start=$(echo $entry | cut -d " " -f2)
		size=$(echo $entry | cut -d " " -f3)

		hole_end=$start

		if [ $hole_end -ge $hole_start ] && [ $((hole_end - hole_start)) -ge $subid_size ]; then
			echo "$subid_user:$hole_start:$subid_size" >>$subid_file
			return
		fi

		hole_start=$((start + size))
	done

	hole_end=$subid_max
	if [ $((hole_end - hole_start)) -lt $subid_size ]; then
		echo "failed to allocate $subid_size sub ids in range $subid_min:$subid_max"
		return
	else
		echo "$subid_user:$hole_start:$subid_size" >>$subid_file
		return
	fi
}

function config_crio_for_sysrunc() {
	echo "Adding Sysrunc to CRI-O config ..."

	if [ ! -f ${host_crio_conf_file_backup} ]; then
		cp ${host_crio_conf_file} ${host_crio_conf_file_backup}
	fi

	# overlayfs with metacopy=on improves startup time of CRI-O rootless containers significantly
	if ! dasel -n get string -f "${host_crio_conf_file}" -p toml -s 'crio.storage_option' | grep -q "metacopy=on"; then
		dasel put string -f "${host_crio_conf_file}" -p toml -m 'crio.storage_driver' "overlay"
		dasel put string -f "${host_crio_conf_file}" -p toml -m 'crio.storage_option.[]' "overlay.mountopt=metacopy=on"
	fi

	local runtime_path="${host_bin#/mnt/host}/sysrunc"
	dasel put object -f "${host_crio_conf_file}" -p toml -t string -t string -t string "crio.runtime.runtimes.sysrunc" \
		"runtime_path=${runtime_path}" "runtime_type=oci" "monitor_path=/usr/local/bin/crio-conmon"
	dasel put string -f "${host_crio_conf_file}" -p toml "crio.runtime.runtimes.sysrunc.allowed_annotations.[0]" \
		"io.kubernetes.cri-o.userns-mode"

}

function unconfig_crio_for_sysrunc() {
	echo "Removing Sysrunc from CRI-O config ..."

	# Note: dasel does not yet have a proper delete command, so we need the "sed" below.
	dasel put document -f "${host_crio_conf_file}" -p toml '.crio.runtime.runtimes.sysrunc' ''
	sed -i "s/\[crio.runtime.runtimes.sysrunc\]//g" "${host_crio_conf_file}"
}

#
# Containerd Configuration Functions
#

function config_containerd_runtime() {
	local runtime_name=$1 runtime_path=$2
	local cri_v1="plugins.io\\.containerd\\.grpc\\.v1\\.cri.containerd.runtimes.${runtime_name}"
	local cri_v2="plugins.io\\.containerd\\.cri\\.v1\\.runtime.containerd.runtimes.${runtime_name}"

	for cri_path in "${cri_v1}" "${cri_v2}"; do
		dasel put string -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.runtime_type" -v "io.containerd.runc.v2"
		dasel put string -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.options.BinaryName" -v "${runtime_path}"
		dasel put bool -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.options.SystemdCgroup" -v true
		dasel delete -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.pod_annotations" >/dev/null 2>&1 || true
		local annotation
		local index=0
		for annotation in sysrunc.io/rootfs-rw-layer sysbox/rootfs-rw-layer sysrunc.io/fs sysrunc.io/ticker; do
			dasel put string -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.pod_annotations.[${index}]" -v "${annotation}"
			index=$((index + 1))
		done
		if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
			dasel put string -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.snapshotter" -v "sysrunc"
		else
			dasel delete -f "${host_containerd_conf_file}" -p toml -s "${cri_path}.snapshotter" >/dev/null 2>&1 || true
		fi
	done
}

function config_containerd_for_sysrunc() {
	echo "Adding Sysrunc to containerd config ..."
	if [ ! -f "${host_containerd_conf_file_backup}" ]; then
		cp "${host_containerd_conf_file}" "${host_containerd_conf_file_backup}"
	fi
	config_containerd_runtime "sysrunc" "${host_bin#/mnt/host}/sysrunc"
	if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
		dasel put string -f "${host_containerd_conf_file}" -p toml -s "proxy_plugins.sysrunc.type" -v "snapshot"
		dasel put string -f "${host_containerd_conf_file}" -p toml -s "proxy_plugins.sysrunc.address" -v "/run/sysrunc-snapshotter.sock"
		dasel delete -f "${host_containerd_conf_file}" -p toml -s "proxy_plugins.sysrunc.capabilities" >/dev/null 2>&1 || true
		dasel put string -f "${host_containerd_conf_file}" -p toml -m "proxy_plugins.sysrunc.capabilities.[]" "remap-ids"
	else
		dasel delete -f "${host_containerd_conf_file}" -p toml -s "proxy_plugins.sysrunc" >/dev/null 2>&1 || true
	fi
	systemctl restart containerd
}

function unconfig_containerd_for_sysrunc() {
	echo "Removing Sysrunc from containerd config ..."
	if [ -f "${host_containerd_conf_file}" ]; then
		dasel delete -f "${host_containerd_conf_file}" -p toml -s "plugins.io\\.containerd\\.grpc\\.v1\\.cri.containerd.runtimes.sysrunc" >/dev/null 2>&1 || true
		dasel delete -f "${host_containerd_conf_file}" -p toml -s "plugins.io\\.containerd\\.cri\\.v1\\.runtime.containerd.runtimes.sysrunc" >/dev/null 2>&1 || true
		dasel delete -f "${host_containerd_conf_file}" -p toml -s "proxy_plugins.sysrunc" >/dev/null 2>&1 || true
		systemctl restart containerd
	fi
}

#
# K3s Containerd Configuration Functions
#

function is_k3s_runtime() {
	[[ "${k8s_runtime:-}" == "k3s" ]] || [[ "${k8s_runtime:-}" == "k3s-agent" ]]
}

function get_k3s_service_name() {
	if systemctl is-active --quiet k3s-agent; then
		echo "k3s-agent"
	else
		echo "k3s"
	fi
}

function restart_k3s() {
	local service_name=$(get_k3s_service_name)

	echo "Restarting K3s service ${service_name} to apply containerd config ..."
	systemctl restart "${service_name}"
}

function write_default_k3s_containerd_template() {
	local snapshotter_config=""
	local proxy_config=""
	if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
		snapshotter_config='  snapshotter = "sysrunc"'
		proxy_config='[proxy_plugins."sysrunc"]
  type = "snapshot"
  address = "/run/sysrunc-snapshotter.sock"
  capabilities = ["remap-ids"]
'
	fi

	cat >"${host_k3s_containerd_conf_template}" <<EOF
version = 3
imports = ["/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/*.toml"]
root = "/var/lib/rancher/k3s/agent/containerd"
state = "/run/k3s/containerd"

${proxy_config}

[grpc]
  address = "/run/k3s/containerd/containerd.sock"

[plugins.'io.containerd.internal.v1.opt']
  path = "/var/lib/rancher/k3s/agent/containerd"

[plugins.'io.containerd.grpc.v1.cri']
  stream_server_address = "127.0.0.1"
  stream_server_port = "10010"

[plugins.'io.containerd.cri.v1.runtime']
  enable_selinux = false
  enable_unprivileged_ports = true
  enable_unprivileged_icmp = true
  device_ownership_from_security_context = false

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
  disable_snapshot_annotations = false
  use_local_image_pull = true

[plugins.'io.containerd.cri.v1.images'.pinned_images]
  sandbox = "rancher/mirrored-pause:3.6"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
  SystemdCgroup = true

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc]
  runtime_type = "io.containerd.runc.v2"
  pod_annotations = ["sysrunc.io/rootfs-rw-layer", "sysbox/rootfs-rw-layer", "sysrunc.io/fs", "sysrunc.io/ticker"]
${snapshotter_config}

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc.options]
  SystemdCgroup = true
  BinaryName = "${host_bin#/mnt/host}/sysrunc"

[plugins.'io.containerd.cri.v1.images'.registry]
  config_path = "/var/lib/rancher/k3s/agent/etc/containerd/certs.d"
EOF
}

function config_k3s_containerd_for_sysrunc() {
	echo "Adding Sysrunc to K3s containerd config ..."
	mkdir -p "${host_k3s_containerd_conf_dir}"
	local config_before=""
	if [[ -f "${host_k3s_containerd_conf_template}" ]]; then
		config_before=$(sed '/^[[:space:]]*$/d' "${host_k3s_containerd_conf_template}" | sha256sum | cut -c1-64)
	fi
	if [ -f "${host_k3s_containerd_conf_template}" ] && [ ! -f "${host_k3s_containerd_conf_template_backup}" ]; then
		cp "${host_k3s_containerd_conf_template}" "${host_k3s_containerd_conf_template_backup}"
	fi
	if [ ! -f "${host_k3s_containerd_conf_template}" ]; then
		write_default_k3s_containerd_template
	else
		# Replace only sections owned by Sysrunc. Existing Sysbox handlers stay intact.
		sed -i '/runtimes.sysrunc.options]/,/^$/d; /runtimes.sysrunc]/,/^$/d; /^\[proxy_plugins\."sysrunc"\]/,/^$/d' "${host_k3s_containerd_conf_template}"
		local snapshotter_config=""
		if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
			snapshotter_config='  snapshotter = "sysrunc"'
			cat >>"${host_k3s_containerd_conf_template}" <<EOF

[proxy_plugins."sysrunc"]
  type = "snapshot"
  address = "/run/sysrunc-snapshotter.sock"
  capabilities = ["remap-ids"]
EOF
		fi
		cat >>"${host_k3s_containerd_conf_template}" <<EOF

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc]
  runtime_type = "io.containerd.runc.v2"
  pod_annotations = ["sysrunc.io/rootfs-rw-layer", "sysbox/rootfs-rw-layer", "sysrunc.io/fs", "sysrunc.io/ticker"]
${snapshotter_config}

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysrunc.options]
  SystemdCgroup = true
  BinaryName = "${host_bin#/mnt/host}/sysrunc"
EOF
	fi
	if [[ "$config_before" != "$(sed '/^[[:space:]]*$/d' "${host_k3s_containerd_conf_template}" | sha256sum | cut -c1-64)" ]]; then
		restart_k3s
	else
		echo "K3s containerd config unchanged; no K3s restart needed."
	fi
}

function unconfig_k3s_containerd_for_sysrunc() {
	echo "Removing Sysrunc from K3s containerd config ..."
	if [ -f "${host_k3s_containerd_conf_template}" ] && grep -q "runtimes.sysrunc]" "${host_k3s_containerd_conf_template}"; then
		sed -i '/runtimes.sysrunc.options]/,/^$/d; /runtimes.sysrunc]/,/^$/d; /^\[proxy_plugins\."sysrunc"\]/,/^$/d' "${host_k3s_containerd_conf_template}"
		restart_k3s
	fi
}

#
# General Helper Functions
#

function die() {
	msg="$*"
	echo "ERROR: $msg" >&2
	exit 1
}

function print_usage() {
	echo "Usage: $0 [ce|ee] [install|cleanup] [--snapshotter-enabled=true|false]"
}

function parse_bool_arg() {
	case "$2" in
	true|false)
		printf '%s\n' "$2"
		;;
	*)
		print_usage
		die "invalid $1 value: $2"
		;;
	esac
}

function get_k8s_version() {
	local version=$(kubectl get node $NODE_NAME -o jsonpath='{.status.nodeInfo.kubeletVersion}' | awk -F "." '{print $1 "." $2}')

	if [ "$?" -ne 0 ]; then
		die "invalid Kubernetes version"
	fi

	echo "$version"
}

function get_container_runtime() {
	local runtime=$(kubectl get node $NODE_NAME -o jsonpath='{.status.nodeInfo.containerRuntimeVersion}')

	if [ "$?" -ne 0 ]; then
		die "invalid node name"
	fi
	if echo "$runtime" | grep -qE 'containerd.*-k3s'; then
		if systemctl is-active --quiet k3s-agent; then
			echo "k3s-agent"
		else
			echo "k3s"
		fi
	else
		echo "$runtime" | awk -F '[:]' '{print $1}'
	fi
}

function get_host_distro() {
	local distro_name=$(grep -w "^ID" "$host_os_release" | cut -d "=" -f2 | tr -d '"')
	local version_id=$(grep -w "^VERSION_ID" "$host_os_release" | cut -d "=" -f2 | tr -d '"')
	echo "${distro_name}-${version_id}"
}

function get_sys_arch() {
	local uname_m=$(uname -m)

	if [[ "$uname_m" == "x86_64" ]]; then
		sys_arch=amd64
	elif [[ "$uname_m" == "aarch64" ]]; then
		sys_arch=arm64
	elif [[ "$uname_m" == "arm" ]]; then
		sys_arch=armhf
	elif [[ "$uname_m" == "armel" ]]; then
		sys_arch=armel
	fi

	echo "${sys_arch}"
}

function host_flatcar_distro() {
	local distro=$(get_host_distro)
	echo $distro | grep -q "flatcar"
}

function get_host_kernel() {
	uname -r
}

function is_supported_distro() {

	local distro=$os_distro_release

	if [[ "$distro" == "ubuntu-24.04" ]] ||
		[[ "$distro" == "ubuntu-22.04" ]] ||
		[[ "$distro" == "ubuntu-21.10" ]] ||
		[[ "$distro" == "ubuntu-20.04" ]] ||
		[[ "$distro" == "ubuntu-18.04" ]] ||
		[[ "$distro" == "centos-9" ]] ||
		[[ "$distro" =~ "debian" ]] ||
		[[ "$distro" =~ "flatcar" ]]; then
		return
	fi

	false
}

function is_supported_kernel() {

	local kversion=$(echo $os_kernel_release | cut -d "." -f1-2)

	# Ubuntu distro is supported starting with kernel 5.3+.
	if [[ "$os_distro_release" =~ "ubuntu" ]]; then
		if semver_lt $kversion 5.3; then
			echo "Unsupported kernel version $kversion for Ubuntu distribution (< 5.3)."
			return 1
		fi

		return 0
	fi

	# For all other distros, Sysrunc requires 5.5+.
	if semver_lt $kversion 5.5; then
		echo "Unsupported kernel version $kversion for $os_distro_release distribution (< 5.5)."
		return 1
	fi

	return 0
}

function is_supported_arch() {
	if [[ "$sys_arch" == "amd64" ]] || [[ "$sys_arch" == "arm64" ]]; then
		return
	fi

	false
}

function is_supported_k8s_version() {

	local ver=$k8s_version

	if [[ "$ver" == "v1.32" ]] ||
		[[ "$ver" == "v1.33" ]] ||
		[[ "$ver" == "v1.34" ]] ||
		[[ "$ver" == "v1.35" ]] ||
		[[ "$ver" == "v1.36" ]] ||
		[[ "$ver" == "v1.37" ]] ; then
		return
	fi

	if [[ "$ver" == "v1.19" ]] ||
		[[ "$ver" == "v1.20" ]] ||
		[[ "$ver" == "v1.21" ]] ||
		[[ "$ver" == "v1.22" ]] ||
		[[ "$ver" == "v1.23" ]] ||
		[[ "$ver" == "v1.24" ]] ||
		[[ "$ver" == "v1.25" ]] ||
		[[ "$ver" == "v1.26" ]] ||
		[[ "$ver" == "v1.27" ]] ||
		[[ "$ver" == "v1.28" ]] ||
		[[ "$ver" == "v1.29" ]] ||
		[[ "$ver" == "v1.30" ]] ||
		[[ "$ver" == "v1.31" ]] ; then
		echo "Unsupported kubernetes version: $ver (EOL release)."
	fi

	false
}

function is_kernel_upgraded() {
	local cur_kernel=$os_kernel_release

	if [ ! -f ${host_var_lib_sysrunc_deploy_k8s}/os_kernel_release ]; then
		false
		return
	fi

	local prev_kernel=$(cat ${host_var_lib_sysrunc_deploy_k8s}/os_kernel_release)
	if [[ ${cur_kernel} == ${prev_kernel} ]]; then
		false
		return
	fi

	true
}

function is_sysrunc_upgraded() {
	# If Sysrunc hasn't been installed yet, then there's no upgrade needed.
	if [ ! -f ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed ]; then
		false
		return
	fi

	# If the sysrunc version file does not exist, then we must be dealing with an
	# old Sysrunc version (<v0.6.3-0), which did not have this file. In this case
	# proceed with the upgrade.
	if [ ! -f ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed_version ]; then
		echo "No previous Sysrunc version records found, proceed to upgrade to Sysrunc ${sysrunc_version} release."
		true
		return
	fi

	# Upgrade if Sysrunc's new and current versions don't match.
	local sysrunc_installed_version=$(cat ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed_version)
	if [[ "${sysrunc_version}" != "${sysrunc_installed_version}" ]]; then
		echo "Mismatch found between the existing (${sysrunc_installed_version}) and the new (${sysrunc_version} Sysrunc versions. Upgrade to the new one."
		true
		return
	fi

	false
}

# A same-version image can contain a rebuilt runtime. Compare the packaged
# artifacts with the binaries actually installed on this node.
function is_sysrunc_artifact_changed() {
	local artifacts_dir name source installed source_hash installed_hash
	artifacts_dir=$(get_artifacts_dir)
	local names=(sysrunc sysrunc-fs sysrunc-admission)
	if [[ "${sysrunc_snapshotter_enabled}" == "true" ]]; then
		names+=(sysrunc-snapshotter)
	fi
	for name in "${names[@]}"; do
		source="${artifacts_dir}/${name}"
		installed="${host_bin}/${name}"
		[[ -f "$source" ]] || die "missing Sysrunc artifact: $source"
		if [[ ! -f "$installed" ]]; then
			echo "Sysrunc artifact $name is not installed; updating."
			return 0
		fi
		source_hash=$(sha256sum "$source" | cut -c1-64)
		installed_hash=$(sha256sum "$installed" | cut -c1-64)
		if [[ "$source_hash" != "$installed_hash" ]]; then
			echo "Sysrunc artifact $name changed; updating."
			return 0
		fi
	done
	return 1
}

# Check if the sysrunc operational config has changed.
function is_sysrunc_config_changed() {
	local sysrunc_fs_config=""
	local sysrunc_snapshotter_config="true"
	local sysrunc_sysctl_config=""
	local sysrunc_mod_config=""

	# Check if there are sysrunc config changes.

	if [ -f ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_fs_config ]; then
		sysrunc_fs_config=$(cat ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_fs_config)
	fi
	if [ -f ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_snapshotter_enabled ]; then
		sysrunc_snapshotter_config=$(cat ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_snapshotter_enabled)
	fi

	if [ "$sysrunc_fs_config" != "$SYSRUNC_FS_CONFIG" ] || \
		[ "$sysrunc_snapshotter_config" != "$sysrunc_snapshotter_enabled" ]; then
		echo "Sysrunc operational settings have changed -- sysrunc-fs: ${SYSRUNC_FS_CONFIG}, snapshotter-enabled: ${sysrunc_snapshotter_enabled}"
		return 0
	fi

	# Check if there are sysrunc-env config changes.

	if [ -f ${host_sysctl}/99-sysrunc-sysctl.conf ]; then
		sysrunc_sysctl_config=$(cat ${host_sysctl}/99-sysrunc-sysctl.conf)
	fi
	if [ -f ${host_lib_mod}/50-sysrunc-mod.conf ]; then
		sysrunc_mod_config=$(cat ${host_lib_mod}/50-sysrunc-mod.conf)
	fi

	if [ "$sysrunc_sysctl_config" != "$(cat ${sysrunc_artifacts}/systemd/99-sysrunc-sysctl.conf)" ] || \
		[ "$sysrunc_mod_config" != "$(cat ${sysrunc_artifacts}/systemd/50-sysrunc-mod.conf)" ]; then
		echo "Sysrunc env settings have changed."
		return 0
	fi

	return 1
}

function add_label_to_node() {
	local label=$1
	echo "Adding K8s label \"$label\" to node ..."
	kubectl label node "$NODE_NAME" --overwrite "${label}"
}

function rm_label_from_node() {
	local label=$1
	echo "Removing K8s label \"$label\" from node ..."
	kubectl label node "$NODE_NAME" "${label}-"
}

function is_containerd_with_userns() {

	# containerd 2.0 introduces support for user-namespaces, but versions 2.0.1
	# through 2.0.4 have a bug that prevents user-namespaced pods with alternative
	# runtimes (like Sysrunc) from working properly. The bug causes the pod sandbox
	# to use the alternative runtime, but containers within the sandbox still use
	# the default runc runtime.

	local runtime_version=$(kubectl get node $NODE_NAME -o jsonpath='{.status.nodeInfo.containerRuntimeVersion}')

	# Check if it's containerd
	if ! echo "$runtime_version" | grep -q "containerd://"; then
		return 1
	fi

	# Extract version number (e.g., "containerd://2.0.4" -> "2.0.4")
	local version=$(echo "$runtime_version" | sed 's/containerd:\/\///' | sed -E 's/^([0-9]+(\.[0-9]+){0,2}).*/\1/')

	echo "Detected containerd version $version."

	# Check if version >= 2.0.0
	version_compare "$version" "2.0.0"
	local cmp_result=$?
	if [[ ! $cmp_result =~ ^[01]$ ]]; then
		echo "containerd version $version does not support user-namespaces (requires >= 2.0.0)."
		return 1
	fi

	# Exclude buggy versions 2.0.1 through 2.0.4 (inclusive)
	version_compare "$version" "2.0.1"
	local cmp_ge_201=$?
	version_compare "2.0.4" "$version"
	local cmp_204_ge=$?

	if [[ $cmp_ge_201 =~ ^[01]$ ]] && [[ $cmp_204_ge =~ ^[01]$ ]]; then
		echo "containerd version $version does not support user-namespaces with alternative runtimes (buggy versions 2.0.1 -> 2.0.4)."
		return 1
	fi

	return 0
}

function runtime_precheck() {
	do_crio_install="false"
	do_kubelet_use_crio="false"

	# env var SYSRUNC_USE_CRIO=true|yes forces CRI-O installation
	# (may be set via ConfigMap for testing purposes).
	if is_k3s_runtime; then
		echo "Will use K3s containerd as the container runtime for Sysrunc."

	elif [[ "${SYSRUNC_USE_CRIO:-}" =~ ^([Tt][Rr][Uu][Ee]|[Yy][Ee][Ss])$ ]]; then
		if ! systemctl is-active --quiet crio; then
			do_crio_install="true"
			do_kubelet_use_crio="true"
			echo "Will install CRI-O as container runtime for Sysrunc (SYSRUNC_USE_CRIO=${SYSRUNC_USE_CRIO})."
		fi

	elif is_containerd_with_userns; then
		echo "Will use containerd as the container runtime for Sysrunc (containerd version supports user-namespaces)."

	else
		if ! systemctl is-active --quiet crio; then
			do_crio_install="true"
			do_kubelet_use_crio="true"
			echo "Will install CRI-O as container runtime for Sysrunc (containerd version does NOT support user-namespaces)."
		fi
	fi
}

function install_precheck() {

	# check if we need to install CRI-O or not
	runtime_precheck

	if systemctl is-active --quiet sysrunc; then
		do_sysrunc_install="false"
	fi

	if kubectl get node $NODE_NAME -o jsonpath='{.metadata.labels.sysrunc-runtime}' | grep -q "installing"; then
		sysrunc_install_in_progress="true"
	fi

	# Update if the same-version image contains rebuilt binaries, too.
	if is_kernel_upgraded || is_sysrunc_upgraded || is_sysrunc_config_changed || is_sysrunc_artifact_changed; then
		do_sysrunc_update="true"
	fi
}

# Compare two versions in SemVer format.
#
# Examples:  (1.0.1, 1.0.1) = 0
#            (1.0.1, 1.0.2) = 2
#            (1.0.1, 1.0.0) = 1
#            (1, 1.0) = 0
#            (3.0.4.10, 3.0.4.2) = 1
#            (5.0.0-22, 5.0.0-22) = 0
#            (5.0.0-22, 5.0.0-21) = 1
#            (5.0.0-21, 5.0.0-22) = 2
#
function version_compare() {

	if [[ $1 == $2 ]]; then
		return 0
	fi

	local IFS='.|-'
	local i ver1=($1) ver2=($2)

	# Fill empty fields in ver1 with zeros.
	for ((i = ${#ver1[@]}; i < ${#ver2[@]}; i++)); do
		ver1[i]=0
	done

	for ((i = 0; i < ${#ver1[@]}; i++)); do
		if [[ -z ${ver2[i]} ]]; then
			# Fill empty fields in ver2 with zeros.
			ver2[i]=0
		fi
		if ((10#${ver1[i]} > 10#${ver2[i]})); then
			return 1
		fi
		if ((10#${ver1[i]} < 10#${ver2[i]})); then
			return 2
		fi
	done

	return 0
}

# Compare semantic versions; takes two semantic version numbers of the form
# x.y.z (or x.y), and returns 0 if the first is less than the
# second, and 1 otherwise.
function semver_lt() {
	version_compare $1 $2
	if [ "$?" -eq "2" ]; then
		return 0
	else
		return 1
	fi
}

# Compare semantic versions; takes two semantic version numbers of the form
# x.y.z (or x.y), and returns 0 if the first is greater than or equal to the
# second, and 1 otherwise.
function semver_ge() {
	version_compare $1 $2
	if [ "$?" -ne "2" ]; then
		return 0
	else
		return 1
	fi
}

function do_edition_adjustments() {
	local edition_tag=$1

	# Set the Sysrunc edition name being installed.
	if [[ ${edition_tag} == "ce" ]]; then
		sysrunc_edition="Sysrunc"
	elif [[ ${edition_tag} == "ee" ]]; then
		sysrunc_edition="Sysrunc-EE"
	else
		print_usage
		die "invalid sysrunc edition value: $edition_tag"
	fi
}

# Function holds all the adjustments that need to be carried out to meet
# distro-specific requirements. For example, in Flatcar's case these special
# requirements are a consequence of its particular partition scheme (read-only
# /usr). For readability and maintainability purposes, we opted by placing this
# adjustment logic away from the natural location where each file component is
# utilized, so we must keep this point in mind if the files being edited here
# were to be modified prior to the invocation of this routine.
function do_distro_adjustments() {

	local distro=$(get_host_distro)
	if [[ ! ${distro} =~ "flatcar" ]]; then
		return
	fi

	# Ensure that Flatcar installation proceeds only in Sysrunc-EE case.
	if [[ ${sysrunc_edition} != "Sysrunc-EE" ]]; then
		die "Flatcar OS distribution is only supported on Sysrunc Enterprise-Edition. Exiting ..."
	fi

	# Adjust global vars.
	host_bin="/mnt/host/opt/bin"
	host_local_bin="/mnt/host/opt/local/bin"
	host_systemd="/mnt/host/etc/systemd/system"
	host_sysctl="/mnt/host/opt/lib/sysctl.d"
	host_lib_mod="/mnt/host/opt/lib/modules-load.d"

	# Ensure that required folders are already present.
	mkdir -p ${host_bin} ${host_local_bin} ${host_systemd} ${host_sysctl} ${host_lib_mod}

	# Adjust crio helper scripts and services.
	sed -i 's@/usr/local/bin/crio@/opt/local/bin/crio@g' ${crio_artifacts}/systemd/crio-installer.service
	sed -i '/Type=oneshot/a Environment=PATH=/opt/local/bin:/sbin:/bin:/usr/sbin:/usr/bin' ${crio_artifacts}/systemd/crio-removal.service
	sed -i 's@/usr/local/bin/crio@/opt/local/bin/crio@g' ${crio_artifacts}/systemd/crio-removal.service

	# Adjust kubelet helper scripts and services.
	sed -i '/^ExecStart=/ s@/usr/local/bin@/opt/local/bin@' ${crio_artifacts}/systemd/kubelet-config-helper.service
	sed -i '/^ExecStart=/ s@/usr/local/bin@/opt/local/bin@' ${crio_artifacts}/systemd/kubelet-unconfig-helper.service
	sed -i '/^crictl_bin/ s@/usr/local/bin@/opt/local/bin@' ${crio_artifacts}/scripts/kubelet-config-helper.sh
	sed -i '/^crictl_bin/ s@/usr/local/bin@/opt/local/bin@' ${crio_artifacts}/scripts/kubelet-unconfig-helper.sh

	# Adjust sysrunc helper scripts and services.
	sed -i '/^ExecStart=/ s@/usr/bin/sysrunc-fs@/opt/bin/sysrunc-fs@' ${sysrunc_artifacts}/systemd/sysrunc-fs.service
	sed -i '/Type=notify/a Environment=PATH=/opt/bin:/sbin:/bin:/usr/sbin:/usr/bin' ${sysrunc_artifacts}/systemd/sysrunc-fs.service
	sed -i '/^ExecStart=/ s@/usr/bin@/opt/bin@g' ${sysrunc_artifacts}/systemd/sysrunc.service
	sed -i '/^ExecStart=/ s@/usr/local/bin@/opt/local/bin@g' ${sysrunc_artifacts}/systemd/sysrunc-installer-helper.service
	sed -i '/^ExecStart=/ s@/usr/local/bin@/opt/local/bin@g' ${sysrunc_artifacts}/systemd/sysrunc-removal-helper.service

	# Sysctl adjustments.
	sed -i '/^kernel.unprivileged_userns_clone/ s/^#*/# /' ${sysrunc_artifacts}/systemd/99-sysrunc-sysctl.conf
}

# determines if running on a GKE cluster by checking metadata endpoint
function check_is_gke() {
	# GKE nodes will respond with an HTTP 200 for this URL and Metadata-Flavor header.
	# Other clouds, URLs, etc. will throw a 404 error. If curl cannot connect the code will be 000.
	is_cluster=$(curl -s -o /dev/null \
	 		-w "%{http_code}" \
	   		--connect-timeout 1 \
	     		-H "Metadata-Flavor: Google" \
	       		169.254.169.254/computeMetadata/v1/instance/attributes/cluster-name)
	if [ $is_cluster -ne 200 ]; then
		false
		return
	fi
	true
}

# Fixes an issue with crio network bridge on GKE not working.
# Also adds correct path to k8s binaries on GKE nodes in /home/kubernetes/bin
function config_crio_for_gke() {
	rm -rf ${host_etc}/cni/net.d/100-crio-bridge.conf
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.network.plugin_dirs.[]' "/opt/cni/bin/"
	dasel put string -f ${host_crio_conf_file} -p toml -m 'crio.network.plugin_dirs.[]' "/home/kubernetes/bin"
}

# Delete all sysrunc pods in the node to ensure that the newly installed/updated sysrunc runtime
# is used. This is needed because the sysrunc pods are not automatically restarted when the sysrunc
# runtime is updated, and these may not work correctly otherwise. Similarly, this routine is also
# needed after sysrunc removal to ensure that the sysrunc pods are not left running.
function delete_sysrunc_pods() {

	# Turn off errexit in these steps as we don't want to interrupt the installation/update
	# process if any of the pods fail to be deleted for any reason.
	set +e

	echo "Deleting all the preexisting sysrunc pods in the node..."
	kubectl get pod -A -o json | jq -r --arg node "$NODE_NAME" '.items[] |
		select(.spec.runtimeClassName=="sysrunc" and .spec.nodeName==$node) | "\(.metadata.name) \(.metadata.namespace)"' | \
        while read name namespace; do \
                echo "Deleting sysrunc pod ${name} from namespace ${namespace}..."; \
				kubectl delete pod/${name} -n ${namespace} --grace-period=1 --wait=false; \
        done

	set -e
}

function cleanup_sysrunc_admission_resources() {
	set +e

	echo "Cleaning up sysrunc-admission runtime-owned Kubernetes resources..."
	kubectl delete mutatingwebhookconfiguration "${SYSRUNC_ADMISSION_WEBHOOK_NAME}" --ignore-not-found
	kubectl delete secret -n "${SYSRUNC_ADMISSION_NAMESPACE}" "${SYSRUNC_ADMISSION_CA_SECRET_NAME}" "${SYSRUNC_ADMISSION_TLS_SECRET_NAME}" --ignore-not-found
	kubectl delete lease.coordination.k8s.io -n "${SYSRUNC_ADMISSION_NAMESPACE}" "${SYSRUNC_ADMISSION_LEASE_NAME}" --ignore-not-found

	set -e
}

#
# Main Function
#

function main() {

	euid=$(id -u)
	if [[ $euid -ne 0 ]]; then
		die "This script must be run as root"
	fi

	os_distro_release=$(get_host_distro)
	# The installer runs in its own mount namespace, so container-local
	# systemctl may only report that it is running in a chroot. Always target
	# the host's PID 1 when managing host services.
	systemctl() {
		nsenter -t 1 -m -u -i -n -p -- /proc/1/root/usr/bin/systemctl "$@"
	}
	if ! is_supported_distro; then
		echo "Warning: Sysrunc is not officially supported on this host's distro ($os_distro_release)".
	fi

	os_kernel_release=$(get_host_kernel)
	if ! is_supported_kernel; then
		die "Sysrunc is not supported on this host's kernel release ($os_kernel_release)".
	fi

	sys_arch=$(get_sys_arch)
	if ! is_supported_arch; then
		die "Sysrunc is not supported on this platform architecture ($sys_arch)".
	fi

	k8s_version=$(get_k8s_version)
	if ! is_supported_k8s_version; then
		die "Sysrunc is not supported on this Kubernetes version ($k8s_version)".
	fi

	k8s_runtime=$(get_container_runtime)
	if [[ $k8s_runtime == "" ]]; then
		die "Failed to detect K8s node runtime."
	elif [ "$k8s_runtime" == "cri-o" ]; then
		k8s_runtime="crio"
	fi

	echo "Detected Kubernetes version $k8s_version"

	local edition_tag=${1:-}
	if [ -z "$edition_tag" ]; then
		print_usage
		die "invalid arguments"
	fi

	# Adjust env-vars associated to the Sysrunc product edition being (un)installed.
	do_edition_adjustments $edition_tag

	local action=${2:-}
	if [ -z "$action" ]; then
		print_usage
		die "invalid arguments"
	fi
	shift 2
	while [[ $# -gt 0 ]]; do
		case "$1" in
		--snapshotter-enabled=*)
			sysrunc_snapshotter_enabled=$(parse_bool_arg "--snapshotter-enabled" "${1#*=}")
			;;
		*)
			print_usage
			die "invalid argument: $1"
			;;
		esac
		shift
	done

	# Perform distro-specific adjustments.
	do_distro_adjustments

	local crio_restart_pending=false

	case "$action" in
	install)
		install_precheck
		if [[ "$do_sysrunc_install" == "true" ]] ||
			[[ "$do_sysrunc_update" == "true" ]]; then
			precheck_fs_upgrade
			if [[ "$legacy_fs_migration_required" == "true" ]]; then
				migrate_legacy_fs_pods
			fi
		fi
		mkdir -p ${host_var_lib_sysrunc_deploy_k8s}

		# Install CRI-O (if necessary)
		if [[ "$do_crio_install" == "true" ]]; then
			add_label_to_node "crio-runtime=installing"
			deploy_crio_installer_service
			remove_crio_installer_service
			config_crio
			# if running on GKE patch the CRI-O config
			if $(check_is_gke); then
				echo "Configuring CRI-O for GKE"
				config_crio_for_gke
			fi
			crio_restart_pending=true
			echo "yes" >${host_var_lib_sysrunc_deploy_k8s}/crio_installed
		fi

		# Install or update Sysrunc
		if [[ "$do_sysrunc_install" == "true" ]] ||
			[[ "$do_sysrunc_update" == "true" ]]; then
			add_label_to_node "sysrunc-runtime=installing"
			install_sysrunc_deps
		fi

		# Dependencies may install the profile; reconcile it even on same-version installs.
		config_sysrunc_apparmor

		if [[ "$do_sysrunc_install" == "true" ]] ||
			[[ "$do_sysrunc_update" == "true" ]]; then
			install_sysrunc
			# Adjust the active container runtime to account for Sysrunc presence.
			if is_k3s_runtime; then
				config_k3s_containerd_for_sysrunc
			elif [[ "$do_crio_install" == "true" ]]; then
				config_crio_for_sysrunc
				crio_restart_pending=true
			elif is_containerd_with_userns; then
				config_containerd_for_sysrunc
			fi
			echo "yes" >${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed
			echo "$os_kernel_release" >${host_var_lib_sysrunc_deploy_k8s}/os_kernel_release
		fi

		if [[ "$crio_restart_pending" == "true" ]]; then
			restart_crio
		fi

		# Switch the K8s runtime to CRI-O (if necessary)
		#
		# Note: this will configure the Kubelet to use CRI-O and restart it,
		# thereby killing all pods on the K8s node (including this daemonset).
		# The K8s control plane will then re-create the pods, but this time
		# with CRI-O. The operation can take up to 1 minute.
		if [[ "$do_kubelet_use_crio" == "true" ]]; then
			if [[ "$k8s_runtime" != "crio" ]]; then
				echo "yes" >${host_var_lib_sysrunc_deploy_k8s}/kubelet_reconfigured
				deploy_kubelet_config_service
			fi

			# Kubelet config service cleanup
			if [ -f ${host_var_lib_sysrunc_deploy_k8s}/kubelet_reconfigured ]; then
				remove_kubelet_config_service
				rm -f ${host_var_lib_sysrunc_deploy_k8s}/kubelet_reconfigured
				echo "Kubelet reconfig completed."
			fi

			add_label_to_node "crio-runtime=running"
		fi

		# Remove all the sysrunc pods in the node to ensure that the newly installed/updated
		# sysrunc binaries are used. No action will be taken if this daemonset is being updated
		# with no changes to the sysrunc runtime or any of its dependencies.
		if [[ "$do_sysrunc_install" == "true" ]] ||
			[[ "$do_sysrunc_update" == "true" ]] ||
			[[ "$sysrunc_install_in_progress" == "true" ]]; then
			delete_sysrunc_pods
		fi

		add_label_to_node "sysrunc-runtime=running"
		if [[ "$legacy_fs_cordoned" == "true" ]]; then
			kubectl uncordon "$NODE_NAME"
			legacy_fs_cordoned="false"
		fi

		if [[ "$do_sysrunc_install" == "true" ]] || [[ "$sysrunc_install_in_progress" == "true" ]]; then
			if [[ "$do_kubelet_use_crio" == "true" ]]; then
				echo "The k8s runtime on this node is CRI-O + Sysrunc."
			else
				echo "The k8s runtime on this node is containerd + Sysrunc."
			fi
			echo "$sysrunc_edition installation completed (version $sysrunc_version)."

		elif [[ "$do_sysrunc_update" == "true" ]]; then
			echo "$sysrunc_edition update completed (version $sysrunc_version)."
		fi
		;;

	cleanup)
		mkdir -p ${host_var_lib_sysrunc_deploy_k8s}
		cleanup_sysrunc_admission_resources

		# Switch the K8s runtime away from CRI-O (but only if this daemonset installed CRI-O previously)
		if [ -f ${host_var_lib_sysrunc_deploy_k8s}/crio_installed ] && [[ "$k8s_runtime" == "crio" ]]; then
			add_label_to_node "crio-runtime=removing"

			# Note: this will restart kubelet with the prior runtime (not
			# CRI-O), thereby killing all pods (including this daemonset)
			echo "yes" >${host_var_lib_sysrunc_deploy_k8s}/kubelet_reconfigured
			deploy_kubelet_unconfig_service
		fi

		if [ -f ${host_var_lib_sysrunc_deploy_k8s}/kubelet_reconfigured ]; then
			remove_kubelet_unconfig_service
			rm -f ${host_var_lib_sysrunc_deploy_k8s}/kubelet_reconfigured
			echo "Kubelet reconfig completed."
		fi

		# Uninstall Sysrunc
		if [ -f ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed ]; then
			add_label_to_node "sysrunc-runtime=removing"
			if [ -f ${host_var_lib_sysrunc_deploy_k8s}/crio_installed ]; then
				unconfig_crio_for_sysrunc
				crio_restart_pending=true
			elif is_k3s_runtime; then
				unconfig_k3s_containerd_for_sysrunc
			else
				unconfig_containerd_for_sysrunc
			fi
			remove_sysrunc
			remove_sysrunc_deps
			rm -f ${host_var_lib_sysrunc_deploy_k8s}/sysrunc_installed
			rm -f ${host_var_lib_sysrunc_deploy_k8s}/os_kernel_release
			rm_label_from_node "sysrunc-runtime"
			echo "$sysrunc_edition removal completed."
		fi

		# Uninstall CRI-O
		if [ -f ${host_var_lib_sysrunc_deploy_k8s}/crio_installed ]; then
			deploy_crio_removal_service
			remove_crio_removal_service
			crio_restart_pending=false
			rm -f ${host_var_lib_sysrunc_deploy_k8s}/crio_installed
			rm_label_from_node "crio-runtime"
			echo "CRI-O removal completed."
		fi

		rm -rf ${host_var_lib_sysrunc_deploy_k8s}

		if [[ "$crio_restart_pending" == "true" ]]; then
			restart_crio
		fi

		# Remove all the sysrunc pods in the node to ensure that no sysrunc pods are
		# left behind in an inconsistent state.
		delete_sysrunc_pods
		;;

	*)
		echo invalid arguments
		print_usage
		;;
	esac

	# This script will be called as a daemonset. Do not return, otherwise the
	# daemonset will restart and rexecute the script
	echo "Done."

	sleep infinity
}

main "$@"
