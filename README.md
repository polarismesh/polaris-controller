# Polaris Controller

README：

- [介绍](#介绍)
- [安装说明](#安装说明)
- [使用指南](#使用指南)

## 介绍

polaris-controller 用于北极星和 K8s 生态的对接，提供两个可选功能：

- K8s Service 同步到北极星：将 K8s Service 同步到北极星，使用北极星进行服务发现和治理
- polaris-sidecar 自动注入：在应用 Pod 中注入 polaris-sidecar

polaris-sidecar 提供两个可选功能：

- 本地 DNS：使用 DNS 解析的方式访问北极星上的服务
- 服务网格：通过劫持流量的方式实现服务发现和治理，开发侵入性低

本文档介绍如何在 K8s 集群中安装和使用 polaris-controller。

## 安装说明

**前提条件**

在安装 polaris-controller 前，请先安装北极星服务端，安装方式请参考[北极星服务端安装文档](https://polarismesh.cn/zh/doc/%E5%BF%AB%E9%80%9F%E5%85%A5%E9%97%A8/%E5%AE%89%E8%A3%85%E6%9C%8D%E5%8A%A1%E7%AB%AF/%E5%AE%89%E8%A3%85%E5%8D%95%E6%9C%BA%E7%89%88.html)。

**下载安装包**

从 [Releases]() 下载最新版本的安装包。

**修改配置文件**

修改配置文件 configmap.yaml，配置 K8s Service 同步模式和北极星服务端地址，配置示例如下：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: injector-mesh
  namespace: polaris-system
data:
  mesh: |-
    # K8s cluster name
    clusterName: "default"
    # service sync
    serviceSync
      mode: "all"
      serverAddress: "polaris-server address"
```

支持两种 K8s Service 同步模式：

- all：全量同步服务。将 K8s Service 全部同步到北极星。
- demand：按需同步服务。默认不会将 K8s Service 同步到北极星，需要在 Namespace 或者 Service 上添加北极星的 annotation。

北极星支持跨 K8s 集群的服务发现和治理，多个 K8s 集群的 Service 可以同步到一个北极星集群，同步规则如下：

- K8s Namespace 和 Service 名称作为北极星的命名空间名称
- 如果多个 K8s 集群存在相同的 Namespace 和 Service，全部 Pod 同步到一个北极星服务中
- polaris-controller 在北极星服务实例上添加 K8s-cluster-name 标签，用于区分服务实例的来源
- 如果存在多个 K8s Service 同步到一个北极星服务的情况，每个 K8s 集群的 polaris-controller 需要配置不同的 clusterName

**运行安装脚本**

在安装 kubectl 的机器上运行安装脚本：

```shell
bash ./install.sh 
```

查看 polaris-controller 是否正常运行：

```shell
kubectl get pod -n polaris-system

NAME                                  READY   STATUS    RESTARTS   AGE
polaris-controller-545df9775c-48cqt   1/1     Running   0          2d9h
```

## 使用指南

### 全量同步服务

以全量同步服务的模式启动 polaris-controller，将 K8s Service 全部同步到北极星。

### 按需同步服务

以按需同步服务的模式启动 polaris-controller，默认不会将 K8s Service 同步到北极星。

如果需要将某个 Namespace 中的全部 Service 同步到北极星，请在 Namespace 上添加北极星的 annotation，配置方式如下： 

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  annotations:
    polarismesh.cn/sync: "true"
```

如果需要将某个 Service 同步到北极星，请在 Service 上添加北极星的 annotation，配置方式如下： 

```yaml
apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/sync: "true"
```

如果需要将某个 Namespace 中的 Service同步到北极星并且排除某个 Service，配置方式如下： 

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

### 创建服务别名

北极星支持服务别名的功能，允许为一个服务设置一个或者多个服务别名，使用服务别名进行服务发现的效果等同使用服务名称进行服务发现的效果。

polaris-controller 将 K8s Service 同步到北极星的名称映射规则如下：

- K8s Namespace作为北极星的命名空间名称
- K8s Service作为北极星的服务名称

如果需要在 Service 同步到北极星时，为其创建一个服务别名，配置方式如下：

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

### Sidecar 自动注入

如果需要使用 polaris-sidecar，可以在应用 Pod 中自动注入，配置方式如下：

```shell
kubectl label namespace default polaris-injection=enabled 
```

查看 K8s Namespace 是否支持 polaris-sidecar 自动注入：

```shell
kubectl get namespace -L polaris-injection

NAME             STATUS   AGE    POLARIS-INJECTION
default          Active   3d2h   enabled
```

在开启自动注入后，新建的 Pod 会自动注入，存量的 Pod 不会自动注入 polaris-sidecar。
