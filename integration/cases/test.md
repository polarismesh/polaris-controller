# Polaris Controller 集成测试用例说明

**更新时间**：2026-11-19

**适用范围**：`integration/cases` 下的 Service 同步、Namespace/Service/Endpoints 生命周期、
Sidecar 注入、组合场景、可选别名切换、全量模式切换和清理用例。

> 本文说明每个用例的预期结果和验证方式。环境准备、变量说明和更简短的运行示例见同目录 `README.md` / `README-zh.md`。

---

## 1. 测试目标

验证以下行为：

1. Kubernetes Namespace/Service/Endpoints 到 Polaris Service/Instance 的同步。
2. Service Annotation、Pod Label、保留字段和受保护字段的优先级。
3. `polarismesh.cn/sync`、`polarismesh.cn/enableRegister`、Namespace 注解和 `serviceSync.mode` 的组合语义。
4. TTL、多端口协议、StatefulSet 自定义权重、NotReady 实例隔离和别名。
5. DNS Sidecar、Java Agent 的注入选择、默认不注入、配置覆盖、hostNetwork 和旧版 Namespace Label 兼容。
6. Service 同步与 Sidecar 注入同时启用时的组合行为。
7. Namespace、Service、Endpoints 三条生命周期用例，包括删除保留语义和地址集合收敛。

---

## 2. 前置条件

### 2.1 集群和工具

- 测试机可访问目标 Kubernetes 集群，具备创建/删除 Namespace、Deployment、StatefulSet、Service、Endpoints 和 Pod 的权限。
- `kubectl`、`curl`、`jq` 可用，或通过 `KUBECTL`、`CURL`、`JQ` 覆盖。
- controller 已部署在 `polaris-system`，StatefulSet 名为 `polaris-controller`。
- 测试机能访问 Polaris HTTP API，默认从 `injector-mesh` ConfigMap 的 `serverAddress:8090` 推导。
- Polaris Token 可以查询 Namespace/Service/Instance/Alias，并能在 Service 生命周期用例中注册和删除测试实例。
- `injector-mesh` 的 `mesh` 中配置了非空 `clusterName`；Service 生命周期用例用它区分本集群和其他集群的 controller 实例。
- 集群允许创建 NodePort 和 LoadBalancer Service；Service 生命周期用例会覆盖这两种类型。
- 测试集群默认使用 `serviceSync.mode=demand`；`RUN_ALL_MODE=1` 会临时切换为 `all` 并恢复。
- 默认不注入用例要求 controller 的 `sidecarInject.mode` 为 `""` 或 `none`；该用例验证默认安装行为，不适用于显式配置了 `mesh`、`dns` 或 `java-agent` 的集群。

### 2.2 专用 Namespace

测试会创建并删除以下专用 Namespace：

```text
it-pc-sync
it-pc-sync-legacy
it-pc-sync-all
it-pc-inject-default
it-pc-inject-podlabel
it-pc-inject-java
it-pc-inject-legacy
it-pc-inject-no-mode
it-pc-combo
it-pc-ns-lifecycle
it-pc-svc-lifecycle
it-pc-svc-alias
it-pc-ep-lifecycle
```

不要在这些 Namespace 中放置业务资源。`RESET_CASES=1` 会先删除它们。

`it-pc-ns-lifecycle` 会由 Namespace 生命周期用例主动删除；但删除后 Polaris
中的同名 Namespace 会保留，`cleanup.sh` 不会删除 Polaris Namespace。

### 2.3 数据映射规则

- Kubernetes Service `metadata.labels` 写入 Polaris Service metadata。
- `polarismesh.cn/metadata` 和实际 Pod Labels 合并为 Polaris Instance metadata。
- Pod Label 与 Service Annotation 同名时，Pod Label 优先。
- `source`、`clusterName`、`k8sServiceUID` 由 controller 保护，不能被业务配置覆盖。
- `version`、`protocol`、`region`、`zone`、`campus` 是实例保留字段，提升到实例顶层属性，不写入业务 metadata。
- `polarismesh.cn/customVersion` 优先于 Pod Label `version` 决定顶层 Version。
- EndpointPort 和 `polarismesh.cn/protocols` 决定顶层 Protocol；Pod Label `protocol` 和 metadata 中的 `protocol` 会被剔除。
- HTTP `/naming/v1/instances` 不返回保留字段的 metadata 副本，测试应断言顶层 `version`、`protocol`、`location`。

---

## 3. 执行方式

### 3.1 默认完整运行

```bash
cd integration/cases
./run-all.sh
```

默认执行顺序：

