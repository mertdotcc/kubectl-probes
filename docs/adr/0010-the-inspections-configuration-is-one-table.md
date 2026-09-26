# The Inspection's configuration is one table, a row per probe and a column per setting

The Inspection printed each probe's configuration as a heading line and six or seven `field  value  (default)` rows under it, 21 lines per Container, so comparing a Container's three probes meant reading down three lists. We decided the Configuration section is one table, with a row per probe and a column per setting. The columns use `kubectl describe`'s short names: `PROBE DELAY PERIOD TIMEOUT SUCCESS FAILURE [GRACE] ACTS AFTER HANDLER`. The table shows the running pod's values, per [ADR 0001](0001-pod-spec-is-the-primary-configuration-source.md). Every duration is printed the way the Overview prints one, as in `0s`, `1m30s` and `1h`, and the thresholds are bare counts. ACTS AFTER is the Overview's headline for the probe: the budget for a startup probe, and failure detection for the other two.

```
container api
  Configuration
    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER
    startup    0s     5s      1s       1        30       2m30s       GET /healthz:8080
    readiness  0s     30s*    2s       1        3        1m30s       GET /ready:http
    liveness   10s    10s     1s       1        3        30s         GET /healthz:8080
  Effective timing
    …
  Drift
    PROBE      FIELD   RUNNING  TEMPLATE
    readiness  period  30s      10s
```

- **Defaults are gray, with no text marker.** A value the spec didn't write prints in the gray that already means "not configured". With colour off, in a pipe, and in the golden files, a default reads like a written value. That's accepted: the API server writes every default except `initialDelaySeconds` into a live pod, so there was almost nothing left for a marker to mark, and the Report still tells an unset field from a written one. **This changes [issue #8](https://github.com/mertdotcc/kubectl-probes/issues/8)**, which asked for defaults "shown and marked as defaults".
- **HANDLER is the last column,** so a long exec command runs to the right edge without pushing the numbers out of line.
- **GRACE appears only when it has something to show.** That's when one of the Container's running probes sets `terminationGracePeriodSeconds`, or when grace has drifted. A probe that doesn't set it shows a gray `-`. Without the column, grace isn't mentioned at all. The old `(the pod's own)` row said nothing about the probe.
- **HTTP headers go on a line under the table**, one line per probe that sends any, such as `readiness sends X-Probe: kubelet`. They're too long for a cell and too important to leave out.
- **A probe that isn't configured keeps its row**, as its name and a gray `-`. `no probes are configured` is only for a Container with no probe on either side. A Container whose only probe is in the template, such as a rollout adding its first probe, gets the table, so the drift mark has a row to go on.
- **Drift is a red `*` on the cell that drifted**, the same mark the Overview uses:
  - on a field's cell where the running and template values differ
  - on HANDLER where the handler differs
  - on PROBE for a probe configured on one side only, or for a difference that no column explains

  The table and the Drift section read from one comparison, so they can't disagree. The Drift section stays, and it uses the table's names: `delay`, `period`, `timeout`, `success`, `failure`, `grace`, `handler` and `probe`, with durations humanised.
- **Effective timing is unchanged.** ACTS AFTER counts from container start for a startup probe and from the failure for the other two. The sentences are what explain that, and they stay word for word.

## Considered Options

- **Raw fields only, with no ACTS AFTER.** Rejected. That duration is the number a reader looks for first, and the Overview already shows it. A table that leaves it to the sentences below answers every question except that one.
- **`WORST CASE`, or two duration columns.** Rejected. `WORST CASE` fits failure detection but not a startup budget, which is an allowance. Two columns, one for budget and one for failure detection, leave one of the two empty on every row. `acts after` is the Effective timing sentences' own phrase.
- **The spec's full field names as headers** (`INITIALDELAYSECONDS`, `FAILURETHRESHOLD`). Rejected. They're many times wider than the values under them, and `kubectl describe` already shortens them this way (`delay=0s timeout=1s period=10s #success=1 #failure=3`), so the short names are ones a reader has already seen.
- **Raw integers**, `90` rather than `1m30s`. Rejected. The table would then write durations differently from the Overview and the sentences, and turning seconds into minutes is arithmetic this plugin exists to do.
- **Another way of marking defaults: a symbol, `(default)` in the cell, or no marking at all.** A symbol is one more mark to explain. `(default)` makes every numeric column several times wider. No marking at all throws away the one distinction a manifest read with `-f` still has. Gray costs no width, and it says "not written" in the colour that already means "not configured".
- **The handler second, or truncated.** Rejected. Second puts every number after it at the mercy of the longest exec command. Truncated hides the part of a command that differs, which is often the end.
- **Effective timing trimmed or dropped** now that ACTS AFTER is in the table. Rejected. The sentences say what a cell can't: where the count starts, when the first check runs, and when traffic can arrive.

## Consequences

- **The Report doesn't change**, and neither do the Overview and `-o wide`. This decision is about how the Inspection prints, not what it knows. A script that needs to tell a default from a written value reads `-o json`.
- **Two Containers in one Inspection can have different columns**, because GRACE is decided per Container.
- The golden Inspections and the README's example change only in their Configuration and Drift blocks.
