#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib.sh"

polaris_init

kube apply -f "${SCRIPT_DIR}/manifests.yaml" >/dev/null
wait_rollout deployment "${NS_SYNC}" sync-shared

suffix="$(date +%s)-$$"
service_a="alias-a-${suffix}"
service_b="alias-b-${suffix}"
alias_name="shared-alias-${suffix}"

cleanup_services() {
  kube -n "${NS_SYNC}" delete service "${service_a}" "${service_b}" \
    --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
}
trap cleanup_services EXIT

apply_alias_service() {
  local name="$1"
  local port="$2"
  kube apply -f - <<YAML
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  namespace: ${NS_SYNC}
  annotations:
    polarismesh.cn/sync: "true"
    polarismesh.cn/aliasNamespace: ${NS_SYNC}
    polarismesh.cn/aliasService: ${alias_name}
spec:
  selector:
    app: sync-shared
  ports:
    - name: http
      port: ${port}
      targetPort: 8080
YAML
}

log "CASE sync/alias-stable-winner-and-handover"
apply_alias_service "${service_a}" 18084
wait_service "${NS_SYNC}" "${service_a}"
wait_for "initial alias winner" "${RESYNC_TIMEOUT}" assert_alias_target \
  "${NS_SYNC}" "${alias_name}" "${NS_SYNC}" "${service_a}"

sleep 2
apply_alias_service "${service_b}" 18085
wait_service "${NS_SYNC}" "${service_b}"
assert_alias_target "${NS_SYNC}" "${alias_name}" "${NS_SYNC}" "${service_a}"

kube -n "${NS_SYNC}" delete service "${service_a}" --wait=true
wait_service_absent "${NS_SYNC}" "${service_a}"
wait_for "alias handover to second Service" "${RESYNC_TIMEOUT}" assert_alias_target \
  "${NS_SYNC}" "${alias_name}" "${NS_SYNC}" "${service_b}"

log "alias winner assertion passed; alias ${NS_SYNC}/${alias_name} remains intentionally"
