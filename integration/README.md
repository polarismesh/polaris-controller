# Polaris Controller Integration Tests

This directory validates Polaris Controller against a real Kubernetes cluster
and a deployed Polaris instance. The tests create real resources and call the
Polaris HTTP API; there is no mocked backend.

Documentation:

- [English test cases](cases/test.md)
- [中文用例说明](cases/test-zh.md)
- [Polaris Controller User Guide](../docs/README.md)

## Warning

- Tests use fixed Namespace names and a full run deletes old test Namespaces by
  default.
- Do not reuse those Namespace names in a shared cluster.
- Lifecycle cases intentionally delete or scale down objects. Keep mode does
  not recreate objects deleted by a case.
- A local pass is not a substitute for CI or release acceptance.

## Directory Layout

```text
integration/
├── README.md / README-zh.md
├── build-materials.sh             # Build the manual-upload zip
├── run.sh                         # Run and persist logs
├── cleanup.sh                     # Clean test resources
├── pack-logs.sh                   # Package logs and cluster snapshots
└── cases/
    ├── test.md / test-zh.md       # English and Chinese case references
    ├── run-all.sh                 # Main test entry point
    ├── inspect.sh                 # Show retained resources
    ├── cleanup.sh                 # Delete test Namespaces
    ├── service-sync/              # Service sync and optional mode cases
    ├── lifecycle/                 # Namespace/Service/Endpoints lifecycle
    ├── inject/                    # Sidecar and Java Agent injection
    └── combined/                  # Combined sync and injection
```

## Prerequisites

1. Controller is deployed in `polaris-system` as
   `statefulset/polaris-controller`.
2. `polaris-system/injector-mesh` exists and contains `serverAddress` and, when
   required, `accessToken`.
3. The current kubeconfig can create, modify, and delete test resources. The
   all-mode case also updates the ConfigMap and restarts the controller.
4. The runner can reach the Polaris HTTP API. The default URL is
   `http://<serverAddress>:8090`.
5. The Polaris token can query Namespace, Service, Instance, and Alias objects,
   and register or delete lifecycle test instances.
6. `bash`, `kubectl`, `curl`, and `jq` are installed.
7. The cluster can pull test images from `ccr.ccs.tencentyun.com`.
8. The cluster permits NodePort and LoadBalancer Services for lifecycle cases.

Quick checks:

```bash
kubectl config current-context
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system get configmap injector-mesh
command -v bash kubectl curl jq
```

`KUBECTL`, `CURL`, and `JQ` accept an executable name or path, not extra flags.

## Quick Start

```bash
cd integration/cases
./run-all.sh
```

The default run:

1. waits for the controller rollout;
2. deletes old test Namespaces;
3. runs Service sync, lifecycle, injection, and combined suites;
4. retains resources that still exist at the end;
5. prints retained Namespaces plus inspection and cleanup commands.

Optional cases:

```bash
RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh --keep
```

`RUN_ALL_MODE=1` temporarily switches `serviceSync.mode=all`, restarts the
controller, and restores the original mode.

## Retention and Cleanup

```bash
# Inspect manually
./inspect.sh

# Clean after inspection
./cleanup.sh

# Or clean when run-all exits
./run-all.sh --cleanup
```

`KEEP_RESOURCES=1` is the default for `run-all.sh` and conflicts with
`CLEANUP=1`. `CLEANUP=1 ./run-all.sh` remains supported. A run still starts
with `RESET_CASES=1`, so confirm whether the previous run's resources must be
kept before starting another one.

## Package, Upload, and Run

### Build the zip

```bash
cd integration
./build-materials.sh
```

Default output:

```text
integration/dist/polaris-controller-integration.zip
```

The zip contains directly executable scripts and the complete test suite:

```text
polaris-controller-integration/
├── run.sh
├── cleanup.sh
├── pack-logs.sh
├── PACKAGE-INFO.txt
├── integration/
└── docs/README.md
```

Use `--output PATH` for a custom zip path or `--clean` to remove
`integration/dist/`.

### Upload and run manually

Upload the zip to a cloud test machine that already has kubeconfig:

```bash
scp integration/dist/polaris-controller-integration.zip <user>@<host>:/tmp/
```

On the test machine:

```bash
cd /tmp
unzip polaris-controller-integration.zip
cd polaris-controller-integration

./run.sh
```

`run.sh` streams output and also writes it to
`.logs/run-<timestamp>-<pid>/run.log`, preserving the suite exit code. Other
arguments are passed to `integration/cases/run-all.sh`:

```bash
./run.sh --keep
RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run.sh --keep
./run.sh --log-dir /tmp/polaris-logs --keep
```

Kubernetes test resources are retained by default for inspection.

### Package and return logs

```bash
./pack-logs.sh
```

The default archive is written in the extracted directory:

```text
polaris-controller-integration-logs-<timestamp>.zip
```

