# Polaris Controller

README：

- [介绍](#介绍)
- [快速入门](#快速入门)
- [使用指南](#使用指南)

## 介绍

polaris-controller 用于北极星和 K8s 生态的对接，提供两个可选功能：

- K8s Service 同步到北极星：将 K8s Service 同步到北极星，使用北极星进行服务发现和治理。
- polaris-sidecar 自动注入：在应用 Pod 中注入 polaris-sidecar。

polaris-sidecar 提供两个可选功能：

- 本地 DNS：使用 DNS 解析的方式访问北极星上的服务
- 服务网格：通过劫持流量的方式实现服务发现和治理，开发侵入性低

本文档介绍如何在 K8s 集群中安装和使用 polaris-controller。

## 编译打包

获取 Polaris Controller 代码

```
git clone https://github.com/PolarisMesh/polaris-controller.git

cd polaris-controller/deploy/polaris-controller
```

## 快速入门

### 安装步骤

**前提条件**

在安装 polaris-controller 前，请先安装北极星服务端。安装方式请参考[北极星服务端安装文档](https://polarismesh.cn/zh/doc/%E5%BF%AB%E9%80%9F%E5%85%A5%E9%97%A8/%E5%AE%89%E8%A3%85%E6%9C%8D%E5%8A%A1%E7%AB%AF/%E5%AE%89%E8%A3%85%E5%8D%95%E6%9C%BA%E7%89%88.html)。

**安装 polaris-controller**

下载release包

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
    serviceSync:
      # service sync mode: all, demand
      mode: "all"
      polaris:
        serverAddress: "polaris-server address"
```

polaris-controller 支持两种 K8s Service 同步模式：

- all：全量同步服务的模式。将 K8s Service 全部同步到北极星。
- demand：按需同步服务的模式。默认不会将 K8s Service 同步到北极星，需要在 Namespace 或者 Service 上添加北极星的 annotation。

修改 configmap.yaml 和 polaris-controller.yaml，配置下面的参数：

将 `deploy/polaris-controller/polaris-controller.yaml` 中 `polaris-controller` 容器的启动参数 `cluster-name`替换为您需要的值（默认为`default`）。
北极星支持多集群接入的方案，即多个集群的同名服务，同步到北极星是一个服务，通过北极星的服务发现，可以发现多个 k8s 的同名服务的实例列表。
当使用多集群方案时，您需要为多个集群指定不同的 `cluster-name` ， Polaris Controller 会只处理自己所在的集群的实例，若多个集群使用了相同的`cluster-name`，且多个集群有同名的service，会出现多个 Controller 相互覆盖对方同步到北极星上的实例的情况。

**运行安装脚本**

在安装 kubectl 的机器上运行安装脚本：

```
bash ./install.sh 
```

查看 polaris-controller 是否正常运行：

```
kubectl get pod -n polaris-system

NAME                                  READY   STATUS    RESTARTS   AGE
polaris-controller-545df9775c-48cqt   1/1     Running   0          2d9h
```

### K8s annotation

您可以在 k8s 的 service 和 namespace 指定注解，控制 polaris-controller 同步的行为，当前支持以下注解。

```
polarismesh.cn/sync

支持添加 annotation 的资源：Namespace、Service
值类型：true/false
功能：1. 配置在命名空间上：只在按命名空间同步模式下生效，表示打开或关闭某个 namespace 的自动同步功能。2.配置在 service 上，在按集群同步模式、按命名空间同步模式下均生效，可以配置关闭特定 service 的自动同步。
```

```
polarismesh.cn/enableRegister（已废弃）

效果等同polarismesh.cn/sync。兼容存量，不建议使用
```

```
polarismesh.cn/aliasNamespace 和 polarismesh.cn/aliasService

支持添加 annotation 的资源：Service
值类型：字符串
功能：在 Service 同步到北极星时，为其创建北极星的服务别名
```

## 使用指南

### 全量同步服务

以全量同步服务的模式启动 polaris-controller，启动配置如下：

```
--sync-mode=all
```

在全量同步服务的模式下，将 K8s Service 全部同步到北极星。

### 按需同步服务

以按需同步服务的模式启动 polaris-controller，启动配置如下：

```
--sync-mode=demand
```

在按需同步服务的模式下，默认不会将 K8s Service 同步到北极星。

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

### Sidecar自动注入

#### 开启自动注入功能

以下命令为 `default` 命名空间启用注入，`Polaris Controller` 的注入器，会将 `Envoy Sidecar` 容器注入到在此命名空间下创建的 pod 中：

```
kubectl label namespace default polaris-injection=enabled 
```

使用一下命令来验证 `default` 命名空间是否已经正确启用：

```
kubectl get namespace -L polaris-injection
```

此时应该返回：

```
NAME             STATUS   AGE    POLARIS-INJECTION
default          Active   3d2h   enabled
```

这时已经打开了自动注入，新创建的 pod 会自动被注入 sidecar 容器。存量的 pod 需要删除重建来触发自动注入。如果您使用的是工作负载，可以使用下面的命令滚动更新：

```
kubectl rollout restart deployment xxx --namespace xxxx
```

#### 自动注入配置

Polaris Controller 允许您在 pod 的 Annotations 中指定一些配置来控制自动注入的行为，目前支持的配置有：

- sidecar.istio.io/inject: 您可以对命名空间开启自动注入，并对某些特殊的 pod 配置 annotation sidecar.istio.io/inject=false ，关闭这个 pod 的自动注入。
- polarismesh.cn/proxyCPU: 自动注入的 Envoy 的 cpu request。
- polarismesh.cn/proxyMemory: 自动注入的 Envoy 的 memory request。
- polarismesh.cn/proxyCPULimit: 自动注入的 Envoy 的 cpu limit。
- polarismesh.cn/proxyMemoryLimit: 自动注入的 Envoy 的 memory limit。
