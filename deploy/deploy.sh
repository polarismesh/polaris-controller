#!/usr/bin/env bash

set -euo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}"

DEFAULT_RELEASE="${RELEASE_NAME:-polaris-controller}"
DEFAULT_NAMESPACE="${CONTROLLER_NAMESPACE:-polaris-system}"
DEFAULT_TIMEOUT="${HELM_TIMEOUT:-10m}"
LOG_HOLD_SECONDS="${LOG_HOLD_SECONDS:-0}"
HELM_BIN="${HELM:-helm}"
KUBECTL_BIN="${KUBECTL:-kubectl}"
JQ_BIN="${JQ:-jq}"

KNOWN_VARIABLES=(
  POLARIS_HOST
  CONTROLLER_VERSION
  SIDECAR_VERSION
  POLARIS_TOKEN
  POLARIS_OPERATOR
  ENVOY_VERSION
  CLUSTER_NAME
  JAVA_AGENT_INIT
  SYNC_MODE
)

TMP_DIR=""
HOLD_LOGS="false"

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]]; then
    rm -rf "${TMP_DIR}"
  fi
  if [[ "${HOLD_LOGS}" == "true" && "${LOG_HOLD_SECONDS}" =~ ^[0-9]+$ && "${LOG_HOLD_SECONDS}" -gt 0 ]]; then
    printf '[polaris-controller][INFO] process exiting with status %s; waiting %ss for log collection\n' \
      "${status}" "${LOG_HOLD_SECONDS}"
    sleep "${LOG_HOLD_SECONDS}"
  fi
  exit "${status}"
}
trap cleanup EXIT

log_info() {
  printf '[polaris-controller][INFO] %s\n' "$*"
}

log_warn() {
  printf '[polaris-controller][WARN] %s\n' "$*" >&2
}

die() {
  printf '[polaris-controller][ERROR] %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

make_temp_dir() {
  if [[ -z "${TMP_DIR}" ]]; then
    TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/polaris-controller.XXXXXX")"
  fi
}

usage() {
  cat <<'EOF'
Usage:
  deploy/deploy.sh render helm [--kubernetes-version v1.21|v1.22|all]
      [--variables-file FILE] [--output-dir DIR] [--set KEY=VALUE]...

  deploy/deploy.sh install helm [--kubernetes-version v1.21|v1.22]
      [--release NAME] [--namespace polaris-system] [--variables-file FILE]
      [--set KEY=VALUE]... [--dry-run] [--no-wait] [--timeout DURATION]

  deploy/deploy.sh uninstall helm [RELEASE] [RELEASE_NAMESPACE]
      [--cleanup-residual]

  deploy/deploy.sh render manifests [--kubernetes-version v1.21|v1.22]
      [--variables-file FILE] [--output-dir DIR] [--set KEY=VALUE]...

  deploy/deploy.sh install manifests [--kubernetes-version v1.21|v1.22]
      [--variables-file FILE] [--set KEY=VALUE]... [--dry-run]
      [--no-wait] [--render-only DIR]

  deploy/deploy.sh uninstall manifests [--kubernetes-version v1.21|v1.22]
      [--variables-file FILE] [--set KEY=VALUE]... [--dry-run] [--no-wait]
      [--delete-namespace]

Variable precedence:
  variables file < PC_<KEY> environment variable < --set KEY=VALUE

Examples:
  PC_POLARIS_TOKEN='<token>' deploy/deploy.sh install helm \
    --kubernetes-version v1.22 --set CONTROLLER_VERSION=v1.7.1

  deploy/deploy.sh install manifests \
    --kubernetes-version v1.22 --dry-run

  deploy/deploy.sh uninstall helm polaris-controller polaris-system

Run with LOG_HOLD_SECONDS=300 to keep install/uninstall output available for
external log collection. The default is 0.
EOF
}

normalize_version() {
  case "$1" in
    v1.21|1.21) printf '%s\n' "v1.21" ;;
    v1.22|1.22) printf '%s\n' "v1.22" ;;
    *) die "unsupported Kubernetes manifest version '$1'; use v1.21 or v1.22" ;;
  esac
}

normalize_render_version() {
  if [[ "$1" == "all" ]]; then
    printf '%s\n' "all"
    return 0
  fi
  normalize_version "$1"
}