```text
等待 controller rollout
清理全部专用 Namespace
service-sync
lifecycle/namespace
lifecycle/service
lifecycle/endpoints
inject
combined
```

### 3.2 可选用例

```bash
RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh
```

`RUN_ALIAS_WINNER=1` 追加别名稳定 winner 和 handover 用例。

`RUN_ALL_MODE=1` 追加 `serviceSync.mode=all` 切换用例。该用例会修改 `polaris-system/injector-mesh` 并重启 controller，默认结束后恢复原模式。

### 3.3 单独运行套件

```bash
RESET_CASES=1 ./service-sync/run.sh
RESET_CASES=1 ./lifecycle/namespace/run.sh
RESET_CASES=1 ./lifecycle/service/run.sh
RESET_CASES=1 ./lifecycle/endpoints/run.sh
RESET_CASES=1 ./inject/run.sh
RESET_CASES=1 ./combined/run.sh
./service-sync/alias-winner.sh
./service-sync/switch-mode.sh all
./cleanup.sh
```

`RESET_CASES=1` 会先删除所有专用 Namespace，避免旧资源污染断言。

### 3.4 常用环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `RESET_CASES` | `run-all.sh` 中为 `1` | 运行前删除专用 Namespace |
| `CLEANUP` | `0` | 退出时删除专用 Namespace |
| `RUN_ALIAS_WINNER` | `0` | 运行可选别名 winner/handover 用例 |
| `RUN_ALL_MODE` | `0` | 运行可选 `serviceSync.mode=all` 用例 |
| `WAIT_TIMEOUT` | `120` | Pod、Namespace、rollout 等等待时间，单位秒 |
| `RESYNC_TIMEOUT` | `180` | Polaris Service/Instance 收敛等待时间，单位秒 |
| `EXTERNAL_DELETE_TIMEOUT` | `30` | Service 生命周期用例清理手工外部实例的等待时间，单位秒 |
| `POLARIS_HTTP_URL` | 自动推导 | Polaris HTTP API 地址 |
| `POLARIS_TOKEN` | 自动从 ConfigMap 读取 | Polaris API token |
| `KUBECTL` / `CURL` / `JQ` | 命令名 | 覆盖工具路径 |

---

## 4. 用例总览

| ID | 用例 | 核心预期 | 类型 |
|---|---|---|---|
| SYNC-01 | modes-are-independent-service-only-has-no-sidecar | Service 同步不自动注入 sidecar | 默认 |
| SYNC-02 | namespace-annotation-and-service-disable | Namespace 注解与 Service `sync=false` 优先级正确 | 默认 |
| SYNC-03 | enableRegister | 旧版 `enableRegister=true` 可启用同步 | 默认 |
| SYNC-04 | all-mode-service-is-not-synced-in-demand | demand 模式不自动同步普通 Namespace | 默认 |
| SYNC-05 | service-and-instance-metadata | Service/Pod metadata 合并、保留字段、优先级正确 | 默认 |
| SYNC-06 | ttl | TTL 和 TCP protocol 正确，Pod protocol Label 被忽略 | 默认 |
| SYNC-07 | multi-port-protocol | 多端口生成多个实例并应用协议映射 | 默认 |
| SYNC-08 | statefulset-custom-weight | StatefulSet 每个 Pod 使用自定义权重 | 默认 |
| SYNC-09 | not-ready-endpoint-isolated | NotReady Endpoint 注册为隔离实例 | 默认 |
| SYNC-10 | alias | Service 别名正确创建并指向源 Service | 默认 |
| SYNC-11 | service-label-convergence | Service Label 的增删收敛到 Polaris Service metadata | 默认 |
| SYNC-12 | pod-label-convergence | Pod Label 的 metadata/保留字段变更收敛 | 默认 |
| SYNC-13 | namespace-disable-keeps-explicit-service | 关闭 Namespace 同步清理实例但保留 Service | 默认 |
| SYNC-14 | service-disable-cleans-polaris-instances | 关闭单个 Service 同步清理实例但保留 Service | 默认 |
| NS-LIFE-01 | namespace-create-sync-and-service | Namespace 创建后 Polaris Namespace/Service 出现 | 默认 |
| NS-LIFE-02 | namespace-delete-retains-polaris-resources | 删除 K8s Namespace 后 Polaris Namespace/Service 保留、实例移除 | 默认 |
| SVC-LIFE-01 | service-types-and-cross-namespace-alias | ClusterIP/NodePort/LoadBalancer 同步；跨 Namespace 别名正确 | 默认 |
| SVC-LIFE-02 | service-delete-retains-service-removes-owned-instances | 删除 K8s Service 后 Polaris Service 保留，只删除本集群 controller 实例 | 默认 |
| EP-LIFE-01 | endpoints-address-health-and-ip-port-mapping | Endpoints IP:port 与实例一致，Ready 实例健康 | 默认 |
| EP-LIFE-02 | endpoints-scale-up | 地址增加后实例增加 | 默认 |
| EP-LIFE-03 | endpoints-scale-down | 地址减少后实例减少 | 默认 |
| EP-LIFE-04 | endpoints-remove-all-addresses | 地址全部移除后实例归零、Service 保留 | 默认 |
| ALIAS-01 | alias-stable-winner-and-handover | 别名稳定 winner；源 Service 删除后 handover | 可选 |
| MODE-01 | all-mode service sync | all 模式同步普通 Service；恢复 demand 后实例归零 | 可选 |
| INJ-01 | dns-mode-and-sidecar-config | DNS sidecar/init/env 配置正确 | 默认 |
| INJ-02 | namespace-and-pod-label-selection | Pod Label 可触发注入 | 默认 |
| INJ-03 | explicit-disable-annotation | Pod 注解显式关闭注入 | 默认 |
| INJ-04 | never-inject-label | `disabled` Label 禁止注入 | 默认 |
| INJ-05 | dns-hostnetwork-disabled | DNS 模式 hostNetwork 不注入 | 默认 |
| INJ-06 | javaagent-config-priority | Java Agent 配置优先于默认值 | 默认 |
| INJ-07 | javaagent-workload-name-fallback | Deployment 自动推导应用名/Namespace | 默认 |
| INJ-08 | javaagent-hostnetwork-allowed | hostNetwork 仍允许 Java Agent 注入 | 默认 |
| INJ-09 | legacy-namespace-label | 旧版 `polaris-injection` Label 兼容 | 默认 |
| INJ-10 | default-mode-not-injected | Webhook 命中但未配置 mode 时不注入 | 默认 |
| INJ-11 | modes-are-independent | 注入模式不触发 Service 同步 | 默认 |
| COMBO-01 | service-sync-and-dns-injection | 同一 Pod 同时完成 Service 同步和 DNS 注入 | 默认 |
| CLEAN-01 | cleanup.sh | 删除全部专用 Namespace 并等待释放 | 清理工具 |

