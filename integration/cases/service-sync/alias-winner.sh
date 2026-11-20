#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making Polaris available.
#
# Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
#
# Licensed under the BSD 3-Clause License (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# https://opensource.org/licenses/BSD-3-Clause
#
# Unless required by applicable law or agreed to in writing, software distributed
# under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
# CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

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
wait_instance_count "${NS_SYNC}" "${service_a}" 0
if service_exists "${NS_SYNC}" "${service_a}"; then
  log "Polaris Service ${NS_SYNC}/${service_a} retained with zero instances"
else
  log "Polaris Service ${NS_SYNC}/${service_a} is not present"
fi
wait_for "alias handover to second Service" "${RESYNC_TIMEOUT}" assert_alias_target \
  "${NS_SYNC}" "${alias_name}" "${NS_SYNC}" "${service_b}"

log "alias winner assertion passed; alias ${NS_SYNC}/${alias_name} remains intentionally"
