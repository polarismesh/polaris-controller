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

namespaces_absent() {
  all_ns_absent "${NS_SYNC}" "${NS_SYNC_LEGACY}" "${NS_SYNC_ALL}"
}

notready_pod_exists() {
  [[ -n "$(kube -n "${NS_SYNC}" get pod -l app=notready-demo -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)" ]]
}

notready_endpoint_exists() {
  kube -n "${NS_SYNC}" get endpoints notready-demo -o json 2>/dev/null | \
    "${JQ}" -e '(.subsets // []) | map(.notReadyAddresses // []) | flatten | length > 0' >/dev/null
}

pod_metadata_converged() {
  instances_json "${NS_SYNC}" metadata-demo | "${JQ}" -e '
    .instances[0].metadata.tenant == "pod-tenant-v2" and
    .instances[0].metadata.team == "pod-team-v2" and
    .instances[0].version == "v9" and
    .instances[0].location.region == "cn-south" and
    .instances[0].location.zone == "zone-b" and
    .instances[0].location.campus == "campus-2" and
    (.instances[0].metadata | has("version")) == false and
    (.instances[0].metadata | has("protocol")) == false and
    (.instances[0].metadata | has("region")) == false and
    (.instances[0].metadata | has("zone")) == false and
    (.instances[0].metadata | has("campus")) == false
  ' >/dev/null
}

if [[ "${RESET_CASES:-0}" == "1" ]]; then
  log "resetting case namespaces"
  cleanup_case_namespaces
  wait_for "namespace deletion" "${WAIT_TIMEOUT}" namespaces_absent
fi

if [[ "${CLEANUP:-0}" == "1" ]]; then
  trap cleanup_case_namespaces EXIT
fi

log "apply service-sync manifests"
kube apply -f "${SCRIPT_DIR}/manifests.yaml"

wait_rollout deployment "${NS_SYNC}" sync-shared
wait_rollout deployment "${NS_SYNC}" ttl-demo
wait_rollout statefulset "${NS_SYNC}" stateful-demo
wait_rollout deployment "${NS_SYNC_LEGACY}" enable-register-demo
wait_rollout deployment "${NS_SYNC_ALL}" all-mode-demo
wait_for "notready pod creation" "${WAIT_TIMEOUT}" notready_pod_exists

log "CASE modes-are-independent-service-only-has-no-sidecar"
sync_pod="$(kube -n "${NS_SYNC}" get pod -l app=sync-shared -o jsonpath='{.items[0].metadata.name}')"
kube -n "${NS_SYNC}" get pod "${sync_pod}" -o json | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 0 and
  ([.spec.initContainers[]? | select(.name | startswith("polaris-"))] | length) == 0
' >/dev/null

log "CASE sync/namespace-annotation-and-service-disable"
wait_service "${NS_SYNC}" namespace-only-demo
wait_service_absent "${NS_SYNC}" disabled-demo
wait_service_absent "${NS_SYNC}" sync-false-overrides-enable-register
wait_service "${NS_SYNC}" enable-register-false-demo

log "CASE sync/enableRegister"
wait_service "${NS_SYNC_LEGACY}" enable-register-demo

log "CASE sync/all-mode-service-is-not-synced-in-demand"
sync_mode="$(kube -n polaris-system get configmap injector-mesh -o jsonpath='{.data.mesh}' | awk '
  /^serviceSync:/ {inside=1; next}
  inside && /^  mode:/ {print $2; exit}
')"
if [[ "${sync_mode}" == "demand" ]]; then
  wait_instance_count "${NS_SYNC_ALL}" all-mode-demo 0
  if service_exists "${NS_SYNC_ALL}" all-mode-demo; then
    log "Polaris Service ${NS_SYNC_ALL}/all-mode-demo retained with zero instances"
  else
    log "Polaris Service ${NS_SYNC_ALL}/all-mode-demo is not present"
  fi
fi

log "CASE sync/service-and-instance-metadata"
wait_service "${NS_SYNC}" metadata-demo
wait_instance_count "${NS_SYNC}" metadata-demo 1
assert_service_metadata "${NS_SYNC}" metadata-demo owner payments
assert_service_metadata "${NS_SYNC}" metadata-demo tier backend
service_json "${NS_SYNC}" metadata-demo | "${JQ}" -e \
  '([.services[] | select(.namespace == "it-pc-sync" and .name == "metadata-demo")][0].metadata | has("app")) == false' \
  >/dev/null || fail "Service metadata must not contain Pod-only label app"