variables_file_for() {
  local version=$1
  printf '%s/kubernetes_%s/kubernetes/variables.txt\n' "${ROOT_DIR}" "${version}"
}

chart_dir_for() {
  local version=$1
  printf '%s/kubernetes_%s/helm\n' "${ROOT_DIR}" "${version}"
}

manifest_dir_for() {
  local version=$1
  printf '%s/kubernetes_%s/kubernetes\n' "${ROOT_DIR}" "${version}"
}

is_known_variable() {
  local candidate=$1 key
  for key in "${KNOWN_VARIABLES[@]}"; do
    if [[ "${key}" == "${candidate}" ]]; then
      return 0
    fi
  done
  return 1
}

set_variable() {
  local key=$1 value=$2
  is_known_variable "${key}" || die "unsupported variable '$key'"
  printf -v "${key}" '%s' "${value}"
}

load_variables_file() {
  local file=$1 key value
  [[ -f "${file}" ]] || die "variables file not found: ${file}"
  while IFS=: read -r key value || [[ -n "${key}" ]]; do
    key="${key%$'\r'}"
    value="${value%$'\r'}"
    [[ -z "${key}" || "${key}" == \#* ]] && continue
    is_known_variable "${key}" || die "unsupported variable '${key}' in ${file}"
    printf -v "${key}" '%s' "${value}"
  done < "${file}"
}

apply_environment_overrides() {
  local key env_name env_value
  for key in "${KNOWN_VARIABLES[@]}"; do
    env_name="PC_${key}"
    if declare -p "${env_name}" >/dev/null 2>&1; then
      env_value="${!env_name}"
      set_variable "${key}" "${env_value}"
    fi
  done
}

apply_set_overrides() {
  local override key value
  for override in "$@"; do
    [[ "${override}" == *=* ]] || die "--set expects KEY=VALUE, got '${override}'"
    key="${override%%=*}"
    value="${override#*=}"
    set_variable "${key}" "${value}"
  done
}

validate_variables() {
  local key value
  for key in "${KNOWN_VARIABLES[@]}"; do
    if ! declare -p "${key}" >/dev/null 2>&1; then
      die "variable '$key' is not set"
    fi
    value="${!key}"
    case "${key}" in
      POLARIS_TOKEN|POLARIS_OPERATOR)
        ;;
      *)
        [[ -n "${value}" ]] || die "variable '$key' must not be empty"
        ;;
    esac
  done
  case "${SYNC_MODE}" in
    all|demand) ;;
    *) die "SYNC_MODE must be 'all' or 'demand', got '${SYNC_MODE}'" ;;
  esac
}

escape_sed_replacement() {
  printf '%s' "$1" | sed 's/[\\&?]/\\&/g'
}

replace_placeholder() {
  local file=$1 key=$2 value escaped
  value="${!key}"
  escaped="$(escape_sed_replacement "${value}")"
  sed -i.bak "s?#${key}#?${escaped}?g" "${file}"
  rm -f "${file}.bak"
}

ensure_no_known_placeholders() {
  local file=$1 key
  for key in "${KNOWN_VARIABLES[@]}"; do
    if grep -Fq "#${key}#" "${file}"; then
      die "unreplaced placeholder #${key}# remains in ${file}"
    fi
  done
}

render_values_file() {
  local version=$1 output_file=$2 source_file template key
  source_file="$(chart_dir_for "${version}")/values.yaml"
  [[ -f "${source_file}" ]] || die "Helm values file not found: ${source_file}"
  mkdir -p "$(dirname "${output_file}")"
  template="$(mktemp "${output_file}.XXXXXX")"
  cp "${source_file}" "${template}"
  for key in "${KNOWN_VARIABLES[@]}"; do
    replace_placeholder "${template}" "${key}"
  done
  ensure_no_known_placeholders "${template}"
  chmod 600 "${template}"
  mv "${template}" "${output_file}"
}

