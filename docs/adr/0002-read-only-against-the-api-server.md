# The tool is read-only against the API server and never exercises a probe

It is tempting to have the tool run a probe itself through a port-forward or `pods/exec` to produce live evidence the kubelet does not report. We decided the first product only reads Pods, Events, and owner objects from the API server. Exercising probes needs extra RBAC, is intrusive on production workloads, and runs on a different network path from the kubelet, so its result can disagree with what the cluster actually does.

## Consequences

- Failure evidence is limited to what the kubelet and controllers already reported: container statuses, pod conditions, and `Unhealthy` events with their default one-hour retention.
- The tool needs only `get` and `list` on pods, events, and the owning workload kinds, and must degrade gracefully when events are forbidden.
- Any future active-probing feature is a separate decision and should reopen this ADR rather than slip in as a flag.

## Amendment, 2026-09-21: the kubelet is out of bounds too

[Issue #38](https://github.com/mertdotcc/kubectl-probes/issues/38) proposed reading the kubelet's `prober_probe_total` counters through `nodes/proxy`, as a read-only way to see probes that succeeded. It is closed rather than parked. The plugin reads Pods, Events and owner objects from the API server and nothing else: no `nodes/proxy`, no kubelet endpoints, no metrics. A plugin that asks for less access is easier to trust and to install, and a well-scoped first plugin is worth more than the one signal the counters would add. Proposing it again means superseding this ADR, not adding a flag.
