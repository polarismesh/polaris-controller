# Polaris Controller Integration Test Cases

**Updated**: 2026-11-20

**Scope**: Service synchronization, Namespace/Service/Endpoints lifecycle,
Sidecar and Java Agent injection, combined scenarios, optional alias and mode
changes, cleanup, and orchestration under `integration/cases`.

> This document describes each case, its expected result, and its verification
> method. Environment setup, resource retention, cleanup, packaging, and manual
> upload are documented in [README.md](../README.md). The Chinese case
> reference is [test-zh.md](test-zh.md).

---

## 1. Test Objectives

The suite verifies:

1. Synchronization from Kubernetes Namespace, Service, Endpoints, and Pods to
   Polaris Services and instances.
2. Precedence among Service annotations, Pod labels, reserved fields, and
   protected fields.
3. The combined semantics of `polarismesh.cn/sync`,
   `polarismesh.cn/enableRegister`, Namespace annotations, and
   `serviceSync.mode`.
4. TTL, multi-port protocols, StatefulSet custom weights, NotReady instance
   isolation, and aliases.
5. DNS Sidecar and Java Agent selection, explicit disable paths, default
   non-injection, configuration overrides, hostNetwork behavior, and legacy
   Namespace labels.
6. Service synchronization and Sidecar injection running together.
7. Namespace, Service, and Endpoints lifecycle behavior, including retained
   Polaris objects and address-set convergence.

---

## 2. Prerequisites

### 2.1 Cluster and Tools

- The test machine can access the target Kubernetes cluster and create or
  delete Namespaces, Deployments, StatefulSets, Services, Endpoints, and Pods.
- `kubectl`, `curl`, and `jq` are available, or `KUBECTL`, `CURL`, and `JQ`
  point to usable executables.
- Polaris Controller is deployed in `polaris-system` as
  `statefulset/polaris-controller`.
- The test machine can reach the Polaris HTTP API. By default, the URL is
  derived from the `injector-mesh` ConfigMap as
  `http://<serverAddress>:8090`.
- The Polaris token can query Namespace, Service, Instance, and Alias objects.
  The Service lifecycle case also registers and deletes test instances.
- `injector-mesh.mesh.clusterName` is non-empty. The Service lifecycle case
  uses it to distinguish controller instances from the current cluster and
  another cluster.
- The cluster permits NodePort and LoadBalancer Services.
- The cluster uses `serviceSync.mode=demand` by default. `RUN_ALL_MODE=1`
  temporarily switches it to `all` and restores it.
- INJ-10 requires the controller `sidecarInject.mode` to be empty or `none`.
  A cluster explicitly configured with `mesh`, `dns`, or `java-agent` does not
  satisfy this case.

### 2.2 Dedicated Namespaces

The tests create and delete these dedicated Namespaces:

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

Do not place business resources in these Namespaces. `RESET_CASES=1` deletes
them before applying test manifests.

The Namespace lifecycle case deletes `it-pc-ns-lifecycle` itself. The matching
Polaris Namespace is intentionally retained, and `cleanup.sh` does not delete
Polaris Namespaces.

### 2.3 Data Mapping Rules

- Kubernetes Service `metadata.labels` become Polaris Service metadata.
- Service `polarismesh.cn/metadata` and actual Pod labels are merged into
  Polaris instance metadata.
- A Pod label overrides a Service annotation with the same key.
- `source`, `clusterName`, and `k8sServiceUID` are controller-protected fields.
  User configuration cannot override them.
- `version`, `protocol`, `region`, `zone`, and `campus` are reserved fields
  promoted to instance top-level attributes. They are not copied into business
  metadata.
- `polarismesh.cn/customVersion` has higher priority than the Pod label
  `version` when determining the instance top-level Version.
- EndpointPort and `polarismesh.cn/protocols` determine the top-level Protocol.
  A Pod label or metadata value named `protocol` is ignored.
- The HTTP `/naming/v1/instances` response does not duplicate reserved fields
  in metadata. Assertions use the top-level `version`, `protocol`, and
  `location`.

---

## 3. Execution

### 3.1 Default Full Run

```bash
cd integration/cases
./run-all.sh
```

Default order:

```text
wait for controller rollout
RESET_CASES=1: delete dedicated Namespaces
service-sync
lifecycle/namespace
lifecycle/service
lifecycle/endpoints
inject
combined
```

