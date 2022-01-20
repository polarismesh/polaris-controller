# Polaris Controller

README：

- [介绍](#介绍)
- [快速入门](#快速入门)
- [使用指南](#使用指南)

## 介绍

Polaris Controller 是 Polaris 网格方案中的组件，部署在每个需要接入到 Polaris 的 k8s 集群中。提供将 k8s 服务自动注册到 Polaris ，和自动注入 Sidecar 的能力。

本文档将介绍如何在 k8s 集群上安装、配置 Polaris Controller 。

## 安装说明

### 环境准备

您需要先下载 Polaris 并启动，详细可参考[服务端安装指南](https://github.com/polarismesh/website/blob/main/docs/zh/doc/%E5%BF%AB%E9%80%9F%E5%85%A5%E9%97%A8/%E5%AE%89%E8%A3%85%E6%9C%8D%E5%8A%A1%E7%AB%AF/%E5%AE%89%E8%A3%85%E9%9B%86%E7%BE%A4%E7%89%88.md)。

### 安装 Polaris Controller

#### 获取 Polaris Controller 代码

```
git clone https://github.com/PolarisMesh/polaris-controller.git

cd polaris-controller/deploy/polaris-controller
```

#### 配置 Polaris Server 地址和集群信息

修改 configmap.yaml 和 polaris-controller.yaml，配置下面的参数：

- 将 `POLARIS_SERVER_URL` 替换为 Polaris server 的 ip。Polaris Controller 会将 k8s service 同步到这个 Poarlis server。
- 将 `deploy/polaris-controller/polaris-controller.yaml` 中 `polaris-controller` 容器的启动参数 `cluster-name`替换为您需要的值（默认为`default`）。北极星支持多集群接入的方案，即多个集群的同名服务，同步到北极星是一个服务，通过北极星的服务发现，可以发现多个 k8s 的同名服务的实例列表。当使用多集群方案时，您需要为多个集群指定不同的 `cluster-name` ， Polaris Controller 会只处理自己所在的集群的实例，若多个集群使用了相同的`cluster-name`，且多个集群有同名的service，会出现多个 Controller 相互覆盖对方同步到北极星上的实例的情况。

#### 配置 polaris-controller 同步模式

您可以在 polaris-controller.yaml 中配置启动参数 `sync-mode` ，指定同步模式。

##### 按集群同步

配置 `--sync-mode=ALL` ，以`集群同步模式`启动 polaris-controller。这种同步模式下会将 k8s 集群中所有的 namespace 和 service 同步到北极星。

##### 按命名空间同步

配置 `--sync-mode=NAMESPACE` ，以`命名空间同步模式`启动 polaris-controller。这种同步模式下 polaris-controller 默认不会同步任何资源。
您需要为 namespace 配置以下的 annotations 来启用这个 namespace 和其下 service 的自动同步。 

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  annotations:
    polarismesh.cn/sync: "true"
... ...
```

##### 关闭特定 service 的同步
`集群同步模式` 和 `命名空间同步模式` 下，均可以通过给特定 service 配置注解，关闭特定 service 的自动同步。例如下面的例子，
关闭 service details 的自动同步。（目前暂不支持开启特定 service 的自动同步）

```yaml
apiVersion: v1
kind: Service
metadata:
  name: details
  annotations:
    polarismesh.cn/sync: false
... ...
```


#### 运行安装脚本

您需要在安装了 kubectl 的机器上运行下面的脚本：

```
bash ./install.sh 
```

使用下面的命令观察 Polaris Controller 是否已经正常运行：

```
kubectl get pod -n polaris-system
```

正常是可以看到：

```
NAME                                  READY   STATUS    RESTARTS   AGE
polaris-controller-545df9775c-48cqt   1/1     Running   0          2d9h
```

#### 验证服务同步功能

Polaris Controller 正常运行后，登录 Polaris 的控制台，可以看到 k8s 集群中，当前的 namespace、service、endpoints 信息已经同步到了北极星中。

例如您在 samples 命名空间下部署了 istio 的 bookinfo demo，安装完 Polaris Controller 后，可以从 Polaris 的控制台上看到，北极星的命名空间中增加了 samples ，且 samples 下有 reviews、productpage、ratings、details 这四个北极星服务，每个服务下有一个北极星实例，ip 为 k8s 集群的 pod ip。

### polaris-controller 支持的注解
您可以在 k8s 的 service 和 namespace 指定注解，控制 polaris-controller 同步的行为，当前支持以下注解。

| 注解名称 |注解支持的 k8s 资源|值| 注解说明 |
|---------|--------|------|-----|
| polarismesh.cn/enableRegister（待废弃） |service|布尔值，以字符串表示，"true" 或 "false"。| 是否同步这个服务到北极星。true 同步，false 不同步，默认同步 |
| polarismesh.cn/aliasService |service|字符串| 把 k8s service 同步到北极星时，同时创建的服务别名的名字 |
| polarismesh.cn/aliasNamespace |service|字符串| 创建的别名所在的命名空间，配合 polarismesh.cn/aliasService 使用 |
| polarismesh.cn/sync |namespace/service|布尔值，以字符串表示，"true" 或 "false"。| 1. 配置在命名空间上：只在按命名空间同步模式下生效，表示打开或关闭某个 namespace 的自动同步功能。2.配置在 service 上，在按集群同步模式、按命名空间同步模式下均生效，可以配置关闭特定 service 的自动同步。  |


##### 关闭自动同步示例

polaris-controller 默认会同步 k8s 集群所有的 service。某些场景下，您可能不想同步某个 service 到北极星，这时可以使用 polarismesh.cn/enableRegister 注解关闭自动同步。

下面的 service 创建时，polaris-controller 不会在北极星创建对应的服务。

```
apiVersion: v1
kind: Service
metadata:
  name: details
  annotations:
    polarismesh.cn/enableRegister: false
... ...
```

##### 创建别名示例

polaris-controller 默认会以 service 名字，创建一个对应的北极星服务。可能有以下情况，需要创建服务别名：

1. 您不想用 service 名作为北极星服务的名字。例如您希望北极星的服务名是大写，但是 k8s 的 service 名限制只能小写。这时可以使用别名注解指定一个大写的北极星服务名。
2. 您希望将某个 namespace 下的某个 service 暴露到另外命名空间中。这时可以使用别名注解指定另一个命名空间。


下面示例的 service 创建时，polaris-controller 会在北极星的 development 命名空间下创建一个名为 productpage 的服务。同时也会在 Development 命名空间下创建一个名为 Productpage 的服务别名。

```
apiVersion: v1
kind: Service
metadata:
  namespace: development
  name: productpage
  annotations:
    polarismesh.cn/aliasService: Productpage
    polarismesh.cn/aliasNamespace: Development
... ...
```

## 使用指南

### 开启自动注入功能

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

### 自动注入配置

Polaris Controller 允许您在 pod 的 Annotations 中指定一些配置来控制自动注入的行为，目前支持的配置有：

- sidecar.istio.io/inject: 您可以对命名空间开启自动注入，并对某些特殊的 pod 配置 annotation sidecar.istio.io/inject=false ，关闭这个 pod 的自动注入。
- polarismesh.cn/proxyCPU: 自动注入的 Envoy 的 cpu request。
- polarismesh.cn/proxyMemory: 自动注入的 Envoy 的 memory request。
- polarismesh.cn/proxyCPULimit: 自动注入的 Envoy 的 cpu limit。
- polarismesh.cn/proxyMemoryLimit: 自动注入的 Envoy 的 memory limit。
