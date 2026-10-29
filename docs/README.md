# Polaris Controller 用户指南

本文介绍 Polaris Controller 的两种独立使用模式：

1. **Kubernetes Service 同步**：把 Kubernetes Service、Endpoints 和 Pod 信息同步为 Polaris Service 和实例。
2. **Sidecar 注入**：通过 Kubernetes Mutating Webhook 向 Pod 注入 DNS/Mesh Sidecar 或 Java Agent。

两种模式可以单独使用，也可以组合使用。开启注入 **不会** 自动开启 Service 同步；只开启 Service 同步也 **不会** 自动给 Pod 注入 Sidecar。

## Beta 功能警告

- Mesh 注入当前处于 Beta 调试阶段，功能尚不完善，请勿在生产环境启用。
- ConfigMap/配置同步当前处于 Beta 调试阶段，功能尚不完善，请勿在生产环境启用。
- Beta 阶段的行为、默认值、资源格式和兼容性可能变化，升级前必须重新验证。

## 选择使用模式

| 模式 | 主要作用 | 用户配置入口 | 是否注册实例 |
| --- | --- | --- | --- |
| Kubernetes Service 同步 | Kubernetes Service/Endpoints 到 Polaris 的对象同步 | Service 和 Namespace Annotation | 是，由 controller 写入 Polaris |
| Sidecar 注入 | 修改新建 Pod 的容器、入口流量和 DNS 配置 | Pod/Namespace Label 和 Pod Annotation | 注入本身不负责创建 Polaris Service |

典型选择：

- 只希望 Kubernetes 工作负载出现在 Polaris 控制台：使用 Kubernetes Service 同步。
- 只希望业务进程通过 Sidecar 或 Java Agent 接入 Polaris：使用 Sidecar 注入。
- 无代码侵入接入：同时让 Pod 获得 dnsagent 代理的服务发现能力和k8s service同步代理的服务注册能力：组合使用两种模式。

## 通用规则

- Annotation 和 Label 的 key 区分大小写。
- 文档中的 `Service` 指 Kubernetes `Service` 对象，`Pod` 指 Service 所选 Endpoints 背后的实际 Pod。
- 用户配置的布尔值通常应写成字符串，例如 `"true"` 和 `"false"`。
- Service 同步和 Sidecar 注入使用不同的匹配入口。同步看 Service/Namespace Annotation，注入入口主要由 Namespace/Pod Label 决定。
- `kube-system`、`kube-public`、`istio-system`、`polaris-system`、`kube-node-lease` 中的 Service 默认不参与同步。
- Sidecar Webhook 默认忽略 `kube-system` 和 `kube-public`。
- Service 同步要求 Service 至少有一个 `port`，并且 `spec.selector` 不能为 `nil`。

---

# 第一部分：Kubernetes Service 同步

## 一、数据流转和元数据映射

Kubernetes Service 同步的主要数据流如下：

```text
Kubernetes Service
  ├─ metadata.labels          -> Polaris Service metadata
  ├─ annotations              -> 同步开关、别名、权重、TTL 等控制项
  └─ spec.selector / ports
        └─ Kubernetes Endpoints
              └─ Endpoint TargetRef Pod
                    ├─ metadata.labels -> Polaris Instance metadata
                    └─ IP / port       -> Polaris Instance 地址
```

### 元数据对应关系

| Kubernetes 数据来源 | Polaris 写入位置 | 行为 |
| --- | --- | --- |
| Kubernetes Service `metadata.labels` | Polaris **Service metadata** | 创建 Polaris Service 时写入；更新时合并 label |
| Service Annotation `polarismesh.cn/metadata` | Polaris **Instance metadata** | 作为同一 Service 下所有实例的公共 metadata |
| 实际 Pod `metadata.labels` | Polaris **Instance metadata** | 合并到每个 Endpoint 对应实例，同名 key 覆盖 Service Annotation |
| Pod Label `version` | Polaris **Instance Version** | 未设置自定义版本时的默认值 |
| 合并后 metadata 中的 `polarismesh.cn/customVersion` | Polaris **Instance Version** | 优先级高于 Pod Label `version` |
| controller 自动写入的 `source`、`clusterName` | Polaris **Instance metadata** | 受保护字段，用户配置不能覆盖 |
| controller 自动写入的 `k8sServiceNamespace`、`k8sServiceName`、`k8sServiceUID` | Polaris **Instance metadata** | 标识实例所属 Kubernetes Service，受保护且不能覆盖 |

必须区分以下边界：

- **Service Label 不会直接写入 Instance metadata。**
- **Pod Label 不会写入 Polaris Service metadata。**
- controller 读取的是实际 Pod Label。Deployment 模板 Label 只有实际复制到 Pod 后才会被读取。
- Polaris Service metadata 和 Polaris Instance metadata 是两个不同层级的数据。
- `k8sServiceUID` 用于防止同名 Service 删除重建时误删新实例；历史实例缺少该字段时按 legacy 实例兼容。

controller 会在自身 Namespace 创建内部 ConfigMap：

```text
polaris-controller-sync-state-00
...
polaris-controller-sync-state-0f
```

这些 ConfigMap 保存 Service 同步归属和待删除状态，用于 controller 重启或 leader 切换后的恢复。不要手工修改或删除。

### 合并示例

Service 配置：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  labels:
    owner: payment
    env: prod
  annotations:
    polarismesh.cn/sync: "true"
    polarismesh.cn/metadata: '{"tenant":"service-tenant","env":"service-env"}'
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

Pod 配置：

```yaml
metadata:
  labels:
    app: demo
    tenant: pod-tenant
    version: v2
```

最终结果：

```text
Polaris Service metadata:
  owner=payment
  env=prod

Polaris Instance metadata:
  tenant=pod-tenant
  env=service-env
  app=demo
  version=v2
  source=polaris-controller
  clusterName=<当前 controller clusterName>

Polaris Instance Version:
  v2
```

`tenant` 同时出现在 Service Annotation 和 Pod Label 中，最终使用 Pod Label 的 `pod-tenant`。

## 二、同步启用判定

### Service Annotation `polarismesh.cn/sync`

作用：控制单个 Kubernetes Service 是否参与 Polaris 同步。

取值：

