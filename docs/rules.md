# Rules

A **rule** is a named check that produces a **finding**: a named, opinionated observation about a container's probe configuration or history. Findings say what was observed and why it is worth a look. They never propose a fix, because what the right probe is depends on the container, and only you know that.

Findings are kept apart from facts in every surface and in the `findings` field of the JSON and YAML Report, so you can always tell an opinion from something the cluster reported. `--no-findings` leaves the rules unrun, and a Report produced that way carries none in any output format.

There are seven rules. They have no severity, no thresholds you can set, and no configuration file. A finding's `rule` name is part of the Report's schema and is what a script should key on; the message is prose and may be reworded.

Rules read the **effective** configuration: the probe with the kubelet's defaults filled in (`initialDelaySeconds: 0`, `periodSeconds: 10`, `timeoutSeconds: 1`, `successThreshold: 1`, `failureThreshold: 3`) and the handler's own defaults applied (`path: /`, `scheme: HTTP`). A probe that writes a default out and one that leaves it unset are the same probe to every rule here, because the kubelet does the same thing with both.

Where several of a container's probes qualify, the rule reports one finding per probe, in the order startup, readiness, liveness. The message quoted under each rule below is the one it writes, with one container's numbers filled in.

---

## `no-readiness-probe`

**Fires when** the container has no readiness probe. Sidecars included: a sidecar that is not ready holds its whole pod out of service. Containers of a Job or CronJob are skipped, because their pods run to completion and nothing sends them traffic.

> No readiness probe is configured, so the kubelet reports this container ready the moment it starts and traffic arrives from then on.

## `liveness-without-startup`

**Fires when** a liveness probe exists, there is no startup probe, and the liveness probe's effective `initialDelaySeconds` is 30 or more. A liveness probe told to wait half a minute is one whose author already knew the container was slow to come up, and the delay is a fixed guess where a startup probe would be a measurement.

> Liveness waits 2m0s before its first check and there is no startup probe, so a container slower than that to come up is restarted while it is still starting.

## `liveness-same-as-readiness`

**Fires when** the liveness and readiness probes have identical effective handlers: the same type, path, port, host, scheme, headers, command, and gRPC service.

> Liveness and readiness both check GET /healthz:8080, so whatever takes this container out of service also restarts it.

## `liveness-faster-than-readiness`

**Fires when** both probes exist and liveness detects failure sooner than readiness does. Failure detection is `periodSeconds × failureThreshold`: the worst case between a container going bad and the probe acting on it.

> Liveness detects failure in 15s and readiness in 30s, so a failing container is restarted before it is taken out of service.

## `timeout-at-default`

**Fires when** a probe's effective `timeoutSeconds` is the kubelet's default of 1. It makes no difference whether the spec left the field unset or wrote `1` out: the kubelet allows the same second either way, and the finding is about what the container has to beat.

> The readiness probe allows the default 1s for an answer, so a container that is only slow to answer counts as failing.

## `timeout-exceeds-period`

**Fires when** a probe's `timeoutSeconds` is greater than its `periodSeconds`. The kubelet does not start a check while the last one is still outstanding, so failure takes longer to detect than the period reads like it does.

> The readiness probe allows 15s for an answer but runs every 10s, so a check can still be outstanding when the next one is due.

## `probe-failures-recent`

**Fires when** the cluster reports at least one `Unhealthy` event for the container. This is the one rule about evidence rather than configuration.

Events expire — an hour after the fact by default — so this says nothing about a container that was failing yesterday, and nothing at all where events could not be read. Pods on their way out are left out of the count, because their failures are history. The probes named are the ones the event messages name; an event that names no probe is still counted.

> The cluster reports 9 recent Unhealthy events for the readiness and liveness probes, so this is failing now and not only on paper.
