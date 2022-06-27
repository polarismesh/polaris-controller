# Polaris Controller

[中文文档](./README-zh.md)

- [Introduce](#Introduce)
- [Kubernetes's Version](#Kubernetes_Version)
- [Installation](#Installation)
- [Guidance](#Guidance)

## Introduce

polaris-controller For the docking of Polaris and K8s ecology, providing two optional functions：

- K8s Service Sync to Polaris: Sync K8s Service to Polaris, use Polaris for service discovery and governance
- polaris-sidecar Auto-inject: inject polaris-sidecar in app pod

polaris-sidecar Provides two optional functions：

- Local DNS: Use DNS resolution to access services on Polaris
- Service mesh: realize service discovery and governance by hijacking traffic, with low development intrusion

This document describes how to install and use polaris-controller in a K8s cluster.

## Kubernetes_Version

- The current version that supports only ** kubernetes ** is (, 1.21]

## Installation

**Preconditions**

Before installing polaris-controller, please install the Polaris server first. For the installation method, please refer to [Polaris Server Installation Documentation](https://polarismesh.cn/zh/doc/%E5%BF%AB%E9%80%9F%E5%85%A5%E9%97%A8/%E5%AE%89%E8%A3%85%E6%9C%8D%E5%8A%A1%E7%AB%AF/%E5%AE%89%E8%A3%85%E5%8D%95%E6%9C%BA%E7%89%88.html)。

**Download the installation package**

From [Releases](https://github.com/polarismesh/polaris-controller/releases) download the latest version of the installation package。

**Modify the configuration file**

Modify the configuration file configmap.yaml to configure the K8s Service synchronization mode and the Polaris server address. The configuration example is as follows：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: injector-mesh
  namespace: polaris-system
data:
  mesh: |-
    # k8s cluster name
    clusterName: "default"
    # polaris-sidecar inject run mode
    sidecarInject:
      mode: "mesh"
    # service sync
    serviceSync:
      mode: "all"
      # Please make sure that the HTTP port listened by the Arctic Star is 8090 and the GRPC port for service registration & governance is 8091
      serverAddress: "polaris-server ip or domain"
      # When Polaris enables service authentication, the token corresponding to the user/user group needs to be configured here.
      accessToken: ""
    defaultConfig:
      proxyMetadata:
        # Please make sure that the HTTP port listened by the Arctic Star is 8090 and the GRPC port for service registration & governance is 8091
        serverAddress: "polaris-server ip or domain"
```

Two K8s Service synchronization modes are supported:

- all: Full synchronization service. Sync all K8s Service to Polaris.
- demand: Sync service on demand. By default, the K8s Service will not be synchronized to Polaris, you need to add the Polaris annotation on the Namespace or Service.

Polaris supports service discovery and governance across K8s clusters. Services of multiple K8s clusters can be synchronized to a Polaris cluster. The synchronization rules are as follows:

- K8s Namespace and Service name as North Star's namespace name
- If the same Namespace and Service exist in multiple K8s clusters, all Pods are synchronized to one Polaris service
- polaris-controller adds clusterName label on Polaris service instances to distinguish service instances from different K8s clusters
- If there are multiple K8s Services synchronized to a Polaris service, the polaris-controller of each K8s cluster needs to be configured with a different clusterName

**Run the installation script**

Run the installation script on the machine where kubectl is installed：

```shell
bash ./install.sh 
```

Check if polaris-controller is working：

```shell
kubectl get pod -n polaris-system

NAME                                  READY   STATUS    RESTARTS   AGE
polaris-controller-545df9775c-48cqt   1/1     Running   0          2d9h
```

## Annotations

| Annotations name              | Annotations description                                                                                                         |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| polarismesh.cn/sync           | Whether to synchronize this service to Polarismesh.TRUE synchronization, False is not synchronized, default is not synchronized |
| polarismesh.cn/aliasService   | Synchronize K8S Service to PolarisMesh, and the name of the service alias created at the same time                              |
| polarismesh.cn/aliasNamespace | The named space where the owner is located, with PolarisMesh.cn/aliasservice use                                                |



## Guidance

### Full synchronization service

Start polaris-controller in full synchronization service mode, and synchronize all K8s services to Polaris. The startup configuration is as follows：

```yaml
apiVersion: v1
kind: ConfigMap
data:
  mesh:
    serviceSync
      mode: "all"
```

### On-demand sync service

Start polaris-controller in the mode of on-demand synchronization service. By default, K8s Service will not be synchronized to Polaris. The startup configuration is as follows：

```yaml
apiVersion: v1
kind: ConfigMap
data:
  mesh:
    serviceSync
      mode: "demand"
```

If you need to synchronize all services in a Namespace to Polaris, please add Polaris annotation on the Namespace, the configuration is as follows： 

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  annotations:
    polarismesh.cn/sync: "true"
```

If you need to synchronize a Service to Polaris, please add the Polaris annotation on the Service, the configuration is as follows： 

```yaml
apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/sync: "true"
```

If you need to synchronize a Service in a Namespace to Polaris and exclude a Service, the configuration is as follows： 

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  annotations:
    polarismesh.cn/sync: "true"

apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/sync: "false"
```

### Create service alias

Polaris supports the function of service alias, allowing one or more service aliases to be set for a service. The effect of using service alias for service discovery is the same as that of using service name for service discovery.

The name mapping rules for polaris-controller to synchronize K8s Service to Polaris are as follows:

- K8s Namespace as Polaris namespace name
- K8s Service as the service name of Polaris

If you need to create a service alias for Service when it is synchronized to Polaris, the configuration is as follows：

```yaml
apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/aliasNamespace: aliasDefault
    polarismesh.cn/aliasService: aliasTest
```

### Sidecar auto inject

If polaris-sidecar needs to be used, it can be automatically injected in the application Pod, and the configuration is as follows：

```shell
kubectl label namespace default polaris-injection=enabled 
```

Check whether K8s Namespace supports automatic injection of polaris-sidecar：

```shell
kubectl get namespace -L polaris-injection

NAME             STATUS   AGE    POLARIS-INJECTION
default          Active   3d2h   enabled
```

View the operation mode after polaris-sidecar injection corresponding to K8s Namespace：

```shell
kubectl get namespace -L polaris-sidecar-mode

NAME              STATUS   AGE   POLARIS-SIDECAR-MODE
default           Active   10d   mesh
```

After automatic injection is enabled, newly created Pods will be automatically injected, and existing Pods will not be automatically injected into polaris-sidecar. If you want stock pods to experience polaris-sidecar as well, for pods managed by Deployment, DaemonSet or StatefulSet controllers, you can run the following command

```shell
# Deployment
kubectl rollout restart deployment/DEPLOYMENT_NAME --namespace NAMESPACE

# DaemonSet
kubectl rollout restart daemonset/DAEMONSET_NAME --namespace NAMESPACE

# StatefulSet
kubectl rollout restart statefulset/STATEFULSET_NAME --namespace NAMESPACE
```