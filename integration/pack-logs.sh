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

# Package run logs and optional Kubernetes snapshots for transfer to a local host.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${SCRIPT_DIR}/cases/lib.sh" ]]; then
  BUNDLE_ROOT="${SCRIPT_DIR}"
  CASES_DIR="${SCRIPT_DIR}/cases"
elif [[ -f "${SCRIPT_DIR}/integration/cases/lib.sh" ]]; then
  BUNDLE_ROOT="${SCRIPT_DIR}"
  CASES_DIR="${SCRIPT_DIR}/integration/cases"
else
  printf '[FAIL] cannot locate integration/cases/lib.sh\n' >&2
  exit 1
fi

LOG_DIR="${LOG_DIR:-${BUNDLE_ROOT}/.logs}"
COLLECT_CLUSTER="${COLLECT_CLUSTER:-1}"
OUTPUT=""

usage() {
  cat <<'EOF'
Usage: ./pack-logs.sh [options]

Options:
  --log-dir DIR    Log directory (default: ./.logs)
  --output FILE    Archive path (default: ./polaris-controller-integration-logs-<timestamp>.zip)
  --no-cluster     Do not collect kubectl state and controller logs
  -h, --help       Show this help

Environment:
  LOG_DIR          Default log directory
  COLLECT_CLUSTER  1 to collect cluster snapshots (default), 0 to skip
EOF
}

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

while (($# > 0)); do
  case "$1" in
    --log-dir)
      (($# >= 2)) || fail "--log-dir requires a directory"
      LOG_DIR="$2"
      shift 2
      ;;
    --output)
      (($# >= 2)) || fail "--output requires a file"
      OUTPUT="$2"
      shift 2
      ;;
    --no-cluster)
      COLLECT_CLUSTER=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[[ -d "${LOG_DIR}" ]] || fail "log directory not found: ${LOG_DIR}"
[[ "${COLLECT_CLUSTER}" == "0" || "${COLLECT_CLUSTER}" == "1" ]] || \
  fail "COLLECT_CLUSTER must be 0 or 1"
log_dir="$(cd "${LOG_DIR}" && pwd)"
timestamp="$(date '+%Y%m%d-%H%M%S')"
if [[ -z "${OUTPUT}" ]]; then
  OUTPUT="${BUNDLE_ROOT}/polaris-controller-integration-logs-${timestamp}.zip"
fi
output_dir="$(dirname "${OUTPUT}")"
mkdir -p "${output_dir}"
output_dir="$(cd "${output_dir}" && pwd)"
OUTPUT="${output_dir}/$(basename "${OUTPUT}")"
if [[ "${OUTPUT}" == "${log_dir}"/* ]]; then
  fail "output must be outside the log directory"
fi
rm -f "${OUTPUT}"

if [[ "${COLLECT_CLUSTER}" == "1" ]]; then
  source "${CASES_DIR}/lib.sh"
  snapshot_dir="${log_dir}/cluster-${timestamp}"
  mkdir -p "${snapshot_dir}"
  {
    printf 'collected_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
    printf 'host=%s\n' "$(uname -n)"
    printf 'kubectl=%s\n' "${KUBECTL}"
  } > "${snapshot_dir}/summary.txt"

  if command -v "${KUBECTL}" >/dev/null 2>&1; then
    kube config current-context > "${snapshot_dir}/kube-context.txt" 2>&1 || true
    kube -n polaris-system logs statefulset/polaris-controller \
      --all-containers=true --tail=2000 \
      > "${snapshot_dir}/polaris-controller.log" 2>&1 || true

    while IFS= read -r namespace; do
      if ! kube get namespace "${namespace}" --request-timeout=10s >/dev/null 2>&1; then
        continue
      fi
      namespace_dir="${snapshot_dir}/namespaces/${namespace}"
      mkdir -p "${namespace_dir}"
      kube -n "${namespace}" get namespace "${namespace}" -o yaml \
        > "${namespace_dir}/namespace.yaml" 2>&1 || true
      kube -n "${namespace}" get all,endpoints -o wide \
        > "${namespace_dir}/resources.txt" 2>&1 || true
      kube -n "${namespace}" get events --sort-by=.lastTimestamp \
        > "${namespace_dir}/events.txt" 2>&1 || true
      kube -n "${namespace}" describe pods \
        > "${namespace_dir}/pods-describe.txt" 2>&1 || true
    done < <(case_namespace_names)
  else
    printf 'kubectl not found; cluster snapshot skipped\n' >> "${snapshot_dir}/summary.txt"
  fi
fi

if [[ -f "${BUNDLE_ROOT}/PACKAGE-INFO.txt" ]]; then
  cp -p "${BUNDLE_ROOT}/PACKAGE-INFO.txt" "${log_dir}/PACKAGE-INFO.txt"
fi

if command -v zip >/dev/null 2>&1 && command -v unzip >/dev/null 2>&1; then
  [[ "${OUTPUT}" == *.zip ]] || OUTPUT="${OUTPUT}.zip"
  (
    cd "${log_dir}"
    COPYFILE_DISABLE=1 zip -rq "${OUTPUT}" .
  )
  unzip -tq "${OUTPUT}" >/dev/null || fail "zip verification failed"
else
  if [[ "${OUTPUT}" == *.zip ]]; then
    OUTPUT="${OUTPUT%.zip}.tar.gz"
  elif [[ "${OUTPUT}" != *.tar.gz ]]; then
    OUTPUT="${OUTPUT}.tar.gz"
  fi
  tar -czf "${OUTPUT}" -C "${log_dir}" .
fi

size="$(du -h "${OUTPUT}" | awk '{print $1}')"
printf '[logs] archive: %s (%s)\n' "${OUTPUT}" "${size}"
printf '[logs] copy to local host, for example:\n'
printf '  scp <user>@<cloud-host>:%s .\n' "${OUTPUT}"
printf '[logs] review the archive before sharing; it may contain cluster metadata.\n'
