# The released plugin is the CLI alone; the Dashboard is built only when asked for

[ADR 0006](0006-the-dashboard-ships-inside-the-plugin-binary.md) decided how to build the Dashboard and, in its last consequence, that it ships in v0.1.0 as a release gate. It was right about the how and wrong about the when. The Dashboard now exists and has been run against the demo cluster mid-chaos, and what it is good for is narrower than the ticket assumed: for a multi-replica workload whose probes are failing right now it shows two things the terminal cannot, which node the failures are on and whether they cluster in time, and for everything else `kubectl probes` is denser, faster and already finished.

Meanwhile the first release has one job, which is to get through krew review and be worth keeping once installed. What is being judged there is a plugin that does one thing well. So the released binary is the CLI alone: `internal/serve` and the `--serve` flag move behind a `dashboard` build tag, the default build compiles no HTTP server and embeds no browser assets, and the released plugin has no such flag to find.

## Considered Options

- **Ship it labelled experimental.** The cheapest option, since `--serve` is opt-in already, and the one this decision was very nearly. Rejected because a flag in a binary krew installed is a promise, whatever the help text says. An experimental browser surface invites the comparison this plugin loses on breadth, against k9s and Lens and the Grafana dashboards, and it would be doing that in the first impression of a tool whose whole case is that it does one thing well.
- **Keep it compiled in but hide the flag from `--help`.** Rejected twice over: a flag that is in the binary but not in the help reads as unsupported and sneaky rather than unsupported and honest, and it leaves the archive carrying 150 KB of embedded browser assets for something nobody is told about. Leanness is the point, not deniability.
- **Delete it.** Rejected. The work is done and tested, the Report already carries what the drawing needs, and the thing that would make the Dashboard worth finishing is [issue #38](https://github.com/mertdotcc/kubectl-probes/issues/38), the kubelet's own probe counters. Deleting it means building it a second time to find that out.
- **A separate binary, or a repo of its own.** ADR 0006 rejected this for reasons that have not changed: two install paths, two release pipelines, and a Dashboard free to drift from the Report it reads. A build tag gets the lean archive without any of that.

## Consequences

- The default build is what goreleaser builds and krew ships, and it has no `net/http` server, no `go:embed`ed assets and no `--serve`. Building the Dashboard is `go build -tags dashboard`.
- CI has to build and test both configurations. Code behind a tag that nothing compiles is code that rots, and the point of keeping it is that it still works when it is next wanted.
- The root command's help, its `Example` block and `CONTEXT.md` stop naming a flag the released plugin does not have. The Dashboard and the Timeline stay in the glossary, because they are still things this repo builds; they are simply not things it ships.
- **ADR 0005 is untouched.** It governs what the Dashboard may draw, and it governs it whenever the Dashboard is built. Nothing here relaxes the rule that it animates only what the cluster reported.
- **ADR 0006 stands except for its last consequence.** How the Dashboard is built, in the one binary with no JavaScript toolchain, over SSE, as whole Report snapshots, is unchanged, and is how it will be built again.
- v0.1.0's scope is the CLI. The Dashboard's remaining tickets leave the release: the chromedp smoke test is closed rather than deferred, because a browser in CI is a permanent tax on every pull request for something the release does not contain, and the Dashboard's documentation shrinks to a line for contributors.
- Reopening this is a v0.2 decision. The trigger is issue #38: real per-probe counters from the kubelet are the one signal a healthy probe leaves anywhere, and a Dashboard that can draw them is a different proposition from one that cannot.
