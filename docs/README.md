# Polaris Controller 注解与用户使用指南

## 适用范围

本文基于当前 `bug-list-fix` 工作区的代码实现整理，描述用户可以通过 Kubernetes Annotation 和 Label 控制的 Polaris Controller 行为。

整理日期：2026-10-26。

内容覆盖：

- Kubernetes Service 同步到 Polaris。
- Namespace 级同步开关。
- Service 和 Pod 数据到 Polaris Service/Instance 的映射。
- 实例权重、TTL、StatefulSet 按 Pod 调权。
- Sidecar 注入开关、注入模式和 Mesh/DNS 参数。
- Java Agent 注入参数。
- 当前只用于内部实现、不建议用户使用的注解。

本文不是上游版本说明。`polarismesh.cn/enableRegister` 等行为以当前代码为准。

## 目录

- [一、Service 同步开关](#一service-同步开关)
- [二、Namespace 同步](#二namespace-同步)
- [三、Polaris 侧 Namespace、Service 名称](#三polaris-侧-namespaceservice-名称)
- [四、Service Alias](#四service-alias)
- [五、Service 与 Instance 数据](#五service-与-instance-数据)
- [六、实例权重与 TTL](#六实例权重与-ttl)
- [七、Sidecar 注入开关](#七sidecar-注入开关)
- [八、Sidecar 注入模式](#八sidecar-注入模式)
- [九、Mesh/DNS Sidecar 参数](#九mesh-dns-sidecar-参数)
- [十、Java Agent 注入](#十java-agent-注入)
- [十一、Sidecar 服务身份](#十一sidecar-服务身份)
- [十二、内部和自动生成注解](#十二内部和自动生成注解)
- [十三、常用配置示例](#十三常用配置示例)
- [十四、常见错误](#十四常见错误)
- [十五、验证方法](#十五验证方法)
- [十六、代码对应关系](#十六代码对应关系)

## 先了解这几条规则

- Annotation 和 Label 的 key 区分大小写。
- 布尔值通常要求是字符串，例如 `"true"`、`"false"`，不能直接写 YAML boolean `true` 的场合应加引号。
- Service 同步要求 Service 至少有一个 `port`，并且 `spec.selector` 不能为 `nil`。
- `kube-system`、`kube-public`、`istio-system`、`polaris-system`、`kube-node-lease` 中的对象默认不处理。
- Service 上的 `polarismesh.cn/sync` 优先于 Namespace 上的同名注解。
- Service 上的 `polarismesh.cn/sync` 存在时，`polarismesh.cn/enableRegister` 不参与判断。
- Pod label 会参与实例 metadata 合并；若业务使用大量高基数 label，需要自行评估 Polaris 实例 metadata 的规模和查询成本。
- ConfigMap 同步当前没有对外暴露，相关注解不属于受支持的用户接口。

## 一、Service 同步开关

### `polarismesh.cn/sync`

类型：Service Annotation。

取值：

- `"true"`：同步该 Service 及其 controller 管理的实例。
- `"false"`：不同步；已有 controller 实例会被清理。
- 其他值：按 `"false"` 处理。
- 未设置：继续检查 Namespace 同步配置。

`demand` 模式下的实际行为：

- Service 未设置该注解时，Namespace 必须是 `polarismesh.cn/sync=true` 才会同步。
- Service 显式 `false` 时，即使 Namespace 开启同步也不会同步。
- Service 显式 `true` 时，即使 Namespace 关闭同步也保持同步。

`all` 模式下的实际行为：

- Namespace 没有设置该注解时，默认同步其中的 Service。
- Namespace 显式 `false` 时，该 Namespace 下的 Service 默认不同步。
- Service 自己的 `true` 或 `false` 仍然优先。

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

### `polarismesh.cn/enableRegister`

类型：Service Annotation。

这是一个历史兼容注解，不再表示“业务自注册，controller 停止管理实例”。

当前行为：

- Service 没有 `polarismesh.cn/sync` 且 `enableRegister: "true"` 时，等价于开启 Service 同步。
- 开启同步后，controller 仍会正常新增、更新、删除实例。
- 同时存在 `polarismesh.cn/sync` 时，以 `sync` 为准。
- `sync=false` 会覆盖 `enableRegister=true`。
- `enableRegister=false` 自身不会关闭同步；如果没有 `sync`，仍会继续检查 Namespace。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/enableRegister: "true"
```

新接入建议统一使用 `polarismesh.cn/sync`。存量客户可以继续使用 `enableRegister=true` 保持兼容。

## 二、Namespace 同步

### `polarismesh.cn/sync`

类型：Namespace Annotation。

作用：控制该 Namespace 下的 Service 是否默认参与 Polaris 同步。

取值：

- `"true"`：开启。
- `"false"`：关闭。
- 其他非空值：按关闭处理。
- 未设置：`demand` 模式默认关闭；`all` 模式默认开启。

示例：

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: demo
  annotations:
    polarismesh.cn/sync: "true"
```

注意：

- 给已有 Namespace 打开同步后，存量 Service 会被重新投递，不需要先修改 Service。
- Namespace 关闭同步时，没有显式 Service 注解的 Service 和 controller 实例会被清理。
- Service 显式 `sync=true` 不会因为 Namespace 关闭而退出。

## 三、Polaris 侧 Namespace、Service 名称

### `polarismesh.cn/overideNamespace`

类型：Service Annotation。

作用：覆盖写入 Polaris 的 Namespace 名称。

取值：非空字符串。为空时使用 Kubernetes Namespace。

注意：key 中的 `overide` 是当前代码中的实际拼写，不是 `override`。

### `polarismesh.cn/overideService`

类型：Service Annotation。

作用：覆盖写入 Polaris 的 Service 名称。

取值：非空字符串。为空时使用 Kubernetes Service 名称。

示例：

```yaml
apiVersion: v1
kind: Service
metadata:
  name: k8s-demo
  annotations:
    polarismesh.cn/sync: "true"
    polarismesh.cn/overideNamespace: Production
    polarismesh.cn/overideService: demo.prod
```

说明：

- Kubernetes 中仍使用原 Namespace/Service 名称，只有 Polaris 侧使用覆盖后的名称。
- 两个注解可以独立设置。
- controller 不校验目标 Namespace/Service 是否会和其他 Kubernetes Service 冲突。
- 多个 Kubernetes Service 映射到同一个 Polaris Service 时，实例归属仍依赖 `clusterName` 和 `source` metadata。

## 四、Service Alias

### `polarismesh.cn/aliasNamespace`

类型：Service Annotation。

作用：指定 Polaris 服务别名所在的 Namespace。

### `polarismesh.cn/aliasService`

类型：Service Annotation。

作用：指定 Polaris 服务别名名称。

两个注解必须同时非空才会创建别名。只设置其中一个不会创建或删除别名。

示例：

```yaml
annotations:
  polarismesh.cn/sync: "true"
  polarismesh.cn/aliasNamespace: Production
  polarismesh.cn/aliasService: demo.alias
```

注意：

- 别名指向的是覆盖后的 Polaris Namespace/Service 名称。
- 当前代码在修改别名后会尝试创建新别名，但没有删除旧别名的逻辑。删除或移除别名注解前需要人工确认旧别名。

## 五、Service 与 Instance 数据

### `polarismesh.cn/metadata`

类型：Service Annotation。

作用：声明 controller 写入 Polaris 实例的 metadata。

取值：JSON object，key 和 value 都必须是字符串。

示例：

```yaml
annotations:
  polarismesh.cn/metadata: '{"tenant":"t1","env":"prod"}'
```

合并规则：

1. 先使用 Service metadata 注解。
2. 再合并 Pod labels。
3. 同名 key 以 Pod label 为准。
4. 最后强制写入 `source=polaris-controller`。
5. 强制写入 `clusterName=<当前 controller clusterName>`。

`source` 和 `clusterName` 是受保护字段，Pod label 或 Service metadata 不能覆盖。

字段删除语义：

- controller 按完整声明集合管理业务 metadata。
- 从 Service metadata 和 Pod labels 中同时移除一个字段后，下一次实例更新会从 Polaris 实例中删除该字段。
- 直接在 Polaris 中添加、但两处都没有声明的业务字段，会在后续更新中被删除。
- 兼容字段 `version`、`protocol` 和旧版 `platform` 在两处都没有声明时会保留 Polaris 现有值。

Pod label 示例：

```yaml
metadata:
  labels:
    app: demo
    tenant: t1
    version: v2
```

仅修改 Pod label、不修改 Endpoints 时，metadata 通过周期对账收敛，默认最长等待约一个 `resync-duration`（默认 30 秒）。

如果 `polarismesh.cn/metadata` 不是合法 JSON object，controller 会返回错误并重试，不会把它当成空 metadata 删除已有字段。

### `polarismesh.cn/customVersion`

当前实现不是在 Service Annotation 中直接读取该 key，而是从合并后的实例 metadata 中读取 `polarismesh.cn/customVersion`。

如需设置实例 Version 字段，可放在 metadata 中：

```yaml
annotations:
  polarismesh.cn/metadata: '{"polarismesh.cn/customVersion":"v2"}'
```

如果没有该 metadata，实例 Version 默认取 Pod label `version` 的值。

### Service Label 同步

Service 的 Labels 会同步为 Polaris Service metadata，而不是实例 metadata。

当前更新逻辑是合并新增或变化的 label，不保证从 Kubernetes Service 删除 label 后同步删除 Polaris Service metadata 中的旧字段。需要清理 Service metadata 时应人工确认。

## 六、实例权重与 TTL

### `polarismesh.cn/weight`

类型：Service Annotation。

作用：为该 Service 下所有实例设置默认权重。

默认值：`100`。

取值：整数字符串。

注意：当前代码直接解析该值，没有在本地做 `0` 到 `100` 的范围校验。建议用户只使用 `0` 到 `100`，避免依赖 Polaris Server 对非法值的处理。

示例：

```yaml
annotations:
  polarismesh.cn/weight: "80"
```

### `polarismesh.cn/customWeight`

类型：Service Annotation。

作用：按 StatefulSet Pod 序号和 Service 端口覆盖单个实例权重。

取值：JSON object，格式为：

```json
{"<pod-index>-<port>": <weight>}
```

示例：

```yaml
annotations:
  polarismesh.cn/sync: "true"
  polarismesh.cn/workloadKind: "StatefulSet"
  polarismesh.cn/weight: "50"
  polarismesh.cn/customWeight: '{"0-8080":80,"1-8080":20}'
```

规则：

- 权重必须是 `0` 到 `100`。
- 越界值回退到 `polarismesh.cn/weight` 的值。
- 非法 JSON 会被记录日志并视为没有自定义权重。
- Pod 序号来自 StatefulSet Pod 名称的最后一段，例如 `demo-0` 的序号是 `0`。
- 非 StatefulSet 工作负载没有稳定序号，建议使用 `polarismesh.cn/weight`。

### `polarismesh.cn/workloadKind`

类型：Service Annotation。

作用：告知 controller 该 Service 对应的工作负载类型，影响 StatefulSet 序号解析和工作负载类型变化时的实例替换。

代码识别的状态化类型包括：

- `statefulset`
- `statefulsetplus`
- `StatefulSetPlus`
- `StatefulSet`

其他值按非 StatefulSet 处理。

### `polarismesh.cn/ttl`

类型：Service Annotation。

作用：启用实例心跳健康检查并设置 TTL，单位为秒。

有效范围：`1` 到 `60`。

非法值或越界值会被忽略，实例回到无 TTL 健康检查的默认行为。

示例：

```yaml
annotations:
  polarismesh.cn/ttl: "10"
```

注意：controller 只负责注册带 TTL 的实例，不负责替业务持续发送心跳。使用 TTL 时，需要确认业务实例或外部组件会按约定发送心跳。

## 七、Sidecar 注入开关

Pod 是否进入 webhook 与最终是否注入是两个步骤。

当前 Helm 配置通过以下任一条件让 Pod 进入 webhook：

- Namespace 带有 Label `polarismesh.cn/inject=enabled`。
- Pod 带有 Label `polarismesh.cn/inject=enabled`。
- 旧版安装可能使用 Namespace Label `polaris-injection=enabled`。

如果 Namespace 和 Pod Label 都没有命中，只在 Pod 上写 `polarismesh.cn/inject` Annotation 不保证 webhook 被调用。

### Pod Annotation `polarismesh.cn/inject`

作用：在 webhook 已命中 Pod 后，明确开启或关闭注入。

取值不区分大小写，可识别：

- 开启：`y`、`yes`、`true`、`on`、`enable`、`enabled`
- 关闭：`n`、`no`、`false`、`off`、`disable`、`disabled`

优先级：高于 Pod Label、Injection Policy 和 Namespace 默认策略。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/inject: "true"
```

### Pod Annotation `sidecar.polarismesh.cn/inject`

旧版兼容注解。

- 当 `polarismesh.cn/inject` 未设置时作为 fallback。
- 两者都存在时，`polarismesh.cn/inject` 优先。
- 建议新配置使用 `polarismesh.cn/inject`。

### Label `polarismesh.cn/inject`

作用：

- Namespace Label `enabled`：让该 Namespace 下的 Pod 进入 webhook。
- Pod Label `enabled`：让该 Pod 进入 webhook。
- Pod Label `disabled`：当前模板的 `neverInjectSelector` 会阻止注入。

示例：

```sh
kubectl label namespace demo polarismesh.cn/inject=enabled
kubectl label pod demo-0 polarismesh.cn/inject=disabled
```

Pod Annotation 的明确开关优先于 Label。

当前 Helm 默认注入模板的 policy 为 `enabled`，并通过 `neverInjectSelector` 排除带 `polarismesh.cn/inject=disabled` Label 的 Pod。自定义安装修改 policy 后，以实际模板配置为准。

## 八、Sidecar 注入模式

模式选择优先级从高到低：

1. Pod Annotation `polarismesh.cn/javaagent: "true"`。
2. Pod Annotation `sidecar.polarismesh.cn/openOnDemand: "true"`。
3. Pod Annotation `polarismesh.cn/sidecar-mode`。
4. Namespace Label `polaris-sidecar-mode`。
5. controller 配置的默认 sidecar mode。

### `polarismesh.cn/sidecar-mode`

类型：Pod Annotation。

取值：

- `mesh`
- `dns`
- `java-agent`

其他值会按 `mesh` 处理。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/sidecar-mode: dns
```

该注解当前只按 Pod Annotation 读取，不要把它配置在 Service 上期望生效。

### Namespace Label `polaris-sidecar-mode`

作用：为 Namespace 内 Pod 提供默认注入模式。

取值同上。

示例：

```sh
kubectl label namespace demo polaris-sidecar-mode=mesh
```

Pod 级 mode Annotation 优先于 Namespace Label。

### `sidecar.polarismesh.cn/openOnDemand`

类型：Pod Annotation。

取值：`"true"` 时强制选择 Mesh 模式，并开启按需 XDS 能力。

示例：

```yaml
metadata:
  annotations:
    sidecar.polarismesh.cn/openOnDemand: "true"
```

它不是通用布尔开关：非 `"true"` 不会触发 Mesh 模式选择。

## 九、Mesh/DNS Sidecar 参数

以下注解由当前 Mesh/DNS 注入模板使用。

### `polarismesh.cn/tls-mode`

类型：Pod Annotation。

作用：控制 Envoy sidecar 是否启用 mTLS。

建议取值：

- `none`：关闭。
- `strict`：严格 mTLS。
- `permissive`：兼容模式。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/tls-mode: strict
```

使用非 `none` 值时，controller 还会确保目标 Namespace 中存在根证书 Secret。

### `polarismesh.cn/sidecarConfig`

类型：Pod Annotation。

作用：以 JSON 方式配置 Polaris sidecar 的递归、DNS、地域和日志参数。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/sidecarConfig: |
      {
        "recurse": {"enabled": false, "timeout": 500},
        "dns": {"suffix": "cluster.local", "ttl": 30},
        "location": {"region": "beijing", "zone": "zone-a", "campus": "campus-1", "match_level": "zone"},
        "log": {"output_level": "info", "rotation_max_size": 100, "rotation_max_age": 7, "rotation_max_backups": 10}
      }
```

字段说明：

- `dns.suffix`、`dns.ttl`：仅在 DNS 模式生效；TTL 必须大于 0。
- `recurse.enabled`：设为 `false` 时关闭递归。
- `recurse.timeout`：大于 0 时生效。
- `location.region`、`zone`、`campus`：写入 sidecar 地域环境变量。
- `location.match_level`：支持 `region`、`zone`、`campus`。
- `log.output_level`：支持 `debug`、`info`、`warn`、`error`、`fatal`。
- `log.rotation_max_size`、`rotation_max_age`、`rotation_max_backups`：大于 0 时生效。

JSON 非法时注入会失败。

### Proxy 资源

类型：Pod Annotation。

作用：覆盖 Envoy sidecar 的资源 requests/limits。

- `polarismesh.cn/proxyCPU`：requests.cpu
- `polarismesh.cn/proxyMemory`：requests.memory
- `polarismesh.cn/proxyCPULimit`：limits.cpu
- `polarismesh.cn/proxyMemoryLimit`：limits.memory

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/proxyCPU: "100m"
    polarismesh.cn/proxyMemory: "128Mi"
    polarismesh.cn/proxyCPULimit: "500m"
    polarismesh.cn/proxyMemoryLimit: "512Mi"
```

值必须符合 Kubernetes resource quantity 格式。

### 流量拦截端口

类型：Pod Annotation。

- `polarismesh.cn/includeInboundPorts`
  - 默认 `*`，表示入站流量全部重定向。
- `polarismesh.cn/excludeInboundPorts`
  - 默认 `15985,50000,15053`。
- `polarismesh.cn/excludeOutboundPorts`
  - 默认空。
- `polarismesh.cn/excludeOutboundCIDRs`
  - 默认空，按逗号分隔 CIDR。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/includeInboundPorts: "*"
    polarismesh.cn/excludeInboundPorts: "15985,50000,15053,8080"
    polarismesh.cn/excludeOutboundPorts: "3306"
    polarismesh.cn/excludeOutboundCIDRs: "169.254.0.0/16"
```

### `security.polarismesh.cn/autoMTLS`

当前已生成该 Annotation 定义，但实际 mTLS 开关读取的是 `polarismesh.cn/tls-mode`。

设置 `security.polarismesh.cn/autoMTLS` 当前不会改变注入行为，不建议使用。

## 十、Java Agent 注入

### `polarismesh.cn/javaagent`

类型：Pod Annotation。

取值：`"true"`。

作用：选择 Java Agent 注入模式。优先级高于 Mesh、`sidecar-mode` 和 Namespace 默认模式。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/javaagent: "true"
```

### `polarismesh.cn/javaagentVersion`

类型：Pod Annotation。

作用：指定 Java Agent 镜像或 Agent 版本。

未设置或为空时，当前代码使用 `latest`。

示例：

```yaml
metadata:
  annotations:
    polarismesh.cn/javaagentVersion: "1.7.0-RC5"
```

### `polarismesh.cn/javaagentFrameworkName`

类型：Pod Annotation。

作用：指定 Java 应用框架名称，例如 `spring-cloud`。

通常需要和 `javaagentFrameworkVersion` 一起设置。

### `polarismesh.cn/javaagentFrameworkVersion`

类型：Pod Annotation。

作用：指定框架版本。

### `polarismesh.cn/javaagentConfig`

类型：Pod Annotation。

作用：以 JSON 方式覆盖 Java Agent 的应用配置。

取值必须是 JSON object，value 应为字符串。

示例：

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

用户配置会覆盖 workload 推导值和默认 properties。

### `polarismesh.cn/workloadNameAsServiceName`

类型：Pod Annotation。

取值：`"true"`。

作用：在 Java Agent 模式下，使用工作负载名称作为 `spring.application.name`。

### `polarismesh.cn/workloadNamespaceAsServiceNamespace`

类型：Pod Annotation。

取值：`"true"`。

作用：在 Java Agent 模式下，使用 Pod 所在 Namespace 作为 Polaris 服务 Namespace。

冲突优先级：`javaagentConfig` 中显式配置的 `spring.application.name` 或 `spring.cloud.polaris.discovery.namespace` 优先。

## 十一、Sidecar 服务身份

### `sidecar.polarismesh.cn/serviceName`

类型：Pod Annotation。

作用：指定侧车注册或发现时使用的服务名。

Mesh 模式的默认规则：

- 优先使用该 Annotation。
- 未设置时使用 Pod Label `app`。
- 再没有则使用工作负载名称。

Java Agent 模式推荐使用 `javaagentConfig` 或 `workloadNameAsServiceName` 设置服务名。

### `sidecar.polarismesh.cn/serviceNamespace`

类型：Pod Annotation。

设计作用：指定侧车服务所在 Namespace。

Mesh 模式未设置时使用 Pod Namespace。

当前实现的 Mesh 元数据主要通过 Pod Namespace 推导，`sidecar.polarismesh.cn/serviceNamespace` 对跨 Namespace 场景的支持不完整。需要跨 Namespace 注入时，应先在实际环境验证，Java Agent 场景优先使用 `workloadNamespaceAsServiceNamespace` 或 `javaagentConfig`。

Java Agent 当前更推荐使用：

- `polarismesh.cn/workloadNamespaceAsServiceNamespace: "true"`，或
- `javaagentConfig` 中的 `spring.cloud.polaris.discovery.namespace`。

## 十二、内部和自动生成注解

以下键不建议用户手工设置。

### `sidecar.polarismesh.cn/status`

由注入 webhook 自动生成，记录已注入的 init container、container、volume 和 imagePullSecret。

不要手工修改或删除，否则可能影响重复注入或卸载时的兼容处理。

### `sidecar.polarismesh.cn/envoyMetadata`

Mesh 注入时由 controller 根据 Pod label、服务名、Namespace 和 TLS 配置自动生成。

当前 Mesh 注入会覆盖用户手写的值，不建议直接配置。

### `sidecar.polarismesh.cn/bindPorts`

代码中定义了该常量，但当前实现没有读取或下发该 Annotation。设置它不会产生效果。

### 当前模板不消费的兼容键

以下键出现在兼容或校验列表中，但当前实际注入模板没有读取它们，因此不应作为用户功能使用：

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

- 当前 Pod 资源使用的是 `polarismesh.cn/proxyCPU` 和 `polarismesh.cn/proxyMemory`。
- 当前流量端口使用的是 `polarismesh.cn/includeInboundPorts` 等注解。
- `sidecar.polarismesh.cn/proxyCPU` 和 `traffic.sidecar.polarismesh.cn/*` 在当前实现中不应作为生效配置使用。

### ConfigMap 同步内部注解

ConfigMap 同步当前没有对外暴露，以下注解仅属于内部实现，不应提供给用户：

```text
polarismesh.cn/configGroup
polarismesh.cn/enableEncrypt
polarismesh.cn/enableEncryptAlog
internal-sync-source
```

## 十三、常用配置示例

### 只同步 Service 和实例

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
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

### 复用客户历史的 enableRegister 配置

```yaml
metadata:
  annotations:
    polarismesh.cn/enableRegister: "true"
    polarismesh.cn/aliasNamespace: Production
    polarismesh.cn/aliasService: demo.alias
    polarismesh.cn/weight: "100"
    polarismesh.cn/workloadKind: Deployment
```

该配置会开启同步，并且实例仍由 controller 注册。

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

### 开启 Mesh 注入

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

### 开启 Java Agent 注入

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

## 十四、常见错误

### 用 enableRegister=false 关闭同步

错误理解：

```yaml
polarismesh.cn/enableRegister: "false"
```

这不会关闭一个由 `sync=true` 或 Namespace 开启的 Service。需要关闭时使用：

```yaml
polarismesh.cn/sync: "false"
```

### 使用了 override 拼写

当前代码实际读取：

```text
polarismesh.cn/overideNamespace
polarismesh.cn/overideService
```

写成 `overrideNamespace` 或 `overrideService` 不会生效。

### 以为 alias 注解可以删除别名

同时删除 `aliasNamespace` 和 `aliasService` 不会删除 Polaris 中已经创建的别名。

### 把 metadata 写成 YAML object

`polarismesh.cn/metadata` 的值必须是 JSON 字符串，并且 value 应为字符串：

```yaml
polarismesh.cn/metadata: '{"replicas":"3","enabled":"true"}'
```

### 使用不生效的 sidecar 资源 key

错误：

```yaml
sidecar.polarismesh.cn/proxyCPU: "100m"
```

当前应使用：

```yaml
polarismesh.cn/proxyCPU: "100m"
```

### 只写注入 Annotation，没有让 webhook 命中

当前 Helm 默认通过 Namespace/Pod Label `polarismesh.cn/inject=enabled` 选择 Pod。仅在 Pod Annotation 中写 `polarismesh.cn/inject=true`，但 Namespace 和 Pod Label 都没命中时，webhook 可能根本不会被调用。

### 期望 ConfigMap 同步注解生效

ConfigMap 同步当前没有对外暴露。不要把 `polarismesh.cn/configGroup` 等内部注解写进用户配置。

## 十五、验证方法

### 检查 Service 同步配置

```sh
kubectl get service demo -o yaml
kubectl get namespace demo -o yaml
```

确认：

- Service 有 selector 和 port。
- `sync` / `enableRegister` 的值符合预期。
- 覆盖名称和 alias 成对配置。
- metadata 是合法 JSON。

### 检查实例结果

在 Polaris 控制台或 API 中确认：

- Namespace 和 Service 名称符合覆盖规则。
- 实例数量与 Endpoints 一致。
- 实例 metadata 包含预期业务字段。
- `source=polaris-controller`。
- `clusterName` 为当前 controller 配置。
- weight、TTL、Version 符合预期。

### 检查注入结果

```sh
kubectl get pod demo-0 -o yaml
kubectl describe pod demo-0
kubectl logs -n polaris-system deploy/polaris-controller
```

确认：

- Pod 已被 webhook 处理。
- 注入容器、init container 和 volume 存在。
- `sidecar.polarismesh.cn/status` 已生成。
- `sidecar.polarismesh.cn/serviceName`、`serviceNamespace` 和 `envoyMetadata` 符合预期。
- Java Agent 的 `JAVA_TOOL_OPTIONS` 包含所需 `-D` 参数。

## 十六、代码对应关系

- Service 和通用 Annotation 常量：`../pkg/util/types.go`
- Service 同步判断：`../pkg/controller/controller.go`
- Namespace 同步：`../pkg/controller/namespace.go`
- 名称覆盖：`../pkg/util/controller_utils.go`
- metadata 合并和实例写入：`../pkg/controller/apis.go`
- 权重和 StatefulSet 序号：`../pkg/util/address/address.go`
- 注入开关和模式选择：`../pkg/inject/pkg/kube/inject/inject.go`、`common.go`
- 注入 Annotation 和状态：`../pkg/inject/pkg/kube/inject/pod.go`
- Mesh/DNS sidecar 参数：`../pkg/inject/pkg/kube/inject/apply/mesh`
- Java Agent 参数：`../pkg/inject/pkg/kube/inject/apply/javaagent`
- 注入模板实际读取的键：`deploy/kubernetes_v1.22/helm/templates/_params.tpl`、`controller-configmap-sidecar.yaml`
- Annotation 生成列表：`../pkg/inject/api/annotation/annotations.gen.go`

