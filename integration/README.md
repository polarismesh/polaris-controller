# Polaris Controller Integration Tests

This directory contains end-to-end integration cases for Polaris Controller. The
cases create real Kubernetes resources and query a deployed Polaris instance;
they are not unit tests and do not use a mocked Kubernetes or Polaris backend.

The expected behavior is documented in [Polaris Controller User Guide](../docs/README.md).

## Warning

The scripts use fixed Kubernetes Namespace names and delete those Namespaces
before a full run by default. Do not run them in a shared cluster where the same
Namespace names are already in use.

These cases verify the test branch against a locally reachable deployment. A
passing local run is not a substitute for CI or a release acceptance test.

## Directory Layout

```text
integration/
├── README.md
├── README-zh.md
└── cases/
    ├── lib.sh                     # Shared helpers; not an entry point
    ├── run-all.sh                 # Main entry point
    ├── cleanup.sh                 # Delete all dedicated test Namespaces
    ├── service-sync/
    │   ├── manifests.yaml
    │   ├── run.sh                 # Service sync cases
    │   ├── alias-winner.sh        # Alias winner and handover case
    │   └── switch-mode.sh         # serviceSync.mode verification
    ├── inject/
    │   ├── manifests.yaml
    │   └── run.sh                 # Sidecar and Java Agent injection cases
    └── combined/
        ├── manifests.yaml
        └── run.sh                 # Service sync plus injection case
```

## Prerequisites

Before running the cases, verify all of the following:

1. Polaris Controller is installed in the `polaris-system` Namespace.
   `run-all.sh` waits for the `statefulset/polaris-controller` rollout.
2. The `polaris-system/injector-mesh` ConfigMap exists and contains the
   `serverAddress` and, when required, `accessToken` settings.
3. The current Kubernetes context has permission to create and delete
   Namespaces, create and modify workloads and Services, read Endpoints, patch
   the `injector-mesh` ConfigMap, and restart the controller StatefulSet.
4. The machine running the scripts can reach the Polaris HTTP API. By default,
   the URL is derived from `injector-mesh` as `http://<serverAddress>:8090`.
5. The Polaris token has permission to query Services, instances, and aliases.
6. `bash`, `kubectl`, `curl`, and `jq` are installed.
7. The cluster can pull the test images from `ccr.ccs.tencentyun.com`. Some
   injection cases also use the Java Agent test image from the same registry.

Confirm the context and required objects before a run:

```bash
kubectl config current-context
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system get configmap injector-mesh
command -v bash kubectl curl jq
```

The scripts expect `KUBECTL`, `CURL`, and `JQ` to be executable names or paths.
To use another cluster, switch the current context or set, for example,
`KUBECTL=/path/to/kubectl`. Do not put additional kubectl flags in `KUBECTL`,
because the value is executed as one command name.

## Quick Start

Run the default suite from the `cases` directory:

```bash
cd integration/cases
./run-all.sh
```

The default run:

- checks the controller rollout;
- deletes the dedicated case Namespaces once;
- runs Service sync cases;
- runs sidecar and Java Agent injection cases;
- runs the combined Service sync and injection case;
- leaves the case Namespaces and workloads in place for inspection.

Use the following command for the broadest local pass. It additionally runs
the alias winner case and the all-mode check, then removes the case Namespaces
when the run exits:

```bash
CLEANUP=1 RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh
```

`RUN_ALL_MODE=1` temporarily changes `serviceSync.mode` to `all`, restarts the
controller, verifies the result, and restores the original mode. This is a
configuration-changing test and should not be used against a shared controller.

## Configuration

Common environment variables:

| Variable | Default | Used by | Description |
| --- | --- | --- | --- |
| `POLARIS_HTTP_URL` | `http://<serverAddress>:8090` | Polaris assertions | Override the Polaris HTTP API endpoint. Use this when the API uses a different port, a proxy, or an externally reachable address. |
| `POLARIS_TOKEN` | Read from `injector-mesh` | Polaris assertions | Override the Polaris API token. If explicitly set to an empty string, requests are sent without a token header. |
| `KUBECTL` | `kubectl` | All cases | kubectl executable name or path. |
| `CURL` | `curl` | Polaris assertions | curl executable name or path. |
| `JQ` | `jq` | Polaris assertions | jq executable name or path. |
| `WAIT_TIMEOUT` | `120` | All cases | Timeout in seconds for rollout, Pod, and Namespace state waits. |
| `RESYNC_TIMEOUT` | `180` | All cases | Timeout in seconds for controller reconciliation and Polaris assertions. |
| `RESET_CASES` | `1` for `run-all.sh`; `0` for individual cases | All case runners | Delete the dedicated case Namespaces before applying manifests. |
| `CLEANUP` | `0` | All case runners | Delete the dedicated case Namespaces on exit. |
| `RUN_ALIAS_WINNER` | `0` | `run-all.sh` | Run `service-sync/alias-winner.sh` after the main Service sync cases. |
| `RUN_ALL_MODE` | `0` | `run-all.sh` | Run `service-sync/switch-mode.sh all` after the other cases. The original mode is restored on exit. |
| `KEEP_MODE` | `0` | `switch-mode.sh` | Keep the requested mode instead of restoring the original mode. Intended for debugging. |

Example with explicit API settings and longer timeouts:

```bash
POLARIS_HTTP_URL="http://10.0.0.10:8090" \
POLARIS_TOKEN="<polaris-token>" \
WAIT_TIMEOUT=180 \
RESYNC_TIMEOUT=300 \
./run-all.sh
```

The Namespace variables in `lib.sh` are test fixtures, not general isolation
controls. The manifests contain fixed names such as `it-pc-sync` and
`it-pc-inject-default`; overriding only the variables will make the assertions
look at different Namespaces from the applied resources.

## Script Reference

### `run-all.sh`

Main entry point. It checks prerequisites, applies the reset policy, and calls
the individual case scripts in this order:

1. `service-sync/run.sh`
2. `service-sync/alias-winner.sh` when `RUN_ALIAS_WINNER=1`
3. `inject/run.sh`
4. `combined/run.sh`
5. `service-sync/switch-mode.sh all` when `RUN_ALL_MODE=1`

It exports `RESET_CASES=0` after the initial reset so individual cases reuse the
same cluster state instead of deleting each other's resources.

### `service-sync/run.sh`

Runs the main Kubernetes Service sync suite:

```bash
cd integration/cases
RESET_CASES=1 ./service-sync/run.sh
```

It covers sync enablement and precedence, the legacy `enableRegister`
annotation, `all` versus `demand` behavior, Service and instance metadata,
protocol and weight mapping, TTL, StatefulSet custom weights, not-ready
Endpoints, aliases, label convergence, and cleanup after disabling sync.

The script reads the current `serviceSync.mode` and skips the all-mode assertion
unless the controller is in `demand` mode. Run the full all-mode case separately
with `switch-mode.sh`.

### `service-sync/alias-winner.sh`

Verifies that multiple Services requesting the same alias keep one stable winner,
and that the alias moves to the remaining Service after the winner is deleted:

```bash
cd integration/cases
./service-sync/alias-winner.sh
```

The script expects the `service-sync` workloads to be creatable and applies
`service-sync/manifests.yaml` itself. It generates unique Service and alias names
for each run. The generated alias is intentionally left in Polaris after the
temporary Services are deleted.

### `service-sync/switch-mode.sh`

Switches `serviceSync.mode`, waits for the controller rollout, verifies the
`it-pc-sync-all/all-mode-demo` Service and instance, and restores the original
mode unless `KEEP_MODE=1` is set:

```bash
cd integration/cases
./service-sync/run.sh
./service-sync/switch-mode.sh all
./service-sync/switch-mode.sh demand
```

Arguments:

```text
./service-sync/switch-mode.sh <all|demand>
```

The `it-pc-sync-all` workload must already exist. Running
`service-sync/run.sh` first satisfies that requirement. This script patches
`polaris-system/injector-mesh` and restarts
`statefulset/polaris-controller`.

### `inject/run.sh`

Runs the sidecar and Java Agent injection cases:

```bash
cd integration/cases
./inject/run.sh
```