The default policy retains resources after the run:

```bash
./run-all.sh --keep
```

### 3.2 Optional Cases

```bash
RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh --keep
```

`RUN_ALIAS_WINNER=1` adds the alias winner and handover case.

`RUN_ALL_MODE=1` adds the `serviceSync.mode=all` case. It changes the
`polaris-system/injector-mesh` ConfigMap and restarts the controller. The
original mode is restored by default.

### 3.3 Individual Suites

```bash
RESET_CASES=1 ./service-sync/run.sh
RESET_CASES=1 ./lifecycle/namespace/run.sh
RESET_CASES=1 ./lifecycle/service/run.sh
RESET_CASES=1 ./lifecycle/endpoints/run.sh
RESET_CASES=1 ./inject/run.sh
RESET_CASES=1 ./combined/run.sh
./service-sync/alias-winner.sh
./service-sync/switch-mode.sh all
./inspect.sh
./cleanup.sh
```

`RESET_CASES=1` first deletes every dedicated Namespace to avoid stale
resources.

### 3.4 Environment Variables

| Variable | Default | Description |
|---|---|---|
| `RESET_CASES` | `1` in `run-all.sh` | Delete dedicated Namespaces before running |
| `CLEANUP` | `0` | Delete dedicated Namespaces on exit |
| `KEEP_RESOURCES` | `1` | Retain dedicated Namespaces and resources on exit; conflicts with `CLEANUP=1` |
| `RUN_ALIAS_WINNER` | `0` | Run the optional alias winner/handover case |
| `RUN_ALL_MODE` | `0` | Run the optional `serviceSync.mode=all` case |
| `WAIT_TIMEOUT` | `120` | Pod, Namespace, and rollout wait timeout in seconds |
| `RESYNC_TIMEOUT` | `180` | Polaris Service/Instance convergence timeout in seconds |
| `EXTERNAL_DELETE_TIMEOUT` | `30` | Timeout for deleting manually registered external instances |
| `POLARIS_HTTP_URL` | Auto-derived | Polaris HTTP API address |
| `POLARIS_TOKEN` | Read from ConfigMap | Polaris API token |
| `KUBECTL` / `CURL` / `JQ` | Command names | Override tool paths |

---

## 4. Case Index

