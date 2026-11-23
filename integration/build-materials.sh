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

# Build a self-contained integration test bundle for a cloud test machine.
#
# Usage:
#   ./build-materials.sh
#   ./build-materials.sh --output /tmp/polaris-controller-integration.zip
#   ./build-materials.sh --clean

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DIST_DIR="${SCRIPT_DIR}/dist"
BUNDLE_NAME="polaris-controller-integration"
OUTPUT="${DIST_DIR}/${BUNDLE_NAME}.zip"
ZIP="${ZIP:-zip}"
UNZIP="${UNZIP:-unzip}"
STAGE_DIR=""

usage() {
  cat <<'EOF'
Usage: ./build-materials.sh [options]

Options:
  -o, --output PATH  Output zip path (default: integration/dist/polaris-controller-integration.zip)
      --clean        Remove integration/dist and exit
  -h, --help         Show this help

Environment:
  ZIP                zip executable (default: zip)
  UNZIP              unzip executable (default: unzip)
EOF
}

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "${STAGE_DIR}" && -d "${STAGE_DIR}" ]]; then
    rm -rf "${STAGE_DIR}"
  fi
}
trap cleanup EXIT

require_bin() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    return 1
  fi
}

clean_dist=0
while (($# > 0)); do
  case "$1" in
    -o|--output)
      (($# >= 2)) || fail "$1 requires a path"
      OUTPUT="$2"
      shift 2
      ;;
    --clean)
      clean_dist=1
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

if ((clean_dist == 1)); then
  rm -rf "${DIST_DIR}"
  printf '[package] removed %s\n' "${DIST_DIR}"
  exit 0
fi

require_bin "${ZIP}"
require_bin "${UNZIP}"
[[ -f "${SCRIPT_DIR}/cases/run-all.sh" ]] || fail "missing integration entry point"
[[ -f "${REPO_ROOT}/docs/README.md" ]] || fail "missing docs/README.md"

output_dir="$(dirname "${OUTPUT}")"
mkdir -p "${output_dir}"
output_dir="$(cd "${output_dir}" && pwd)"
OUTPUT="${output_dir}/$(basename "${OUTPUT}")"
[[ "${OUTPUT}" == *.zip ]] || fail "output path must end with .zip: ${OUTPUT}"
rm -f "${OUTPUT}"

STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pc-integration.XXXXXX")"
bundle_dir="${STAGE_DIR}/${BUNDLE_NAME}"
mkdir -p "${bundle_dir}/integration" "${bundle_dir}/docs"

while IFS= read -r -d '' file; do
  relative="${file#"${SCRIPT_DIR}"/}"
  mkdir -p "${bundle_dir}/integration/$(dirname "${relative}")"
  cp -p "${file}" "${bundle_dir}/integration/${relative}"
done < <(
  find "${SCRIPT_DIR}" -type f \
    ! -path "${DIST_DIR}/*" \
    ! -path '*/.logs/*' \
    ! -name '.DS_Store' \
    ! -name '*.log' \
    ! -name 'polaris-controller-integration-logs-*.zip' \
    ! -name 'polaris-controller-integration-logs-*.tar.gz' \
    -print0
)

cp -p "${REPO_ROOT}/docs/README.md" "${bundle_dir}/docs/README.md"
cp -p "${SCRIPT_DIR}/run.sh" "${bundle_dir}/run.sh"
cp -p "${SCRIPT_DIR}/cleanup.sh" "${bundle_dir}/cleanup.sh"
cp -p "${SCRIPT_DIR}/pack-logs.sh" "${bundle_dir}/pack-logs.sh"
find "${bundle_dir}" -type f -name '*.sh' -exec chmod +x {} +

git_branch="unknown"
git_commit="unknown"
git_dirty="unknown"
if git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git_branch="$(git -C "${REPO_ROOT}" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  git_commit="$(git -C "${REPO_ROOT}" rev-parse --short=12 HEAD 2>/dev/null || true)"
  if [[ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=normal 2>/dev/null)" ]]; then
    git_dirty="yes"
  else
    git_dirty="no"
  fi
fi

{
  printf 'format=1\n'
  printf 'built_at=%s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"
  printf 'git_branch=%s\n' "${git_branch}"
  printf 'git_commit=%s\n' "${git_commit}"
  printf 'git_dirty=%s\n' "${git_dirty}"
  printf 'entrypoint=run.sh\n'
  printf 'case_entrypoint=integration/cases/run-all.sh\n'
} > "${bundle_dir}/PACKAGE-INFO.txt"

required_files=(
  "run.sh"
  "cleanup.sh"
  "pack-logs.sh"
  "integration/cases/lib.sh"
  "integration/cases/run-all.sh"
  "integration/cases/cleanup.sh"
  "integration/cases/inspect.sh"
  "integration/cases/test.md"
  "integration/cases/test-zh.md"
  "integration/cases/service-sync/run.sh"
  "integration/cases/service-sync/alias-winner.sh"
  "integration/cases/service-sync/switch-mode.sh"
  "integration/cases/lifecycle/namespace/run.sh"
  "integration/cases/lifecycle/service/run.sh"
  "integration/cases/lifecycle/endpoints/run.sh"
  "integration/cases/inject/run.sh"
  "integration/cases/combined/run.sh"
  "docs/README.md"
)
for relative in "${required_files[@]}"; do
  [[ -f "${bundle_dir}/${relative}" ]] || fail "package is missing ${relative}"
done

(
  cd "${STAGE_DIR}"
  COPYFILE_DISABLE=1 "${ZIP}" -rq "${OUTPUT}" "${BUNDLE_NAME}"
)
"${UNZIP}" -tq "${OUTPUT}" >/dev/null || fail "zip verification failed: ${OUTPUT}"

file_count="$(find "${bundle_dir}" -type f | wc -l | tr -d ' ')"
size="$(du -h "${OUTPUT}" | awk '{print $1}')"
checksum="$(sha256 "${OUTPUT}" || true)"

printf '[package] output: %s\n' "${OUTPUT}"
printf '[package] size: %s, files: %s\n' "${size}" "${file_count}"
if [[ -n "${checksum}" ]]; then
  printf '[package] sha256: %s\n' "${checksum}"
fi
printf '[package] extracted entrypoint: %s/run.sh\n' "${BUNDLE_NAME}"
printf '[package] remote usage:\n'
printf '  unzip %s\n' "$(basename "${OUTPUT}")"
printf '  cd %s\n' "${BUNDLE_NAME}"
printf '  ./run.sh\n'
printf '  ./pack-logs.sh\n'
printf '  ./cleanup.sh\n'