It verifies DNS sidecar injection and configuration, Namespace and Pod label
selection, explicit disable annotations and labels, hostNetwork behavior, Java
Agent configuration and workload name fallback, and the legacy injection label.
It also checks that injection alone does not create a Polaris Service.

### `combined/run.sh`

Verifies that Service sync and injection can operate together:

```bash
cd integration/cases
./combined/run.sh
```

It checks sidecar injection and `sidecarConfig`, Service metadata, instance
metadata, and the synced instance count for `it-pc-combo/combo-demo`.

### `cleanup.sh`

Deletes the dedicated case Namespaces and waits for them to disappear:

```bash
cd integration/cases
./cleanup.sh
```

It deletes the same Namespace set used by `CLEANUP=1` and additionally waits
for the Namespaces to disappear. It does not delete Polaris aliases. After the
suite, check and remove aliases such as `shared-alias`, `metadata-demo-alias`,
and generated `shared-alias-<timestamp>-<pid>` aliases manually if the Polaris
environment must be clean.

### `lib.sh`

Shared shell helpers for logging, binary checks, Namespace cleanup, kubectl
waits, Polaris HTTP queries, and assertions. Do not execute it directly.

## Notes for Individual Runs

All case scripts source `lib.sh` and resolve paths relative to their own
location, so they can be started from any working directory:

```bash
/absolute/path/to/integration/cases/service-sync/run.sh
```

However, individual runners default to `RESET_CASES=0`. Set
`RESET_CASES=1` when a clean start is required. In the current implementation,
`RESET_CASES=1` on any individual runner calls the shared cleanup helper and
can delete all dedicated integration Namespaces, not only the Namespaces for
that case. Use it only in a disposable cluster.

Likewise, `CLEANUP=1` on an individual runner uses the shared cleanup helper and
deletes all dedicated case Namespaces when the runner exits.

## Dedicated Namespaces

The cases use these fixed Namespaces:

| Namespace | Purpose |
| --- | --- |
| `it-pc-sync` | Service sync and metadata assertions |
| `it-pc-sync-legacy` | Legacy `enableRegister` assertion |
| `it-pc-sync-all` | `serviceSync.mode` assertion |
| `it-pc-inject-default` | Sidecar injection and disable rules |
| `it-pc-inject-podlabel` | Pod label selection |
| `it-pc-inject-java` | Java Agent injection |
| `it-pc-inject-legacy` | Legacy Namespace label |
| `it-pc-combo` | Combined sync and injection |

`cleanup.sh` removes all eight Namespaces. A Namespace may remain visible as
`Terminating` while Kubernetes finalizes its resources; the script waits up to
`WAIT_TIMEOUT` seconds.

## Troubleshooting

Collect the controller and case state before retrying:

```bash
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system logs statefulset/polaris-controller --tail=200
kubectl get namespaces | grep '^it-pc-'
kubectl -n it-pc-sync get pods,services,endpoints
kubectl -n it-pc-inject-default get pods
```

Common failures:

- `required command not found`: install the missing binary or set `KUBECTL`,
  `CURL`, or `JQ` to its path.
- `cannot determine Polaris serverAddress from injector-mesh`: inspect the
  ConfigMap and set `POLARIS_HTTP_URL` explicitly when the address is managed
  outside that field.
- HTTP API timeout or connection failure: verify that the machine running the
  script can reach Polaris on port 8090, or provide the correct
  `POLARIS_HTTP_URL` and `POLARIS_TOKEN`.
- Pod creation or rollout timeout: check image pull events, node capacity, and
  the readiness probes. The `notready-demo` case intentionally uses a failing
  readiness probe; it is expected to have a not-ready Endpoint.
- Repeated Service assertion timeout: check that the controller can reach
  Polaris and that the selected `serviceSync.mode` is appropriate for the case.
- Namespace cleanup timeout: inspect finalizers and resources left in the
  Namespace, then remove the test resources before retrying.
- `switch-mode.sh` interrupted with `KEEP_MODE=1`: restore `serviceSync.mode`
  manually and restart the controller. Without `KEEP_MODE=1`, the script's EXIT
  trap attempts to restore the original mode.
