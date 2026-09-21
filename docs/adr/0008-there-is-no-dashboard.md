# There is no Dashboard; the plugin is a CLI and nothing else

[ADR 0007](0007-the-released-plugin-is-the-cli-alone.md) took the Dashboard out of the released plugin but kept it in the repo behind a `dashboard` build tag, so that it would still work if [issue #38](https://github.com/mertdotcc/kubectl-probes/issues/38) ever made it worth finishing. We decided to delete it instead: `internal/serve`, its embedded browser assets, the pod and event watch that fed it, the `--serve` wiring, the tagged CI jobs, and the Report's unused slot for probe counters. Six thousand lines are gone, and the default build's binary is unchanged, because the release never contained them.

The plugin's case is that it does one thing well in a terminal, in the manner of kubectl-tree and kubectl-cond. A browser surface is a second product. Keeping it behind a tag still meant a second CI configuration, a watch path in `collect` that the CLI never took, and a standing invitation to finish it. What the Dashboard showed that the terminal could not, which node failures are on and whether they cluster in time, is a question for the CLI if it matters at all.

## Considered Options

- **Keep it behind the build tag (ADR 0007).** Rejected. Keeping it was a hedge on a future this project is not going to pursue, and the hedge cost CI time and attention on every change.
- **Move it to a branch or a separate repo.** Rejected. A branch that nobody rebases rots silently, and it would still be a reason to come back to it. Git history is enough of an archive: the last commit with the Dashboard is `6ded3a7`.

## Consequences

- ADRs [0005](0005-the-dashboard-draws-only-what-the-cluster-reported.md), [0006](0006-the-dashboard-ships-inside-the-plugin-binary.md) and [0007](0007-the-released-plugin-is-the-cli-alone.md) are superseded. They stay in the repo as the record of why it was built and why it was removed.
- **Dashboard** and **Timeline** are no longer terms in `CONTEXT.md`.
- `collect` has one entry point, `Collect`, which reads once and returns. There is no long-running mode.
- The Report no longer reserves `counts` for the kubelet's probe counters. If #38 is taken up, it is as a CLI feature with its own ADR reopening [ADR 0002](0002-read-only-against-the-api-server.md), and it adds the field it needs then.
- The pod's `node` stays in the Report. It is a fact the cluster reported, and it costs nothing.
