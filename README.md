# kubectl-probes

A read-only kubectl plugin that shows, per workload and container, which startup, readiness, and liveness probes are configured, what their settings mean in practice, and what evidence of failure the cluster currently reports.

## Usage

_Coming soon._

## Rules

Alongside the facts it reads, the plugin reports **findings**: named, opinionated observations about a container's probe configuration and history. [`docs/rules.md`](docs/rules.md) lists every rule, what makes it fire, and what it says. Findings are always printed apart from the facts, and `--no-findings` leaves them out entirely.
