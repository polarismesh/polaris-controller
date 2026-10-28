#!/usr/bin/env bash

set -euo pipefail

RELEASE_NAME="${1:-polaris-controller}"
RELEASE_NAMESPACE="${2:-}"
CONTROLLER_NAMESPACE="${CONTROLLER_NAMESPACE:-polaris-system}"
HELM_BIN="${HELM:-helm}"
KUBECTL_BIN="${KUBECTL:-kubectl}"

release_found=false
if [[ -n "${RELEASE_NAMESPACE}" ]]; then
  if "${HELM_BIN}" status "${RELEASE_NAME}" --namespace "${RELEASE_NAMESPACE}" >/dev/null 2>&1; then
    release_found=true
    "${HELM_BIN}" uninstall "${RELEASE_NAME}" --namespace "${RELEASE_NAMESPACE}" --wait
  fi
else
  if "${HELM_BIN}" status "${RELEASE_NAME}" >/dev/null 2>&1; then
    release_found=true
    "${HELM_BIN}" uninstall "${RELEASE_NAME}" --wait
  fi
fi

# Helm removes the resources it owns. The controller creates the sync-state
# ConfigMaps at runtime, so they have to be cleaned up separately.
if [[ "${release_found}" == "true" ]]; then
  if "${KUBECTL_BIN}" get namespace "${CONTROLLER_NAMESPACE}" >/dev/null 2>&1; then
    "${KUBECTL_BIN}" --namespace "${CONTROLLER_NAMESPACE}" delete configmap \
      --selector polarismesh.cn/sync-state=true --ignore-not-found=true --wait=true
  fi
else
  echo "warning: helm release ${RELEASE_NAME} not found, skip sync-state cleanup" >&2
fi

# Release-owned too, but an interrupted uninstall can leave these behind.
"${KUBECTL_BIN}" delete clusterrole,clusterrolebinding \
  --selector "app.kubernetes.io/instance=${RELEASE_NAME}" \
  --ignore-not-found=true --wait=true

echo "polaris-controller release ${RELEASE_NAME} uninstalled"