- `"true"`：同步该 Service、Endpoints 和 controller 管理的实例。
- `"false"`：不同步；已有的 controller 管理实例会被清理。
- 其他值：按不同步处理。
- 未设置：继续检查 `polarismesh.cn/enableRegister` 和 Namespace 配置。

优先级：

1. Service 上的 `polarismesh.cn/sync`。
2. 没有 `sync` 时检查 Service 上的 `polarismesh.cn/enableRegister`。
3. 前两项都没有决定结果时，检查 Namespace 上的 `polarismesh.cn/sync`。

示例：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  annotations:
    polarismesh.cn/sync: "true"
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

重要行为：

- Service 显式 `sync=true` 时，即使 Namespace 关闭同步，该 Service 仍保持同步。
- Service 显式 `sync=false` 时，即使 Namespace 开启同步，该 Service 也不同步。
- 一个已经同步的 Service 从 `sync=true` 改为 `sync=false`，不只是停止管理，还会清理 controller 管理的实例。

### Service Annotation `polarismesh.cn/enableRegister`

这是历史兼容的同步开关，表示“同步该 Service 并注册其实例”，而不是“让业务自行注册，controller 停止管理”。

当前行为：

- Service 没有 `polarismesh.cn/sync` 且 `enableRegister: "true"` 时，开启同步。
- 开启后，controller 仍会新增、更新和删除实例。
- 同时存在 `sync` 时，`sync` 优先。
- `sync=false` 会覆盖 `enableRegister=true`。
- `enableRegister=false` 本身不会关闭由 Namespace 开启的同步。

```yaml
metadata:
  annotations:
    polarismesh.cn/enableRegister: "true"
```

新接入建议使用 `polarismesh.cn/sync`。仅为了保持存量行为时继续使用 `enableRegister=true`。

### Namespace Annotation `polarismesh.cn/sync`

作用：设置 Namespace 下 Service 的默认同步策略。

取值：

- `"true"`：默认开启。
- `"false"`：默认关闭。
- 其他非空值：按关闭处理。
- 未设置：`demand` 模式默认关闭；`all` 模式默认开启。

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: demo
  annotations:
    polarismesh.cn/sync: "true"
```

Namespace 行为：

- `demand` 模式下，未显式配置 Service 的 Namespace 必须设置 `sync=true` 才会同步。
- `all` 模式下，未设置 Namespace 配置的 Service 默认同步。
- Namespace 从关闭改为开启时，存量 Service 会被重新投递。
- Namespace 从开启改为关闭时，没有显式开启的 Service 会退出同步。
- Service 显式 `sync=true` 不受 Namespace 关闭影响。

### controller `serviceSync.mode`

Service 同步模式属于 controller 配置，不属于 Kubernetes Annotation：

```yaml
serviceSync:
  mode: demand
```

也可以通过启动参数指定：

```sh
--sync-mode=demand
```

取值：

- `demand`：按需同步，Service 未显式开启时需要 Namespace 设置 `polarismesh.cn/sync=true`。
- `all`：默认同步 Namespace 内的有效 Service，Namespace 仍可用 `sync=false` 关闭。

启动参数优先于 YAML 配置。当前代码未显式配置模式时走 `all` 行为。

### 多端口 Service 只同步指定端口

当前 controller 的同步粒度是 Kubernetes Service。一个 Service 开启同步后，controller 会同步该 Service 对应的全部 Endpoint 端口；当前没有按端口过滤的 Annotation。

如果只希望把部分端口同步到 Polaris，可以为每个目标端口创建一个单端口 Service，并让它们选择同一组 Pod。原来的多端口 Service 可以继续用于 Kubernetes 集群内访问，但必须关闭 Polaris 同步。

假设业务原本通过 `demo` 提供 `http` 和 `grpc` 两个端口：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  annotations:
    polarismesh.cn/sync: "false"
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      targetPort: 8080
    - name: grpc
      port: 9090
      targetPort: 9091
```

只为需要同步的端口创建单端口 Service。下面两个 Service 分别同步到 Polaris：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo-http
  annotations:
    polarismesh.cn/sync: "true"
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      targetPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: demo-grpc
  annotations:
    polarismesh.cn/sync: "true"
spec:
  selector:
    app: demo
  ports:
    - name: grpc
      port: 9090
      targetPort: 9091
```

最终 Polaris 中会得到两个独立 Service：

| Polaris Service | Instance |
| --- | --- |
| `demo-http` | Pod IP `:8080` |
| `demo-grpc` | Pod IP `:9091` |

如果只需要同步 `http`，不要创建 `demo-grpc`；如果两个端口都需要同步，但需要在 Polaris 中使用不同的服务名，可以分别使用。

注意事项：

- 单端口 Service 的 `selector` 必须与工作负载 Pod Label 匹配，`targetPort` 必须是该 Pod 实际监听的端口。
- `all` 同步模式下，必须给原来的多端口 Service 显式设置 `polarismesh.cn/sync: "false"`，否则它仍会默认同步全部端口。
- 当前不支持从同一个 Kubernetes Service 中只选择部分端口并继续写入同一个 Polaris Service。如果需要这种能力，需要 controller 增加端口级过滤配置。

## 三、Polaris 侧别名

Polaris Service 的 Namespace 和 Name 直接使用 Kubernetes Service 自身的 Namespace 和 Name。

### `polarismesh.cn/aliasNamespace` 和 `polarismesh.cn/aliasService`

类型：Service Annotation。

作用：创建指向当前 Polaris Service 的别名。

alias 与 Polaris Service 名称是一对一关系：

- 每个 Kubernetes Service 最多声明一个 alias。
- 每个 alias 在同一时刻只能指向一个 Polaris Service。
- 多个 Kubernetes Service 声明相同 alias 时，最早创建的 Service 获得绑定；其他 Service 等待。
- 当前 winner 移除 alias Annotation、关闭同步或删除后，剩余 Service 中最早创建的一个接管。

两个值必须同时非空才会创建别名。只设置其中一个不会创建或删除别名。

```yaml
annotations:
  polarismesh.cn/sync: "true"
  polarismesh.cn/aliasNamespace: Production
  polarismesh.cn/aliasService: demo.alias
