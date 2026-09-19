# The tool is read-only against the API server and never exercises a probe

It is tempting to have the tool run a probe itself through a port-forward or `pods/exec` to produce live evidence the kubelet does not report. We decided the first product only reads Pods, Events, and owner objects from the API server. Exercising probes needs extra RBAC, is intrusive on production workloads, and runs on a different network path from the kubelet, so its result can disagree with what the cluster actually does.

## Consequences

- Failure evidence is limited to what the kubelet and controllers already reported: container statuses, pod conditions, and `Unhealthy` events with their default one-hour retention.
- The tool needs only `get` and `list` on pods, events, and the owning workload kinds, and must degrade gracefully when events are forbidden.
- Any future active-probing feature is a separate decision and should reopen this ADR rather than slip in as a flag.
