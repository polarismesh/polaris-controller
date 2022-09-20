# polaris-controller

[polaris-controller](https://github.com/kubernetes/ingress-nginx) Polaris controller for Kubernetes is used to synchronize endpoints to polaris and inject sidecars into business pods

![Version: 1.0](https://img.shields.io/badge/Version-1.0-informational?style=flat-square) ![AppVersion: 1.3.0](https://img.shields.io/badge/AppVersion-1.3.0-informational?style=flat-square)

# Polaris Controller
## Introduce

polaris-controller For the docking of Polaris and K8s ecology, providing two optional functions：

- K8s Service Sync to Polaris: Sync K8s Service to Polaris, use Polaris for service discovery and governance
- polaris-sidecar Auto-inject: inject polaris-sidecar in app pod

The operating mode of *sidecar* in the Polaris-Controller provides two optional functions:

- LocalDNS (dns): Inject **polaris-sidecar**, realize service discovery and governance by intercepting DNS requests
- ServiceMesh (mesh): Inject **polaris-sidecar** and **envoy**, realize service discovery and governance through hijacking traffic, and develop low invasion

Two K8s Service synchronization modes are supported:

- all: Full synchronization service. Sync all K8s Service to Polaris.
- demand: Sync service on demand. By default, the K8s Service will not be synchronized to Polaris, you need to add the Polaris annotation on the Namespace or Service.

Polaris supports service discovery and governance across K8s clusters. Services of multiple K8s clusters can be synchronized to a Polaris cluster. The synchronization rules are as follows:

- K8s Namespace and Service name as North Star's namespace name
- If the same Namespace and Service exist in multiple K8s clusters, all Pods are synchronized to one Polaris service
- polaris-controller adds clusterName label on Polaris service instances to distinguish service instances from different K8s clusters
- If there are multiple K8s Services synchronized to a Polaris service, the polaris-controller of each K8s cluster needs to be configured with a different clusterName


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