```

注意：

- 别名指向当前 Kubernetes Service 对应的 Polaris Namespace/Service。
- alias 是全局唯一的服务入口，不用于把多个 Polaris Service 聚合为同一个名称。
- controller 会周期核对 alias：不存在时创建，已存在但指向旧 Service 时更新指向。
- 修改 alias 名称时只为新 alias 建立绑定，不删除旧 alias。
- 删除或修改别名配置前，需要人工确认并清理旧别名。

## 四、Instance metadata 生命周期

### Service Annotation `polarismesh.cn/metadata`

作用：声明写入同一 Service 下所有 Polaris 实例的公共 metadata。

取值：JSON object，key 和 value 都必须是字符串。

```yaml
annotations:
  polarismesh.cn/metadata: '{"tenant":"t1","env":"prod"}'
```

合并规则：

1. 读取 Service Annotation 中的 JSON object。
2. 合并 Endpoint TargetRef 对应 Pod 的 `metadata.labels`。
3. 同名 key 使用 Pod Label。
4. 保留部分已有默认兼容字段。
5. 最后强制写入 `source=polaris-controller`。
6. 最后强制写入 `clusterName=<当前 controller clusterName>`。

`source` 和 `clusterName` 不能被 Pod Label 或 Service Annotation 覆盖。

字段生命周期：

- controller 按 Service Annotation 和 Pod Label 的完整声明集合管理业务 metadata。
- 从两个来源同时移除一个业务字段后，后续实例更新会从 Polaris Instance metadata 删除该字段。
- 直接在 Polaris 中添加、但两个来源都没有声明的业务字段，会在后续更新中被删除。
- 兼容字段 `version`、`protocol` 和旧版 `platform` 在两个来源都未声明时保留 Polaris 现有值。

Pod Label 示例：

```yaml
metadata:
  labels:
    app: demo
    tenant: t1
    version: v2
```

仅修改 Pod Label、不修改 Endpoints 时，metadata 通过周期对账收敛。默认 Polaris 对账周期是 30 秒，实际生效时间还包含队列处理时间。

对账周期可以通过 `--resync-duration` 或 YAML `server.resyncDuration` 调整。这里的周期是 controller 主动对 Polaris 数据执行完整对账的间隔，不是 Kubernetes Informer 的 `--min-resync-period`。

`polarismesh.cn/metadata` 不是合法 JSON object 时，controller 会返回错误并重试，不会把非法配置当成空 metadata 删除已有字段。

### `polarismesh.cn/protocols`

类型：Service Annotation。

作用：按 Service 端口名自定义 Polaris Instance 顶层 `protocol` 字段。

取值：JSON object，key 和 value 都必须是字符串。key 必须是当前 Service 中已命名端口的 `name`。

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  annotations:
    polarismesh.cn/protocols: '{"http":"http","grpc":"grpc"}'
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      protocol: TCP
    - name: grpc
      port: 9090
      protocol: TCP
    - name: admin
      port: 8081
      protocol: UDP
```

实例顶层 `protocol` 的取值规则：

1. 命名端口出现在 `polarismesh.cn/protocols` 中时，使用注解 value。
2. 命名端口未出现在注解中时，使用 Kubernetes EndpointPort 的 `protocol`。
3. 未命名端口不参与该注解匹配，直接使用 Kubernetes EndpointPort 的 `protocol`。
4. EndpointPort 的 `protocol` 为空时，兜底为 `TCP`。

上例最终写入：

| 端口名 | Kubernetes Protocol | Instance protocol |
| --- | --- | --- |
| `http` | `TCP` | `http` |
| `grpc` | `TCP` | `grpc` |
| `admin` | `UDP` | `UDP` |

校验规则：

- 注解必须是合法 JSON object，且 key/value 都是字符串。
- key 不能为空，也不能引用当前 Service 不存在的端口名。
- value 必须非空，不能包含首尾空白，长度不能超过 32 字符。
- 非法配置会导致本次同步失败并重试，不会静默回落到 Kubernetes Protocol。

该注解只控制 Polaris Instance 顶层 `protocol` 字段，不会写入或修改 Instance metadata 中的 `protocol`。如需自定义 Instance metadata，使用 `polarismesh.cn/metadata` 或 Pod Label。

删除或移除某个端口的映射后，下一次对账会回退到对应 Kubernetes EndpointPort 的 `protocol`。

### `polarismesh.cn/customVersion`

这不是直接读取的 Service Annotation，而是合并后 metadata 中使用的 key。

```yaml
annotations:
  polarismesh.cn/metadata: '{"polarismesh.cn/customVersion":"v2"}'
```

版本优先级：

1. metadata 中的 `polarismesh.cn/customVersion`。
2. Pod Label `version`。
3. 两者都不存在时，controller 不管理实例顶层 Version，保留 Polaris 当前值。

控制台手工修改的实例顶层 Version：

- 存在上述 Kubernetes 来源时，会在下一轮对账恢复为 Kubernetes 的值。
- 两个来源都不存在时，由用户或控制台管理，controller 不会在后续对账中覆盖。

Version 生命周期：

- 新增实例时，按 `customVersion`、Pod Label `version` 的优先级写入顶层 Version。
- 修改 `customVersion` 或 Pod Label `version` 时，metadata 和顶层 Version 会随实例 Update 收敛。
- 删除全部 Version 来源时，controller 停止管理顶层 Version，保留删除前的值；之后控制台修改的
  值也不会被覆盖。
- 周期对账只在 Version 来源存在时检查顶层 Version；来源不存在时不会清理控制台手工值。
- 删除 Endpoint、Service 或关闭同步时，实例仍按 Service、host、port 删除，Version 不参与实例
  身份和删除判断。

如果需要让 Service Annotation 中的 `version` 只作为 Instance metadata，而不影响顶层 Version，
可以继续使用 `polarismesh.cn/metadata`；它不会驱动顶层 Version。Pod Label `version` 则会同时
写入 Instance metadata 和顶层 Version。

### Polaris Service metadata 和 Service Label

Kubernetes Service `metadata.labels` 写入 Polaris Service metadata，而不是 Instance metadata。

当前更新逻辑会把 Kubernetes Service Label 合并到 Polaris Service metadata：

