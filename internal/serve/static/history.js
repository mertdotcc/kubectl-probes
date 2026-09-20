// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// A workload's history: what happened to it, when the cluster says it
// happened, and what the diagram therefore shows at any moment.
//
// There are two sources and they are not the same kind of thing, which is why
// this file keeps them apart all the way to the drawing. Every Report received
// since the Dashboard started is *observed*: it was seen, and the diagram at
// that moment is simply the Report. Everything before that is *reconstructed*
// from the timestamps inside the first Report, which is all the cluster
// remembers: when a condition last flipped, when a container last died, and
// when a group of Unhealthy events was first and last seen. A reconstruction
// is thinner than an observation and says so on screen.
//
// Nothing here invents a probe. A successful probe leaves no trace in the API
// server, so there is no entry for one and no timer producing one. See
// ADR-0005.

import { handlerLine, timeOf } from "./sentences.js";

// Where an entry came from, which decides whether it is drawn solid or
// hatched.
export const OBSERVED = "observed";
export const RECONSTRUCTED = "reconstructed";

// What an entry is about. The three are the only things the cluster reports
// that move: a probe failed, a container died, a pod condition flipped.
export const FAILURE = "failure";
export const RESTART = "restart";
export const CONDITION = "condition";

// The pod conditions a probe moves, in the order internal/analyze carries
// them. They are model.ConditionReady and model.ConditionContainersReady.
export const CONDITION_TYPES = ["Ready", "ContainersReady"];

// UNSCHEDULED is the slot pods with no node go under. It is not a node name
// and cannot collide with one: a node name is a DNS subdomain.
export const UNSCHEDULED = "";

// TEMPLATE is the slot a workload with no pods goes under: a manifest read
// from -f, or a workload scaled to zero. There is no kubelet and no pod, only
// the containers the template configures, so the drawing shows those and says
// what the CLI says in its place. See ADR-0003.
export const TEMPLATE = "\u0000template";

// The states a container chip can be in. Unknown is not a kubelet state: it is
// the reconstructed era, where the cluster reports no per-container history at
// all and the chip has nothing to say.
export const READY = "ready";
export const UNREADY = "unready";
export const UNSTARTED = "unstarted";
export const UNKNOWN = "unknown";

// entriesFor is everything that happened to one workload, oldest first.
//
// The first Report is read for both sources: the timestamps in it reach back
// before the Dashboard started, and anything they reach back to is
// reconstructed, while anything at or after startedAt happened on the
// Dashboard's watch and is observed. Every later Report is read against the
// one before it, because a whole snapshot says what is true and only the
// difference says what happened.
export function entriesFor(reports, startedAt, name) {
  const entries = new Map();
  const keep = (entry) => {
    if (entry !== null && !entries.has(entry.key)) {
      entries.set(entry.key, entry);
    }
  };

  for (const [i, report] of reports.entries()) {
    const workload = workloadIn(report, name);
    if (workload === null) {
      continue;
    }
    // A workload the Dashboard is seeing for the first time is read the same
    // way whether it is the first Report or a workload that has just been
    // created: every timestamp in it is something that already happened.
    const before = i === 0 ? null : workloadIn(reports[i - 1], name);
    const found =
      before === null
        ? recalled(workload, startedAt)
        : changed(before, workload, timeOf(report.generatedAt) ?? 0);
    for (const entry of found) {
      keep(entry);
    }
  }

  return [...entries.values()].sort((a, b) => a.at - b.at || compare(a.key, b.key));
}

// recalled is the first Report read as history: every timestamp in it is
// something that already happened, whether or not anybody was watching.
function recalled(workload, startedAt) {
  const out = [];
  const source = (at) => (at < startedAt ? RECONSTRUCTED : OBSERVED);

  for (const { pod, container } of podEntries(workload)) {
    for (const condition of pod.conditions ?? []) {
      const at = timeOf(condition.lastTransitionTime);
      if (at !== null) {
        out.push(conditionEntry(pod, condition, at, source(at)));
      }
    }
    const died = timeOf(pod.lastTermination?.finishedAt);
    if (died !== null) {
      out.push(restartEntry(pod, container, died, source(died)));
    }
    for (const group of pod.events ?? []) {
      // A group is a count over a span, not a list of moments: the cluster
      // records when it first and last saw the probe fail and nothing in
      // between. Drawing two marks and saying how many are missing is the
      // whole of what is known.
      const first = timeOf(group.firstSeen);
      const last = timeOf(group.lastSeen);
      const hidden = Math.max(0, (group.count ?? 0) - 2);
      const note = hidden > 0 ? `and ${hidden} more between` : "";
      if (first !== null) {
        out.push(failureEntry(pod, container, group, first, source(first), note));
      }
      if (last !== null && last !== first) {
        out.push(failureEntry(pod, container, group, last, source(last), note));
      }
    }
  }
  return out;
}

