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
source "${SCRIPT_DIR}/../../lib.sh"

polaris_init

external_host="192.0.2.10"
external_port="$((19000 + ($$ % 1000)))"
foreign_controller_host="192.0.2.11"
foreign_controller_port="$((external_port + 1))"

service_namespaces_absent() {
  all_ns_absent "${NS_SVC_LIFECYCLE}" "${NS_SVC_ALIAS}"
}

cleanup_service_lifecycle() {
  delete_external_instance "${NS_SVC_LIFECYCLE}" svc-delete "${external_host}" "${external_port}" || true
  delete_external_instance "${NS_SVC_LIFECYCLE}" svc-delete \
    "${foreign_controller_host}" "${foreign_controller_port}" || true
  if [[ "${CLEANUP:-0}" == "1" ]]; then
    cleanup_case_namespaces
  fi
}

if [[ "${RESET_CASES:-0}" == "1" ]]; then
  log "resetting service lifecycle case"
  cleanup_case_namespaces
  wait_for "service lifecycle case cleanup" "${WAIT_TIMEOUT}" service_namespaces_absent
fi

trap cleanup_service_lifecycle EXIT

log "CASE lifecycle/service-types-and-cross-namespace-alias"
kube apply -f "${SCRIPT_DIR}/manifests.yaml"
wait_rollout deployment "${NS_SVC_LIFECYCLE}" svc-demo
wait_namespace "${NS_SVC_LIFECYCLE}"
wait_namespace "${NS_SVC_ALIAS}"

for service in svc-clusterip svc-nodeport svc-loadbalancer svc-cross-ns-alias svc-delete; do
  wait_service "${NS_SVC_LIFECYCLE}" "${service}"
  wait_instance_count "${NS_SVC_LIFECYCLE}" "${service}" 1
done

wait_for "cross-namespace alias target" "${RESYNC_TIMEOUT}" assert_alias_target \
  "${NS_SVC_ALIAS}" cross-ns-alias "${NS_SVC_LIFECYCLE}" svc-cross-ns-alias

log "CASE lifecycle/service-delete-retains-service-removes-owned-instances"
current_cluster="$(controller_cluster_name)"
[[ -n "${current_cluster}" ]] || fail "cannot determine current controller clusterName"

register_external_instance "${NS_SVC_LIFECYCLE}" svc-delete "${external_host}" "${external_port}"
register_external_instance "${NS_SVC_LIFECYCLE}" svc-delete \
  "${foreign_controller_host}" "${foreign_controller_port}" \
  polaris-controller integration-other-cluster foreign-cluster-service-uid
wait_instance_count "${NS_SVC_LIFECYCLE}" svc-delete 3

kube -n "${NS_SVC_LIFECYCLE}" delete service svc-delete --wait=true
service_exists "${NS_SVC_LIFECYCLE}" svc-delete || \
  fail "Polaris Service ${NS_SVC_LIFECYCLE}/svc-delete was deleted with the Kubernetes Service"
wait_instance_count "${NS_SVC_LIFECYCLE}" svc-delete 2
wait_controller_instance_count "${NS_SVC_LIFECYCLE}" svc-delete "${current_cluster}" 0
wait_instance_key "${NS_SVC_LIFECYCLE}" svc-delete "${external_host}" "${external_port}"
wait_instance_key "${NS_SVC_LIFECYCLE}" svc-delete \
  "${foreign_controller_host}" "${foreign_controller_port}"

instances_json "${NS_SVC_LIFECYCLE}" svc-delete | "${JQ}" -e \
  --arg sdk_host "${external_host}" --argjson sdk_port "${external_port}" \
  --arg other_host "${foreign_controller_host}" --argjson other_port "${foreign_controller_port}" '
    ([.instances[] | select(
      .host == $sdk_host and
      .port == $sdk_port and
      (.metadata.source // "") == "sdk" and
      (.metadata.clusterName // "") == "integration-external"
    )] | length) == 1 and
    ([.instances[] | select(
      .host == $other_host and
      .port == $other_port and
      (.metadata.source // "") == "polaris-controller" and
      (.metadata.clusterName // "") == "integration-other-cluster"
    )] | length) == 1
  ' >/dev/null

delete_external_instance "${NS_SVC_LIFECYCLE}" svc-delete "${external_host}" "${external_port}"
delete_external_instance "${NS_SVC_LIFECYCLE}" svc-delete \
  "${foreign_controller_host}" "${foreign_controller_port}"

log "service lifecycle assertions passed"
