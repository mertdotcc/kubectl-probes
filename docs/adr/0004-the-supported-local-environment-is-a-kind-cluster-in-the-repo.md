# The supported local environment is a kind cluster that ships in the repo

Trying the plugin against something real requires a cluster, and a cluster assembled by hand is a cluster nobody else can reproduce. We ship one: a three-node kind cluster defined by `hack/demo-cluster/`, with a coherent set of workloads whose probe configuration is chosen to exercise every rule. kind is named as *the* supported local environment in the README, so a contributor, a blog reader, and the maintainer are all looking at the same cluster. The manifests are the source of truth and the cluster is disposable; runtime failure evidence is regenerated on demand rather than preserved, because events are garbage-collected on `--event-ttl` and cannot survive a suspend either way.

## Considered Options

- **No supported environment; point people at their own cluster.** Rejected because probe behaviour depends on what else is running, so two people reading the same README would see different output, and a screenshot in a blog post would be unreproducible.
- **minikube or k3d.** Both work. kind was chosen because it is upstream Kubernetes rather than a distribution, and this plugin reads owner references and events from the API server, which is exactly the surface a distribution is most likely to differ on. k3d's first-class `cluster stop/start` is the one thing we give up; `docker stop` on the node containers covers it.
- **Helm for the platform tier.** `kube-prometheus-stack` is how Prometheus is genuinely installed, but it drags in an operator, CRDs, and roughly 2–3 GB for components this plugin treats as ordinary Deployments. Rejected so that `kubectl` and `kind` are the only tools a reader needs.
- **Snapshotting the cluster between sessions.** Rejected because a snapshot cannot be reviewed in a diff, rots against Docker and kind upgrades, and still loses the failure evidence it would exist to preserve.
- **Generating golden test files from the demo cluster.** Rejected outright. `testdata/` stays deterministic and owned by the tests; the demo cluster stays realistic and owned by humans. Asserting against a live cluster would make the test suite depend on Docker.
- **Running the demo cluster in CI.** Rejected for v0.1.0. `integration/` already covers the pipeline against fixtures in about two minutes; a live cluster would buy little and cost a slow, flaky pipeline.

## Consequences

- The demo cluster is a release gate, not a nicety: it is the first time the plugin runs against a live API server, so runtime state, drift, and failure evidence get their first real exercise there. Bugs it finds are v0.1.0 bugs.
- Every authored workload is a maintenance liability, so the set is kept to nine and each one earns its place by covering a rule, an owner kind, or a thing a reader recognises.
- Image tags are pinned explicitly and recorded in the demo's own README. Nothing floats on `latest`, or the blog post stops matching reality.
- The service tier is several podinfo instances rather than distinct software. This is a deliberate trade of authenticity for controllability: the chaos script needs runtime endpoints that can fail a probe on command, and no set of unrelated real images offers a common one.
- ~~Reaching the cluster from a browser was meant to need no `/etc/hosts` editing, because `*.localtest.me` resolves to `127.0.0.1`. In practice consumer routers reject it: DNS rebinding protection refuses any answer pointing at loopback, and `nip.io` and `sslip.io` fail the same way, so there is no wildcard provider to fall back to. `make hosts` writes the names locally, and `kubectl port-forward` remains the no-sudo escape hatch.~~
- ~~The demo needs a front door of its own. podinfo renders the service chain but has nowhere to say what it is rendering, so `shop.localtest.me` is a hand-written page served by nginx that draws the request path and names every element of the podinfo pages, and podinfo moves to `web.localtest.me`. This is why there are ten authored workloads rather than nine.~~
  **Withdrawn on 2026-09-21, with the Dashboard ([ADR 0008](0008-there-is-no-dashboard.md)):** the cluster exists to be read by `kubectl probes`, not browsed. The landing page, ingress-nginx, the Ingresses and `make hosts` are gone, and the authored set is back to nine.
- Failure evidence is generated, not preserved, and `make chaos` breaks two probes in deliberately different ways: a liveness failure that restarts on a loop, and a readiness failure that stalls a rolling update while the old pods keep serving. The second is what exercises drift, and it is the only path in which the plugin's `*` template-mismatch marker appears.
- `hack/` is outside what goreleaser builds, so none of this ships in a release archive or affects the krew manifest.

## Amendment, 2026-09-26: minikube is supported too

The demo cluster also runs on minikube: `TOOL=minikube` on any `make` target. kind stays the default. People try a plugin on the local cluster they already have, and minikube is at least as common as kind; v0.1.0 was tested on minikube before release. The manifests do not change, only the cluster's lifecycle, so the Makefile holds both: three nodes, Kubernetes v1.37.0 and a 24h `--event-ttl` on either, pinned by the node image's digest in `kind.yaml` and by `--kubernetes-version` for minikube. The whole flow, `up` through `chaos`, `heal`, `stop`, `start` and `down`, was run from scratch on both.

The worry under Considered Options, that a distribution differs on the surface this plugin reads, did not hold for minikube: it runs kubeadm-built upstream Kubernetes, and owner references, events and static pods read the same as on kind. k3d stays unsupported. The Makefile's `TOOL` accepts `kind` or `minikube` and fails on anything else, so adding a third tool is a decision, not a flag.
