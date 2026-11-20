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

KUBECTL="${KUBECTL:-kubectl}"
CURL="${CURL:-curl}"
JQ="${JQ:-jq}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-120}"
RESYNC_TIMEOUT="${RESYNC_TIMEOUT:-180}"
EXTERNAL_DELETE_TIMEOUT="${EXTERNAL_DELETE_TIMEOUT:-30}"

NS_SYNC="${NS_SYNC:-it-pc-sync}"
NS_SYNC_LEGACY="${NS_SYNC_LEGACY:-it-pc-sync-legacy}"
NS_SYNC_ALL="${NS_SYNC_ALL:-it-pc-sync-all}"
NS_INJECT="${NS_INJECT:-it-pc-inject-default}"
NS_INJECT_POD_LABEL="${NS_INJECT_POD_LABEL:-it-pc-inject-podlabel}"
NS_INJECT_JAVA="${NS_INJECT_JAVA:-it-pc-inject-java}"
NS_INJECT_LEGACY="${NS_INJECT_LEGACY:-it-pc-inject-legacy}"
NS_INJECT_NO_MODE="${NS_INJECT_NO_MODE:-it-pc-inject-no-mode}"
NS_COMBO="${NS_COMBO:-it-pc-combo}"
NS_NS_LIFECYCLE="${NS_NS_LIFECYCLE:-it-pc-ns-lifecycle}"
NS_SVC_LIFECYCLE="${NS_SVC_LIFECYCLE:-it-pc-svc-lifecycle}"
NS_SVC_ALIAS="${NS_SVC_ALIAS:-it-pc-svc-alias}"
NS_EP_LIFECYCLE="${NS_EP_LIFECYCLE:-it-pc-ep-lifecycle}"

log() {
  printf '[case] %s\n' "$*"
}

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

require_bin() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

kube() {
  "${KUBECTL}" "$@"
}

all_ns_absent() {
  local ns
  for ns in "$@"; do
    if kube get namespace "${ns}" >/dev/null 2>&1; then
      return 1
    fi
  done
  return 0
}

wait_for() {
  local description="$1"
  local timeout="$2"
  shift 2
  local deadline=$((SECONDS + timeout))
  while ! "$@"; do
    if ((SECONDS >= deadline)); then
      fail "timeout after ${timeout}s: ${description}"
    fi
    sleep 2
  done
}

wait_rollout() {
  local kind="$1"
  local namespace="$2"
  local name="$3"
  kube -n "${namespace}" rollout status "${kind}/${name}" --timeout="${WAIT_TIMEOUT}s"
}

wait_pod_exists() {
  local namespace="$1"
  local name="$2"
  kube -n "${namespace}" get pod "${name}" >/dev/null 2>&1
}

urlencode() {
  "${JQ}" -rn --arg value "$1" '$value | @uri'
}

