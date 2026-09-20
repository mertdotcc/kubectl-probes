// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The Inspection: one workload, drawn as the kubelet-and-pods diagram and
// replayed along the Timeline, with the CLI's own Failure evidence and
// Findings underneath.
//
// This file owns none of the three. history.js decides what happened and what
// is true at a moment, diagram.js draws that moment, and timeline.js says
// which moment it is. What is here is the wiring: which workload, which pod
// the reader has opened, and the one rule that keeps the drawing honest --
// every state on screen comes from a Report, and every animation comes from an
// entry history.js built out of one. See ADR-0005.

import {
  FAILURE,
  OBSERVED,
  RECONSTRUCTED,
  RESTART,
  entriesFor,
  extentOf,
  reportAt,
  stateAt,
} from "./history.js";
import { MARKER_KINDS, SOURCE_WORDS, SPEEDS, newTimeline } from "./timeline.js";
import {
  RECONSTRUCTED_NOTE,
  SCHEDULE_NOTE,
  STILL_NOTE,
  TRAFFIC_NOTE,
  newDiagram,
} from "./diagram.js";
import { workloadNamed } from "./overview.js";
import {
  EVIDENCE_COLUMNS,
  HEADING_FAILURE_EVIDENCE,
  HEADING_FINDINGS,
  NO_EVENTS_SAID,
  NO_PODS_SAID,
  NO_TEMPLATE_NOTE,
  UNKNOWN_EVENTS,
  age,
  podLine,
  timeOf,
} from "./sentences.js";

// What each chip state means, for the legend. The glyph and the word carry the
// state between them; the colour only repeats it.
const chipLegend = [
  ["ready", "the kubelet reports this container ready"],
  ["unready", "the kubelet reports this container not ready"],
  ["unstarted", "the container has not started yet"],
  ["unknown", "nothing is known about this container at this moment"],
];