- 新增 Label 会写入。
- 同名 Label 的值变化会覆盖 Polaris 现有值。
- 从 Kubernetes Service 删除 Label 不会同步删除 Polaris Service metadata 中的旧字段。

需要严格删除 Polaris Service metadata 时，应人工确认并清理。

### 实例归属和自注册实例

实例顶层 `protocol` 字段默认同步 Kubernetes EndpointPort 的 `protocol`；配置 `polarismesh.cn/protocols` 时，命名端口按映射值覆盖。EndpointPort 的 `protocol` 未显式填写时按 Kubernetes 默认值 `TCP` 处理；端口名不会参与拼接，避免超过 Polaris 的 32 字符限制。已存在的历史拼接值会在后续实例更新中被改写为最终映射值或 EndpointPort 的 `protocol`。

controller 只把当前被判定为 controller 管理的实例纳入更新和删除集合。识别依据是：

```text
clusterName == 当前 controller clusterName
并且 (source == polaris-controller 或 platform == polaris-controller)
```

其中 `platform` 是兼容旧版的归属字段。实例同步保留两份按 `host:port` 索引的集合：

- `currentAll`：该 Polaris Service 下的全部已有实例，用于判断是否需要新增。SDK/Java Agent 或其他集群已注册同一地址时，不再重复 Add。
- `currentController`：符合上述归属条件的实例，用于更新和删除。SDK/Java Agent 实例不符合该条件时，不会因 protocol、健康状态、权重或 metadata 差异被 Update。

删除范围不因全量索引而扩大：Endpoint 中已消失的地址，只有属于 `currentController` 才会被删除。没有 controller 归属标记的 SDK/Java Agent 实例、没有集群标记的实例，以及其他集群的实例均不会被删除。

如果自注册实例仍带有当前集群的 controller 归属标记，则仍按 controller 托管实例处理，可能被更新或删除；仅启用心跳不会改变这一归属判定。

## 五、权重、工作负载和 TTL

### `polarismesh.cn/weight`

类型：Service Annotation。

作用：为该 Service 下所有实例设置默认权重。

默认值：`100`。

建议取值：整数字符串 `0` 到 `100`。

当前代码没有在本地完整校验范围，超过建议范围的值取决于 Polaris Server 的处理。

```yaml
annotations:
  polarismesh.cn/weight: "80"
```

### `polarismesh.cn/customWeight`

类型：Service Annotation。

作用：按 StatefulSet Pod 序号和端口覆盖单个实例权重。

格式：

```json
{"<pod-index>-<port>": <weight>}
```

```yaml
annotations:
  polarismesh.cn/sync: "true"
  polarismesh.cn/workloadKind: "StatefulSet"
  polarismesh.cn/weight: "50"
  polarismesh.cn/customWeight: '{"0-8080":80,"1-8080":20}'
```

规则：

- 权重应为 `0` 到 `100`。
- 越界值回退到 `polarismesh.cn/weight`。
- 非法 JSON 会记录日志并视为没有自定义权重。
- Pod 序号来自 StatefulSet Pod 名称最后一段，例如 `demo-0` 的序号是 `0`。
- 非 StatefulSet 工作负载没有稳定序号，使用 `polarismesh.cn/weight`。

### `polarismesh.cn/workloadKind`

类型：Service Annotation。

作用：告知 controller 对应工作负载类型，影响 StatefulSet 序号解析和工作负载类型变化时的实例替换。

当前识别为 StatefulSet 的值：

```text
statefulset
statefulsetplus
StatefulSetPlus
StatefulSet
```

其他值按非 StatefulSet 处理。

### `polarismesh.cn/ttl`

类型：Service Annotation。

作用：声明 controller 是否管理实例心跳健康检查，以及心跳 TTL。

`polarismesh.cn/ttl` 使用三态语义：

- Annotation 不存在：controller 不管理健康检查，实例 Update 不发送 `enableHealthCheck` 和
  `healthCheck`，保留控制台或其他写入方的设置。
- `0`：明确关闭健康检查。
- `1` 到 `60`：开启心跳健康检查，并设置对应 TTL。

非法值、负数或大于 `60` 的值不会被当成“关闭健康检查”。controller 会记录错误、产生
Event，并停止本轮实例写入，避免一次配置错误改变现有健康状态。

```yaml
annotations:
  polarismesh.cn/ttl: "10"
```

修改有效 TTL（例如从 `10` 改为 `20`）会触发实例 Update。删除该 Annotation 后，controller
不再管理该 Service 下实例的健康检查，不会主动恢复或关闭修改前的状态。

controller 只负责注册带 TTL 的实例，不负责代替业务持续发送心跳。使用 TTL 时，必须确认业务实例或外部组件会发送心跳。

## 六、Endpoint 和健康状态

controller 根据 Endpoint 地址同步实例：

- Ready Address 注册为正常实例。
- NotReady Address 仍会同步，但以隔离状态注册，避免服务发现完全丢失该地址。
- Pod 实例 metadata 来自 Endpoint 的 `TargetRef` 指向的实际 Pod。
- Endpoint 没有可解析的 Pod TargetRef，或 Pod 查询失败时，本轮实例对账应停止并重试，避免用不完整 metadata 误删字段。

Service 的 `spec.selector` 用于 Kubernetes 选择 Endpoint，不会直接成为 Polaris Instance metadata。

## 七、Service 同步示例

### 仅同步一个 Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  labels:
    owner: payment
  annotations:
    polarismesh.cn/sync: "true"
    polarismesh.cn/metadata: '{"tenant":"t1"}'
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

对应 Pod：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
        tenant: t1
        version: v2
    spec:
      containers:
        - name: app
          image: example/demo:latest
          ports:
            - containerPort: 8080
```

结果：

- Polaris Service metadata 包含 `owner=payment`。
- Polaris Instance metadata 包含 `app=demo`、`tenant=t1`、`version=v2`、`source=polaris-controller` 和 `clusterName`。
- `tenant` 如果在 Service Annotation 和 Pod Label 中都存在，以 Pod Label 为准。

### 兼容历史 `enableRegister=true`

```yaml
metadata:
  annotations:
    polarismesh.cn/enableRegister: "true"
    polarismesh.cn/aliasNamespace: Production
    polarismesh.cn/aliasService: demo.alias
    polarismesh.cn/weight: "100"
    polarismesh.cn/workloadKind: Deployment
