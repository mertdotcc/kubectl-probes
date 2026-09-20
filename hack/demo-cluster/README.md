# The demo cluster

A three-node [kind](https://kind.sigs.k8s.io/) cluster with a small but real
system running on it, for trying `kubectl probes` against a live API server.

**kind is the supported local environment for this project.** Output shown in
the repository README and in write-ups comes from this cluster, so anyone
following along sees what you see. Why kind and not minikube or k3d, and why
there is no Helm here, is in
[ADR 0004](../../docs/adr/0004-the-supported-local-environment-is-a-kind-cluster-in-the-repo.md).

## What you need

`kubectl`, `kind`, and a container runtime. Nothing else — no Helm, no
operators, no CRDs.

On macOS, with [colima](https://github.com/abiosoft/colima) rather than Docker
Desktop:

```sh
brew install colima kind kubectl
colima start --cpu 4 --memory 12 --disk 60
```

12 GB is comfortable rather than necessary; the cluster itself wants roughly
5–6 GB with everything running.

## Start it

```sh
cd hack/demo-cluster
make up
```

First run is mostly image pulls. Afterwards:

```sh
make probes        # build the plugin from this repo and run it against the cluster
```

## The daily loop

`make up` and `make down` create and destroy. Between sessions you want
neither:

```sh
make stop          # suspend, keeping the cluster's state
make start         # back in about 30 seconds
```

The manifests are the source of truth, so `make down && make up` gets you the
same cluster again. Nothing is snapshotted.

One thing does not survive a suspend: **failure evidence**. The API server
garbage-collects events on `--event-ttl`, which `kind.yaml` raises to 24h but
cannot extend forever. Restart counts persist; `Unhealthy` events do not.
Generate evidence when you need it instead:

```sh
make chaos         # break some probes on purpose
make heal          # put them back
```

## What is in it

Nine authored workloads, plus ingress-nginx, plus the nine or so kind brings
with it — so `kubectl probes -A` has something to say.

Probe configuration is deliberate. Each workload either demonstrates a rule or
demonstrates probes done well, and the configurations are meant to look like
code someone actually wrote rather than strawmen.

| Workload | Kind | Findings | What it shows |
|---|---|---|---|
| `shop/web` | Deployment | — | Probes done well: a startup probe covers the slow boot, readiness and liveness ask different questions, every timeout written down |
| `shop/api` | Deployment | 1 | `timeout-at-default` — careful config that never got a liveness timeout |
| `shop/catalog` | Deployment | 2 | `no-readiness-probe`, `timeout-at-default` |
| `shop/postgres` | StatefulSet | 2 | `liveness-without-startup`, `timeout-exceeds-period` — a fixed delay standing in for a startup probe |
| `shop/redis` | Deployment | 1 | `liveness-same-as-readiness` — one command answering both questions |
| `shop/backup` | CronJob | 1 | A non-Deployment owner the plugin has to walk CronJob → Job → Pod to name |
| `observability/prometheus` | StatefulSet | 1 | `liveness-faster-than-readiness` — restarted on its way out of service |
| `observability/grafana` | Deployment | — | Readiness only, no liveness, which is a legitimate choice |
| `observability/node-exporter` | DaemonSet | 1 | `timeout-at-default`, on every node |

That covers all six configuration rules. The seventh,
`probe-failures-recent`, needs a cluster that is actually failing — which is
what `make chaos` is for.

## In a browser

| | |
|---|---|
| http://shop.localtest.me:8080 | podinfo's UI, showing a live call through to `api` |
| http://api.localtest.me:8080 | the backend on its own |
| http://grafana.localtest.me:8080 | Grafana, anonymous access, Prometheus already wired up |
| http://prometheus.localtest.me:8080 | Prometheus |

`localtest.me` is public DNS that resolves to `127.0.0.1`, so none of this
needs `/etc/hosts` editing. It is third-party DNS though, so if your resolver
will not answer for it — offline, or behind DNS filtering — skip ingress:

```sh
kubectl port-forward -n shop svc/web 9898:9898            # localhost:9898
kubectl port-forward -n observability svc/grafana 3000:3000
```

### Watching a probe do its job

Open http://shop.localtest.me:8080, then in another terminal:

```sh
make chaos
```

`web`'s readiness probe starts failing. The pods keep running and the page
keeps working — because two replicas never fail at exactly the same moment —
but they leave the Service, and `kubectl probes -n shop deploy/web` reports
readiness failures with no restarts. Meanwhile `catalog` is being restarted on
a loop by its liveness probe. The contrast between those two is the thing
worth seeing.

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
| ingress-nginx | `controller-v1.11.2` (`INGRESS_REF` in the Makefile) |
| Kubernetes | `kindest/node:v1.37.0`, pinned by digest in `kind.yaml` |
| kind | v0.33.0 (the version whose default node image is pinned above) |

The node image is pinned by digest, so the Kubernetes version does not drift
with whichever kind you happen to have installed. To move it, read the tag off
`make up` and replace the `image:` on all three nodes in `kind.yaml`.

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
