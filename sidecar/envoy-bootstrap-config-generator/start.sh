#!/bin/bash
set -ex
readonly PACKAGE_DIRECTORY=$(dirname "$0")
BOOTSTRAP_TEMPLATE="${PACKAGE_DIRECTORY}/bootstrap_template.yaml"
readonly BOOTSTRAP_INSTANCE="${PACKAGE_DIRECTORY}/bootstrap_instance.yaml"

function prepare_envoy() {
  # Generate Envoy bootstrap.
  # namespace/$uuidgen~$tlsmode~$hostname
  envoy_node_id="${NAMESPACE}/$(uuidgen)~$(hostname -i)"
  if [[ -v TLS_MODE ]]
  then
    BOOTSTRAP_TEMPLATE="${PACKAGE_DIRECTORY}/bootstrap_template_tls.yaml"
  fi 
  cat "${BOOTSTRAP_TEMPLATE}" \
      | sed -e "s|ENVOY_NODE_ID|${envoy_node_id}|g" \
      | sed -e "s|CLUSTER_NAME|${CLUSTER_NAME}|g" \
      | sed -e "s|POLARIS_SERVER_URL|${POLARIS_SERVER_URL}|g" \
      | sed -e "s|METADATA|${METADATA}|g" \
      > "${BOOTSTRAP_INSTANCE}"
}

printenv polaris-client-config > /data/polaris-client-config/polaris.yaml

prepare_envoy

if  [[ -v DEBUG_MODE ]]
then
   cat "${BOOTSTRAP_INSTANCE}"
fi

mv "${BOOTSTRAP_INSTANCE}" /var/lib/data/envoy.yaml