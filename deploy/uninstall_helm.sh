#!/usr/bin/env bash

set -euo pipefail

RELEASE_NAME="${1:-polaris-controller}"
RELEASE_NAMESPACE="${2:-}"
CONTROLLER_NAMESPACE="${CONTROLLER_NAMESPACE:-polaris-system}"
HELM_BIN="${HELM:-helm}"
KUBECTL_BIN="${KUBECTL:-kubectl}"
JQ_BIN="${JQ:-jq}"

if ! command -v "${JQ_BIN}" >/dev/null; then
  echo "error: jq is required to verify release ownership; no cleanup performed" >&2
  exit 1
fi
status_args=("${RELEASE_NAME}")
if [[ -n "${RELEASE_NAMESPACE}" ]]; then
  status_args+=(--namespace "${RELEASE_NAMESPACE}")
fi
if ! release_json="$("${HELM_BIN}" status "${status_args[@]}" --output json)"; then
  echo "error: cannot confirm helm release ${RELEASE_NAME}; no cleanup performed" >&2
  exit 1
fi
if ! confirmed_namespace="$(printf '%s\n' "${release_json}" | "${JQ_BIN}" -er \
  --arg release "${RELEASE_NAME}" --arg namespace "${RELEASE_NAMESPACE}" \
  'select(.name == $release and (.namespace | type) == "string" and (.namespace | length) > 0
    and ($namespace == "" or .namespace == $namespace)
    and (.version | type) == "number" and .version > 0) | .namespace')"; then
  echo "error: release name, namespace, or revision mismatch; no cleanup performed" >&2
  exit 1
fi
# Helm status omits chart metadata; list exposes the chart name and version.
if ! release_list_json="$("${HELM_BIN}" list --all --namespace "${confirmed_namespace}" \
  --filter "^${RELEASE_NAME}$" --output json)"; then
  echo "error: cannot verify release chart; no cleanup performed" >&2
  exit 1
fi
if ! printf '%s\n' "${release_list_json}" | "${JQ_BIN}" -e \
  --arg release "${RELEASE_NAME}" --arg namespace "${confirmed_namespace}" --argjson status "${release_json}" \
  'map(select(.name == $release and .namespace == $namespace
    and .revision == ($status.version | tostring) and .status == $status.info.status
    and (.chart | test("^polaris-controller-[0-9]+\\.[0-9]+\\.[0-9]+([+-].*)?$")))) | length == 1' >/dev/null; then
  echo "error: release chart or revision could not be confirmed; no cleanup performed" >&2
  exit 1
fi

"${HELM_BIN}" uninstall "${RELEASE_NAME}" --namespace "${confirmed_namespace}" --wait

# Helm removes the resources it owns. The controller creates the sync-state
# ConfigMaps at runtime, so they have to be cleaned up separately.
if "${KUBECTL_BIN}" get namespace "${CONTROLLER_NAMESPACE}" >/dev/null 2>&1; then
  "${KUBECTL_BIN}" --namespace "${CONTROLLER_NAMESPACE}" delete configmap \
    --selector polarismesh.cn/sync-state=true,app.kubernetes.io/managed-by=polaris-controller \
    --ignore-not-found=true --wait=true
fi

# Only explicitly owned residual RBAC may be removed after uninstall succeeds.
rbac_json="$("${KUBECTL_BIN}" get clusterrole,clusterrolebinding \
  --selector "app.kubernetes.io/instance=${RELEASE_NAME},qcloud-app=polaris-controller,app.kubernetes.io/managed-by=Helm" \
  --output json)"
owned_rbac="$(printf '%s\n' "${rbac_json}" | "${JQ_BIN}" -r \
  --arg release "${RELEASE_NAME}" --arg namespace "${confirmed_namespace}" \
  '.items[] | select((.kind == "ClusterRole" or .kind == "ClusterRoleBinding")
    and .metadata.annotations["meta.helm.sh/release-name"] == $release
    and .metadata.annotations["meta.helm.sh/release-namespace"] == $namespace
    and .metadata.labels["app.kubernetes.io/instance"] == $release
    and .metadata.labels["qcloud-app"] == "polaris-controller"
    and .metadata.labels["app.kubernetes.io/managed-by"] == "Helm")
    | [.kind, .metadata.name] | @tsv')"
while IFS=$'\t' read -r kind name; do
  if [[ -n "${kind}" && -n "${name}" ]]; then
    "${KUBECTL_BIN}" delete "${kind}" --ignore-not-found=true --wait=true -- "${name}"
  fi
done <<< "${owned_rbac}"

echo "polaris-controller release ${RELEASE_NAME} in ${confirmed_namespace} uninstalled"
