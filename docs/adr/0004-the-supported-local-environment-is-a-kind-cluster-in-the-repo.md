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
- Reaching the cluster from a browser depends on `*.localtest.me` resolving to `127.0.0.1`, which is third-party DNS. `kubectl port-forward` is documented as the offline fallback.
- `hack/` is outside what goreleaser builds, so none of this ships in a release archive or affects the krew manifest.
