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

Ten authored workloads, plus ingress-nginx, plus the nine or so kind brings
with it — so `kubectl probes -A` has something to say.

Probe configuration is deliberate. Each workload either demonstrates a rule or
demonstrates probes done well, and the configurations are meant to look like
code someone actually wrote rather than strawmen.

| Workload | Kind | Findings | What it shows |
|---|---|---|---|
| `shop/landing` | Deployment | — | The explainer page, nginx. Probes done well: readiness and liveness on separate endpoints, every timeout written down |
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
| http://shop.localtest.me:8080 | **Start here.** A page explaining the cluster, the request path, and how to read the rest |
| http://web.localtest.me:8080 | the frontend, podinfo, showing a live call through to `api` |
| http://api.localtest.me:8080 | the backend on its own |
| http://grafana.localtest.me:8080 | Grafana, anonymous access, Prometheus already wired up |
| http://prometheus.localtest.me:8080 | Prometheus |

`localtest.me` is public DNS that resolves to `127.0.0.1`, so in principle
none of this needs `/etc/hosts` editing.

In practice, **many home routers block it.** DNS rebinding protection refuses
any answer pointing at a loopback or private address, and it is on by default
on Fritz!Box and others. The symptom is `dig +short shop.localtest.me`
returning nothing, and `nip.io` and `sslip.io` failing the same way. If that
is you:

```sh
make hosts       # adds the four hostnames to /etc/hosts, once, with sudo
```

Or skip ingress entirely:

```sh
kubectl port-forward -n shop svc/web 9898:9898            # localhost:9898
kubectl port-forward -n observability svc/grafana 3000:3000
```

### What you are looking at

`shop.localtest.me` is a hand-written page that says all of this on screen —
the request path, what each dot means, what to run next. It exists because
podinfo's own UI has nowhere to put it: no way to label the dots or draw the
path. If you only open one thing, open that.

The `web` and `api` pages are [podinfo](https://github.com/stefanprodan/podinfo),
a small Go app built to be a test workload for Kubernetes — the same one the
Flux and Linkerd tutorials use. It does nothing useful on purpose. What it has
is real `/healthz` and `/readyz` endpoints, Prometheus metrics, the ability to
call a backend and draw the chain, and built-in fault injection, which is what
`make chaos` drives.

The request path behind `http://shop.localtest.me:8080`:

```
  your browser
      │  Host: shop.localtest.me
      ▼
  /etc/hosts  ──▶ 127.0.0.1
      │
      ▼
  colima VM        forwards host :8080
      │
      ▼
  kind node        probes-demo-control-plane, extraPortMapping 8080 ─▶ :80
      │
      ▼
  ingress-nginx    routes on the Host header
      │              shop.localtest.me    ─▶ Service shop/web
      │              api.localtest.me     ─▶ Service shop/api
      │              grafana.localtest.me ─▶ Service observability/grafana
      ▼
  Service shop/web ──balances──▶ one of 2 web pods
                                      │  PODINFO_BACKEND_URL
                                      ▼
                                 Service shop/api ──▶ one of 2 api pods
```

On the page itself:

| What you see | What it is |
|---|---|
| The purple cuttlefish | podinfo's logo. Decoration. |
| The title line | `PODINFO_UI_MESSAGE`, set in `manifests/shop/web.yaml` |
| **Served by `web-…-mb26x`** | Which of the two `web` pods answered. **Refresh and it changes** — that is the Service load-balancing |
| Two green dots | The service chain. Top is the `web` pod, bottom is the `api` pod it called. Green means the hop answered |
| PING, and the number on it | Sends another request down the chain, and counts how many you have sent |

Open `http://api.localtest.me:8080` and you get a **green** page with **one**
dot: you reached the backend directly, and nothing sits behind it. The colour
and the message are the fastest way to tell which service you are on.

### Watching a probe do its job

Open http://shop.localtest.me:8080, then in another terminal:

```sh
make chaos
```

Two different failures, and the contrast is the point.

`catalog`'s liveness probe starts failing, so the kubelet restarts it on a
loop: restart counts climb and the evidence names the liveness probe.

`web` is the interesting one. Patching its readiness probe starts a rolling
update, and the new pod never becomes ready — so **the rollout stalls**. The
two old pods are still there and still serving, which means the page keeps
working and nothing is restarted. From outside, `web` looks fine. The plugin
shows `2/3` ready and marks the readiness column with `*`, meaning the running
pods no longer match their own template. That is drift, and it is the failure
people miss.

## When it goes wrong

**`failed calling webhook "validate.nginx.ingress.kubernetes.io" ... connection
refused`** during `make up`.

ingress-nginx's controller reports Ready from a probe on `:10254`, while its
admission webhook listens on `:8443` and starts accepting later. So the pod is
Ready, the admission Service has a ready endpoint, and an `Ingress` apply is
still refused. No condition or event marks the moment `:8443` comes up, so
`scripts/apply.sh` retries rather than waiting. If you hit this on an older
checkout, or it exhausts its retries:

```sh
make apply       # safe to re-run, and the way to recover a half-applied cluster
```

Everything except the two Ingresses will already be running; `make apply` is
idempotent and finishes the job.

**`make up` fails because the cluster already exists.** `make up` is composed
of `cluster`, `ingress`, `apply`, `ready`. Run whichever one you need rather
than tearing down.

**The browser cannot reach `shop.localtest.me` but the cluster looks fine.**
DNS, not Kubernetes. Check with:

```sh
curl -H "Host: shop.localtest.me" http://localhost:8080/
```

A 200 there means everything works and only name resolution is missing — run
`make hosts`. See the browser section above.

**A platform or manifest error on the node image.** The image is pinned by
digest. Drop to the plain tag in `kind.yaml` if your setup cannot resolve it:
`image: kindest/node:v1.37.0`.

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