---

## 5. Service 同步用例

### SYNC-01 modes-are-independent-service-only-has-no-sidecar

**目的**：确认 Service 同步模式不会自动注入 sidecar。

**操作**：读取 `it-pc-sync/sync-shared` Pod 的容器列表。

**预期结果**：

- 存在业务容器 `app`。
- 不存在 `polaris-sidecar` 容器。
- 不存在以 `polaris-` 开头的 init container。

**验证方式**：

```bash
kubectl -n it-pc-sync get pod -l app=sync-shared -o json | \
  jq -e '([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 0'
```

### SYNC-02 namespace-annotation-and-service-disable

**目的**：验证 Namespace 注解、Service `sync` 和旧版 `enableRegister` 的优先级。

**测试对象**：

- `namespace-only-demo`：无 Service 注解，Namespace `it-pc-sync` 为 `sync=true`。
- `disabled-demo`：Service `sync=false`。
- `sync-false-overrides-enable-register`：Service `sync=false` 且 `enableRegister=true`。
- `enable-register-false-demo`：Service `enableRegister=false`，无 `sync`，Namespace 为 `sync=true`。

**预期结果**：

- `namespace-only-demo` 同步到 Polaris。
- `disabled-demo` 不同步。
- `sync-false-overrides-enable-register` 不同步，`sync=false` 优先。
- `enable-register-false-demo` 同步，因为 Namespace 的 `sync=true` 仍然生效，`enableRegister=false` 不是强制关闭。

**验证方式**：`service-sync/run.sh` 中的 `wait_service` / `wait_service_absent` 断言。

### SYNC-03 enableRegister

**目的**：验证旧版 `polarismesh.cn/enableRegister=true` 的兼容行为。

**测试对象**：`it-pc-sync-legacy/enable-register-demo`，Service 只设置 `enableRegister=true`，Namespace 没有 `sync` 注解。

**预期结果**：Service 同步到 Polaris。

### SYNC-04 all-mode-service-is-not-synced-in-demand

**目的**：验证 demand 模式下，普通 Namespace 中的未显式开启同步的 Service 不会同步。

**测试对象**：`it-pc-sync-all/all-mode-demo`，Namespace 和 Service 都没有 `sync` 注解。

**预期结果**：当前 `serviceSync.mode=demand` 时，`it-pc-sync-all/all-mode-demo` 的实例数为 0。如果之前的 all 模式用例留下 Polaris Service，该 Service 可以保留，但不能有 controller 注册的实例。