```

该配置会开启同步，实例仍由 controller 注册和维护。

### StatefulSet 按 Pod 调权

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  annotations:
    polarismesh.cn/sync: "true"
    polarismesh.cn/workloadKind: StatefulSet
    polarismesh.cn/weight: "50"
    polarismesh.cn/customWeight: '{"0-8080":80,"1-8080":20}'
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

---

# 第二部分：Sidecar 注入

## 八、Admission 命中规则

Pod 进入 Webhook 和 Webhook 最终决定是否注入是两个步骤。

当前 Helm 通过任一条件让 Pod 进入 Webhook：

- Namespace 带有 Label `polarismesh.cn/inject=enabled`。
- Pod 带有 Label `polarismesh.cn/inject=enabled`。
- 部分旧版 Manifest 仍配置 Namespace Label `polaris-injection=enabled`。

因此，只在 Pod Annotation 中写 `polarismesh.cn/inject=true`，但 Namespace 和 Pod Label 都未命中时，Webhook 可能根本不会被调用。

### Pod Annotation `polarismesh.cn/inject`

作用：在 Pod 已进入 Webhook 后，明确开启或关闭注入。

取值不区分大小写，可识别：

- 开启：`y`、`yes`、`true`、`on`、`enable`、`enabled`
- 关闭：`n`、`no`、`false`、`off`、`disable`、`disabled`

优先级高于 Pod Label、Injection Policy 和 Namespace 默认策略。

```yaml
metadata:
  annotations:
    polarismesh.cn/inject: "true"
```

### Pod Annotation `sidecar.polarismesh.cn/inject`

旧版兼容 Annotation。

- `polarismesh.cn/inject` 未设置时作为 fallback。
- 两者都存在时，`polarismesh.cn/inject` 优先。

新配置应使用 `polarismesh.cn/inject`。

### Label `polarismesh.cn/inject`

作用：

- Namespace Label `enabled`：让 Namespace 内 Pod 进入 Webhook。
- Pod Label `enabled`：让该 Pod 进入 Webhook。
- Pod Label `disabled`：当前模板的 `neverInjectSelector` 会阻止注入。

```sh
kubectl label namespace demo polarismesh.cn/inject=enabled
kubectl label pod demo-0 polarismesh.cn/inject=disabled
```

Pod Annotation 的明确开关优先于 Label。

当前 Helm 模板的默认 policy 是 `enabled`，并使用 `neverInjectSelector` 排除带 `polarismesh.cn/inject=disabled` Label 的 Pod。自定义安装修改 `alwaysInjectSelector`、`neverInjectSelector` 或 policy 后，以实际配置为准。

### 不注入的情况

- `polarismesh.cn/inject` 明确设置为关闭值。
- Pod 命中 `neverInjectSelector`。
- Pod 位于 `kube-system` 或 `kube-public`。
- Mesh/DNS 模式下 Pod 使用 `hostNetwork`。

Java Agent 模式不受 `hostNetwork` 这一限制。

## 九、注入模式选择

模式选择优先级从高到低：

1. Pod Annotation `polarismesh.cn/javaagent: "true"`。
2. Pod Annotation `sidecar.polarismesh.cn/openOnDemand: "true"`。
3. Pod Annotation `polarismesh.cn/sidecar-mode`。
4. Namespace Label `polaris-sidecar-mode`。
5. Webhook 配置的默认 sidecar mode。

可识别的模式：

| 模式 | 注入内容 | 主要用途 |
| --- | --- | --- |
| `dns` | `polaris-sidecar` | 拦截和解析 Polaris DNS |
| `mesh` | `polaris-sidecar` 和 Envoy | 流量拦截和 Mesh 访问，当前为 Beta |
| `java-agent` | Java Agent init container，并修改业务容器启动参数 | Java 应用接入 |

### Pod Annotation `polarismesh.cn/sidecar-mode`

取值：

```text
mesh
dns
java-agent
```

匹配区分大小写。其他值会按 `mesh` 处理，因此不要把错误拼写留在配置中。

```yaml
metadata:
  annotations:
    polarismesh.cn/sidecar-mode: dns
```

该 Annotation 只在 Pod 上读取，配置在 Service 上不会生效。

### Namespace Label `polaris-sidecar-mode`

作用：为 Namespace 内 Pod 提供默认注入模式。

```sh
kubectl label namespace demo polaris-sidecar-mode=mesh
```

Pod 级模式 Annotation 优先于 Namespace Label。

### `sidecar.polarismesh.cn/openOnDemand`

类型：Pod Annotation。

作用：选择 Mesh 模式并开启按需 XDS 能力。

```yaml
metadata:
  annotations:
    sidecar.polarismesh.cn/openOnDemand: "true"
```

只有精确值 `"true"` 会触发该行为。它不是通用关闭开关。

### `polarismesh.cn/javaagent`

类型：Pod Annotation。

作用：选择 Java Agent 模式。

```yaml
metadata:
  annotations:
    polarismesh.cn/javaagent: "true"
