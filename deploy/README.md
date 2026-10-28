# Polaris Controller helm

English | [简体中文](./README-zh.md)

This page show how to get polaris-controller service started by helm chart。

## Prerequisites
Make sure k8s cluster is installed and helm is installed. And the polaris-server is running in the polaris-system namespaces of k8s.
(Guidance:[polaris installation by using helm](https://github.com/polarismesh/polaris/tree/main/release/cluster/helm))

## Installation
### helm init
Confirm that the variable assignments in the `deploy/variables.txt` file are as expected, the example is as follows
```shell
cd deploy
$ cat variables.txt                                                                                                                                                                                                                                                                               deploy -> main ? ! |•
POLARIS_HOST:polaris.polaris-system
CONTROLLER_VERSION:v1.7.1
SIDECAR_VERSION:v1.5.1
POLARIS_TOKEN:token
ENVOY_VERSION:v1.26.2
CLUSTER_NAME:default
JAVA_AGENT_INIT:v0.0.1% 
```
Initialize the `values.yaml` file of the helm project
```shell
sh init_helm.sh
```

### install
Use the `helm install ${release_name}.` command to install, replacing `${release_name}` with the release name you need.
Examples are as follows
```shell
cd helm
helm install polaris-controller .
```

### update
Use the `helm upgrade -i ${release_name} .` command to update and replace `${release_name}` with the release name you need.
Examples are as follows
```shell
helm upgrade -i polaris-controller .
```

### uninstall
Use `uninstall_helm.sh` to remove the release, replacing `${release_name}` with the release name you need.
The script also removes the sync-state ConfigMaps created by the controller in `polaris-system` and any
ClusterRole/ClusterRoleBinding left behind by an interrupted uninstall.
```shell
cd deploy
$ sh uninstall_helm.sh polaris-controller
```

The script looks up the release in the current kubeconfig context. Pass the release namespace as the second argument if the chart was installed with `--namespace`:
```shell
$ sh uninstall_helm.sh polaris-controller ${release_namespace}
```

The controller namespace defaults to `polaris-system` and can be overridden with the `CONTROLLER_NAMESPACE` environment variable.

## Configuration
Configs in `values.yaml` of helm will explain how to configure the service.