### SYNC-05 service-and-instance-metadata

**目的**：验证 Service Label、Service Annotation metadata、Pod Label、保留字段和受保护字段的最终结果。

**测试对象**：`it-pc-sync/metadata-demo` 和 `sync-shared` Pod。

**Polaris Service 预期**：

```text
service.metadata.owner = payments
service.metadata.tier = backend
service.metadata 不包含 Pod-only label app
```

**Polaris Instance 预期**：

```text
metadata.tenant = pod-tenant
metadata.team = pod-team
metadata.env = service-env
metadata.app = sync-shared
metadata.polarismesh.cn/customVersion = v9
metadata.source = polaris-controller
metadata.clusterName 非空
metadata 不包含 version/protocol/region/zone/campus
version = v9
protocol = http
weight = 70
location.region = cn-north
location.zone = zone-a
location.campus = campus-1
```

**验证方式**：`service-sync/run.sh` 中的 `assert_service_metadata` 和 `instances_json` jq 断言。

### SYNC-06 ttl

**目的**：验证 `polarismesh.cn/ttl` 和协议保留字段处理。

**测试对象**：`it-pc-sync/ttl-demo`，Pod Label 带有 `protocol=grpc`。

**预期结果**：

- 实例数 1。
- `enableHealthCheck=true`。
- `healthCheck.heartbeat.ttl=60`。
- 顶层 `protocol=TCP`，因为没有配置 `polarismesh.cn/protocols`。
- Pod Label `protocol=grpc` 被忽略，metadata 不包含 `protocol`。

### SYNC-07 multi-port-protocol

**目的**：验证多端口 Service 生成多个实例，并应用命名端口协议映射。

**测试对象**：`it-pc-sync/multiport-demo`，端口 `http:8083` 和 `grpc:9090`，协议映射为 `http/grpc`。

**预期结果**：

- 实例数 2。
- 实例顶层 protocol 排序后为 `["grpc","http"]`。

### SYNC-08 statefulset-custom-weight

**目的**：验证 StatefulSet 实例的索引权重映射。

**测试对象**：`it-pc-sync/stateful-demo`，2 个副本，`customWeight` 为 `0-8080:80`、`1-8080:20`。

**预期结果**：

- 实例数 2。
- 权重排序为 `[20,80]`。

### SYNC-09 not-ready-endpoint-isolated

**目的**：验证 NotReady Endpoint 不会漏注册，而是生成隔离实例。

**测试对象**：`it-pc-sync/notready-demo`，readinessProbe 指向未监听的 18080。

**预期结果**：

- Endpoints 中存在 `notReadyAddresses`。
- Polaris 实例数 1。
- 实例 `isolate=true`。

### SYNC-10 alias

**目的**：验证 Service 别名创建和 metadata alias。

**测试对象**：

- `alias-winner-a` → `shared-alias`
- `metadata-demo` → `metadata-demo-alias`

**预期结果**：

- `it-pc-sync/shared-alias` 指向 `it-pc-sync/alias-winner-a`。
- `it-pc-sync/metadata-demo-alias` 指向 `it-pc-sync/metadata-demo`。

### SYNC-11 service-label-convergence

**目的**：验证 Kubernetes Service Label 增删会收敛到 Polaris Service metadata。

**操作**：

```text
为 metadata-demo 增加 owner=payments-v2
删除 metadata-demo 的 tier Label
```

**预期结果**：

- Polaris Service `owner` 变为 `payments-v2`。
- Polaris Service `tier` 字段被删除。
- Pod Label `app` 不会出现在 Service metadata 中。

### SYNC-12 pod-label-convergence

**目的**：验证 Pod Label 变更通过周期对账收敛，并验证保留字段优先级。

**操作**：修改 Pod Label：

```text
tenant=pod-tenant-v2
team=pod-team-v2
version=v3
region=cn-south
zone=zone-b
campus=campus-2
```

**预期结果**：

- metadata 中 tenant/team 更新。
- 顶层 Version 仍为 `v9`，因为 `polarismesh.cn/customVersion=v9` 优先于 Pod Label `version=v3`。
- 顶层 Location 更新为 `cn-south/zone-b/campus-2`。
- metadata 不包含 `version/protocol/region/zone/campus`。

### SYNC-13 namespace-disable-keeps-explicit-service

**目的**：验证关闭 Namespace 同步时只清理 controller 管理的实例，不删除 Polaris Service，也不影响显式同步的 Service。

**操作**：

```text
将 it-pc-sync Namespace 注解改为 sync=false
等待收敛
再改回 sync=true
```

