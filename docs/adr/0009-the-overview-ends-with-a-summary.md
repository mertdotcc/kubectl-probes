# The Overview ends with a Summary of probe coverage and failure detection

The plugin's job is to say what the probe situation is across a set of workloads, and to say why for any one of them. The Overview answers the first question one Container at a time, which is fine for a namespace and useless for a cluster with three thousand of them. We decided the Overview ends with a Summary, in the manner of `kubectl node-resource allocation`, and that the Summary has exactly two sections: **coverage**, which is how many Containers have each kind of probe, and **failure detection**, which is how long a failing Container goes unnoticed by its readiness and liveness probes, as percentiles naming the Container at each and a histogram. `--summary=show|only|hide` controls it, and `show` is the default. The Inspection is about one Workload and never has a Summary.

The Summary counts Containers, the Overview's rows, over the same scope the Overview covers, so `-n`, `-A`, `-l`, `-f` and the positional argument mean the same thing to both. It aggregates figures the Report already carries and computes no new ones: failure detection is the Effective timing the Overview's readiness and liveness cells already show.

## Considered Options

- **A `summary` subcommand.** Rejected for the reason [ADR 0006](0006-the-dashboard-ships-inside-the-plugin-binary.md) gave against a `serve` subcommand: it collides with the optional `TYPE/NAME` positional.
- **Print the Summary only with `-A`.** Rejected. An output shape that depends on a scope flag is a surprise, and a large namespace needs a Summary as much as a cluster does.
- **More sections: handler mix, startup budget, findings by rule, failing now.** Rejected for now. `--sort severity` already puts what is failing at the top of the table, and the others wait until the dogfood log ([#47](https://github.com/mertdotcc/kubectl-probes/issues/47)) shows they are needed. A section is cheap to add and expensive to take away once a script depends on it.
- **New rules to go with the Summary.** Rejected. The seven rules stay as they are, and the Summary reports facts, not findings.

## Consequences

- The Report gains an optional `summary`, which is additive, `omitempty`, and keeps `v1alpha1`.
- `--summary=only` is how a large cluster is read: the Summary without the table above it.
