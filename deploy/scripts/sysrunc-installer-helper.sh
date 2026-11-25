#!/bin/bash

#
# Copyright 2019-2021 Nestybox, Inc.
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
# Helper script to install Sysrunc userspace dependencies on a Kubernetes host
#

set -o errexit
set -o pipefail
set -o nounset

function die() {
	msg="$*"
	echo "ERROR: $msg" >&2
	exit 1
}

function install_package_deps() {
	# Reinstalling the runtime does not require refreshing the host package index
	# when the userspace helpers are already present. This also keeps an offline
	# node upgrade from waiting indefinitely on an unavailable package mirror.
	if command -v rsync >/dev/null 2>&1 &&
		command -v fusermount3 >/dev/null 2>&1 &&
		command -v fuse-overlayfs >/dev/null 2>&1 &&
		command -v iptables >/dev/null 2>&1; then
		return
	fi

	if grep -q '^ID="\?centos"\?$' /etc/os-release; then
		dnf install -y rsync fuse3 fuse-overlayfs iptables
		return
	fi

	# Need this to work-around "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem."
	dpkg --configure -a

	# Certificates package is required prior to running apt-update.
	apt-get -y install ca-certificates
	apt-get update
	apt-get install -y rsync fuse3 fuse-overlayfs iptables
}

function flatcar_distro() {
	grep -q "^ID=flatcar" /etc/os-release
}

function check_procfs_mount_userns() {

	# Attempt to mount procfs from a user-namespace.
	if unshare -U -p -f --mount-proc -r cat /dev/null; then
		return 0
	fi

	# Find out if there's anything we can do to workaround this situation. In certain
	# scenarios (e.g. Flatcar >= 3033.2.4), a fake (bind-mounted) '/proc/cmdline' can
	# prevent procfs from being mounted within a user-namespace. In these cases we'll
	# attempt to unmount this resource and try again.
	if mount | egrep -q "cmdline" && umount /proc/cmdline; then
		if unshare -U -p -f --mount-proc -r cat /dev/null; then
			return 0
		fi
	fi

	return 1
}

function main() {

	euid=$(id -u)
	if [[ $euid -ne 0 ]]; then
		die "This script must be run as root"
	fi

	if ! check_procfs_mount_userns; then
		die "Sysrunc unmet requirement: node is unable to mount procfs from within unprivileged user-namespaces."
	fi

	# Flatcar carries the required userspace tools in the host image.
	if flatcar_distro; then
		modprobe configfs || true
		return
	fi

	install_package_deps
	modprobe configfs || true
}

main "$@"
