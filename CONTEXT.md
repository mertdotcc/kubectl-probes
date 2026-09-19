# kubectl-probes

A read-only kubectl plugin that shows, per workload and container, which startup, readiness, and liveness probes are configured, what their settings mean in practice, and what evidence of failure the cluster currently reports.

## Language

### Subjects

**Workload**:
The top-most owner of a set of pods, found by walking `ownerReferences` upward: a Deployment, StatefulSet, DaemonSet, Job, CronJob, a custom owner such as a Rollout, or a bare Pod with no owner. ReplicaSets are never workloads; they collapse into their Deployment.
_Avoid_: Controller, owner, app, service

**Container**:
A named container definition within a workload, identified by workload plus container name. It is a probe-bearing regular container or a sidecar. A workload has one container entry per name regardless of how many pods run it.
_Avoid_: Process, instance

**Sidecar**:
An init container with `restartPolicy: Always`. Sidecars can carry all three probes and are in scope. Plain init containers and ephemeral containers cannot carry probes and are out of scope.
_Avoid_: Init container (when meaning sidecar), helper

**Pod**:
A running or terminated instance of a workload. Pods are where runtime state and failure evidence live; configuration is read from the pod spec.

### Surfaces

**Overview**:
The default output: one row per Container with handler type, headline effective timing per probe, aggregated runtime state, and a finding count. Sorted alphabetically unless asked otherwise. Zero-pod workloads always appear.
_Avoid_: List, summary, table

**Inspection**:
The detailed output for a single Workload: full probe configuration, effective timing in sentences, drift, per-pod runtime state, failure evidence, then findings.
_Avoid_: Describe, detail view, drilldown

**Report**:
The structured export of an Overview or Inspection as JSON or YAML, carrying `apiVersion` and `kind` so consumers can detect schema changes. Starts at `v1alpha1`.
_Avoid_: Dump, export (as a noun)

### Configuration

**Probe**:
One of the three kubelet health checks on a container: startup, readiness, or liveness. Each has a handler (HTTP, TCP, gRPC, or exec) and timing fields.
_Avoid_: Health check, healthcheck

**Effective timing**:
The practical durations derived from a probe's raw fields, such as time until the first check, the worst-case time to detect failure, the maximum startup budget, and the time until traffic can arrive.
_Avoid_: Computed settings, interpretation

**Drift**:
A difference between a probe's configuration in a running pod's spec and in its workload's pod template, typically during or after a rollout. The pod spec is the primary source; the template is what lets a zero-pod workload appear at all.
_Avoid_: Mismatch, stale config

### Runtime

**Runtime state**:
What the kubelet currently reports about a container and its pod: `ready`, `started`, restart count, last termination reason and exit code, and the pod's `Ready` and `ContainersReady` conditions.
_Avoid_: Status, health

**Failure evidence**:
Cluster-reported facts that a probe has failed or a container has died: `Unhealthy` events with their probe failure messages, non-zero restart counts, and last termination details. Logs and metrics are not failure evidence in this project.
_Avoid_: Errors, diagnostics

**Finding**:
A named, opinionated observation about a container's probe configuration or history, produced by a rule, and always reported separately from facts so a user can tell fact from opinion. Findings tell the user where to investigate; they never propose a fix.
_Avoid_: Warning, lint error, recommendation, suggestion

**Rule**:
The named, kebab-case check that produces a Finding, such as `no-readiness-probe` or `liveness-faster-than-readiness`. Rules have no severity levels and no user configuration.
_Avoid_: Check, linter, policy