**预期结果**：

- `namespace-only-demo` 的实例数变为 0。
- `namespace-only-demo` 的 Polaris Service 对象保留。
- 显式 `sync=true` 的 `multiport-demo` 仍有 2 个实例，Service 保留。
- 恢复 `sync=true` 后，`namespace-only-demo` 实例数恢复为 1。

**说明**：`sync=false` 的语义是清理实例，不是删除 Polaris Service。验证时应等待实例数归零并确认 Service 保留，不要使用 `wait_service_absent` 判断 Service 对象消失。

### SYNC-14 service-disable-cleans-polaris-instances

**目的**：验证单个 Service 关闭同步时清理实例并保留 Polaris Service。

**操作**：

```text
设置 metadata-demo 的 polarismesh.cn/sync=false
```

**预期结果**：

- `metadata-demo` 实例数变为 0。
- `metadata-demo` Polaris Service 对象保留。

---

## 6. 可选 Service 同步用例

### ALIAS-01 alias-stable-winner-and-handover

**运行条件**：`RUN_ALIAS_WINNER=1`，或单独运行 `service-sync/alias-winner.sh`。

**目的**：验证多个 Service 竞争同一别名时 winner 稳定，源 Service 删除后发生 handover。

**操作**：

1. 创建 `alias-a-<suffix>`，声明别名 `shared-alias-<suffix>`。
2. 等待别名指向 A。
3. 创建 `alias-b-<suffix>`，声明同一别名。
4. 确认别名仍指向 A，不因后创建者改变。
5. 删除 A。
6. 等待别名 handover 到 B。

**预期结果**：

- A 是初始 winner。
- B 创建后不会抢占 A。
- A 删除后，别名指向 B。
- 源 Service 删除后，脚本断言实例数为 0；Polaris Service 是否存在会记录
  日志，不作为该脚本的失败条件。
- 别名对象有意保留，不自动删除。

### MODE-01 all-mode service sync

**运行条件**：`RUN_ALL_MODE=1`，或单独运行 `service-sync/switch-mode.sh all`。

**目的**：验证 `serviceSync.mode=all` 会同步普通 Namespace 中未显式设置 `sync` 的 Service。

**操作**：

1. 读取当前 `injector-mesh` 中的 `serviceSync.mode`。
2. 切换为 `all` 并重启 controller。
3. 等待 `it-pc-sync-all/all-mode-demo` 的 Service 和 1 个实例。
4. 恢复原模式，默认为 `demand`。

**预期结果**：

- all 模式下 `all-mode-demo` 被同步，实例数 1。
- 恢复 demand 后 `all-mode-demo` 的实例被清理。
- 恢复 demand 后脚本断言实例数为 0；Polaris Service 是否存在会记录日志，
  但不是该脚本的失败条件。

> 该用例会重启 controller；在 EKlet 或镜像拉取较慢的环境，建议将 `WAIT_TIMEOUT` 调大（例如 300 秒以上）。

---

## 7. Sidecar / Java Agent 注入用例

### INJ-01 dns-mode-and-sidecar-config

**对象**：`it-pc-inject-default/dns-ok`。

**预期结果**：

- 存在 `polaris-sidecar` 容器。
- 存在 `polaris-sidecar-init` init container。
- Pod 注解 `sidecar.polarismesh.cn/status` 非空。
- 不存在 `envoy` 容器（DNS 模式不注入 envoy）。
- Sidecar env 包含：

```text
SIDECAR_DNS_SUFFIX=cluster.local
SIDECAR_DNS_TTL=30
SIDECAR_REGION=beijing
SIDECAR_NEARBY_MATCH_LEVEL=zone
SIDECAR_LOG_LEVEL=debug
```

### INJ-02 namespace-and-pod-label-selection

**对象**：`it-pc-inject-podlabel/pod-label-injected`。

**条件**：Namespace 没有注入 Label；Pod 自身有 `polarismesh.cn/inject=enabled` 和注入注解。

**预期结果**：存在 `polaris-sidecar` 容器。

### INJ-03 explicit-disable-annotation

**对象**：`it-pc-inject-default/dns-disabled-annotation`。

**条件**：Pod 注解 `polarismesh.cn/inject=false`。

**预期结果**：不存在 `polaris-sidecar`，也不存在 `polaris-*` init container。

### INJ-04 never-inject-label

**对象**：`it-pc-inject-default/dns-disabled-label`。

**条件**：Pod Label `polarismesh.cn/inject=disabled`。

**预期结果**：不存在 sidecar 和 `polaris-*` init container。

