#!/bin/sh
set -eu

case "${SYSRUNC_INSTALL_MODE:-}:${1:-}" in
  host:install)
    exec /opt/sysrunc/scripts/sysrunc-deploy-k8s.sh ce install \
      "--snapshotter-enabled=${SYSRUNC_SNAPSHOTTER_ENABLED:-false}"
    ;;
  host:cleanup)
    exec /opt/sysrunc/scripts/sysrunc-deploy-k8s.sh ce cleanup
    ;;
  nested:install|nested:status|nested:uninstall)
    exec /opt/sysrunc/scripts/sysrunc-inner-agent.sh "$1"
    ;;
  *)
    echo "sysrunc-installer: unsupported mode or action" >&2
    exit 2
    ;;
esac