// newInspection takes over the panel the page laid out. It finds its own parts
// inside it rather than being handed a dozen of them: the Inspection is one
// section with one shape, and listing every element of it at the call site
// would be a second copy of the markup to keep in step.
export function newInspection({ panel, onClose }) {
  const el = {
    name: panel.querySelector("#inspection-name"),
    where: panel.querySelector("#inspection-where"),
    connection: panel.querySelector("#inspection-connection"),
    connectionState: panel.querySelector("#inspection-connection-state"),
    note: panel.querySelector("#inspection-note"),
    body: panel.querySelector("#inspection-body"),
    evidence: panel.querySelector("#inspection-evidence"),
  };

  panel.querySelector("#inspection-close").addEventListener("click", onClose);
  drawLegend(panel.querySelector("#diagram-legend"));
  drawSpeeds(panel.querySelector("#timeline-speed"));

  // reports is every Report the Dashboard holds, oldest first, and selected is
  // the workload the hash names. Everything else on screen is derived from
  // those two and the moment the Timeline is at.
  let reports = [];
  let entries = [];
  let startedAt = 0;
  let selected = "";
  // pod is the one the reader has opened to see its schedule, which is the
  // only state here that is theirs rather than the cluster's.
  let pod = "";
  // drawn names the moment last drawn, so sixty frames a second of playback
  // redraw the diagram only when the picture actually changes.
  let drawn = "";

  const diagram = newDiagram({
    host: panel.querySelector("#diagram-nodes"),
    pulses: panel.querySelector("#diagram-pulses"),
    onSelectPod(name) {
      pod = pod === name ? "" : name;
      drawn = "";
      show(timeline.moment());
    },
  });

  const timeline = newTimeline({
    root: panel.querySelector("#timeline"),
    lane: panel.querySelector("#timeline-lane"),
    track: panel.querySelector("#timeline-track"),
    scrub: panel.querySelector("#timeline-scrub"),
    play: panel.querySelector("#timeline-play"),
    follow: panel.querySelector("#timeline-live"),
    speed: panel.querySelector("#timeline-speed"),
    clock: panel.querySelector("#timeline-clock"),
    from: panel.querySelector("#timeline-from"),
    to: panel.querySelector("#timeline-to"),
    onDraw: show,
    onFire: fire,
  });

  return function draw({ reports: held, startedAt: began, still, selected: wanted, connection }) {
    panel.hidden = wanted === "";
    if (panel.hidden) {
      return;
    }

    if (wanted !== selected) {
      // A different workload is a different history. Nothing that has been
      // played is about this one, and no pod of the last one is open.
      selected = wanted;
      pod = "";
      drawn = "";
      timeline.reset();
    }
    reports = held;
    startedAt = began;

    el.name.textContent = selected;
    say(connection);

    const latest = reports.length > 0 ? reports[reports.length - 1] : null;
    if (workloadNamed(latest, selected) === null) {
      // Either the run was opened on a name that covers more than one
      // workload, or the workload has since left the cluster. Saying which
      // Report was looked in is the difference between the two.
      el.note.textContent = `No workload named ${selected} is in the newest Report.`;
      el.body.hidden = true;
      return;
    }
    el.note.textContent = "";
    el.body.hidden = false;

    entries = entriesFor(reports, startedAt, selected);
    const now = Math.max(Date.now(), timeOf(latest.generatedAt) ?? 0);
    timeline.still(still);
    timeline.update(entries, { ...extentOf(entries, reports, now), startedAt });
  };

  // show draws the moment the Timeline is at. It runs on every frame of
  // playback, so it works out which picture the moment calls for and does
  // nothing when that is the picture already on screen.
  function show(at) {
    const observed = reportAt(reports, at);
    const report = observed ?? (reports.length > 0 ? reports[0] : null);
    if (report === null) {
      return;
    }
    // In the reconstructed era no Report names the moment, and the picture
    // changes only where an entry says it does, so the entries gone by name
    // it instead.
    const key = [
      report.generatedAt,
      observed === null ? entries.filter((entry) => entry.at <= at).length : "",
      pod,
    ].join("\u0000");
    if (key === drawn) {
      return;
    }
    drawn = key;

    const state = stateAt(reports, selected, at);
    diagram.draw(state, pod);
    panel.dataset.era = state.era;

    const workload = workloadNamed(report, selected);
    el.where.textContent = whereWords(workload);
    drawEvidence(el.evidence, workload, timeOf(report.generatedAt) ?? 0, state.era);
  }

  // fire is an entry the clock has just passed. The diagram is told what
  // happened in its own terms; it decides what moves.
  function fire(entry) {
    if (entry.kind === FAILURE) {
      diagram.failure(entry);
      return;
    }
    if (entry.kind === RESTART) {
      diagram.restart(entry);
    }
    // A condition flipping needs nothing here: the pod's border and its
    // traffic dot follow the condition and carry a colour transition, so the
    // redraw is the animation.
  }

  function say(connection) {
    el.connection.dataset.state = connection.state;
    el.connection.title = connection.said ?? "";
    el.connectionState.textContent = connection.state;
  }
}

// whereWords is the line under the workload's name: where it is and how much
// of it there is, in the words the CLI's own header uses.
function whereWords(workload) {
  if (workload === null) {
    return "";
  }
  const parts = [];
  if (workload.namespace) {
    parts.push(`-n ${workload.namespace}`);
  }
  parts.push(podLine(workload));
  return parts.join(" · ");
}

// drawEvidence is the CLI's Failure evidence table and Findings list, under
// the drawing and in the CLI's words. It is rebuilt rather than written into:
// it changes only when the moment being drawn does, and it is a few rows.
function drawEvidence(host, workload, now, era) {
  host.replaceChildren();
  if (workload === null) {
    return;
  }
  if (era === RECONSTRUCTED) {
    host.append(note("evidence-era", RECONSTRUCTED_NOTE));
  }
  if (workload.templateAvailable !== true) {
    host.append(note("evidence-era", NO_TEMPLATE_NOTE));
  }

  for (const container of workload.containers ?? []) {
    const section = document.createElement("section");
    section.className = "evidence-container";

    const heading = document.createElement("h4");
    heading.className = "mono";
    heading.textContent = container.sidecar
      ? `container ${container.name} (sidecar)`
      : `container ${container.name}`;
    section.append(heading, subheading(HEADING_FAILURE_EVIDENCE), evidenceBody(container, now));

    if ((container.findings ?? []).length > 0) {
      section.append(subheading(HEADING_FINDINGS), findingsList(container.findings));
    }
    host.append(section);
  }
}

