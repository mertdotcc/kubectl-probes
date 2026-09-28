# kubectl probes

A read-only `kubectl` plugin that shows, per workload and container, which startup,
readiness, and liveness probes are configured, what their settings mean in practice, and
what evidence of failure the cluster currently reports. Probe settings are a handful of
numbers whose consequences are not obvious — `periodSeconds: 30` with `failureThreshold: 3`
is 90 seconds of traffic to a container that is already broken — so the plugin does that
arithmetic, fills in the defaults the spec left out, and puts the `Unhealthy` events next
to the configuration that produced them. It never exercises a probe itself.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/img/probes-dark.gif">
  <img alt="kubectl probes: the startup, readiness and liveness probes of a container drawn as three rings, each row showing the timing the plugin computes and the state it reads" src="docs/img/probes-light.gif">
</picture>

[popeye](https://github.com/derailed/popeye) and [kube-score](https://github.com/zegl/kube-score)
check whether probes are present and look safe, alongside much else. This plugin looks at
nothing but probes: what their timing adds up to, whether a running pod still matches its
template, and what failures the cluster reports.

## Usage

```sh
kubectl probes                      # every workload in the current namespace
kubectl probes -A -o wide           # every namespace, with handlers and drift
kubectl probes deploy/api           # one workload, in full detail
kubectl probes -f deploy.yaml       # a manifest that has not been applied yet
kubectl probes -A --summary=only    # coverage and failure detection, cluster-wide

# Containers with no readiness probe, anywhere
kubectl probes -A -o json | jq -r '.workloads[] | .displayName as $w
  | .containers[] | select(.readiness == null) | "\($w)\t\(.name)"'
```

> **`-l` matches pod labels, not workload labels.** `kubectl probes -l app=api` finds the
> workloads whose *pods* carry `app=api`, which is usually what you meant but is not what
> `kubectl get deploy -l app=api` does.

## Example

```console
$ kubectl probes
WORKLOAD                      CONTAINER    STARTUP     READINESS    LIVENESS    READY  RESTARTS  FAILURES  FINDINGS
cj/nightly                    report       -           -            exec:1m30s  1/1    0         0         2
deploy/api                    api          http:2m30s  http:1m30s*  http:30s    2/2    0         0         3
deploy/web                    istio-proxy  http:1m     http:1m      -           1/1    1         2         2
deploy/web                    web          -           tcp:30s      tcp:1m      1/1    0         0         4
job/import                    import       -           -            -           1/1    0         0         0
pod/debug                     debug        -           -            -           1/1    0         0         1
rollout.argoproj.io/checkout  checkout     -           grpc:30s     -           1/1    0         0         1
sts/cache                     cache        -           tcp:30s      -           0/0    0         0         0
sts/db                        db           -           exec:30s     tcp:10s     1/2    7         13        4
* running config differs from workload template
```

The table is followed by a Summary: how many containers have each probe, and how long a
failing container goes unnoticed, with the container at each percentile named so an
outlier is one command away. `--summary=only` prints it alone:

```console
$ kubectl probes --summary=only
Summary: 9 containers in 8 workloads

Coverage
  readiness   6 / 7   86%   (2 Job and CronJob containers not counted)
  liveness    4 / 9   44%
  startup     2 / 9   22%
  none        2 / 9   22%

Readiness failure detection (6 containers)
  P100 (max)  1m30s  deploy/api  api
  P99         1m30s  deploy/api  api
  P90         1m30s  deploy/api  api
  P50         30s    sts/cache   cache
  P10         30s    deploy/web  web
  P0 (min)    30s    deploy/web  web

    ≤10s                          0
  10–30s    ████████████████████  4
  30s–1m    █████                 1
    1–2m    █████                 1
    2–5m                          0
     >5m                          0

Liveness failure detection (4 containers)
  …
```

Naming one workload switches to the detailed view. `sts/db` is the row above with seven
restarts and thirteen failures:

```console
$ kubectl probes sts/db
sts/db -n prod
2 pods

container db
  Configuration
    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER
    startup    -
    readiness  0s     10s     1s       1        3        30s         exec /bin/sh -c pg_isready -U postgres -h 127.0.0.1
    liveness   0s     5s      1s       1        2        10s         tcp :5432
  Effective timing
    Readiness acts after 3 consecutive failures, at worst 30s after the container stops responding.
    Liveness acts after 2 consecutive failures, at worst 10s after the container stops responding.
  Runtime state
    POD   READY  STARTED  RESTARTS  LAST TERMINATION                     POD-READY        CONTAINERS-READY
    db-0  false  true     7         Error, exit 137, signal 9, 8m2s ago  False (28m ago)  False (28m ago)
    db-1  true   true     0         -                                    True (37h ago)   True (37h ago)
  Failure evidence
    POD   PROBE      FAILURES  FIRST SEEN  LAST SEEN
    db-0  readiness  9         28m ago     110s ago
      Readiness probe failed: /bin/sh: pg_isready: connection to server at "127.0.0.1", port 5432 failed: Connection refused
    db-0  liveness   4         15m ago     8m8s ago
      Liveness probe failed: dial tcp 10.44.2.17:5432: connect: connection refused
  Findings
    liveness-faster-than-readiness  Liveness detects failure in 10s and readiness in 30s, so a failing container is restarted before it is taken out of service.
    timeout-at-default              The readiness probe allows the default 1s for an answer, so a container that is only slow to answer counts as failing.
    timeout-at-default              The liveness probe allows the default 1s for an answer, so a container that is only slow to answer counts as failing.
    probe-failures-recent           The cluster reports 13 recent Unhealthy events for the readiness and liveness probes, so this is failing now and not only on paper.
```

## Installation

Download the archive for your platform from
[Releases](https://github.com/mertdotcc/kubectl-probes/releases), unpack it, and put the
`kubectl-probes` binary on your `PATH`.

With [krew](https://krew.sigs.k8s.io/), once the plugin is listed in krew-index:
`kubectl krew install probes`

`go install github.com/mertdotcc/kubectl-probes@latest` works too, but krew cannot upgrade
a binary it did not install.

## Trying it on a real cluster

[`hack/demo-cluster/`](hack/demo-cluster/) ships a local cluster to try it on: three
nodes, a small system running on them, and probe configuration chosen so every rule has
something to fire on. It runs on [kind](https://kind.sigs.k8s.io/) or
[minikube](https://minikube.sigs.k8s.io/), and both are supported.

```sh
cd hack/demo-cluster
make up                  # or: make up TOOL=minikube
make probes
```

`kubectl` and `kind` or `minikube` are the only tools it needs. `make chaos` breaks
probes on purpose when you want failure evidence to look at. The plugin itself is not
tied to either: it reads whatever cluster your kubeconfig points at.

## Flags

- `-A`, `--all-namespaces` — every namespace, with a `NAMESPACE` column.
- `-n`, `--namespace` — the namespace to read, as everywhere else in `kubectl`.
- `-l`, `--selector` — label selector, matched against pod labels.
- `-f`, `--filename` — read workloads from manifests instead of the cluster. Repeatable.
- `-o`, `--output` — `table` (default), `wide`, `json`, or `yaml`.
- `--sort` — `name` (default) or `severity`, which puts the rows worth looking at first.
- `--summary` — `show` (default) prints the Summary after the table, `only` prints it
  alone, and `hide` leaves it out. It means the same in `-o json` and `-o yaml`. Naming a
  single workload never prints one.
- `--no-findings` — facts only, in every output format.
- `-c`, `--color` — `auto` (default), `always`, or `never`.
- `-v` — client-go log verbosity, for when a read did not return what you expected.

The usual `kubectl` connection flags (`--context`, `--kubeconfig`, `--as`, …) work too.

## How to read the output

Each probe column holds the handler type and the one duration that probe is read for,
computed from the *effective* configuration: the spec with the kubelet's defaults
(`periodSeconds: 10`, `timeoutSeconds: 1`, `failureThreshold: 3`, and the rest) filled in.

- **`STARTUP`** is the startup budget, `initialDelaySeconds + periodSeconds × failureThreshold`:
  everything the container is allowed before the kubelet gives up and restarts it.
- **`READINESS`** and **`LIVENESS`** are the failure detection time, `periodSeconds × failureThreshold`:
  the worst case between a container going bad and the probe acting on it. Both are counted
  from the startup probe succeeding where there is one, since neither runs before then.
- **`*`** marks drift: the running pod's spec differs from its workload's pod template.
  Normal mid-rollout, worth a look afterwards. `-o wide` names the drifted probes, and the
  detailed view puts the `*` on each field that drifted and prints both sides of it.
- **`-`** in a probe column means no probe is configured. In `FAILURES` it means the count
  is unknown because listing events was forbidden, which is not the same as zero.
- **`READY`** is ready pods over total pods. `0/0` is a workload with no pods, read from
  its pod template alone.

> **`FAILURES` is recent history only.** It counts `Unhealthy` events, and the API server
> keeps those about an hour by default. Zero means nothing has failed recently, not that
> nothing has ever failed.

## Findings

Alongside the facts it reads, the plugin reports **findings**: named, opinionated
observations about a container's probe configuration and history.
[`docs/rules.md`](docs/rules.md) lists every rule, what makes it fire, and what it says.
Findings are always printed apart from the facts, and `--no-findings` leaves them out
entirely.

> **The report schema is `probes.kubectl.dev/v1alpha1` and may still change.** `-o json`
> and `-o yaml` carry `apiVersion` and `kind` so a script can tell when it moves. Key on a
> finding's `rule`, which is part of the schema; the `message` is prose and may be reworded.

## License

Apache 2.0. See [LICENSE](LICENSE).