service_instances="$(instances_json "${NS_SYNC}" metadata-demo)"
printf '%s\n' "${service_instances}" | "${JQ}" -e '
  .code == 200000 and
  (.instances | length) == 1 and
  .instances[0].metadata.tenant == "pod-tenant" and
  .instances[0].metadata.team == "pod-team" and
  .instances[0].metadata.env == "service-env" and
  .instances[0].metadata.app == "sync-shared" and
  .instances[0].metadata["polarismesh.cn/customVersion"] == "v9" and
  .instances[0].metadata.source == "polaris-controller" and
  (.instances[0].metadata.clusterName | length) > 0 and
  .instances[0].location.region == "cn-north" and
  .instances[0].location.zone == "zone-a" and
  .instances[0].location.campus == "campus-1" and
  (.instances[0].metadata | has("version")) == false and
  (.instances[0].metadata | has("protocol")) == false and
  (.instances[0].metadata | has("region")) == false and
  (.instances[0].metadata | has("zone")) == false and
  (.instances[0].metadata | has("campus")) == false and
  .instances[0].version == "v9" and
  .instances[0].protocol == "http" and
  .instances[0].weight == 70
' >/dev/null

log "CASE sync/ttl"
wait_service "${NS_SYNC}" ttl-demo
wait_instance_count "${NS_SYNC}" ttl-demo 1
instances_json "${NS_SYNC}" ttl-demo | "${JQ}" -e '
  .instances[0].enableHealthCheck == true and
  .instances[0].healthCheck.heartbeat.ttl == 60 and
  .instances[0].protocol == "TCP" and
  (.instances[0].metadata | has("protocol")) == false
' >/dev/null

log "CASE sync/multi-port-protocol"
wait_service "${NS_SYNC}" multiport-demo
wait_instance_count "${NS_SYNC}" multiport-demo 2
instances_json "${NS_SYNC}" multiport-demo | "${JQ}" -e '
  ([.instances[].protocol] | sort) == ["grpc", "http"]
' >/dev/null

log "CASE sync/statefulset-custom-weight"
wait_service "${NS_SYNC}" stateful-demo
wait_instance_count "${NS_SYNC}" stateful-demo 2
instances_json "${NS_SYNC}" stateful-demo | "${JQ}" -e '
  ([.instances[].weight] | sort) == [20, 80]
' >/dev/null

log "CASE sync/not-ready-endpoint-isolated"
wait_service "${NS_SYNC}" notready-demo
wait_instance_count "${NS_SYNC}" notready-demo 1
wait_for "notready endpoint" "${WAIT_TIMEOUT}" notready_endpoint_exists
instances_json "${NS_SYNC}" notready-demo | "${JQ}" -e '
  .instances[0].isolate == true
' >/dev/null

log "CASE sync/alias"
wait_service "${NS_SYNC}" alias-winner-a
wait_for "alias target" "${RESYNC_TIMEOUT}" assert_alias_target \
  "${NS_SYNC}" shared-alias "${NS_SYNC}" alias-winner-a
wait_for "metadata alias target" "${RESYNC_TIMEOUT}" assert_alias_target \
  "${NS_SYNC}" metadata-demo-alias "${NS_SYNC}" metadata-demo

log "CASE sync/service-label-convergence"
kube -n "${NS_SYNC}" label service metadata-demo owner=payments-v2 --overwrite
kube -n "${NS_SYNC}" label service metadata-demo tier-
wait_for "Service label owner convergence" "${RESYNC_TIMEOUT}" \
  assert_service_metadata "${NS_SYNC}" metadata-demo owner payments-v2
wait_for "Service label removal convergence" "${RESYNC_TIMEOUT}" \
  assert_service_metadata_absent "${NS_SYNC}" metadata-demo tier

log "CASE sync/pod-label-convergence"
pod="$(kube -n "${NS_SYNC}" get pod -l app=sync-shared -o jsonpath='{.items[0].metadata.name}')"
kube -n "${NS_SYNC}" label pod "${pod}" tenant=pod-tenant-v2 team=pod-team-v2 version=v3 \
  region=cn-south zone=zone-b campus=campus-2 --overwrite
wait_for "Pod label metadata convergence" "${RESYNC_TIMEOUT}" pod_metadata_converged

log "CASE sync/namespace-disable-keeps-explicit-service"
kube annotate namespace "${NS_SYNC}" polarismesh.cn/sync=false --overwrite
wait_instance_count "${NS_SYNC}" namespace-only-demo 0
service_exists "${NS_SYNC}" namespace-only-demo || fail "Polaris Service namespace-only-demo was deleted"
wait_instance_count "${NS_SYNC}" multiport-demo 2
service_exists "${NS_SYNC}" multiport-demo || fail "Polaris Service multiport-demo was deleted"
kube annotate namespace "${NS_SYNC}" polarismesh.cn/sync=true --overwrite
wait_instance_count "${NS_SYNC}" namespace-only-demo 1

log "CASE sync/service-disable-cleans-polaris-instances"
kube annotate service metadata-demo -n "${NS_SYNC}" polarismesh.cn/sync=false --overwrite
wait_instance_count "${NS_SYNC}" metadata-demo 0
service_exists "${NS_SYNC}" metadata-demo || fail "Polaris Service metadata-demo was deleted"

log "all service-sync assertions passed"
log "alias shared-alias may remain in Polaris if the test namespace was reused; run cleanup in a disposable namespace or remove it manually"