### INJ-05 dns-hostnetwork-disabled

**对象**：`it-pc-inject-default/dns-hostnetwork`。

**条件**：`hostNetwork=true`，注解要求注入 DNS 模式。

**预期结果**：Pod 保持 `hostNetwork=true`，但不注入 `polaris-sidecar`。

### INJ-06 javaagent-config-priority

**对象**：`it-pc-inject-java/java-config`。

**条件**：Namespace 开启注入；Pod 配置 Java Agent 和 `javaagentConfig`。

**预期结果**：

- 存在 `polaris-javaagent-init` init container。
- `JAVA_TOOL_OPTIONS` 包含：

```text
-Dspring.application.name=java-config-service
-Dspring.cloud.polaris.discovery.namespace=it-pc-inject-java
```

### INJ-07 javaagent-workload-name-fallback

**对象**：`it-pc-inject-java/java-workload` Deployment。

**条件**：配置 `workloadNameAsServiceName=true` 和 `workloadNamespaceAsServiceNamespace=true`。

**预期结果**：

- 存在 `polaris-javaagent-init`。
- 应用名自动取 workload 名 `java-workload`。
- Namespace 取 `it-pc-inject-java`。

### INJ-08 javaagent-hostnetwork-allowed

**对象**：`it-pc-inject-java/java-hostnetwork`。

**条件**：`hostNetwork=true`，开启 Java Agent。

**预期结果**：保持 `hostNetwork=true`，并且仍存在 `polaris-javaagent-init`。

### INJ-09 legacy-namespace-label

**对象**：`it-pc-inject-legacy/legacy-label-injected`。

**条件**：Namespace 使用旧版 `polaris-injection=enabled`。

**预期结果**：存在 `polaris-sidecar`。

### INJ-10 default-mode-not-injected

**对象**：`it-pc-inject-no-mode/no-mode-not-injected`。

**条件**：

- Namespace 设置 `polarismesh.cn/inject=enabled`，使 Pod 进入 Webhook。
- Namespace 不设置 `polaris-sidecar-mode`。
- Pod 不设置 `polarismesh.cn/sidecar-mode`，也不设置 `javaagent` 或
  `sidecar.polarismesh.cn/openOnDemand`。
- controller 的 `sidecarInject.mode` 为 `""` 或 `none`。

**预期结果**：

- 业务 Pod 创建成功，Webhook 不阻塞 Pod 创建。
- 存在业务容器 `app`。
- 不存在 `polaris-sidecar` 容器。
- 不存在 `envoy` 容器。
- 不存在以 `polaris-` 开头的 init container。
- Pod 不包含 `sidecar.polarismesh.cn/status` 注入状态注解。

**验证方式**：

```bash
kubectl -n it-pc-inject-no-mode get pod no-mode-not-injected -o json | \
  jq -e '
    ([.spec.containers[] | select(.name == "app")] | length) == 1 and
    ([.spec.containers[] | select(.name == "polaris-sidecar" or .name == "envoy")] | length) == 0 and
    ([.spec.initContainers[]? | select(.name | startswith("polaris-"))] | length) == 0 and
    ((.metadata.annotations["sidecar.polarismesh.cn/status"] // "") == "")
  '
```

该用例覆盖“Webhook 已被 Namespace Label 命中，但没有任何有效 sidecar mode”
的默认路径；它不同于 INJ-03/INJ-04 的显式关闭，也不同于 INJ-05 的 hostNetwork 限制。

### INJ-11 modes-are-independent

**目的**：确认 Sidecar 注入和 Service 同步是独立模式。

**测试对象**：

- `it-pc-inject-default/inject-only-service`：显式 `sync=false`。
- `it-pc-inject-default/dns-ok`：只有 Pod 注入，不创建 Polaris Service。

**预期结果**：Polaris 中不存在 `inject-only-service` 和 `dns-ok`。

---

## 8. 组合用例

### COMBO-01 service-sync-and-dns-injection

**对象**：`it-pc-combo/combo-demo`。

**条件**：Namespace 同时开启 Service 同步和 DNS 注入。

**预期结果**：

- Pod 存在 `polaris-sidecar` 和 `polaris-sidecar-init`。
- `SIDECAR_DNS_TTL=15`。
- Polaris Service metadata：`owner=combo-team`。
- Polaris 实例数 1。
- 实例 metadata：`tenant=combo-pod-tenant`（Pod Label 覆盖 Service Annotation）、`env=combo`、`source=polaris-controller`。
- 实例顶层 `version=v1`。
- 保留字段不出现在业务 metadata 中。

---

## 9. 清理和编排

### CLEAN-01 cleanup.sh