If `zip` is unavailable on the test machine, the script falls back to
`.tar.gz`.

It contains `.logs/` and, by default, also collects:

- the current kubectl context;
- the last 2000 lines of `polaris-controller` logs;
- remaining test Namespace, Pod, workload, Service, and Endpoints state;
- Namespace events and Pod describe output.

Use `--no-cluster` or `COLLECT_CLUSTER=0` to package only existing run logs:

```bash
./pack-logs.sh --no-cluster
```

The script prints an `scp` example for returning the archive. Review it before
sharing because it can contain cluster metadata.

### Clean resources

```bash
./cleanup.sh
```

Cleanup removes Kubernetes test Namespaces. It does not delete Polaris aliases,
Polaris Namespaces, retained Polaris Services, `.logs/`, or log archives.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `POLARIS_HTTP_URL` | Auto-derived | Polaris HTTP API address. |
| `POLARIS_TOKEN` | Read from ConfigMap | Polaris API token. |
| `KUBECTL` / `CURL` / `JQ` | Command names | Override tool paths. |
| `WAIT_TIMEOUT` | `120` | Rollout, Pod, and Namespace timeout in seconds. |
| `RESYNC_TIMEOUT` | `180` | Polaris reconciliation and assertion timeout in seconds. |
| `EXTERNAL_DELETE_TIMEOUT` | `30` | External instance cleanup timeout in seconds. |
| `RESET_CASES` | `1` in `run-all.sh` | Delete fixed test Namespaces before running. |
| `KEEP_RESOURCES` | `1` | Retain resources on exit; used by `run-all.sh`. |
| `CLEANUP` | `0` | Delete test Namespaces on exit; conflicts with keep mode. |
| `RUN_ALIAS_WINNER` | `0` | Run the alias winner/handover case. |
| `RUN_ALL_MODE` | `0` | Run the all-mode case and restore the original mode. |
| `KEEP_MODE` | `0` | Debug option that does not restore `serviceSync.mode`. |
| `LOG_DIR` | `./.logs` | Log directory used by `run.sh` and `pack-logs.sh`. |
| `COLLECT_CLUSTER` | `1` | Whether `pack-logs.sh` collects Kubernetes and controller snapshots. |

Example:

```bash
POLARIS_HTTP_URL="http://10.0.0.10:8090" \
POLARIS_TOKEN="<polaris-token>" \
WAIT_TIMEOUT=180 \
RESYNC_TIMEOUT=300 \
./run-all.sh --keep
```

Namespace names in the manifests are fixed. Do not override only `NS_*`
variables without updating the manifests; assertions and applied resources
would diverge.

## Execution Entry Points

| Entry | Cases |
|---|---|
| `service-sync/run.sh` | `SYNC-01` through `SYNC-14` |
| `service-sync/alias-winner.sh` | `ALIAS-01` |
| `service-sync/switch-mode.sh` | `MODE-01` |
| `lifecycle/namespace/run.sh` | `NS-LIFE-01` through `NS-LIFE-02` |
| `lifecycle/service/run.sh` | `SVC-LIFE-01` through `SVC-LIFE-02` |
| `lifecycle/endpoints/run.sh` | `EP-LIFE-01` through `EP-LIFE-04` |
| `inject/run.sh` | `INJ-01` through `INJ-11` |
| `combined/run.sh` | `COMBO-01` |
| `cleanup.sh` | `CLEAN-01` |
| `run-all.sh` | `ORCH-01` |
| Bundle `run.sh` | Run and persist logs |
| Bundle `cleanup.sh` | Clean test resources |
| Bundle `pack-logs.sh` | Package logs and cluster snapshots |

Individual entries default to `RESET_CASES=0`. Setting `RESET_CASES=1` deletes
every test Namespace, not only the current suite's Namespace. See
[English test cases](cases/test.md) for expected results and verification.

## Test Namespaces

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

`cleanup.sh` deletes these Kubernetes Namespaces. It does not delete Polaris
aliases, Polaris Namespaces, or retained Polaris Services.

## Troubleshooting

```bash
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system logs statefulset/polaris-controller --tail=200
kubectl get namespaces | grep '^it-pc-'
kubectl -n it-pc-sync get pods,services,endpoints
```

- Missing command: install it or set `KUBECTL`, `CURL`, or `JQ`.
- Cannot read `serverAddress`: inspect `injector-mesh` or set
  `POLARIS_HTTP_URL`.
- HTTP API timeout: check network reachability, port, and token permissions.
- Pod or rollout timeout: check image pulls, node capacity, and readiness
  probes.
- Service convergence timeout: check controller-to-Polaris connectivity and
  the active sync mode.
- Namespace remains Terminating: inspect finalizers and remaining Pods.
- `switch-mode.sh` interrupted with `KEEP_MODE=1`: restore
  `serviceSync.mode` manually and restart the controller.