| ID | Case | Expected Result | Type |
|---|---|---|---|
| SYNC-01 | modes-are-independent-service-only-has-no-sidecar | Service sync does not inject sidecars | Default |
| SYNC-02 | namespace-annotation-and-service-disable | Namespace annotation and Service `sync=false` precedence | Default |
| SYNC-03 | enableRegister | Legacy `enableRegister=true` enables sync | Default |
| SYNC-04 | all-mode-service-is-not-synced-in-demand | Demand mode does not sync an ordinary Namespace automatically | Default |
| SYNC-05 | service-and-instance-metadata | Service/Pod metadata, reserved fields, and precedence | Default |
| SYNC-06 | ttl | TTL and TCP protocol are correct; Pod protocol label is ignored | Default |
| SYNC-07 | multi-port-protocol | Multiple ports create multiple instances with mapped protocols | Default |
| SYNC-08 | statefulset-custom-weight | StatefulSet Pods use custom indexed weights | Default |
| SYNC-09 | not-ready-endpoint-isolated | NotReady Endpoints become isolated instances | Default |
| SYNC-10 | alias | Service aliases target the source Services | Default |
| SYNC-11 | service-label-convergence | Service label additions and removals converge | Default |
| SYNC-12 | pod-label-convergence | Pod label metadata and reserved fields converge | Default |
| SYNC-13 | namespace-disable-keeps-explicit-service | Disabling a Namespace removes instances and keeps Services | Default |
| SYNC-14 | service-disable-cleans-polaris-instances | Disabling one Service removes its instances and keeps its Service | Default |
| ALIAS-01 | alias-stable-winner-and-handover | The alias winner is stable and hands over after deletion | Optional |
| MODE-01 | all-mode-service-sync | `all` syncs the ordinary Service; restoring `demand` removes instances | Optional |
| INJ-01 | dns-mode-and-sidecar-config | DNS sidecar/init containers and environment are correct | Default |
| INJ-02 | namespace-and-pod-label-selection | Pod labels can trigger injection | Default |
| INJ-03 | explicit-disable-annotation | A Pod annotation disables injection | Default |
| INJ-04 | never-inject-label | `disabled` label prevents injection | Default |
| INJ-05 | dns-hostnetwork-disabled | DNS mode does not inject on hostNetwork | Default |
| INJ-06 | javaagent-config-priority | Java Agent configuration overrides defaults | Default |
| INJ-07 | javaagent-workload-name-fallback | Deployment name and Namespace fallbacks work | Default |
| INJ-08 | javaagent-hostnetwork-allowed | Java Agent injection remains allowed on hostNetwork | Default |
| INJ-09 | legacy-namespace-label | Legacy `polaris-injection` label is supported | Default |
| INJ-10 | default-mode-not-injected | Webhook match without a mode does not inject | Default |
| INJ-11 | modes-are-independent | Injection does not trigger Service sync | Default |
| COMBO-01 | service-sync-and-dns-injection | One Pod is synced and receives a DNS sidecar | Default |
| NS-LIFE-01 | namespace-create-sync-and-service | Namespace and Service appear in Polaris | Default |
| NS-LIFE-02 | namespace-delete-retains-polaris-resources | Kubernetes Namespace deletion retains Polaris Namespace/Service | Default |
| SVC-LIFE-01 | service-types-and-cross-namespace-alias | ClusterIP/NodePort/LoadBalancer and cross-Namespace alias work | Default |
| SVC-LIFE-02 | service-delete-retains-service-removes-owned-instances | Polaris Service remains; only current-cluster controller instances are removed | Default |
| EP-LIFE-01 | endpoints-address-health-and-ip-port-mapping | Endpoints IP:port matches Polaris instances; Ready instances are healthy | Default |
| EP-LIFE-02 | endpoints-scale-up | Scaling up increases endpoints and instances | Default |
| EP-LIFE-03 | endpoints-scale-down | Scaling down decreases endpoints and instances | Default |
| EP-LIFE-04 | endpoints-remove-all-addresses | Removing all addresses leaves zero instances and retains the Service | Default |
| CLEAN-01 | cleanup.sh | Delete all dedicated Kubernetes Namespaces | Tool |
| ORCH-01 | run-all.sh | Run suites in order and retain or clean resources | Tool |

---

## 5. Service Synchronization Cases

### SYNC-01 modes-are-independent-service-only-has-no-sidecar

**Purpose**: Verify that Service sync does not automatically inject a sidecar.

**Operation**: Inspect the containers in the `it-pc-sync/sync-shared` Pod.

**Expected**:

- Business container `app` exists.
- No `polaris-sidecar` container exists.
- No init container whose name starts with `polaris-` exists.

**Verification**:

```bash
kubectl -n it-pc-sync get pod -l app=sync-shared -o json | \
  jq -e '([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 0'
```

### SYNC-02 namespace-annotation-and-service-disable

**Purpose**: Verify precedence among Namespace annotations, Service `sync`, and
the legacy `enableRegister` annotation.

**Objects**:

- `namespace-only-demo`: no Service annotation; Namespace `sync=true`.
- `disabled-demo`: Service `sync=false`.
- `sync-false-overrides-enable-register`: Service `sync=false` and
  `enableRegister=true`.
- `enable-register-false-demo`: Service `enableRegister=false`, no `sync`, and
  Namespace `sync=true`.

**Expected**:

- `namespace-only-demo` is synchronized.
- `disabled-demo` is not synchronized.
- `sync-false-overrides-enable-register` is not synchronized because
  `sync=false` has priority.
- `enable-register-false-demo` is synchronized because Namespace
  `sync=true` still enables it. `enableRegister=false` is not a hard disable.

**Verification**: `wait_service` and `wait_service_absent` in
`service-sync/run.sh`.

### SYNC-03 enableRegister

**Purpose**: Verify compatibility with
`polarismesh.cn/enableRegister=true`.

**Object**: `it-pc-sync-legacy/enable-register-demo`. The Service sets only
`enableRegister=true`; the Namespace has no `sync` annotation.

**Expected**: The Service is synchronized to Polaris.

### SYNC-04 all-mode-service-is-not-synced-in-demand

**Purpose**: Verify that demand mode does not automatically sync an ordinary
Service in a Namespace without an explicit sync annotation.

