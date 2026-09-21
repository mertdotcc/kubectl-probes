# The Overview ends with a Summary of probe coverage and failure detection

The plugin's job is to say what the probe situation is across a set of workloads, and to say why for any one of them. The Overview answers the first question one Container at a time, which is fine for a namespace and useless for a cluster with three thousand of them. We decided the Overview ends with a Summary, in the manner of `kubectl node-resource allocation`, and that the Summary has exactly two sections: **coverage**, which is how many Containers have each kind of probe, and **failure detection**, which is how long a failing Container goes unnoticed by its readiness and liveness probes, as percentiles naming the Container at each and a histogram. `--summary=show|only|hide` controls it, and `show` is the default. The Inspection is about one Workload and never has a Summary.

The Summary counts Containers, the Overview's rows, over the same scope the Overview covers, so `-n`, `-A`, `-l`, `-f` and the positional argument mean the same thing to both. Sidecars count, and so do Workloads with no pods, read from their template, because both are rows. It aggregates figures the Report already carries and computes no new ones: failure detection is the Effective timing the Overview's readiness and liveness cells already show.

```
Summary: 214 containers in 61 workloads

Coverage
  readiness   171 / 202   85%   (12 Job and CronJob containers not counted)
  liveness    133 / 214   62%
  startup      29 / 214   14%
  none         31 / 214   14%

Readiness failure detection (171 containers)
  P100 (max)   3m0s    observability  sts/loki           loki
  P99          2m30s   payments       deploy/ledger      ledger
  P90          1m30s   shop           deploy/web         web
  P50          30s     shop           deploy/api         api
  P10          10s     kube-system    deploy/coredns     coredns
  P0 (min)     3s      kube-system    node/cp-1          etcd

    ≤10s    ████                  18
  10–30s    ████████████████████  97
  30s–1m    ██████                31
    1–2m    ████                  19
    2–5m    █                      5
     >5m                           1
```

- **Percentiles** are node-resource's six, P100, P99, P90, P50, P10 and P0, by nearest rank, each naming the Container that sits there, so an outlier is one command away from its Inspection.
- **Buckets** are fixed and read as durations: 10 seconds or less, then up to 30 seconds, 1 minute, 2 minutes, 5 minutes, and more than that. They do not move with the data, so two clusters' Summaries can be compared by eye.
- **No colour by value.** node-resource colours by pressure because a fuller node is simply worse. Failure detection has no bad direction: a long one sends traffic to a broken container for longer, and a short liveness one restarts containers that were only slow. Colouring either end would be an opinion inside a section of facts.
- **Readiness coverage leaves out Job and CronJob containers**, and says how many, for the reason `no-readiness-probe` skips them: nothing sends their pods traffic. They count towards liveness, startup and none.

## Considered Options

- **A `summary` subcommand.** Rejected for the reason [ADR 0006](0006-the-dashboard-ships-inside-the-plugin-binary.md) gave against a `serve` subcommand: it collides with the optional `TYPE/NAME` positional.
- **Print the Summary only with `-A`.** Rejected. An output shape that depends on a scope flag is a surprise, and a large namespace needs a Summary as much as a cluster does.
- **More sections: handler mix, startup budget, findings by rule, failing now.** Rejected for now. `--sort severity` already puts what is failing at the top of the table, and the others wait until the dogfood log ([#47](https://github.com/mertdotcc/kubectl-probes/issues/47)) shows they are needed. A section is cheap to add and expensive to take away once a script depends on it.
- **New rules to go with the Summary.** Rejected. The seven rules stay as they are, and the Summary reports facts, not findings.

## Consequences

- The Report gains an optional `summary`, which is additive, `omitempty`, and keeps `v1alpha1`.
- `--summary` means the same in every output format. `show` is the Workloads and the Summary, `only` is the Summary without the Workloads, and `hide` is the Workloads without the Summary. `kubectl probes -A -o json --summary=only` is how a script reads a large cluster.
- A Summary over a whole cluster is only as useful as `-A` is fast. The owner walk reads one owner at a time and events are listed one namespace at a time, which has not been measured on a large cluster. It is measured first, as an entry in the dogfood log, and made faster in a change of its own if it needs to be.