polaris_init() {
  require_bin "${KUBECTL}"
  require_bin "${CURL}"
  require_bin "${JQ}"

  local mesh host
  mesh="$(kube -n polaris-system get configmap injector-mesh -o jsonpath='{.data.mesh}')"
  host="$(printf '%s\n' "${mesh}" | awk '$1 == "serverAddress:" {print $2; exit}')"

  if [[ -z "${POLARIS_HTTP_URL:-}" ]]; then
    [[ -n "${host}" ]] || fail "cannot determine Polaris serverAddress from injector-mesh"
    POLARIS_HTTP_URL="http://${host}:8090"
  fi

  if [[ -z "${POLARIS_TOKEN+x}" ]]; then
    POLARIS_TOKEN="$(printf '%s\n' "${mesh}" | awk '
      $1 == "accessToken:" {
        value = $2
        gsub(/^"/, "", value)
        gsub(/"$/, "", value)
        print value
        exit
      }
    ')"
  fi

  export POLARIS_HTTP_URL POLARIS_TOKEN
}

controller_cluster_name() {
  kube -n polaris-system get configmap injector-mesh -o jsonpath='{.data.mesh}' | awk '
    $1 == "clusterName:" {
      value = $2
      gsub(/^"/, "", value)
      gsub(/"$/, "", value)
      print value
      exit
    }
  '
}

polaris_get() {
  local path="$1"
  local args=(-fsS --max-time 15 -H 'Content-Type: application/json')
  if [[ -n "${POLARIS_TOKEN:-}" ]]; then
    args+=(-H "x-polaris-token: ${POLARIS_TOKEN}")
  fi
  "${CURL}" "${args[@]}" "${POLARIS_HTTP_URL%/}${path}"
}

polaris_post() {
  local path="$1"
  local body="$2"
  local args=(-sS --max-time 15 -X POST -H 'Content-Type: application/json')
  if [[ -n "${POLARIS_TOKEN:-}" ]]; then
    args+=(-H "x-polaris-token: ${POLARIS_TOKEN}")
  fi
  "${CURL}" "${args[@]}" --data-binary "${body}" "${POLARIS_HTTP_URL%/}${path}"
}

namespace_json() {
  local name="$1"
  polaris_get "/naming/v1/namespaces?name=$(urlencode "${name}")&offset=0&limit=100"
}

service_json() {
  local namespace="$1"
  local name="$2"
  polaris_get "/naming/v1/services?namespace=$(urlencode "${namespace}")&name=$(urlencode "${name}")&offset=0&limit=100"
}

instances_json() {
  local namespace="$1"
  local name="$2"
  polaris_get "/naming/v1/instances?namespace=$(urlencode "${namespace}")&service=$(urlencode "${name}")&offset=0&limit=100"
}

alias_json() {
  local namespace="$1"
  local alias="$2"
  polaris_get "/naming/v1/service/aliases?alias_namespace=$(urlencode "${namespace}")&alias=$(urlencode "${alias}")&offset=0&limit=10"
}

namespace_exists() {
  local name="$1"
  namespace_json "${name}" | "${JQ}" -e \
    --arg name "${name}" \
    '.code == 200000 and ([.namespaces[]? | select(.name == $name)] | length) == 1' \
    >/dev/null
}

wait_namespace() {
  local name="$1"
  wait_for "Polaris Namespace ${name}" "${RESYNC_TIMEOUT}" namespace_exists "${name}"
}

service_exists() {
  local namespace="$1"
  local name="$2"
  service_json "${namespace}" "${name}" | "${JQ}" -e \
    --arg ns "${namespace}" --arg name "${name}" \
    '.code == 200000 and ([.services[] | select(.namespace == $ns and .name == $name)] | length) == 1' \
    >/dev/null
}

service_absent() {
  local namespace="$1"
  local name="$2"
  service_json "${namespace}" "${name}" | "${JQ}" -e \
    --arg ns "${namespace}" --arg name "${name}" \
    '([.services[] | select(.namespace == $ns and .name == $name)] | length) == 0' \
    >/dev/null
}

wait_service() {
  local namespace="$1"
  local name="$2"
  wait_for "Polaris Service ${namespace}/${name}" "${RESYNC_TIMEOUT}" service_exists "${namespace}" "${name}"
}

wait_service_absent() {
  local namespace="$1"
  local name="$2"
  wait_for "Polaris Service ${namespace}/${name} removal" "${RESYNC_TIMEOUT}" service_absent "${namespace}" "${name}"
}

instance_count() {
  local namespace="$1"
  local name="$2"
  instances_json "${namespace}" "${name}" | "${JQ}" -r '.amount // (.instances | length)'
}

instance_count_is() {
  local namespace="$1"
  local name="$2"
  local expected="$3"
  [[ "$(instance_count "${namespace}" "${name}")" == "${expected}" ]]
}

wait_instance_count() {
  local namespace="$1"
  local name="$2"
  local expected="$3"
  wait_for "Polaris instances ${namespace}/${name} count=${expected}" "${RESYNC_TIMEOUT}" \
    instance_count_is "${namespace}" "${name}" "${expected}"
}

instance_key_exists() {
  local namespace="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  instances_json "${namespace}" "${name}" | "${JQ}" -e \
    --arg host "${host}" --argjson port "${port}" \
    '[.instances[] | select(.host == $host and .port == $port)] | length == 1' \
    >/dev/null
}

wait_instance_key() {
  local namespace="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  wait_for "Polaris instance ${namespace}/${name} ${host}:${port}" "${RESYNC_TIMEOUT}" \
    instance_key_exists "${namespace}" "${name}" "${host}" "${port}"
}

instance_key_absent() {
  local namespace="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  instances_json "${namespace}" "${name}" | "${JQ}" -e \
    --arg host "${host}" --argjson port "${port}" \
    '[.instances[] | select(.host == $host and .port == $port)] | length == 0' \
    >/dev/null
}

wait_instance_key_absent() {
  local namespace="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  local timeout="${5:-${RESYNC_TIMEOUT}}"
  wait_for "Polaris instance removal ${namespace}/${name} ${host}:${port}" "${timeout}" \
    instance_key_absent "${namespace}" "${name}" "${host}" "${port}"
}

controller_instance_count() {
  local namespace="$1"
  local name="$2"
  local cluster="$3"
  instances_json "${namespace}" "${name}" | "${JQ}" -r \
    --arg cluster "${cluster}" \
    '[.instances[] | select(
      (.metadata.source // "") == "polaris-controller" and
      (.metadata.clusterName // "") == $cluster
    )] | length'
}

controller_instance_count_is() {
  local namespace="$1"
  local name="$2"
  local cluster="$3"
  local expected="$4"
  [[ "$(controller_instance_count "${namespace}" "${name}" "${cluster}")" == "${expected}" ]]
}

wait_controller_instance_count() {
  local namespace="$1"
  local name="$2"
  local cluster="$3"
  local expected="$4"
  wait_for "controller-owned instances ${namespace}/${name} cluster=${cluster} count=${expected}" \
    "${RESYNC_TIMEOUT}" \
    controller_instance_count_is "${namespace}" "${name}" "${cluster}" "${expected}"
}

service_token() {
  local namespace="$1"
  local name="$2"
  service_json "${namespace}" "${name}" | "${JQ}" -r \
    --arg ns "${namespace}" --arg name "${name}" \
    '[.services[] | select(.namespace == $ns and .name == $name)][0].token // empty'
}

register_external_instance() {
  local namespace="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  local source="${5:-sdk}"
  local cluster_name="${6:-integration-external}"
  local uid="${7:-}"
  local token body
  token="$(service_token "${namespace}" "${name}")"
  body="$("${JQ}" -cn \
    --arg service "${name}" \
    --arg namespace "${namespace}" \
    --arg token "${token}" \
    --arg host "${host}" \
    --argjson port "${port}" \
    --arg source "${source}" \
    --arg cluster_name "${cluster_name}" \
    --arg uid "${uid}" \
    '[{
      service: $service,
      namespace: $namespace,
      service_token: $token,
      host: $host,
      port: $port,
      protocol: "HTTP",
      version: "external-v1",
      weight: 100,
      healthy: true,
      isolate: false,
      metadata: ({source: $source, clusterName: $cluster_name, owner: "integration"} +
        (if $uid == "" then {} else {k8sServiceUID: $uid} end))
    }]')"
  polaris_post "/naming/v1/instances" "${body}" | "${JQ}" -e \
    '.code == 200000 or .code == 400201 or .code == 400208' >/dev/null
  wait_instance_key "${namespace}" "${name}" "${host}" "${port}"
}

delete_external_instance() {
  local namespace="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  local token body
  token="$(service_token "${namespace}" "${name}")"
  body="$("${JQ}" -cn \
    --arg service "${name}" \
    --arg namespace "${namespace}" \
    --arg token "${token}" \
    --arg host "${host}" \
    --argjson port "${port}" \
    '[{
      service: $service,
      namespace: $namespace,
      service_token: $token,
      host: $host,
      port: $port
    }]')"
  polaris_post "/naming/v1/instances/delete" "${body}" >/dev/null || true
  if service_exists "${namespace}" "${name}"; then
    wait_instance_key_absent "${namespace}" "${name}" "${host}" "${port}" "${EXTERNAL_DELETE_TIMEOUT}"
  fi
}

