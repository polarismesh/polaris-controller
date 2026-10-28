#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KUBECTL_BIN="${KUBECTL:-kubectl}"

delete_manifest() {
  local manifest="$1"
  "${KUBECTL_BIN}" delete -f "${SCRIPT_DIR}/${manifest}" --ignore-not-found=true --wait=true
}

# Stop writers before removing cluster-scoped resources and the namespace.
delete_manifest polaris-controller.yaml
delete_manifest polaris-metrics-svc.yaml
delete_manifest javaagent-configmap.yaml
delete_manifest injector.yaml
delete_manifest configmap.yaml
delete_manifest polaris-client-config-tpl.yaml
delete_manifest rbac.yaml

# Keep this cleanup for installations that removed injector.yaml separately.
"${KUBECTL_BIN}" delete mutatingwebhookconfiguration polaris-sidecar-injector \
  --ignore-not-found=true --wait=true

# Remove controller recovery state before deleting the namespace.
"${KUBECTL_BIN}" -n polaris-system delete configmap \
  -l polarismesh.cn/sync-state=true --ignore-not-found=true --wait=true

# The namespace owns the remaining resources.
delete_manifest namespace.yaml
