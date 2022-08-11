#!/bin/bash

readonly PACKAGE_DIRECTORY=$(dirname "$0")
readonly BOOTSTRAP_TEMPLATE="${PACKAGE_DIRECTORY}/bootstrap_template.yaml"
readonly BOOTSTRAP_INSTANCE="${PACKAGE_DIRECTORY}/bootstrap_instance.yaml"

function prepare_envoy() {
  # Generate Envoy bootstrap.
  envoy_node_id="${NAMESPACE}/$(uuidgen)~$(hostname -i)"
  cat "${BOOTSTRAP_TEMPLATE}" \
      | sed -e "s|ENVOY_NODE_ID|${envoy_node_id}|g" \
      | sed -e "s|CLUSTER_NAME|${CLUSTER_NAME}|g" \
      | sed -e "s|POLARIS_SERVER_URL|${POLARIS_SERVER_URL}|g" \
      > "${BOOTSTRAP_INSTANCE}"
}

printenv polaris-client-config > /data/polaris-client-config/polaris.yaml

prepare_envoy
mv "${BOOTSTRAP_INSTANCE}" /var/lib/data/envoy.yaml