**目的**：删除全部专用 Namespace，并等待 Namespace 完全释放。

**预期结果**：

- 下列 Namespace 全部删除：

```text
it-pc-sync
it-pc-sync-legacy
it-pc-sync-all
it-pc-inject-default
it-pc-inject-podlabel
it-pc-inject-java
it-pc-inject-legacy
it-pc-inject-no-mode
it-pc-combo
it-pc-ns-lifecycle
it-pc-svc-lifecycle
it-pc-svc-alias
it-pc-ep-lifecycle
```

- `all_ns_absent` 逐一确认所有 Namespace 不存在，不会因为其中一个先不存在而提前通过。

**注意**：

- cleanup 不自动删除 Polaris 别名，例如 `metadata-demo-alias`、`shared-alias`。
- cleanup 不删除 Polaris Namespace；Namespace 生命周期用例保留的
  `it-pc-ns-lifecycle` 需要单独清理。
- Polaris Service 对象在关闭同步/删除 Namespace 时可能保留；如果需要完全清理测试数据，应按测试 Namespace 手动清理。
- EKlet 环境偶尔会延迟 Pod 删除；如果 Namespace 长时间 Terminating，可先 force delete 测试 Pod，再重新执行 cleanup。

### ORCH-01 run-all.sh

**目的**：统一编排默认套件和可选套件。

**默认顺序**：

```text
wait_rollout polaris-system/polaris-controller
RESET_CASES=1：清理全部专用 Namespace
service-sync/run.sh
lifecycle/namespace/run.sh
lifecycle/service/run.sh
lifecycle/endpoints/run.sh
inject/run.sh
combined/run.sh
all cases completed
```

**可选顺序**：

```text
RUN_ALIAS_WINNER=1：service-sync/alias-winner.sh
RUN_ALL_MODE=1：service-sync/switch-mode.sh all
```

**预期结果**：

- 默认套件全部打印 `all ... assertions passed`。
- 最后打印 `[case] all cases completed`。
- `CLEANUP=1` 时退出阶段自动调用 cleanup。

---

## 10. 注意事项

1. 不要通过 HTTP 查询的 `metadata.version`、`metadata.protocol`、`metadata.region`、`metadata.zone`、`metadata.campus` 判断同步结果；应断言顶层字段。
2. `sync=false` 的预期是清理 controller 管理的实例，不是删除 Polaris Service。
3. Pod Label `protocol` 和 `polarismesh.cn/metadata` 中的 `protocol` 不会影响顶层 Protocol；顶层 Protocol 由 EndpointPort 和 `polarismesh.cn/protocols` 决定。
4. `customVersion` 优先于 Pod Label `version`。
5. MODE-01 会重启 controller；在 EKlet 或镜像拉取较慢的环境建议增大 `WAIT_TIMEOUT`，避免只失败在滚动等待阶段。
6. 测试 Namespace 必须是专用的；cleanup 会直接删除这些 Kubernetes Namespace，
   不会删除 Polaris Namespace。
7. Namespace 生命周期用例会验证删除 Kubernetes Namespace 后 Polaris Namespace 仍保留；
   该 Polaris Namespace 不会由 cleanup 自动删除。
8. INJ-10 的“默认不注入”只适用于 `sidecarInject.mode` 为空或 `none`；显式配置集群
   mode 时，Pod 即使没有 Namespace/Pod mode 也会按集群配置注入。

---

## 11. Namespace / Service / Endpoints 生命周期用例

生命周期用例位于 `integration/cases/lifecycle`，默认由 `run-all.sh` 在
`service-sync` 之后执行。

### NS-LIFE-01 namespace-create-sync-and-service

**目的**：验证开启同步的 Namespace 会创建 Polaris Namespace，并同步其中的
Service 和实例。

**操作**：

```text
创建 it-pc-ns-lifecycle，注解 polarismesh.cn/sync=true
创建 ns-lifecycle-demo Deployment 和 Service
```

**预期结果**：

- Polaris 中存在 `it-pc-ns-lifecycle` Namespace。
- Polaris 中存在 `it-pc-ns-lifecycle/ns-lifecycle-demo` Service。
- Service 下有 1 个实例。

**说明**：如果 Polaris Namespace 在用例开始前已经存在，用例会跳过
“从不存在到创建”的严格前置断言，但仍验证对象收敛。

**验证方式**：`lifecycle/namespace/run.sh` 使用 `wait_namespace`、
`wait_service` 和 `wait_instance_count`。

### NS-LIFE-02 namespace-delete-retains-polaris-resources

**目的**：验证删除 Kubernetes Namespace 不会删除 Polaris Namespace 和 Service，
但会移除同步实例。