```

只有精确值 `"true"` 会触发 Java Agent 模式。它优先于 Mesh、`sidecar-mode` 和 Namespace 默认模式。

## 十、Sidecar 数据映射

Sidecar 注入中的“元数据”与 Kubernetes Service 同步的 Instance metadata 不是同一层数据。

| Sidecar 数据来源 | 去向 | 行为 |
| --- | --- | --- |
| Pod `metadata.labels` | Mesh 的 `sidecar.polarismesh.cn/envoyMetadata` | 合并为 Envoy XDS metadata |
| Pod Label `app` | Mesh 服务名默认值 | 没有显式 `serviceName` 时使用 |
| Annotation `sidecar.polarismesh.cn/serviceName` | Mesh/Java Agent 服务身份 | 优先于按 `app` 或工作负载推导 |
| Annotation `sidecar.polarismesh.cn/serviceNamespace` | 服务 Namespace | Mesh 跨 Namespace 支持不完整 |
| 工作负载名称 | 服务名 fallback | 无法从 Annotation 或 `app` 推导时使用 |
| `polarismesh.cn/javaagentConfig` | Java Agent application properties | 覆盖推导值和默认 properties |

明确边界：

- Mesh 注入把 Pod Label 写入 Envoy XDS metadata，但这不等于 controller 把 Pod Label 写入了 Polaris Naming Instance metadata。
- 要让实例出现在 Polaris Naming 中，仍需业务注册、Java Agent 注册或开启 Kubernetes Service 同步。
- Kubernetes Service Label 不参与 Sidecar 服务身份推导。

### `sidecar.polarismesh.cn/serviceName`

类型：Pod Annotation。

作用：指定 Sidecar 注册或发现时使用的服务名。

Mesh 模式优先级：

1. `sidecar.polarismesh.cn/serviceName` Annotation。
2. Pod Label `app`。
3. 工作负载名称。

Java Agent 场景推荐使用 `javaagentConfig` 或 `workloadNameAsServiceName` 设置服务名。

### `sidecar.polarismesh.cn/serviceNamespace`

类型：Pod Annotation。

设计作用：指定 Sidecar 服务所在 Namespace。

Mesh 模式未设置时使用 Pod Namespace。

当前 Mesh 元数据主要通过 Pod Namespace 推导，跨 Namespace 支持不完整。需要跨 Namespace 时，应先在真实环境验证。

Java Agent 场景优先使用：

- `polarismesh.cn/workloadNamespaceAsServiceNamespace: "true"`，或
- `javaagentConfig` 中的 `spring.cloud.polaris.discovery.namespace`。

## 十一、DNS 和 Mesh 参数

> Mesh 相关能力当前为 Beta，仅用于测试和验证，不要用于生产环境。

### `polarismesh.cn/tls-mode`

类型：Pod Annotation。

作用：控制 Mesh Envoy 的 mTLS 模式。

取值：

- `none`：关闭。
- `strict`：严格 mTLS。
- `permissive`：兼容模式。

```yaml
metadata:
  annotations:
    polarismesh.cn/tls-mode: strict
```

非 `none` 模式下，controller 会确保目标 Namespace 存在根证书 Secret。

### `polarismesh.cn/sidecarConfig`

类型：Pod Annotation。

作用：以 JSON 配置 `polaris-sidecar` 的递归、DNS、地域和日志参数。

```yaml
metadata:
  annotations:
    polarismesh.cn/sidecarConfig: |
      {
        "recurse": {
          "enabled": false,
          "timeout": 500
        },
        "dns": {
          "suffix": "cluster.local",
          "ttl": 30
        },
        "location": {
          "region": "beijing",
          "zone": "zone-a",
          "campus": "campus-1",
          "match_level": "zone"
        },
        "log": {
          "output_level": "info",
          "rotation_max_size": 100,
          "rotation_max_age": 7,
          "rotation_max_backups": 10
        }
      }
```

字段说明：

- `dns.suffix`、`dns.ttl`：DNS 模式使用，TTL 必须大于 0。
- `recurse.enabled`：是否开启递归。
- `recurse.timeout`：大于 0 时生效。
- `location.region`、`zone`、`campus`：写入 Sidecar 地域环境变量。
- `location.match_level`：支持 `region`、`zone`、`campus`。
- `log.output_level`：支持 `debug`、`info`、`warn`、`error`、`fatal`。
- `log.rotation_max_size`、`rotation_max_age`、`rotation_max_backups`：大于 0 时生效。

JSON 非法时注入会失败。

### Envoy 资源

类型：Pod Annotation。

作用：覆盖 Mesh Envoy 容器的资源。

| Annotation | 资源字段 |
| --- | --- |
| `polarismesh.cn/proxyCPU` | `requests.cpu` |
| `polarismesh.cn/proxyMemory` | `requests.memory` |
| `polarismesh.cn/proxyCPULimit` | `limits.cpu` |
| `polarismesh.cn/proxyMemoryLimit` | `limits.memory` |

```yaml
metadata:
  annotations:
    polarismesh.cn/proxyCPU: "100m"
    polarismesh.cn/proxyMemory: "128Mi"
    polarismesh.cn/proxyCPULimit: "500m"
    polarismesh.cn/proxyMemoryLimit: "512Mi"
```

值必须符合 Kubernetes resource quantity 格式。这些 Annotation 只覆盖 Envoy，不覆盖 `polaris-sidecar`。

### 流量拦截端口

类型：Pod Annotation，Mesh 模式使用。

| Annotation | 作用 | 默认值 |
| --- | --- | --- |
| `polarismesh.cn/includeInboundPorts` | 入站重定向端口 | `*` |
| `polarismesh.cn/excludeInboundPorts` | 排除的入站端口 | `15985,50000,15053` |
| `polarismesh.cn/excludeOutboundPorts` | 排除的出站端口 | 空 |
| `polarismesh.cn/excludeOutboundCIDRs` | 排除的出站 CIDR，逗号分隔 | 空 |

```yaml
metadata:
  annotations:
    polarismesh.cn/includeInboundPorts: "*"
    polarismesh.cn/excludeInboundPorts: "15985,50000,15053,8080"
    polarismesh.cn/excludeOutboundPorts: "3306"
    polarismesh.cn/excludeOutboundCIDRs: "169.254.0.0/16"
```

端口必须是合法整数端口，CIDR 必须符合 CIDR 格式。

### `security.polarismesh.cn/autoMTLS`

当前代码虽然生成了该 Annotation 定义，但实际读取的是 `polarismesh.cn/tls-mode`。

设置 `security.polarismesh.cn/autoMTLS` 当前不会改变注入行为，不建议使用。

## 十二、Java Agent 参数

### `polarismesh.cn/javaagentVersion`

类型：Pod Annotation。

作用：指定 Java Agent init 镜像或 Agent 版本。

```yaml
metadata:
  annotations:
    polarismesh.cn/javaagentVersion: "1.7.0-RC5"
```

旧 Agent 版本可能需要在 `JAVA_TOOL_OPTIONS` 中带版本号路径；不要只更新镜像 tag 而忽略实际兼容性。

### `polarismesh.cn/javaagentFrameworkName`

类型：Pod Annotation。

作用：指定应用框架名称，例如 `spring-cloud`。

通常需要与 `javaagentFrameworkVersion` 一起设置。

### `polarismesh.cn/javaagentFrameworkVersion`

类型：Pod Annotation。

作用：指定应用框架版本。

### `polarismesh.cn/javaagentConfig`

类型：Pod Annotation。

作用：以 JSON object 覆盖 Java Agent 应用配置。

```yaml
metadata:
  annotations:
    polarismesh.cn/javaagent: "true"
    polarismesh.cn/javaagentVersion: "v1.10.0"
    polarismesh.cn/javaagentConfig: |
      {
        "spring.application.name": "demo",
        "spring.cloud.polaris.discovery.namespace": "Production"
      }
