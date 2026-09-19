# Pod spec is the primary configuration source; the workload template is secondary

Probe configuration can be read from a running pod's spec or from the owning workload's pod template, and the two differ during and after rollouts. We read the pod spec first, because the tool's promise is about containers that are actually running, and we report any difference from the template as drift, since drift is itself a reason to investigate. The template is read only to fill in workloads with zero pods and manifests passed with `-f`.

## Considered Options

- **Template only.** Simpler, and what most probe linters do. Rejected because it describes what should be running, not what is, and hides mid-rollout mismatches.
- **Pod spec only.** Rejected because scaled-to-zero workloads and unapplied manifests would be invisible, and the user explicitly wants them shown.

## Consequences

- Every Container row needs a pod-side and a template-side reading, and the model must represent "no pods" without pretending to know runtime state.
- Owner resolution is mandatory, not optional, because the template lives on the top-most owner.