render_manifest_tree() {
  local version=$1 output_dir=$2 source_dir file key
  source_dir="$(manifest_dir_for "${version}")"
  [[ -d "${source_dir}" ]] || die "manifest directory not found: ${source_dir}"
  mkdir -p "${output_dir}"
  cp "${source_dir}"/*.yaml "${output_dir}/"
  mkdir -p "${output_dir}/secrets"
  cp -R "${source_dir}/secrets/." "${output_dir}/secrets/"
  while IFS= read -r -d '' file; do
    for key in "${KNOWN_VARIABLES[@]}"; do
      replace_placeholder "${file}" "${key}"
    done
    ensure_no_known_placeholders "${file}"
    chmod 600 "${file}"
  done < <(find "${output_dir}" -type f -name '*.yaml' -print0)
}

parse_common_variables() {
  VARIABLES_FILE=$1
  shift
  load_variables_file "${VARIABLES_FILE}"
  apply_environment_overrides
  apply_set_overrides "$@"
  validate_variables
}

assert_installable_version() {
  if [[ "${CONTROLLER_VERSION}" == *'#'* ]]; then
    die "CONTROLLER_VERSION='${CONTROLLER_VERSION}' is a packaging placeholder; set a release tag with --set or PC_CONTROLLER_VERSION"
  fi
}

cleanup_residual_rbac() {
  local rbac_json owned_rbac kind name
  rbac_json="$("${KUBECTL_BIN}" get clusterrole,clusterrolebinding \
    --selector "app.kubernetes.io/instance=${RELEASE_NAME},qcloud-app=polaris-controller,app.kubernetes.io/managed-by=Helm" \
    --output json)"
  owned_rbac="$(printf '%s\n' "${rbac_json}" | "${JQ_BIN}" -r \
    --arg release "${RELEASE_NAME}" --arg namespace "${confirmed_namespace}" \
    '.items[] | select((.kind == "ClusterRole" or .kind == "ClusterRoleBinding")
      and .metadata.annotations["meta.helm.sh/release-name"] == $release
      and .metadata.annotations["meta.helm.sh/release-namespace"] == $namespace
      and .metadata.labels["app.kubernetes.io/instance"] == $release
      and .metadata.labels["qcloud-app"] == "polaris-controller"
      and .metadata.labels["app.kubernetes.io/managed-by"] == "Helm")
      | [.kind, .metadata.name] | @tsv')"
  while IFS=$'\t' read -r kind name; do
    if [[ -n "${kind}" && -n "${name}" ]]; then
      "${KUBECTL_BIN}" delete "${kind}" --ignore-not-found=true --wait=true -- "${name}"
    fi
  done <<< "${owned_rbac}"
}

cleanup_runtime_state() {
  local controller_namespace workload_json
  if ! controller_namespace="$("${KUBECTL_BIN}" get namespace "${CONTROLLER_NAMESPACE}" \
    --ignore-not-found -o name)"; then
    log_warn "cannot verify controller namespace ${CONTROLLER_NAMESPACE}; runtime state was preserved"
    return 1
  fi
  if [[ -z "${controller_namespace}" ]]; then
    return 0
  fi
  if ! workload_json="$("${KUBECTL_BIN}" --namespace "${CONTROLLER_NAMESPACE}" get pods \
    --selector "app=sidecar-injector" --output json)"; then
    log_warn "cannot verify controller Pods in ${CONTROLLER_NAMESPACE}; runtime state was preserved"
    return 1
  fi
  if ! printf '%s\n' "${workload_json}" | "${JQ_BIN}" -e \
    'type == "object" and (.items | type) == "array" and (.items | length) == 0' >/dev/null; then
    log_warn "controller Pods in ${CONTROLLER_NAMESPACE} are still present; runtime state was preserved"
    return 1
  fi
  "${KUBECTL_BIN}" --namespace "${CONTROLLER_NAMESPACE}" delete configmap \
    --selector "polarismesh.cn/sync-state=true,app.kubernetes.io/managed-by=polaris-controller" \
    --ignore-not-found=true --wait=true
}

command_render_helm() {
  local version=all variables_file="${ROOT_DIR}/variables.txt" output_dir="${ROOT_DIR}/generated/helm-values"
  local -a overrides=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --kubernetes-version) [[ $# -ge 2 ]] || die "$1 requires a value"; version=$(normalize_render_version "$2"); shift 2 ;;
      --variables-file) [[ $# -ge 2 ]] || die "$1 requires a value"; variables_file=$2; shift 2 ;;
      --output-dir) [[ $# -ge 2 ]] || die "$1 requires a value"; output_dir=$2; shift 2 ;;
      --set) [[ $# -ge 2 ]] || die "$1 requires a value"; overrides+=("$2"); shift 2 ;;
      -h|--help) usage; return 0 ;;
      *) die "unsupported argument for render helm: $1" ;;
    esac
  done
  parse_common_variables "${variables_file}" ${overrides[@]+"${overrides[@]}"}
  if [[ "${CONTROLLER_VERSION}" == *'#'* ]]; then
    log_warn "CONTROLLER_VERSION is '${CONTROLLER_VERSION}'; this is expected only before a release build replaces the version marker"
  fi
  if [[ "${version}" == "all" ]]; then
    render_values_file v1.21 "${output_dir}/v1.21/values.yaml"
    render_values_file v1.22 "${output_dir}/v1.22/values.yaml"
  else
    render_values_file "${version}" "${output_dir}/${version}/values.yaml"
  fi
  log_info "Helm values rendered under ${output_dir}"
}

command_install_helm() {
  local version=v1.22 variables_file="${ROOT_DIR}/variables.txt" output_dir
  local release="${DEFAULT_RELEASE}" namespace="${DEFAULT_NAMESPACE}" timeout="${DEFAULT_TIMEOUT}"
  local dry_run="false" wait="true"
  local -a overrides=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --kubernetes-version) [[ $# -ge 2 ]] || die "$1 requires a value"; version=$(normalize_version "$2"); shift 2 ;;
      --variables-file) [[ $# -ge 2 ]] || die "$1 requires a value"; variables_file=$2; shift 2 ;;
      --release) [[ $# -ge 2 ]] || die "$1 requires a value"; release=$2; shift 2 ;;
      --namespace) [[ $# -ge 2 ]] || die "$1 requires a value"; namespace=$2; shift 2 ;;
      --timeout) [[ $# -ge 2 ]] || die "$1 requires a value"; timeout=$2; shift 2 ;;
      --set) [[ $# -ge 2 ]] || die "$1 requires a value"; overrides+=("$2"); shift 2 ;;
      --dry-run) dry_run="true"; shift ;;
      --no-wait) wait="false"; shift ;;
      -h|--help) usage; return 0 ;;
      *) die "unsupported argument for install helm: $1" ;;
    esac
  done
  [[ "${namespace}" == "polaris-system" ]] ||
    die "the current Helm charts target polaris-system; install with --namespace polaris-system"
  require_command "${HELM_BIN}"
  parse_common_variables "${variables_file}" ${overrides[@]+"${overrides[@]}"}
  assert_installable_version
  make_temp_dir
  output_dir="${TMP_DIR}/helm-values"
  render_values_file "${version}" "${output_dir}/values.yaml"
  local chart
  chart="$(chart_dir_for "${version}")"
  if [[ "${dry_run}" == "true" ]]; then
    "${HELM_BIN}" template "${release}" "${chart}" --namespace "${namespace}" \
      --values "${output_dir}/values.yaml"
    log_info "Helm dry-run completed"
    return 0
  fi
  HOLD_LOGS="true"
  local -a args=(upgrade --install "${release}" "${chart}" --namespace "${namespace}"
    --create-namespace --values "${output_dir}/values.yaml" --timeout "${timeout}")
  if [[ "${wait}" == "true" ]]; then
    args+=(--wait)
  fi
  log_info "installing Helm release ${release} in namespace ${namespace}"
  "${HELM_BIN}" "${args[@]}"
}

command_uninstall_helm() {
  local release="${DEFAULT_RELEASE}" release_namespace="" mode="uninstall"
  if [[ $# -gt 0 && "$1" != --* ]]; then
    release=$1
    shift
  fi
  if [[ $# -gt 0 && "$1" != --* ]]; then
    release_namespace=$1
    shift
  fi
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --cleanup-residual) mode="residual"; shift ;;
      --namespace) [[ $# -ge 2 ]] || die "$1 requires a value"; release_namespace=$2; shift 2 ;;
      --release) [[ $# -ge 2 ]] || die "$1 requires a value"; release=$2; shift 2 ;;
      -h|--help) usage; return 0 ;;
      *) die "unsupported argument for uninstall helm: $1" ;;
    esac
  done
  require_command "${HELM_BIN}"
  require_command "${KUBECTL_BIN}"
  require_command "${JQ_BIN}"
  RELEASE_NAME="${release}"
  CONTROLLER_NAMESPACE="${CONTROLLER_NAMESPACE:-polaris-system}"

  if [[ "${mode}" == "residual" ]]; then
    [[ -n "${release_namespace}" ]] || die "--cleanup-residual requires an explicit release namespace"
    confirmed_namespace="${release_namespace}"
    if ! release_list_json="$("${HELM_BIN}" list --all --namespace "${confirmed_namespace}" --output json)"; then
      die "cannot verify release absence; no cleanup performed"
    fi
    if ! printf '%s\n' "${release_list_json}" | "${JQ_BIN}" -e \
      --arg release "${RELEASE_NAME}" --arg namespace "${confirmed_namespace}" \
      'type == "array" and all(.[]; type == "object"
        and (.name | type) == "string" and (.name | length) > 0
        and .namespace == $namespace and .name != $release)' >/dev/null; then
      die "release still exists or release list is invalid; no cleanup performed"
    fi
    HOLD_LOGS="true"
    cleanup_runtime_state
    cleanup_residual_rbac
    log_info "residual controller state and RBAC for ${RELEASE_NAME} in ${confirmed_namespace} cleaned"
    return 0
  fi

  local -a status_args=("${RELEASE_NAME}")
  if [[ -n "${release_namespace}" ]]; then
    status_args+=(--namespace "${release_namespace}")
  fi
  if ! release_json="$("${HELM_BIN}" status "${status_args[@]}" --output json)"; then
    die "cannot confirm Helm release ${RELEASE_NAME}; no cleanup performed"
  fi
  if ! confirmed_namespace="$(printf '%s\n' "${release_json}" | "${JQ_BIN}" -er \
    --arg release "${RELEASE_NAME}" --arg namespace "${release_namespace}" \
    'select(.name == $release and (.namespace | type) == "string" and (.namespace | length) > 0
      and ($namespace == "" or .namespace == $namespace)
      and (.version | type) == "number" and .version > 0) | .namespace')"; then
    die "release name, namespace, or revision mismatch; no cleanup performed"
  fi
  if ! release_list_json="$("${HELM_BIN}" list --all --namespace "${confirmed_namespace}" \
    --filter "^${RELEASE_NAME}$" --output json)"; then
    die "cannot verify release chart; no cleanup performed"
  fi
  if ! printf '%s\n' "${release_list_json}" | "${JQ_BIN}" -e \
    --arg release "${RELEASE_NAME}" --arg namespace "${confirmed_namespace}" --argjson status "${release_json}" \
    'map(select(.name == $release and .namespace == $namespace
      and .revision == ($status.version | tostring) and .status == $status.info.status
      and (.chart | test("^polaris-controller-[0-9]+\\.[0-9]+\\.[0-9]+([+-].*)?$")))) | length == 1' >/dev/null; then
    die "release chart or revision could not be confirmed; no cleanup performed"
  fi

  HOLD_LOGS="true"
  "${HELM_BIN}" uninstall "${RELEASE_NAME}" --namespace "${confirmed_namespace}" --wait
  cleanup_runtime_state
  cleanup_residual_rbac
  log_info "polaris-controller release ${RELEASE_NAME} in ${confirmed_namespace} uninstalled"
}

command_render_manifests() {
  local version=v1.22 variables_file="" output_dir="${ROOT_DIR}/generated/manifests"
  local -a overrides=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --kubernetes-version) [[ $# -ge 2 ]] || die "$1 requires a value"; version=$(normalize_version "$2"); shift 2 ;;
      --variables-file) [[ $# -ge 2 ]] || die "$1 requires a value"; variables_file=$2; shift 2 ;;
      --output-dir) [[ $# -ge 2 ]] || die "$1 requires a value"; output_dir=$2; shift 2 ;;
      --set) [[ $# -ge 2 ]] || die "$1 requires a value"; overrides+=("$2"); shift 2 ;;
      -h|--help) usage; return 0 ;;
      *) die "unsupported argument for render manifests: $1" ;;
    esac
  done
  variables_file="${variables_file:-$(variables_file_for "${version}")}"
  parse_common_variables "${variables_file}" ${overrides[@]+"${overrides[@]}"}
  render_manifest_tree "${version}" "${output_dir}/${version}"
  log_info "Kubernetes manifests rendered under ${output_dir}/${version}"
}

command_install_manifests() {
  local version=v1.22 variables_file="" dry_run="false" wait="true" render_only=""
  local -a overrides=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --kubernetes-version) [[ $# -ge 2 ]] || die "$1 requires a value"; version=$(normalize_version "$2"); shift 2 ;;
      --variables-file) [[ $# -ge 2 ]] || die "$1 requires a value"; variables_file=$2; shift 2 ;;
      --set) [[ $# -ge 2 ]] || die "$1 requires a value"; overrides+=("$2"); shift 2 ;;
      --dry-run) dry_run="true"; shift ;;
      --no-wait) wait="false"; shift ;;
      --render-only) [[ $# -ge 2 ]] || die "$1 requires a value"; render_only=$2; shift 2 ;;
      -h|--help) usage; return 0 ;;
      *) die "unsupported argument for install manifests: $1" ;;
    esac
  done
  variables_file="${variables_file:-$(variables_file_for "${version}")}"
  parse_common_variables "${variables_file}" ${overrides[@]+"${overrides[@]}"}
  assert_installable_version
  make_temp_dir
  local rendered_dir="${TMP_DIR}/manifests"
  render_manifest_tree "${version}" "${rendered_dir}"
  if [[ -n "${render_only}" ]]; then
    mkdir -p "${render_only}"
    cp -R "${rendered_dir}/." "${render_only}/"
    log_info "manifests rendered to ${render_only}; no Kubernetes resources were changed"
    return 0
  fi
  require_command "${KUBECTL_BIN}"
  if [[ "${dry_run}" != "true" ]]; then
    HOLD_LOGS="true"
  fi
  local -a common=(--wait="${wait}")
  if [[ "${dry_run}" == "true" ]]; then
    common+=(--dry-run=client)
  fi
  local manifest
  "${KUBECTL_BIN}" apply -f "${rendered_dir}/namespace.yaml" "${common[@]}"
  "${KUBECTL_BIN}" create secret generic polaris-sidecar-injector -n polaris-system \
    --from-file="${rendered_dir}/secrets/key.pem" \
    --from-file="${rendered_dir}/secrets/cert.pem" \
    --from-file="${rendered_dir}/secrets/ca-cert.pem" \
    --dry-run=client -o yaml | "${KUBECTL_BIN}" apply -f - "${common[@]}"
  for manifest in \
    rbac.yaml javaagent-configmap.yaml polaris-client-config-tpl.yaml configmap.yaml \
    injector.yaml polaris-metrics-svc.yaml polaris-controller.yaml; do
    "${KUBECTL_BIN}" apply -f "${rendered_dir}/${manifest}" "${common[@]}"
  done
  if [[ "${dry_run}" == "true" ]]; then
    log_info "manifest installation dry-run completed; no resources were changed"
  else
    log_info "manifest installation completed"
  fi
}

delete_manifest_file() {
  local rendered_dir=$1 manifest=$2
  local -a args=(delete -f "${rendered_dir}/${manifest}" --ignore-not-found=true)
  args+=(--wait="${wait}")
  if [[ "${dry_run}" == "true" ]]; then
    args+=(--dry-run=client)
  fi
  "${KUBECTL_BIN}" "${args[@]}"
}

command_uninstall_manifests() {
  local version=v1.22 variables_file="" dry_run="false" wait="true" delete_namespace="false"
  local -a overrides=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --kubernetes-version) [[ $# -ge 2 ]] || die "$1 requires a value"; version=$(normalize_version "$2"); shift 2 ;;
      --variables-file) [[ $# -ge 2 ]] || die "$1 requires a value"; variables_file=$2; shift 2 ;;
      --set) [[ $# -ge 2 ]] || die "$1 requires a value"; overrides+=("$2"); shift 2 ;;
      --dry-run) dry_run="true"; shift ;;
      --no-wait) wait="false"; shift ;;
      --delete-namespace) delete_namespace="true"; shift ;;
      -h|--help) usage; return 0 ;;
      *) die "unsupported argument for uninstall manifests: $1" ;;
    esac
  done
  variables_file="${variables_file:-$(variables_file_for "${version}")}"
  parse_common_variables "${variables_file}" ${overrides[@]+"${overrides[@]}"}
  require_command "${KUBECTL_BIN}"
  make_temp_dir
  local rendered_dir="${TMP_DIR}/manifests"
  render_manifest_tree "${version}" "${rendered_dir}"
  if [[ "${dry_run}" != "true" ]]; then
    HOLD_LOGS="true"
  fi
  local -a webhook_args=(delete mutatingwebhookconfiguration polaris-sidecar-injector
    --ignore-not-found=true --wait="${wait}")
  if [[ "${dry_run}" == "true" ]]; then
    webhook_args+=(--dry-run=client)
  fi
  "${KUBECTL_BIN}" "${webhook_args[@]}"
  local manifest
  for manifest in \
    injector.yaml polaris-controller.yaml polaris-metrics-svc.yaml \
    javaagent-configmap.yaml configmap.yaml polaris-client-config-tpl.yaml; do
    delete_manifest_file "${rendered_dir}" "${manifest}"
  done
  local -a secret_args=(-n polaris-system delete secret polaris-sidecar-injector
    --ignore-not-found=true --wait="${wait}")
  if [[ "${dry_run}" == "true" ]]; then
    secret_args+=(--dry-run=client)
  fi
  "${KUBECTL_BIN}" "${secret_args[@]}"
  if [[ "${dry_run}" == "true" ]]; then
    log_info "controller Pod deletion wait would run for up to 120s before state cleanup"
  else
    local -a pod_wait_args=(-n polaris-system wait pods \
      --for=delete --selector k8s-app=polaris-controller --timeout=120s)
    if ! "${KUBECTL_BIN}" "${pod_wait_args[@]}"; then
      log_warn "controller Pods were not deleted within 120s; forcing deletion"
      if ! "${KUBECTL_BIN}" -n polaris-system delete pods \
        --selector k8s-app=polaris-controller \
        --ignore-not-found=true --force=true --grace-period=0 \
        --wait=false --request-timeout=10s; then
        log_warn "forced controller Pod deletion did not complete within 10s; continuing cleanup"
      fi
    else
      log_info "controller Pods in polaris-system are gone"
    fi
  fi
  local -a state_args=(-n polaris-system delete configmap
    --selector "polarismesh.cn/sync-state=true,app.kubernetes.io/managed-by=polaris-controller"
    --ignore-not-found=true --wait="${wait}")
  if [[ "${dry_run}" == "true" ]]; then
    state_args+=(--dry-run=client)
  fi
  "${KUBECTL_BIN}" "${state_args[@]}"
  delete_manifest_file "${rendered_dir}" rbac.yaml
  if [[ "${delete_namespace}" == "true" ]]; then
    delete_manifest_file "${rendered_dir}" namespace.yaml
  else
    log_info "namespace polaris-system was preserved; pass --delete-namespace only when it is dedicated"
  fi
  if [[ "${dry_run}" == "true" ]]; then
    log_info "manifest uninstall dry-run completed; no resources were changed"
  else
    log_info "manifest uninstall completed"
  fi
}

main() {
  [[ $# -gt 0 ]] || {
    usage
    exit 2
  }
  local command=$1
  shift
  case "${command}" in
    render)
      [[ $# -gt 0 ]] || die "render requires 'helm' or 'manifests'"
      local method=$1
      shift
      case "${method}" in
        helm) command_render_helm "$@" ;;
        manifests|manifest) command_render_manifests "$@" ;;
        *) die "unsupported render method '${method}'" ;;
      esac
      ;;
    install)
      [[ $# -gt 0 ]] || die "install requires 'helm' or 'manifests'"
      local method=$1
      shift
      case "${method}" in
        helm) command_install_helm "$@" ;;
        manifests|manifest) command_install_manifests "$@" ;;
        *) die "unsupported install method '${method}'" ;;
      esac
      ;;
    uninstall)
      [[ $# -gt 0 ]] || die "uninstall requires 'helm' or 'manifests'"
      local method=$1
      shift
      case "${method}" in
        helm) command_uninstall_helm "$@" ;;
        manifests|manifest) command_uninstall_manifests "$@" ;;
        *) die "unsupported uninstall method '${method}'" ;;
      esac
      ;;
    help|-h|--help) usage ;;
    *) die "unknown command '${command}'" ;;
  esac
}

main "$@"
