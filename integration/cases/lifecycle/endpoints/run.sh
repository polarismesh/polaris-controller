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

endpoints_namespace_absent() {
  all_ns_absent "${NS_EP_LIFECYCLE}"
}

endpoint_keys_match_instances() {
  local endpoint_keys instance_keys
  endpoint_keys="$(kube -n "${NS_EP_LIFECYCLE}" get endpoints ep-demo -o json | "${JQ}" -r '
    [.subsets[]? as $subset |
      $subset.addresses[]? as $address |
      $subset.ports[]? as $port |
      "\($address.ip):\($port.port)"] |
    unique | sort | .[]
  ')"
  instance_keys="$(instances_json "${NS_EP_LIFECYCLE}" ep-demo | "${JQ}" -r '
    [.instances[] | "\(.host):\(.port)"] |
    unique | sort | .[]
  ')"
  [[ "${endpoint_keys}" == "${instance_keys}" ]]
}

ready_instances_healthy() {
  instances_json "${NS_EP_LIFECYCLE}" ep-demo | "${JQ}" -e '
    (.instances | length) > 0 and
    ([.instances[] | select(
      (.healthy // false) != true or
      (.isolate // false) != false
    )] | length) == 0
  ' >/dev/null
}

endpoints_have_no_ready_addresses() {
  local endpoints
  if ! endpoints="$(kube -n "${NS_EP_LIFECYCLE}" get endpoints ep-demo \
    --ignore-not-found -o json 2>/dev/null)"; then
    return 1
  fi
  [[ -n "${endpoints}" ]] || return 0
  printf '%s\n' "${endpoints}" | "${JQ}" -e \
    '([.subsets[]?.addresses[]?] | length) == 0' >/dev/null
}

if [[ "${RESET_CASES:-0}" == "1" ]]; then
  log "resetting endpoints lifecycle case"
  cleanup_case_namespaces
  wait_for "endpoints lifecycle case cleanup" "${WAIT_TIMEOUT}" endpoints_namespace_absent
fi

if [[ "${CLEANUP:-0}" == "1" ]]; then
  trap cleanup_case_namespaces EXIT
fi

log "CASE lifecycle/endpoints-address-health-and-ip-port-mapping"
kube apply -f "${SCRIPT_DIR}/manifests.yaml"
wait_rollout deployment "${NS_EP_LIFECYCLE}" ep-demo
wait_service "${NS_EP_LIFECYCLE}" ep-demo
wait_instance_count "${NS_EP_LIFECYCLE}" ep-demo 1
wait_for "Endpoints and Polaris instance IP:port mapping" "${RESYNC_TIMEOUT}" endpoint_keys_match_instances
wait_for "ready Polaris instances are healthy" "${RESYNC_TIMEOUT}" ready_instances_healthy

log "CASE lifecycle/endpoints-scale-up"
kube -n "${NS_EP_LIFECYCLE}" scale deployment ep-demo --replicas=2
wait_rollout deployment "${NS_EP_LIFECYCLE}" ep-demo
wait_instance_count "${NS_EP_LIFECYCLE}" ep-demo 2
wait_for "scale-up Endpoints and Polaris instance mapping" "${RESYNC_TIMEOUT}" endpoint_keys_match_instances
wait_for "scale-up ready Polaris instances are healthy" "${RESYNC_TIMEOUT}" ready_instances_healthy

log "CASE lifecycle/endpoints-scale-down"
kube -n "${NS_EP_LIFECYCLE}" scale deployment ep-demo --replicas=1
wait_rollout deployment "${NS_EP_LIFECYCLE}" ep-demo
wait_instance_count "${NS_EP_LIFECYCLE}" ep-demo 1
wait_for "scale-down Endpoints and Polaris instance mapping" "${RESYNC_TIMEOUT}" endpoint_keys_match_instances

log "CASE lifecycle/endpoints-remove-all-addresses"
kube -n "${NS_EP_LIFECYCLE}" scale deployment ep-demo --replicas=0
wait_rollout deployment "${NS_EP_LIFECYCLE}" ep-demo
wait_instance_count "${NS_EP_LIFECYCLE}" ep-demo 0
wait_for "empty Endpoints ready addresses" "${WAIT_TIMEOUT}" endpoints_have_no_ready_addresses
service_exists "${NS_EP_LIFECYCLE}" ep-demo || \
  fail "Polaris Service ${NS_EP_LIFECYCLE}/ep-demo was deleted after Endpoints removal"

log "endpoints lifecycle assertions passed"