**Object**: `it-pc-sync-all/all-mode-demo`. Neither the Namespace nor the
Service has a `sync` annotation.

**Expected**: When `serviceSync.mode=demand`, the controller instance count for
`it-pc-sync-all/all-mode-demo` is zero. A Polaris Service retained from an
earlier all-mode run may remain, but it must not contain a current controller
instance.

### SYNC-05 service-and-instance-metadata

**Purpose**: Verify the final Service label, Service annotation metadata, Pod
label, reserved-field, and protected-field results.

**Objects**: `it-pc-sync/metadata-demo` and the `sync-shared` Pod.

**Expected Polaris Service**:

```text
service.metadata.owner = payments
service.metadata.tier = backend
service.metadata does not contain the Pod-only label app
```

**Expected Polaris instance**:

```text
metadata.tenant = pod-tenant
metadata.team = pod-team
metadata.env = service-env
metadata.app = sync-shared
metadata.polarismesh.cn/customVersion = v9
metadata.source = polaris-controller
metadata.clusterName is non-empty
metadata does not contain version/protocol/region/zone/campus
version = v9
protocol = http
weight = 70
location.region = cn-north
location.zone = zone-a
location.campus = campus-1
```

**Verification**: `assert_service_metadata` and the jq assertions in
`service-sync/run.sh`.

### SYNC-06 ttl

**Purpose**: Verify `polarismesh.cn/ttl` and reserved protocol handling.

**Object**: `it-pc-sync/ttl-demo`. The Pod label contains `protocol=grpc`.

**Expected**:

- Instance count is 1.
- `enableHealthCheck=true`.
- `healthCheck.heartbeat.ttl=60`.
- Top-level `protocol=TCP` because no `polarismesh.cn/protocols` mapping is
  configured.
- The Pod `protocol=grpc` label is ignored and is absent from metadata.

### SYNC-07 multi-port-protocol

**Purpose**: Verify that a multi-port Service creates multiple instances and
applies named-port protocol mappings.

**Object**: `it-pc-sync/multiport-demo` with `http:8083` and `grpc:9090`,
mapped to `http` and `grpc`.

**Expected**:

- Instance count is 2.
- Sorted top-level protocols are `["grpc","http"]`.

### SYNC-08 statefulset-custom-weight

**Purpose**: Verify StatefulSet indexed custom weights.

**Object**: `it-pc-sync/stateful-demo` with two replicas and
`customWeight={"0-8080":80,"1-8080":20}`.

**Expected**:

- Instance count is 2.
- Sorted weights are `[20,80]`.

### SYNC-09 not-ready-endpoint-isolated

**Purpose**: Verify that a NotReady Endpoint is registered as an isolated
instance rather than omitted.

**Object**: `it-pc-sync/notready-demo`. Its readiness probe targets unserved
port 18080.

**Expected**:

- The Endpoints object contains `notReadyAddresses`.
- Polaris instance count is 1.
- The instance has `isolate=true`.

### SYNC-10 alias

**Purpose**: Verify Service alias creation and metadata-driven aliases.

**Objects**:

- `alias-winner-a` -> `shared-alias`
- `metadata-demo` -> `metadata-demo-alias`

**Expected**:

- `it-pc-sync/shared-alias` targets `it-pc-sync/alias-winner-a`.
- `it-pc-sync/metadata-demo-alias` targets
  `it-pc-sync/metadata-demo`.

### SYNC-11 service-label-convergence

**Purpose**: Verify that Service label additions and removals converge into
Polaris Service metadata.

**Operations**:

```text
Add owner=payments-v2 to metadata-demo.
Remove the tier label from metadata-demo.
```

**Expected**:

- Polaris Service `owner` becomes `payments-v2`.
- Polaris Service `tier` is removed.
- The Pod label `app` does not appear in Service metadata.

### SYNC-12 pod-label-convergence

**Purpose**: Verify periodic reconciliation of Pod label changes and reserved
field precedence.

**Operations**: Change the Pod labels:

```text
tenant=pod-tenant-v2
team=pod-team-v2
version=v3
region=cn-south
zone=zone-b
campus=campus-2
```

**Expected**:

- Metadata `tenant` and `team` are updated.
- Top-level Version remains `v9` because
  `polarismesh.cn/customVersion=v9` has priority over Pod `version=v3`.
