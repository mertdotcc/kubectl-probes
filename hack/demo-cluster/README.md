# The demo cluster

A three-node cluster with a small but real system running on it, for trying
`kubectl probes` against a live API server.

**It runs on [kind](https://kind.sigs.k8s.io/) or
[minikube](https://minikube.sigs.k8s.io/), and both are supported.** kind is the
default. Add `TOOL=minikube` to any `make` target to use minikube instead. Both
give you the same three nodes, the same Kubernetes version and the same
workloads, and both are tested end to end: `up`, `probes`, `chaos`, `heal`,
`stop`, `start`, `down`. Why these two and not k3d, and why there is no Helm
here, is in
[ADR 0004](../../docs/adr/0004-the-supported-local-environment-is-a-kind-cluster-in-the-repo.md).

## What you need

`kubectl`, `kind` or `minikube`, and a container runtime. Nothing else: no
Helm, no operators, no CRDs.

On macOS, with [colima](https://github.com/abiosoft/colima) rather than Docker
Desktop:

```sh
brew install colima kubectl kind      # or: brew install colima kubectl minikube
colima start --cpu 4 --memory 12 --disk 60
```

12 GB is comfortable rather than necessary: the cluster itself wants roughly
5–6 GB with everything running. minikube caps each node at 3 GB, which 12 GB
covers.

## Start it

```sh
cd hack/demo-cluster
make up                  # kind
make up TOOL=minikube    # minikube
```

Every target below takes the same `TOOL`. To stop typing it, run
`export TOOL=minikube` once in your shell.

The first run is mostly image pulls, and a later `make up` takes about two
minutes on either tool. Afterwards:

```sh
make probes        # build the plugin from this repo and run it against the cluster
```

## The daily loop

`make up` and `make down` create and destroy. Between sessions you want
neither:

```sh
make stop          # suspend, keeping the cluster's state
make start         # back where you left it
```

The manifests are the source of truth, so `make down && make up` gets you the
same cluster again. Nothing is snapshotted.

One thing does not survive a suspend: **failure evidence**. The API server
garbage-collects events on `--event-ttl`, which `kind.yaml` and the Makefile's
minikube flags both raise to 24h but cannot extend forever. Restart counts
persist; `Unhealthy` events do not. Generate evidence when you need it instead:

```sh
make chaos         # break some probes on purpose
make heal          # put them back
```

## What is in it

Nine authored workloads, plus the system workloads the cluster brings with it,
so `kubectl probes -A` has something to say. Both tools bring coredns,
kube-proxy, kindnet and the control plane's static pods, which the plugin shows
as `node/<control-plane>`. kind adds local-path-provisioner, and minikube adds a
bare `storage-provisioner` pod.

Probe configuration is deliberate. Each workload either demonstrates a rule or
demonstrates probes done well, and the configurations are meant to look like
code someone actually wrote rather than strawmen. `web`, `api` and `catalog`
are [podinfo](https://github.com/stefanprodan/podinfo), a small test workload
with real `/healthz` and `/readyz` endpoints and built-in fault injection,
which is what `make chaos` drives.

| Workload | Kind | Findings | What it shows |
|---|---|---|---|
| `shop/web` | Deployment | — | Probes done well: a startup probe covers the slow boot, readiness and liveness ask different questions, every timeout written down |
| `shop/api` | Deployment | 1 | `timeout-at-default` — careful config that never got a liveness timeout |
| `shop/catalog` | Deployment | 2 | `no-readiness-probe`, `timeout-at-default` |
| `shop/postgres` | StatefulSet | 2 | `liveness-without-startup`, `timeout-exceeds-period` — a fixed delay standing in for a startup probe |
| `shop/redis` | Deployment | 1 | `liveness-same-as-readiness` — one command answering both questions |
| `shop/backup` | CronJob | — | A non-Deployment owner the plugin has to walk CronJob → Job → Pod to name |
| `observability/prometheus` | StatefulSet | 1 | `liveness-faster-than-readiness` — restarted on its way out of service |
| `observability/grafana` | Deployment | — | Readiness only, no liveness, which is a legitimate choice |
| `observability/node-exporter` | DaemonSet | 1 | `timeout-at-default`, on every node |

That covers all six configuration rules. The seventh,
`probe-failures-recent`, needs a cluster that is actually failing — which is
what `make chaos` is for.

## Watching a probe do its job

```sh
make chaos
```

Two different failures, and the contrast is the point.

`catalog`'s liveness probe starts failing, so the kubelet restarts it on a
loop: restart counts climb and the evidence names the liveness probe.

`web` is the interesting one. Patching its readiness probe starts a rolling
update, and the new pod never becomes ready — so **the rollout stalls**. The
two old pods are still there and still serving, so nothing is restarted. From outside, `web` looks fine. The plugin
shows `2/3` ready and marks the readiness column with `*`, meaning the running
pods no longer match their own template. That is drift, and it is the failure
people miss.

## When it goes wrong

**`make up` fails because the cluster already exists.** `make up` is composed
of `cluster`, `apply`, `ready`. Run whichever one you need rather
than tearing down.

**`too many open files` on colima.** On minikube it shows up as `make up`
failing with `validate CRI v1 runtime API ... unknown service
runtime.v1.RuntimeService`. Underneath, containerd could not start because the
colima VM ran out of inotify instances. kind documents the same limit among its
[known issues](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files).
Raise it, then `make down` and `make up` again:

```sh
colima ssh -- sudo sysctl -w fs.inotify.max_user_instances=8192 fs.inotify.max_user_watches=1048576
```

That lasts until colima restarts. To make it stick, add it to
`~/.colima/default/colima.yaml`:

```yaml
provision:
  - mode: system
    script: sysctl -w fs.inotify.max_user_instances=8192 fs.inotify.max_user_watches=1048576
```

**A platform or manifest error on the node image (kind).** The image is pinned
by digest. Drop to the plain tag in `kind.yaml` if your setup cannot resolve
it: `image: kindest/node:v1.37.0`.

**`make stop` or `make heal` does nothing useful on minikube.** Every target
needs the `TOOL=minikube` that `make up` got. Without it they act on the kind
cluster.

## Versions

Pinned deliberately: nothing floats on `latest`, or output captured today
stops matching what a reader gets tomorrow.

| | |
|---|---|
| podinfo | `ghcr.io/stefanprodan/podinfo:6.7.1` |
| Postgres | `postgres:16.4-alpine` |
| Redis | `redis:7.4-alpine` |
| Prometheus | `prom/prometheus:v2.54.1` |
| Grafana | `grafana/grafana:11.2.0` |
| node-exporter | `prom/node-exporter:v1.8.2` |
| Kubernetes | v1.37.0: `kindest/node:v1.37.0`, pinned by digest in `kind.yaml`, and `--kubernetes-version=v1.37.0` in the Makefile for minikube |
| kind | v0.33.0 (the version whose default node image is pinned above) |
| minikube | v1.39.0, tested |

The kind node image is pinned by digest, so the Kubernetes version does not
drift with whichever kind you happen to have installed. To move it, read the
tag off `make up` and replace the `image:` on all three nodes in `kind.yaml`.
Move `--kubernetes-version` in the Makefile to match, so both tools stay on the
same release.

## Notes

- The Postgres password is in plain text and the database is `emptyDir`. This
  is a laptop cluster reachable from nowhere. Do not copy these manifests into
  anything real.
- `kind.yaml` uses kubeadm **v1beta4** syntax for `event-ttl`, which needs
  Kubernetes 1.31 or newer. The pinned node image is well past that; if you
  ever pin an older one, that patch becomes `extraArgs: {event-ttl: "24h"}`.
- Sidecars — init containers with `restartPolicy: Always`, which this plugin
  treats as in scope and which can carry all three probes — are GA on the
  pinned Kubernetes, but **no demo workload uses one yet**. That path is
  reachable here and currently unexercised.
- Nothing here is exercised by CI. `integration/` covers the pipeline against
  `testdata/` fixtures; this cluster is for humans.
