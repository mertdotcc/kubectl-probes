# A set of `-f` files is read as one cluster

`-f` began as a way to look at a manifest that had not been applied yet, which
gave a Workload with a template and nothing else. We decided that a set of `-f`
files is instead read exactly the way a namespace is: the pods in the files are
reduced to their top-most owners through the same owner walk, the core `v1`
Events in the files are matched to those pods by `involvedObject.uid`, and only
the objects no pod resolved to become template-only Workloads.

An owner reference that points outside the file set behaves like an owner that
could not be read: the walk stops and names it, so `-f replicaset.yaml` still
reports the ReplicaSet as its own Workload, while `-f dump.json` holding both a
ReplicaSet and its Deployment collapses one into the other. The single-manifest
case is the general case with one object in it.

The alternative was a second, simpler reading for files, which would have meant
two owner reductions to keep in step and a `-f` path no test could hold the
cluster path to.

## Consequences

- `kubectl get pods,rs,deploy,events -o json > dump.json` is a Report anyone
  can read back later, or attach to a bug report, without cluster access.
- The whole pipeline is testable without a cluster, which is what
  `integration/` does: fixtures in, golden Overview, Inspection, JSON, and YAML
  out. Per ADR-0002 nothing about this touches the network.
- Only the core `v1` Event is read. The `events.k8s.io/v1` Event carries the
  same facts under different names, and a file holding one is ignored rather
  than guessed at.
- A file set is as truthful as whoever assembled it. Pods without their owners
  report drift as unknown, exactly as they would against an API server that
  would not hand the owner over.