**操作**：删除 `it-pc-ns-lifecycle`，等待 Kubernetes Namespace 消失。

**预期结果**：

- Polaris Namespace `it-pc-ns-lifecycle` 仍存在。
- Polaris Service `it-pc-ns-lifecycle/ns-lifecycle-demo` 仍存在。
- 实例数收敛到 0。

**验证方式**：Kubernetes Namespace 消失后重新查询 Polaris Namespace 和
Service，并等待该 Service 的实例数归零。

### SVC-LIFE-01 service-types-and-cross-namespace-alias

**目的**：验证不同 Service 类型和跨 Namespace 别名。

**测试对象**：

```text
it-pc-svc-lifecycle/svc-clusterip
it-pc-svc-lifecycle/svc-nodeport
it-pc-svc-lifecycle/svc-loadbalancer
it-pc-svc-lifecycle/svc-cross-ns-alias
it-pc-svc-lifecycle/svc-delete
it-pc-svc-alias/cross-ns-alias
```

**预期结果**：

- ClusterIP、NodePort、LoadBalancer 三种 Service 均同步到 Polaris。
- 上述 5 个源 Service 各有 1 个实例；`svc-delete` 随后用于 SVC-LIFE-02。
- `it-pc-svc-alias/cross-ns-alias` 指向
  `it-pc-svc-lifecycle/svc-cross-ns-alias`。

**说明**：ExternalName 没有 selector 和 ports，不在 controller 同步路径内，
因此不纳入正向断言。

**验证方式**：`wait_service`、`wait_instance_count` 和
`assert_alias_target`。

### SVC-LIFE-02 service-delete-retains-service-removes-owned-instances

**目的**：验证删除 Kubernetes Service 时保留 Polaris Service，只清理本集群
controller 管理的实例。

**操作**：

1. 等待 `svc-delete` 同步完成并有 1 个 controller 实例。
2. 向 Polaris 手工注册一个 `source=sdk`、`clusterName=integration-external` 的实例，
   以及一个 `source=polaris-controller`、`clusterName=integration-other-cluster`、
   带独立 `k8sServiceUID` 的实例。
3. 删除 Kubernetes Service `svc-delete`。

手工实例直接通过 Polaris HTTP API 注册，不经过 controller，因此可以显式
指定 `source`、`clusterName` 和 `k8sServiceUID`，用于验证删除边界。

**预期结果**：

- Polaris Service `it-pc-svc-lifecycle/svc-delete` 仍存在。
- 当前集群 controller 管理的实例数变为 0。当前集群名从
  `injector-mesh.mesh.clusterName` 读取。
- 手工注册的 SDK 实例和其他集群 controller 实例仍存在，且
  `source`/`clusterName` metadata 未被改写。

**归属规则**：controller 实例按 `source=polaris-controller` 和
`clusterName=当前集群` 判断；`k8sServiceUID` 不决定实例归属，因此其他集群
controller 实例即使带 `k8sServiceUID` 也不能被本集群删除。

**验证方式**：`register_external_instance`、
`wait_controller_instance_count`、`wait_instance_key` 和显式 jq metadata
断言。外部实例清理等待由 `EXTERNAL_DELETE_TIMEOUT` 控制。

### EP-LIFE-01 endpoints-address-health-and-ip-port-mapping

**目的**：验证 Endpoints 的 IP:port 组合与 Polaris 实例一致，Ready 实例为健康
且未隔离。

**操作**：读取 `it-pc-ep-lifecycle/ep-demo` 的 Endpoints 和 Polaris 实例。

**预期结果**：

- Ready Endpoints 的 `IP:port` 集合与 Polaris 实例集合完全一致。
- 所有实例 `healthy=true`、`isolate=false`。

**验证方式**：`endpoint_keys_match_instances` 和
`ready_instances_healthy`。

### EP-LIFE-02 endpoints-scale-up

**操作**：Deployment 副本数从 1 扩到 2。

**预期结果**：实例数变为 2，IP:port 映射和健康状态继续一致。

### EP-LIFE-03 endpoints-scale-down

**操作**：Deployment 副本数从 2 缩到 1。

**预期结果**：实例数变为 1，被移除 Pod 的实例从 Polaris 删除。

### EP-LIFE-04 endpoints-remove-all-addresses

**操作**：Deployment 副本数缩到 0。

**预期结果**：

- Endpoints 没有 Ready 地址；Endpoints 对象被删除时也视为没有 Ready 地址。
- Polaris 实例数变为 0。
- Polaris Service `it-pc-ep-lifecycle/ep-demo` 仍存在。
