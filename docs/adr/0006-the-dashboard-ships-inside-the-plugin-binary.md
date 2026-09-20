# The Dashboard ships inside the plugin binary, with no JavaScript toolchain

The Dashboard is served by the same binary krew installs: `kubectl probes --serve` composes with every existing flag, binds a loopback address, prints the URL, and serves static assets embedded with `go:embed`. The browser side is plain ES modules, CSS and inline SVG, checked into the repo as written, with no bundler, no framework and no `package.json`. Updates reach the browser as whole v1alpha1 Report snapshots over Server-Sent Events, so the wire format is the one the CLI already publishes with `-o json`.

## Considered Options

- **A `serve` or `ui` subcommand.** The plugin is flag-based with one optional positional, `kubectl probes deploy/api`, in the kubectx and kubectl-tree mould. A subcommand collides with that positional and would make `kubectl probes deploy/api --serve`, which opens straight on that Inspection, impossible to express. Rejected.
- **A separate binary or repo.** Two install paths, two release pipelines, and a Dashboard that can drift from the Report it reads. Rejected.
- **A hosted web app you point at a kubeconfig.** Ships credentials to a server. Rejected on principle.
- **TypeScript built with Bun, as Sam Rose's newer visualisations are.** A workload view has tens of moving elements, not thousands, and a build step only pays for itself once there is real complexity. Adding a JavaScript toolchain to a Go project doubles the CI surface for no gain at this size. Rejected for now; nothing about the file layout prevents it later.
- **Canvas or PixiJS.** For thousands of moving things. Not this.
- **Fine-grained deltas on the wire.** An optimisation. Whole snapshots keep the browser dumb, make a late-joining tab trivial to bring up to date, and reuse a schema that already has a version field. Rejected until a Report is measured to be too large.

## Consequences

- The Dashboard binds `127.0.0.1` on a random free port by default and never authenticates, because it is only ever reachable by the user who ran it. `--serve` takes an optional address for people who know what they are doing.
- The Report gains only what drawing needs and the CLI does not print, such as the pod's node, and an optional slot for probe counts. Every addition is `omitempty` and the `apiVersion` stays `v1alpha1`.
- Watches, not polls: the server keeps informers on pods and events, rebuilds the Report on change, and coalesces bursts to a few hundred milliseconds so a rollout does not push a hundred snapshots a second.
- The server keeps a bounded ring of snapshots, one hour, and replays it to a browser on connect. That ring is the observed part of the Timeline; the reconstructed part comes from the timestamps inside the first snapshot.
- `--serve` with `-f` serves one static snapshot with configuration and schedule and nothing to animate, since a manifest has no runtime state.
- The browser side is tested with chromedp behind the existing integration build tag, so the repo stays Go-only and CI needs no Node.
- The Dashboard is part of what goreleaser builds and the krew archive carries. It is a v0.1.0 release gate, not a follow-up.
