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

# Run the integration suite and preserve the complete console output.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${SCRIPT_DIR}/cases/run-all.sh" ]]; then
  BUNDLE_ROOT="${SCRIPT_DIR}"
  CASES_DIR="${SCRIPT_DIR}/cases"
elif [[ -f "${SCRIPT_DIR}/integration/cases/run-all.sh" ]]; then
  BUNDLE_ROOT="${SCRIPT_DIR}"
  CASES_DIR="${SCRIPT_DIR}/integration/cases"
else
  printf '[FAIL] cannot locate integration/cases/run-all.sh\n' >&2
  exit 1
fi

LOG_DIR="${LOG_DIR:-${BUNDLE_ROOT}/.logs}"
RUN_ARGS=()

usage() {
  cat <<'EOF'
Usage: ./run.sh [run-all options]

Options handled by this wrapper:
  --log-dir DIR   Write logs under DIR (default: ./.logs)
  -h, --help      Show this help

All other options are passed to integration/cases/run-all.sh.
EOF
}

while (($# > 0)); do
  case "$1" in
    --log-dir)
      (($# >= 2)) || {
        printf '[FAIL] --log-dir requires a directory\n' >&2
        exit 1
      }
      LOG_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      RUN_ARGS+=("$1")
      shift
      ;;
  esac
done

command -v tee >/dev/null 2>&1 || {
  printf '[FAIL] required command not found: tee\n' >&2
  exit 1
}

run_id="$(date '+%Y%m%d-%H%M%S')-$$"
run_dir="${LOG_DIR}/run-${run_id}"
run_log="${run_dir}/run.log"
mkdir -p "${run_dir}"

{
  printf 'started_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
  printf 'host=%s\n' "$(uname -n)"
  printf 'working_dir=%s\n' "$PWD"
  printf 'run_args=%s\n' "${RUN_ARGS[*]:-}"
} > "${run_dir}/metadata.txt"

if [[ -f "${BUNDLE_ROOT}/PACKAGE-INFO.txt" ]]; then
  cp -p "${BUNDLE_ROOT}/PACKAGE-INFO.txt" "${run_dir}/PACKAGE-INFO.txt"
fi

{
  printf '===== Polaris Controller integration run =====\n'
  printf 'run_id=%s\n' "${run_id}"
  printf 'started_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
  printf 'command=%s\n\n' "${CASES_DIR}/run-all.sh ${RUN_ARGS[*]:-}"
} > "${run_log}"

set +e
if ((${#RUN_ARGS[@]} > 0)); then
  "${CASES_DIR}/run-all.sh" "${RUN_ARGS[@]}" 2>&1 | tee -a "${run_log}"
else
  "${CASES_DIR}/run-all.sh" 2>&1 | tee -a "${run_log}"
fi
rc=${PIPESTATUS[0]}
set -e

{
  printf '\n===== run finished =====\n'
  printf 'finished_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
  printf 'exit_code=%s\n' "${rc}"
} >> "${run_log}"
printf 'exit_code=%s\n' "${rc}" > "${run_dir}/result.txt"

printf '\n[run] log directory: %s\n' "${run_dir}"
printf '[run] pack logs: %s/pack-logs.sh\n' "${BUNDLE_ROOT}"
printf '[run] cleanup resources: %s/cleanup.sh\n' "${BUNDLE_ROOT}"
exit "${rc}"