- Top-level Location becomes `cn-south/zone-b/campus-2`.
- Metadata does not contain `version/protocol/region/zone/campus`.

### SYNC-13 namespace-disable-keeps-explicit-service

**Purpose**: Verify that disabling Namespace sync removes only
controller-managed instances. It must not delete Polaris Services or affect an
explicitly synced Service.

**Operations**:

```text
Set polarismesh.cn/sync=false on the it-pc-sync Namespace.
Wait for convergence.
Set it back to sync=true.
```

**Expected**:

- `namespace-only-demo` instance count becomes 0.
- The `namespace-only-demo` Polaris Service remains.
- The explicit `multiport-demo` Service still has two instances and remains.
- After restoring `sync=true`, `namespace-only-demo` returns to one instance.

`sync=false` means remove instances, not delete the Polaris Service. Confirm the
Service remains; do not use `wait_service_absent` for this assertion.

### SYNC-14 service-disable-cleans-polaris-instances

**Purpose**: Verify that disabling one Service removes its instances and keeps
its Polaris Service.

**Operation**: Set `polarismesh.cn/sync=false` on `metadata-demo`.

**Expected**:

- `metadata-demo` instance count becomes 0.
- The `metadata-demo` Polaris Service remains.

---

## 6. Optional Service Synchronization Cases

### ALIAS-01 alias-stable-winner-and-handover

**Run condition**: `RUN_ALIAS_WINNER=1` or run
`service-sync/alias-winner.sh`.

**Purpose**: Verify a stable winner when multiple Services request the same
alias, followed by handover after the winner is deleted.

**Operations**:

1. Create `alias-a-<suffix>` with alias `shared-alias-<suffix>`.
2. Wait until the alias targets A.
3. Create `alias-b-<suffix>` with the same alias.
4. Confirm that B does not steal the alias from A.
5. Delete A.
6. Wait for handover to B.

**Expected**:

- A is the initial winner.
- B does not take over while A exists.
- The alias targets B after A is deleted.
- The generated alias remains in Polaris and is not deleted automatically.

### MODE-01 all-mode-service-sync

**Run condition**: `RUN_ALL_MODE=1` or run
`service-sync/switch-mode.sh all`.

**Purpose**: Verify that `serviceSync.mode=all` synchronizes an ordinary
Service without an explicit `sync` annotation.

**Operations**:

1. Read the current `injector-mesh` `serviceSync.mode`.
2. Switch to `all` and restart the controller.
3. Wait for `it-pc-sync-all/all-mode-demo` and one instance.
4. Restore the original mode, normally `demand`.

**Expected**:

- In all mode, `all-mode-demo` is synced with one instance.
- After restoring demand, its controller instances are removed.
- The script waits for instance count 0. Polaris Service retention is logged
  but is not a failure condition for this script.

This case restarts the controller. Use a larger `WAIT_TIMEOUT`, for example 300
seconds, on EKlet or clusters with slow image pulls.

---

## 7. Sidecar and Java Agent Injection Cases

### INJ-01 dns-mode-and-sidecar-config

**Object**: `it-pc-inject-default/dns-ok`.

**Expected**:

- A `polaris-sidecar` container exists.
- A `polaris-sidecar-init` init container exists.
- Annotation `sidecar.polarismesh.cn/status` is non-empty.
- No `envoy` container exists in DNS mode.
- Sidecar environment includes:

```text
SIDECAR_DNS_SUFFIX=cluster.local
SIDECAR_DNS_TTL=30
SIDECAR_REGION=beijing
SIDECAR_NEARBY_MATCH_LEVEL=zone
SIDECAR_LOG_LEVEL=debug
```

### INJ-02 namespace-and-pod-label-selection

**Object**: `it-pc-inject-podlabel/pod-label-injected`.

**Condition**: The Namespace has no injection label; the Pod has
`polarismesh.cn/inject=enabled` and an injection annotation.

**Expected**: A `polaris-sidecar` container exists.

### INJ-03 explicit-disable-annotation

**Object**: `it-pc-inject-default/dns-disabled-annotation`.

**Condition**: Pod annotation `polarismesh.cn/inject=false`.

**Expected**: No `polaris-sidecar` and no `polaris-*` init container exists.

### INJ-04 never-inject-label

**Object**: `it-pc-inject-default/dns-disabled-label`.

