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

NS_SYNC="${NS_SYNC:-it-pc-sync}"
NS_SYNC_LEGACY="${NS_SYNC_LEGACY:-it-pc-sync-legacy}"
NS_SYNC_ALL="${NS_SYNC_ALL:-it-pc-sync-all}"
NS_INJECT="${NS_INJECT:-it-pc-inject-default}"
NS_INJECT_POD_LABEL="${NS_INJECT_POD_LABEL:-it-pc-inject-podlabel}"
NS_INJECT_JAVA="${NS_INJECT_JAVA:-it-pc-inject-java}"
NS_INJECT_LEGACY="${NS_INJECT_LEGACY:-it-pc-inject-legacy}"
NS_COMBO="${NS_COMBO:-it-pc-combo}"

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

polaris_get() {
  local path="$1"
  local args=(-fsS --max-time 15 -H 'Content-Type: application/json')
  if [[ -n "${POLARIS_TOKEN:-}" ]]; then
    args+=(-H "x-polaris-token: ${POLARIS_TOKEN}")
  fi
  "${CURL}" "${args[@]}" "${POLARIS_HTTP_URL%/}${path}"
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
    "${NS_COMBO}" \
    --ignore-not-found=true --wait=false
}