// changed is what one Report says happened since the Report before it. The
// timestamps are the cluster's wherever the cluster has one; a change it dates
// only by having reported it is dated when the Report was generated, which is
// the earliest moment it could be known.
function changed(before, after, now) {
  const was = new Map();
  for (const { pod, container } of podEntries(before)) {
    was.set(podKey(pod, container), pod);
  }

  const out = [];
  for (const { pod, container } of podEntries(after)) {
    const previous = was.get(podKey(pod, container)) ?? null;

    for (const condition of pod.conditions ?? []) {
      // A pod the Dashboard is seeing for the first time has no condition to
      // have flipped away from: what it arrives with is a state rather than a
      // change, and the Report it arrived in is the state.
      const older = conditionIn(previous, condition.type);
      if (older === null || older.status === condition.status) {
        continue;
      }
      out.push(conditionEntry(pod, condition, timeOf(condition.lastTransitionTime) ?? now, OBSERVED));
    }

    if (previous !== null && (pod.restarts ?? 0) > (previous.restarts ?? 0)) {
      out.push(restartEntry(pod, container, timeOf(pod.lastTermination?.finishedAt) ?? now, OBSERVED));
    }

    for (const group of pod.events ?? []) {
      const older = groupIn(previous, group.probe);
      const last = timeOf(group.lastSeen) ?? now;
      const grew = older === null || (group.count ?? 0) > (older.count ?? 0) || last > (timeOf(older.lastSeen) ?? 0);
      if (!grew) {
        continue;
      }
      // Only the newest failure is an event: the ones before it were reported
      // when they happened, and this group is the same group counting on.
      out.push(failureEntry(pod, container, group, last, OBSERVED, ""));
    }
  }
  return out;
}

function conditionEntry(pod, condition, at, source) {
  return entry({
    kind: CONDITION,
    at,
    source,
    pod: pod.name,
    container: "",
    probe: "",
    // The CLI prints a condition as its status and how long it has held. What
    // a Timeline marks is the moment it changed, which is that sentence in the
    // past tense and no more words than that.
    text: `${condition.type} became ${condition.status}`,
    detail: condition.type,
  });
}

function restartEntry(pod, container, at, source) {
  return entry({
    kind: RESTART,
    at,
    source,
    pod: pod.name,
    container,
    probe: "",
    // render.terminationCell, without the age: the Timeline says when on its
    // own, and an age beside a timestamp is the same fact twice.
    text: terminationLine(pod.lastTermination) || "the container restarted",
    detail: container,
  });
}

function failureEntry(pod, container, group, at, source, note) {
  return entry({
    kind: FAILURE,
    at,
    source,
    pod: pod.name,
    container,
    probe: group.probe ?? "",
    // The kubelet's own message, verbatim. It is the sentence the CLI prints
    // under the Failure evidence row that counted it.
    text: group.message ?? "",
    note,
    detail: group.probe ?? "",
  });
}

function entry(fields) {
  return {
    note: "",
    ...fields,
    // The key names the moment rather than the Report that carried it, so the
    // same failure arriving in ten snapshots is one entry and fires once.
    key: [fields.kind, fields.pod, fields.container, fields.probe, fields.at].join("\u0000"),
  };
}

// terminationLine is why the container last died, in the CLI's words.
export function terminationLine(termination) {
  if (!termination) {
    return "";
  }
  const parts = [];
  if (termination.reason) {
    parts.push(termination.reason);
  }
  parts.push(`exit ${termination.exitCode ?? 0}`);
  if (termination.signal) {
    parts.push(`signal ${termination.signal}`);
  }
  return parts.join(", ");
}