**Condition**: Pod label `polarismesh.cn/inject=disabled`.

**Expected**: No sidecar and no `polaris-*` init container exists.

### INJ-05 dns-hostnetwork-disabled

**Object**: `it-pc-inject-default/dns-hostnetwork`.

**Condition**: `hostNetwork=true` and an annotation requests DNS mode.

**Expected**: The Pod remains on the host network but receives no
`polaris-sidecar`.

### INJ-06 javaagent-config-priority

**Object**: `it-pc-inject-java/java-config`.

**Condition**: Namespace injection is enabled and the Pod configures a Java
Agent with `javaagentConfig`.

**Expected**:

- A `polaris-javaagent-init` init container exists.
- `JAVA_TOOL_OPTIONS` contains:

```text
-Dspring.application.name=java-config-service
-Dspring.cloud.polaris.discovery.namespace=it-pc-inject-java
```

### INJ-07 javaagent-workload-name-fallback

**Object**: Deployment `it-pc-inject-java/java-workload`.

**Condition**: `workloadNameAsServiceName=true` and
`workloadNamespaceAsServiceNamespace=true`.

**Expected**:

- A `polaris-javaagent-init` init container exists.
- The application name defaults to `java-workload`.
- The Namespace defaults to `it-pc-inject-java`.

### INJ-08 javaagent-hostnetwork-allowed

**Object**: `it-pc-inject-java/java-hostnetwork`.

**Condition**: `hostNetwork=true` with Java Agent enabled.

**Expected**: The Pod stays on the host network and still receives a
`polaris-javaagent-init` init container.

### INJ-09 legacy-namespace-label

**Object**: `it-pc-inject-legacy/legacy-label-injected`.

**Condition**: The Namespace uses the legacy
`polaris-injection=enabled` label.

**Expected**: A `polaris-sidecar` container exists.

### INJ-10 default-mode-not-injected

**Object**: `it-pc-inject-no-mode/no-mode-not-injected`.

**Conditions**:

- The Namespace has `polarismesh.cn/inject=enabled`, so the Pod enters the
  webhook path.
- The Namespace has no `polaris-sidecar-mode`.
- The Pod has no `polarismesh.cn/sidecar-mode`, `javaagent`, or
  `sidecar.polarismesh.cn/openOnDemand`.
- Controller `sidecarInject.mode` is empty or `none`.

**Expected**:

- The business Pod is created successfully and the webhook does not block it.
- Business container `app` exists.
- No `polaris-sidecar` or `envoy` container exists.
- No init container whose name starts with `polaris-` exists.
- Annotation `sidecar.polarismesh.cn/status` is absent.

**Verification**:

```bash
kubectl -n it-pc-inject-no-mode get pod no-mode-not-injected -o json | \
  jq -e '
    ([.spec.containers[] | select(.name == "app")] | length) == 1 and
    ([.spec.containers[] | select(.name == "polaris-sidecar" or .name == "envoy")] | length) == 0 and
    ([.spec.initContainers[]? | select(.name | startswith("polaris-"))] | length) == 0 and
    ((.metadata.annotations["sidecar.polarismesh.cn/status"] // "") == "")
  '
```

This covers a webhook match without a valid sidecar mode. It differs from the
explicit disable cases INJ-03/INJ-04 and the hostNetwork restriction INJ-05.

### INJ-11 modes-are-independent

**Purpose**: Verify that Sidecar injection and Service synchronization are
independent.

**Objects**:

- `it-pc-inject-default/inject-only-service`: explicit `sync=false`.
- `it-pc-inject-default/dns-ok`: injected Pod with no Service sync.

**Expected**: Neither `inject-only-service` nor `dns-ok` exists in Polaris.

---

## 8. Combined Case

### COMBO-01 service-sync-and-dns-injection

**Object**: `it-pc-combo/combo-demo`.

**Condition**: The Namespace enables both Service sync and DNS injection.

**Expected**:

- The Pod has `polaris-sidecar` and `polaris-sidecar-init`.
- `SIDECAR_DNS_TTL=15`.
- Polaris Service metadata contains `owner=combo-team`.
- Polaris instance count is 1.
- Instance metadata contains `tenant=combo-pod-tenant` from the Pod label,
  `env=combo`, and `source=polaris-controller`.
- Instance top-level `version=v1`.
- Reserved fields do not appear in business metadata.

