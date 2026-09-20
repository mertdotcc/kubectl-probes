# The Dashboard draws only what the cluster reported, never a probe it did not see

The plugin grows a browser surface, the Dashboard, that animates probe behaviour the way Sam Rose's [ngrok probes essay](https://ngrok.com/blog/probes) does: a kubelet, pods, containers changing colour, failures arriving as pulses, a timeline you can scrub. Those essays run on [webernetes](https://github.com/ngrok/webernetes), a simulated cluster with a simulated clock, which is why they can show every probe tick. A real cluster shows none of them: a successful probe leaves no trace in the API server, and a failed one surfaces only as a deduplicated, rate-limited `Unhealthy` event or a flipped `ready` flag. We decided the Dashboard animates only state the cluster actually reported, at the timestamps it reported it, and never draws a probe request it did not observe. It is an operational view of a live cluster, not a teaching simulation.

## Considered Options

- **Draw probe ticks on a timer derived from `periodSeconds`.** Looks exactly like the essays and is exactly what they are: invented. A dot every ten seconds that no data backs would train users to trust a picture over the cluster. Rejected, permanently. Probe schedules may be drawn as static configuration next to a container, in the same way Effective timing is already a derivation and labelled as one.
- **Read the kubelet's `prober_probe_total` counters from `/metrics/probes` through `nodes/proxy`.** The one real per-tick signal that exists: actual success and failure counts per probe and container, polled. It needs RBAC the plugin does not ask for today and is unreachable on some managed clusters. Deferred, not rejected: the Report reserves a place for probe counts, the Dashboard renders "unavailable" until they exist, and adding them reopens [ADR 0002](0002-read-only-against-the-api-server.md) as that ADR asks.
- **Build the Dashboard on a simulation, for the blog post.** A different product. If a teaching visual is wanted later, webernetes is Apache-2.0 and already does it; this repo does not build a second simulator.
- **Interactive controls that act on the cluster** ("Restart container", "Delete pod", "Cause a request to fail"). Writes, and contrary to ADR 0002. Rejected. The demo cluster's `make chaos` is how a failure is caused.

## Consequences

- The Dashboard's sources are exactly the CLI's: pod conditions with their `lastTransitionTime`, container `ready`, `started` and restart counts, last termination with `finishedAt`, and `Unhealthy` events with count, first and last seen. Whatever the Overview and Inspection can print, the Dashboard can draw, and nothing else.
- Steady state is still. A healthy workload does not twinkle. This is the honest picture and it will look less alive than the essays; the README says why.
- Changes must arrive with the cluster's timestamps, so the server watches pods and events rather than polling on its own clock.
- The Timeline is bounded by what the cluster remembers: transition times and events, which age out after the cluster's `--event-ttl`, one hour by default. It starts when `--serve` starts, is held in memory, back-fills from the timestamps in the first Report, and is gone on exit. The Dashboard marks the back-filled part as reconstructed so a user can tell replayed from observed.
- A "receiving traffic" indicator is derived from the pod's `Ready` condition and labelled as derived. Services and EndpointSlices are not read; readiness gating traffic is what the condition means.