```

key 和 value 都应为字符串。用户显式配置会覆盖推导值和默认 properties。

### `polarismesh.cn/workloadNameAsServiceName`

类型：Pod Annotation。

取值：`"true"`。

作用：在 Java Agent 模式下，使用工作负载名称作为服务名。

### `polarismesh.cn/workloadNamespaceAsServiceNamespace`

类型：Pod Annotation。

取值：`"true"`。

作用：在 Java Agent 模式下，使用 Pod 所在 Namespace 作为 Polaris 服务 Namespace。

未开启时，Java Agent 默认模板可能使用 `default` Namespace。需要跟随工作负载 Namespace 时应显式开启。

冲突优先级：`javaagentConfig` 中显式配置的 `spring.application.name` 或 `spring.cloud.polaris.discovery.namespace` 优先。

## 十三、自动生成和内部 Annotation

以下 key 不建议用户手工设置。

### `sidecar.polarismesh.cn/status`

由注入 Webhook 自动生成，记录已注入的 init container、container、volume 和 imagePullSecret。

不要手工修改或删除，否则可能影响重复注入或卸载兼容处理。

### `sidecar.polarismesh.cn/envoyMetadata`

Mesh 注入时由 controller 根据 Pod Label、服务名、Namespace 和 TLS 配置生成。

当前 Mesh 注入会覆盖用户手写的值，不建议直接配置。

### `sidecar.polarismesh.cn/bindPorts`

代码中定义了该常量，但当前实现没有读取或下发。设置它不会产生效果。

### 当前模板不消费的兼容键

以下键出现在兼容或校验列表中，但当前实际注入模板没有读取它们，不应作为用户功能使用：

```text
readiness.status.sidecar.polarismesh.cn/applicationPorts
readiness.status.sidecar.polarismesh.cn/failureThreshold
readiness.status.sidecar.polarismesh.cn/initialDelaySeconds
readiness.status.sidecar.polarismesh.cn/periodSeconds
sidecar.polarismesh.cn/bootstrapOverride
sidecar.polarismesh.cn/discoveryAddress
sidecar.polarismesh.cn/enableCoreDump
sidecar.polarismesh.cn/interceptionMode
sidecar.polarismesh.cn/logLevel
sidecar.polarismesh.cn/proxyCPU
sidecar.polarismesh.cn/proxyImage
sidecar.polarismesh.cn/proxyMemory
sidecar.polarismesh.cn/rewriteAppHTTPProbers
sidecar.polarismesh.cn/statsInclusionPrefixes
sidecar.polarismesh.cn/statsInclusionRegexps
sidecar.polarismesh.cn/statsInclusionSuffixes
sidecar.polarismesh.cn/userVolume
sidecar.polarismesh.cn/userVolumeMount
status.sidecar.polarismesh.cn/port
traffic.sidecar.polarismesh.cn/excludeInboundPorts
traffic.sidecar.polarismesh.cn/excludeOutboundIPRanges
traffic.sidecar.polarismesh.cn/excludeOutboundPorts
traffic.sidecar.polarismesh.cn/includeInboundPorts
traffic.sidecar.polarismesh.cn/includeOutboundIPRanges
```

尤其注意：

- Pod 资源使用 `polarismesh.cn/proxyCPU` 和 `polarismesh.cn/proxyMemory`。
- 流量端口使用 `polarismesh.cn/includeInboundPorts` 等 Annotation。
- `sidecar.polarismesh.cn/proxyCPU` 和 `traffic.sidecar.polarismesh.cn/*` 当前不是生效配置。

ConfigMap/配置同步当前处于 Beta 阶段且没有对外暴露。`polarismesh.cn/configGroup`、`polarismesh.cn/enableEncrypt`、`polarismesh.cn/enableEncryptAlog`、`internal-sync-source` 等内部 key 不应提供给用户使用。

## 十四、Sidecar 注入示例

### DNS 模式

```sh
kubectl label namespace demo polarismesh.cn/inject=enabled
kubectl label namespace demo polaris-sidecar-mode=dns
```

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
      annotations:
        polarismesh.cn/inject: "true"
    spec:
      containers:
        - name: app
          image: example/demo:latest
```

### Mesh 模式

> 该示例仅用于 Beta 验证环境，请勿在生产环境执行。

```sh
kubectl label namespace demo polarismesh.cn/inject=enabled
kubectl label namespace demo polaris-sidecar-mode=mesh
```

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
      annotations:
        polarismesh.cn/tls-mode: strict
        polarismesh.cn/includeInboundPorts: "*"
        polarismesh.cn/excludeInboundPorts: "15985,50000,15053,8080"
    spec:
      containers:
        - name: app
          image: example/demo:latest
          ports:
            - containerPort: 8080
```

### Java Agent 模式

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
      annotations:
        polarismesh.cn/inject: "true"
        polarismesh.cn/javaagent: "true"
        polarismesh.cn/workloadNameAsServiceName: "true"
        polarismesh.cn/workloadNamespaceAsServiceNamespace: "true"
        polarismesh.cn/javaagentConfig: |
          {
            "spring.cloud.polaris.discovery.namespace": "Production"
          }
    spec:
      containers:
        - name: app
          image: example/java-app:latest
```

## 十五、组合使用示例

以下示例同时开启 Service 同步和 DNS 注入：

```sh
kubectl label namespace demo polarismesh.cn/inject=enabled
kubectl label namespace demo polaris-sidecar-mode=dns
```

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  labels:
    owner: payment
  annotations:
    polarismesh.cn/sync: "true"
    polarismesh.cn/metadata: '{"tenant":"t1","env":"prod"}'
spec:
  selector:
    app: demo
  ports:
    - name: http
      port: 80
      targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
        tenant: t1
        version: v2
    spec:
      containers:
        - name: app
          image: example/demo:latest
          ports:
            - containerPort: 8080
```

这个组合中：

- Service Label `owner=payment` 写入 Polaris Service metadata。
- Pod Label 写入 Polaris Instance metadata。
- Sidecar 注入由 Namespace Label 和 Pod 选择器触发。
- DNS Sidecar 负责 DNS 能力，Service 同步负责 Polaris Naming 数据。

## 十六、常见错误

### 误以为 Service Label 会出现在实例上

错误理解：

```text
Kubernetes Service metadata.labels -> Polaris Instance metadata
```

实际行为：

```text
Kubernetes Service metadata.labels -> Polaris Service metadata
实际 Pod metadata.labels          -> Polaris Instance metadata
```

如果希望所有实例都带某个公共字段，使用 Service Annotation `polarismesh.cn/metadata`；如果需要按实例区分，使用 Pod Label。

### 使用 `enableRegister=false` 关闭同步

```yaml
polarismesh.cn/enableRegister: "false"
```

这不会关闭一个由 `sync=true` 或 Namespace 开启的 Service。需要关闭时使用：

```yaml
polarismesh.cn/sync: "false"
```

### 把 `polarismesh.cn/metadata` 写成 YAML object

错误：

```yaml
polarismesh.cn/metadata:
  tenant: t1
```

正确：

```yaml
polarismesh.cn/metadata: '{"replicas":"3","enabled":"true"}'
```

value 应为字符串，不要写数字或布尔值：

```yaml
polarismesh.cn/metadata: '{"replicas":3}'
```

### 以为删除 alias Annotation 会删除已创建别名

同时删除 `aliasNamespace` 和 `aliasService` 不会删除 Polaris 中已经创建的别名，需要人工确认和清理。

### 只写注入 Annotation，没有让 Webhook 命中

当前 Helm 默认通过 Namespace/Pod Label `polarismesh.cn/inject=enabled` 选择 Pod。仅在 Pod Annotation 中写 `polarismesh.cn/inject=true`，但 Namespace 和 Pod Label 都没命中时，Webhook 可能不会被调用。

### 使用不生效的 Sidecar 资源 key

错误：

```yaml
sidecar.polarismesh.cn/proxyCPU: "100m"
```

当前应使用：

```yaml
polarismesh.cn/proxyCPU: "100m"
```

### 把未知注入模式当成关闭开关

`polarismesh.cn/sidecar-mode` 的值只能使用 `mesh`、`dns`、`java-agent`。其他值会按 `mesh` 处理。关闭注入应使用 `polarismesh.cn/inject: "false"`。

### 期望 ConfigMap 同步 Annotation 生效

ConfigMap 同步当前没有对外暴露。不要把 `polarismesh.cn/configGroup` 等内部 Annotation 写进用户配置。

## 十七、验证方法

### 验证 Service 同步配置

```sh
kubectl get service demo -o yaml
kubectl get namespace demo -o yaml
kubectl get endpoints demo -o yaml
```

确认：

- Service 有 selector 和 port。
- `sync` / `enableRegister` 的值符合预期。
- alias Namespace 和 Service 成对配置。
- `polarismesh.cn/metadata` 是合法 JSON。
- Endpoint 的 `TargetRef` 指向实际 Pod。

### 验证 Polaris Service 和实例

在 Polaris 控制台或 API 中确认：

- Polaris Namespace 和 Service 名称与 Kubernetes Namespace 和 Service 名称一致。
- Polaris Service metadata 来自 Kubernetes Service Label。
- 实例数量与 Endpoints 一致。
- Instance metadata 来自 Service Annotation 和 Pod Label。
- 实例 Version 来自 `polarismesh.cn/customVersion` 或 Pod Label `version`。
- `source=polaris-controller`。
- `clusterName` 为当前 controller 配置。
- weight、TTL 和隔离状态符合预期。

重点检查：

```text
Service metadata 不应包含期望只出现在实例上的 Pod Label。
Instance metadata 不应把 Service Label 当成来源。
Pod Label 和 Service Annotation 同名时，应使用 Pod Label。
```

### 验证 Sidecar 注入

```sh
kubectl get pod demo-0 -o yaml
kubectl describe pod demo-0
kubectl logs -n polaris-system deploy/polaris-controller
```

确认：

- Pod 已被 Webhook 处理。
- 注入容器、init container 和 volume 存在。
- `sidecar.polarismesh.cn/status` 已生成。
- `sidecar.polarismesh.cn/serviceName`、`serviceNamespace` 和 `envoyMetadata` 符合预期。
- Java Agent 的 `JAVA_TOOL_OPTIONS` 包含所需 `-D` 参数。

### 验证两种模式没有被混淆

- Service 同步验证 Polaris Naming 中的 Service、Instance、weight 和 metadata。
- Sidecar 注入验证 Pod 中的容器、volume、状态 Annotation 和业务容器启动参数。
- Mesh 的 `envoyMetadata` 不是 Polaris Naming Instance metadata。
- 开启注入后，如果没有 Service 同步或业务注册，不能仅凭 Pod 已注入就认为 Polaris 中已有对应实例。

## 十八、代码对应关系

- Service 和通用 Annotation 常量：`../pkg/util/types.go`
- Service 同步判断：`../pkg/controller/controller.go`
- Namespace 同步：`../pkg/controller/namespace.go`
- metadata 合并和实例写入：`../pkg/controller/apis.go`
- Endpoint 和 Pod Label 读取：`../pkg/util/address/address.go`
- 权重和 StatefulSet 序号：`../pkg/util/address/address.go`
- 注入开关和模式选择：`../pkg/inject/pkg/kube/inject/inject.go`、`common.go`
- 注入 Annotation 和状态：`../pkg/inject/pkg/kube/inject/pod.go`
- Mesh/DNS Sidecar 参数：`../pkg/inject/pkg/kube/inject/apply/mesh`
- Java Agent 参数：`../pkg/inject/pkg/kube/inject/apply/javaagent`
- 注入模板实际读取的 key：`../deploy/kubernetes_v1.22/helm/templates/_params.tpl`、`controller-configmap-sidecar.yaml`
- Annotation 生成列表：`../pkg/inject/api/annotation/annotations.gen.go`