---

## 9. Lifecycle Cases

Lifecycle cases live under `integration/cases/lifecycle` and run after
`service-sync` in `run-all.sh`.

### NS-LIFE-01 namespace-create-sync-and-service

**Purpose**: Verify that a synced Namespace creates a Polaris Namespace and
synchronizes its Service and instance.

**Operations**:

```text
Create it-pc-ns-lifecycle with polarismesh.cn/sync=true.
Create the ns-lifecycle-demo Deployment and Service.
```

**Expected**:

- Polaris Namespace `it-pc-ns-lifecycle` exists.
- Polaris Service `it-pc-ns-lifecycle/ns-lifecycle-demo` exists.
- The Service has one instance.

If the Polaris Namespace already exists before the case, the strict
"created from absent" precondition is skipped, but convergence is still
verified.

**Verification**: `wait_namespace`, `wait_service`, and
`wait_instance_count` in `lifecycle/namespace/run.sh`.

### NS-LIFE-02 namespace-delete-retains-polaris-resources

**Purpose**: Verify that deleting the Kubernetes Namespace does not delete the
Polaris Namespace or Service, while synced instances are removed.

**Operation**: Delete `it-pc-ns-lifecycle` and wait for the Kubernetes
Namespace to disappear.

**Expected**:

- Polaris Namespace `it-pc-ns-lifecycle` remains.
- Polaris Service `it-pc-ns-lifecycle/ns-lifecycle-demo` remains.
- Instance count converges to 0.

**Verification**: Query Polaris after Kubernetes Namespace deletion and wait
for the instance count to become 0.

### SVC-LIFE-01 service-types-and-cross-namespace-alias

**Purpose**: Verify multiple Service types and a cross-Namespace alias.

**Objects**:

```text
it-pc-svc-lifecycle/svc-clusterip
it-pc-svc-lifecycle/svc-nodeport
it-pc-svc-lifecycle/svc-loadbalancer
it-pc-svc-lifecycle/svc-cross-ns-alias
it-pc-svc-lifecycle/svc-delete
it-pc-svc-alias/cross-ns-alias
```

**Expected**:

- ClusterIP, NodePort, and LoadBalancer Services are synchronized.
- Each of the five source Services has one instance; `svc-delete` is used by
  SVC-LIFE-02.
- `it-pc-svc-alias/cross-ns-alias` targets
  `it-pc-svc-lifecycle/svc-cross-ns-alias`.

ExternalName is excluded because it has no selector or ports and is outside the
controller sync path.

**Verification**: `wait_service`, `wait_instance_count`, and
`assert_alias_target`.

### SVC-LIFE-02 service-delete-retains-service-removes-owned-instances

**Purpose**: Verify that deleting a Kubernetes Service retains the Polaris
Service and removes only current-cluster controller instances.

**Operations**:

1. Wait for `svc-delete` to sync with one controller instance.
2. Manually register an SDK instance with `source=sdk` and
   `clusterName=integration-external`.
3. Register another controller instance with
   `clusterName=integration-other-cluster` and a distinct `k8sServiceUID`.
4. Delete Kubernetes Service `svc-delete`.

The manual instances use the Polaris HTTP API directly, so the test can set
`source`, `clusterName`, and `k8sServiceUID` explicitly.

**Expected**:

- Polaris Service `it-pc-svc-lifecycle/svc-delete` remains.
- Current-cluster controller instance count becomes 0. The cluster name comes
  from `injector-mesh.mesh.clusterName`.
- The SDK and other-cluster controller instances remain with unchanged
  `source` and `clusterName` metadata.

Controller instance ownership is determined by
`source=polaris-controller` and `clusterName=<current cluster>`.
`k8sServiceUID` does not determine ownership, so a foreign controller instance
must not be deleted by this cluster.

**Verification**: `register_external_instance`,
`wait_controller_instance_count`, `wait_instance_key`, and explicit jq
metadata assertions. `EXTERNAL_DELETE_TIMEOUT` controls external-instance
cleanup waits.

### EP-LIFE-01 endpoints-address-health-and-ip-port-mapping

**Purpose**: Verify that Endpoints IP:port values match Polaris instances and
that Ready instances are healthy and not isolated.

**Operation**: Read `it-pc-ep-lifecycle/ep-demo` Endpoints and Polaris
instances.