// evidenceBody is the Unhealthy events the cluster reports, grouped by pod and
// probe. A section with nothing in it still says so: the reader asked about
// probes, and "no Unhealthy events are recorded" is an answer where a missing
// table is a gap they have to notice.
function evidenceBody(container, now) {
  if (!container.runtime) {
    return note("evidence-none", NO_PODS_SAID);
  }
  // An absent count means listing events was forbidden, which is not the same
  // as zero. Nothing running is nothing to fail, and that is a zero.
  if (container.runtime.failures === null || container.runtime.failures === undefined) {
    return note("evidence-none", UNKNOWN_EVENTS);
  }

  const rows = [];
  for (const pod of container.pods ?? []) {
    for (const group of pod.events ?? []) {
      rows.push({ pod, group });
    }
  }
  if (rows.length === 0) {
    return note("evidence-none", NO_EVENTS_SAID);
  }

  const body = document.createElement("tbody");
  for (const { pod, group } of rows) {
    body.append(
      evidenceRow([
        pod.name,
        group.probe,
        String(group.count ?? 0),
        age(now, group.firstSeen) || "-",
        age(now, group.lastSeen) || "-",
      ]),
    );
    if (group.message) {
      // The message hangs under the row that counted it, as it does in the
      // terminal, so a message of any length costs the table no width.
      body.append(messageRow(group.message));
    }
  }

  const table = document.createElement("table");
  table.className = "evidence";
  table.append(evidenceHead(), body);
  return table;
}

function evidenceHead() {
  const row = document.createElement("tr");
  for (const column of EVIDENCE_COLUMNS) {
    const th = document.createElement("th");
    th.scope = "col";
    th.textContent = column;
    row.append(th);
  }
  const head = document.createElement("thead");
  head.append(row);
  return head;
}

function evidenceRow(cells) {
  const row = document.createElement("tr");
  for (const [i, text] of cells.entries()) {
    const td = document.createElement("td");
    td.textContent = text;
    // The count is the fact the reader came for, and it is red in the
    // terminal for the same reason.
    if (i === 2) {
      td.dataset.tone = "danger";
    }
    row.append(td);
  }
  return row;
}

function messageRow(message) {
  const row = document.createElement("tr");
  row.className = "evidence-message";
  const td = document.createElement("td");
  td.colSpan = EVIDENCE_COLUMNS.length;
  td.textContent = message;
  row.append(td);
  return row;
}

function findingsList(findings) {
  const list = document.createElement("dl");
  list.className = "findings";
  for (const finding of findings) {
    const rule = document.createElement("dt");
    rule.className = "mono";
    rule.textContent = finding.rule;
    const said = document.createElement("dd");
    said.textContent = finding.message;
    list.append(rule, said);
  }
  return list;
}

function subheading(text) {
  const heading = document.createElement("h5");
  heading.textContent = text;
  return heading;
}

function note(className, text) {
  const paragraph = document.createElement("p");
  paragraph.className = className;
  paragraph.textContent = text;
  return paragraph;
}

// drawLegend says what every mark on the drawing means, once, under it. Two of
// its lines are the ones ADR-0005 insists on: the traffic dot is derived and
// says so, and a drawing that is not moving is a healthy one rather than a
// broken one.
function drawLegend(host) {
  const items = [
    ...chipLegend.map(([state, said]) => ({ mark: "chip-swatch", data: { state }, said })),
    { mark: "traffic-swatch", data: { inService: "true" }, said: TRAFFIC_NOTE },
    { mark: "schedule-swatch", data: {}, said: SCHEDULE_NOTE },
    ...MARKER_KINDS.map(({ kind, said }) => ({ mark: "marker-swatch", data: { kind }, said })),
    ...[OBSERVED, RECONSTRUCTED].map((source) => ({
      mark: "source-swatch",
      data: { source },
      said: SOURCE_WORDS[source],
    })),
    { mark: "still-swatch", data: {}, said: STILL_NOTE },
  ];
  host.replaceChildren(...items.map(legendItem));
}

function legendItem({ mark, data, said }) {
  const item = document.createElement("li");
  const swatch = document.createElement("span");
  swatch.className = mark;
  Object.assign(swatch.dataset, data);
  item.append(swatch, document.createTextNode(said));
  return item;
}

function drawSpeeds(select) {
  select.replaceChildren(
    ...SPEEDS.map((speed) => {
      const option = document.createElement("option");
      option.value = String(speed);
      option.textContent = `${speed}x`;
      return option;
    }),
  );
}