// shapeOf turns a workload inside out. A Report carries pods under each
// container, because the CLI asks about a container and lists the pods running
// it; a diagram asks the other question, and needs the containers under each
// pod, and the pods under the node whose kubelet probes them.
//
// The pod-level facts are the same whichever container they were read from,
// which is what internal/analyze/runtime.go builds, so the first reading of a
// pod wins and the rest only contribute their own container.
export function shapeOf(workload) {
  const pods = new Map();
  for (const container of workload?.containers ?? []) {
    for (const pod of container.pods ?? []) {
      if (!pods.has(pod.name)) {
        pods.set(pod.name, {
          name: pod.name,
          node: pod.node ?? UNSCHEDULED,
          conditions: pod.conditions ?? [],
          containers: [],
        });
      }
      pods.get(pod.name).containers.push({
        name: container.name,
        sidecar: container.sidecar === true,
        ready: pod.ready === true,
        started: pod.started,
        restarts: pod.restarts ?? 0,
        lastTermination: pod.lastTermination ?? null,
        events: pod.events ?? [],
        probes: probesOf(container),
      });
    }
  }

  if (pods.size === 0) {
    return templateShape(workload);
  }

  const nodes = new Map();
  for (const pod of [...pods.values()].sort((a, b) => compare(a.name, b.name))) {
    if (!nodes.has(pod.node)) {
      nodes.set(pod.node, { name: pod.node, pods: [] });
    }
    nodes.get(pod.node).pods.push(pod);
  }

  // The unscheduled slot goes last: it is where a pod waits, not a kubelet,
  // and a reader looking for a node should not have to read past it.
  return [...nodes.values()].sort((a, b) => {
    if ((a.name === UNSCHEDULED) !== (b.name === UNSCHEDULED)) {
      return a.name === UNSCHEDULED ? 1 : -1;
    }
    return compare(a.name, b.name);
  });
}

// templateShape is a workload with nothing running it: the containers the pod
// template configures, and no pod and no node to put them in. Every chip is
// unknown, because a template says how a container will be checked and
// nothing at all about how it is doing.
function templateShape(workload) {
  const containers = (workload?.containers ?? []).map((container) => ({
    name: container.name,
    sidecar: container.sidecar === true,
    ready: false,
    started: undefined,
    restarts: 0,
    lastTermination: null,
    events: [],
    probes: probesOf(container),
  }));
  if (containers.length === 0) {
    return [];
  }
  return [
    {
      name: TEMPLATE,
      pods: [{ name: TEMPLATE, template: true, node: TEMPLATE, conditions: [], containers }],
    },
  ];
}

// probesOf is the container's three probes in the order every surface presents
// them, with the ones nobody configured left out: a schedule bar for a probe
// that does not exist would be a bar for a check nobody runs.
function probesOf(container) {
  const out = [];
  for (const probe of ["startup", "readiness", "liveness"]) {
    const detail = container[probe] ?? null;
    if (detail?.config) {
      out.push({ probe, detail, handler: handlerLine(detail.config.handler) });
    }
  }
  return out;
}

// reportAt is the Report a moment is drawn from: the newest one at or before
// it. A moment before the first Report has none, which is the reconstructed
// era and is drawn from the first Report's timestamps instead.
export function reportAt(reports, at) {
  let found = null;
  for (const report of reports) {
    if ((timeOf(report.generatedAt) ?? 0) > at) {
      break;
    }
    found = report;
  }
  return found;
}

// stateAt is the diagram at one moment: the nodes, their pods, and each pod's
// containers, with every state already decided so the drawing has no judgement
// left to make.
export function stateAt(reports, name, at) {
  const observed = reportAt(reports, at);
  if (observed !== null) {
    return { era: OBSERVED, nodes: nowShape(workloadIn(observed, name)) };
  }
  const first = reports.length > 0 ? workloadIn(reports[0], name) : null;
  return { era: RECONSTRUCTED, nodes: thenShape(first, at) };
}

// nowShape is a Report read as what is true: every state in it is the
// kubelet's own.
function nowShape(workload) {
  return shapeOf(workload).map((node) => ({
    ...node,
    pods: node.pods.map((pod) => ({
      ...pod,
      conditions: statuses(pod, () => null),
      containers: pod.containers.map((container) => ({
        ...container,
        state: pod.template === true ? UNKNOWN : stateOf(container),
      })),
    })),
  }));
}