**Expected**:

- The Ready Endpoints IP:port set exactly matches the Polaris instance set.
- Every instance has `healthy=true` and `isolate=false`.

**Verification**: `endpoint_keys_match_instances` and
`ready_instances_healthy`.

### EP-LIFE-02 endpoints-scale-up

**Operation**: Scale the Deployment from 1 to 2 replicas.

**Expected**: Instance count becomes 2 and IP:port mapping and health remain
consistent.

### EP-LIFE-03 endpoints-scale-down

**Operation**: Scale the Deployment from 2 to 1 replica.

**Expected**: Instance count becomes 1 and the removed Pod's instance is
deleted from Polaris.

### EP-LIFE-04 endpoints-remove-all-addresses

**Operation**: Scale the Deployment to 0 replicas.

**Expected**:

- Endpoints has no Ready addresses. A deleted Endpoints object also counts as
  no Ready addresses.
- Polaris instance count becomes 0.
- Polaris Service `it-pc-ep-lifecycle/ep-demo` remains.

---

## 10. Cleanup and Orchestration

### CLEAN-01 cleanup.sh

**Purpose**: Delete all dedicated Namespaces and wait for complete release.

**Command**:

```bash
cd integration/cases
./cleanup.sh
```

**Expected**:

- All dedicated Namespaces are deleted.
- Every Namespace is checked, so one disappearing early does not make the
  wait pass prematurely.

**Notes**:

- Cleanup does not delete Polaris aliases such as `metadata-demo-alias` and
  `shared-alias`.
- Cleanup does not delete Polaris Namespaces. The
  `it-pc-ns-lifecycle` Polaris Namespace must be removed separately.
- Polaris Services may remain after sync is disabled or a Kubernetes Namespace
  is deleted. Remove test data manually when a completely clean Polaris
  environment is required.
- On EKlet, Pod deletion can be delayed. If a Namespace remains Terminating,
  force-delete test Pods and rerun cleanup.

### ORCH-01 run-all.sh

**Purpose**: Run the default and optional suites in a stable order.

**Default policy**: Retain test Namespaces and any remaining Pods, workloads,
Services, and Endpoints for inspection.

**Default order**:

```text
wait_rollout polaris-system/polaris-controller
RESET_CASES=1: delete all dedicated Namespaces
service-sync/run.sh
lifecycle/namespace/run.sh
lifecycle/service/run.sh
lifecycle/endpoints/run.sh
inject/run.sh
combined/run.sh
all cases completed
```

**Optional order**:

```text
RUN_ALIAS_WINNER=1: service-sync/alias-winner.sh
RUN_ALL_MODE=1: service-sync/switch-mode.sh all
```

**Resource control**:

```bash
./run-all.sh --keep
./run-all.sh --cleanup
./inspect.sh
./cleanup.sh
```

**Expected**:

- Each default suite prints its `all ... assertions passed` message.
- The run ends with `[case] all cases completed`.
- With the default `KEEP_RESOURCES=1`, retained Namespaces and inspection and
  cleanup commands are printed on exit.
- `--cleanup` or `CLEANUP=1` invokes cleanup on exit.
- Setting both `KEEP_RESOURCES=1` and `CLEANUP=1` is an error.
- Objects intentionally deleted or scaled down by lifecycle cases are not
  recreated by keep mode.

---

## 11. Important Notes

1. Do not infer sync results from HTTP metadata keys named `version`,
   `protocol`, `region`, `zone`, or `campus`. Assert the top-level fields.
2. `sync=false` removes controller-managed instances; it does not delete the
   Polaris Service.
3. A Pod label `protocol` and metadata `protocol` do not change top-level
   Protocol. EndpointPort and `polarismesh.cn/protocols` do.
4. `customVersion` has priority over the Pod label `version`.
5. MODE-01 restarts the controller. Increase `WAIT_TIMEOUT` on EKlet or slow
   image-pull environments.
6. Test Namespaces must be dedicated. Cleanup deletes these Kubernetes
   Namespaces directly and does not delete Polaris Namespaces.
7. The Namespace lifecycle case deliberately retains its Polaris Namespace.
8. INJ-10 default non-injection applies only when `sidecarInject.mode` is
   empty or `none`. A configured cluster mode can still inject without a
   Namespace or Pod mode.