assert_service_metadata() {
  local namespace="$1"
  local name="$2"
  local key="$3"
  local expected="$4"
  service_json "${namespace}" "${name}" | "${JQ}" -e \
    --arg ns "${namespace}" --arg name "${name}" --arg key "${key}" --arg expected "${expected}" \
    '([.services[] | select(.namespace == $ns and .name == $name)][0].metadata[$key] // "") == $expected' \
    >/dev/null
}

assert_service_metadata_absent() {
  local namespace="$1"
  local name="$2"
  local key="$3"
  service_json "${namespace}" "${name}" | "${JQ}" -e \
    --arg ns "${namespace}" --arg name "${name}" --arg key "${key}" \
    '([.services[] | select(.namespace == $ns and .name == $name)][0].metadata | has($key)) == false' \
    >/dev/null
}

assert_alias_target() {
  local namespace="$1"
  local alias="$2"
  local target_namespace="$3"
  local target_service="$4"
  alias_json "${namespace}" "${alias}" | "${JQ}" -e \
    --arg ns "${namespace}" --arg alias "${alias}" \
    --arg target_ns "${target_namespace}" --arg target "${target_service}" \
    '.code == 200000 and ([.aliases[] | select(
      .alias_namespace == $ns and .alias == $alias and
      .namespace == $target_ns and .service == $target)] | length) == 1' \
    >/dev/null
}

cleanup_case_namespaces() {
  kube delete namespace \
    "${NS_SYNC}" \
    "${NS_SYNC_LEGACY}" \
    "${NS_SYNC_ALL}" \
    "${NS_INJECT}" \
    "${NS_INJECT_POD_LABEL}" \
    "${NS_INJECT_JAVA}" \
    "${NS_INJECT_LEGACY}" \
    "${NS_INJECT_NO_MODE}" \
    "${NS_COMBO}" \
    "${NS_NS_LIFECYCLE}" \
    "${NS_SVC_LIFECYCLE}" \
    "${NS_SVC_ALIAS}" \
    "${NS_EP_LIFECYCLE}" \
    --ignore-not-found=true --wait=false
}