// thenShape is the first Report read backwards.
//
// A condition that last flipped at T says two things: what it is now, and that
// it was the other thing before T. Both are the cluster's, and the second is
// what makes a reconstruction worth drawing at all. A condition the cluster
// never dated says only the first, so before any moment it is unknown.
//
// A container chip has no such timestamp. The kubelet reports whether a
// container is ready now and never reports when it became so, so in the
// reconstructed era every chip is unknown and the pods drawn are the ones the
// first Report carried, because the cluster does not say when a pod appeared.
function thenShape(workload, at) {
  return shapeOf(workload).map((node) => ({
    ...node,
    pods: node.pods.map((pod) => ({
      ...pod,
      conditions: statuses(pod, (condition) => rewound(condition, at)),
      containers: pod.containers.map((container) => ({
        ...container,
        state: UNKNOWN,
        restarts: rewoundRestarts(container, at),
      })),
    })),
  }));
}

// statuses is a pod's two conditions as a lookup, so the drawing asks for one
// by name rather than searching. A condition the pod does not carry at all is
// unknown, which is what the CLI's gray dash says.
function statuses(pod, rewrite) {
  const out = {};
  for (const type of CONDITION_TYPES) {
    const condition = (pod.conditions ?? []).find((c) => c.type === type) ?? null;
    out[type] = condition === null ? "Unknown" : (rewrite(condition) ?? condition.status);
  }
  return out;
}

function rewound(condition, at) {
  const flipped = timeOf(condition.lastTransitionTime);
  if (flipped === null) {
    // The cluster never dated this condition, so nothing is known about when
    // it held. Saying it held then would be inventing the one thing the
    // Timeline exists to be honest about.
    return "Unknown";
  }
  return at >= flipped ? condition.status : opposite(condition.status);
}

function opposite(status) {
  if (status === "True") {
    return "False";
  }
  return status === "False" ? "True" : "Unknown";
}

// rewoundRestarts steps the counter back over the one restart the cluster
// dates. A container that has restarted five times reports when the fifth
// happened and nothing about the other four, so the count before that moment
// is four and everything before that is as far back as counting goes.
function rewoundRestarts(container, at) {
  const died = timeOf(container.lastTermination?.finishedAt);
  if (died === null || at >= died) {
    return container.restarts;
  }
  return Math.max(0, container.restarts - 1);
}

// stateOf is the chip's state from the kubelet's own flags: a container that
// has not started yet is neither ready nor failing, and a started one is ready
// or it is not.
export function stateOf(container) {
  if (container.started === false) {
    return UNSTARTED;
  }
  return container.ready ? READY : UNREADY;
}

// extentOf is the span the Timeline covers: from the earliest moment anything
// is known about to the latest, which is now while the plugin is still
// talking.
export function extentOf(entries, reports, now) {
  let from = now;
  for (const entry of entries) {
    from = Math.min(from, entry.at);
  }
  for (const report of reports) {
    from = Math.min(from, timeOf(report.generatedAt) ?? now);
  }
  return { from, to: now };
}

function workloadIn(report, name) {
  return (report?.workloads ?? []).find((w) => w.displayName === name) ?? null;
}

// podEntries walks a workload the way the Report is written: every container,
// and under it every pod running that container. A pod running two containers
// of the workload turns up twice, once for each.
function podEntries(workload) {
  const out = [];
  for (const container of workload?.containers ?? []) {
    for (const pod of container.pods ?? []) {
      out.push({ pod, container: container.name });
    }
  }
  return out;
}

function podKey(pod, container) {
  return `${container}\u0000${pod.name}`;
}

function conditionIn(pod, type) {
  return (pod?.conditions ?? []).find((c) => c.type === type) ?? null;
}

function groupIn(pod, probe) {
  return (pod?.events ?? []).find((g) => g.probe === probe) ?? null;
}

// compare orders by code unit rather than by locale, as the CLI compares
// bytes. Two surfaces that disagree about where a thing goes are two different
// pictures.
function compare(a, b) {
  if (a === b) {
    return 0;
  }
  return a < b ? -1 : 1;
}
