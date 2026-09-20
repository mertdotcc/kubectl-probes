// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The Overview's rules, which are the CLI's rules. internal/render/overview.go
// decides what goes in a cell, in what order, and under what mark; everything
// here decides the same thing the same way, from the same Report. A row the
// browser draws and the terminal would not is a row nobody can check against
// the cluster, which ADR-0005 rules out.

import { short } from "./sentences.js";

// The probes in the order every surface presents them, which is the order the
// kubelet puts them to work in. It is model.ProbeTypes.
export const PROBE_TYPES = ["startup", "readiness", "liveness"];

// The marks a cell can carry, and the sentences that explain them. Both the
// marks and the wording are render.absent, render.driftMark and the notes
// beside them.
export const ABSENT = "-";
export const DRIFT_MARK = "*";

export const DRIFT_NOTE = "* running config differs from workload template";
export const FAILURES_NOTE = "- failures are unknown: listing events was forbidden";
export const EMPTY_NOTE = "No workloads found.";

// The orders the rows come in, matching the --sort values that select one.
export const SORT_NAME = "name";
export const SORT_SEVERITY = "severity";

// The columns, in the CLI's order. NAMESPACE is decided separately, because
// the CLI takes it from -A and the browser never sees a flag.
const COLUMNS = [
  "WORKLOAD",
  "CONTAINER",
  "STARTUP",
  "READINESS",
  "LIVENESS",
  "READY",
  "RESTARTS",
  "FAILURES",
  "FINDINGS",
];

// columnsFor is the header the table draws.
export function columnsFor(namespaced) {
  return namespaced ? ["NAMESPACE", ...COLUMNS] : [...COLUMNS];
}

// spansNamespaces says whether the NAMESPACE column is worth drawing. The CLI
// asks the -A flag, since without it every row carries the one namespace the
// user already named. The browser has no flag to ask, so it asks the Report:
// more than one namespace in it is the same condition by another route.
export function spansNamespaces(report) {
  const seen = new Set();
  for (const workload of workloadsOf(report)) {
    seen.add(workload.namespace ?? "");
  }
  return seen.size > 1;
}

// workloadNamed finds the workload a display name refers to, which is how a
// URL hash and the positional --serve was given both resolve to one.
export function workloadNamed(report, name) {
  return workloadsOf(report).find((w) => w.displayName === name) ?? null;
}

function workloadsOf(report) {
  return report?.workloads ?? [];
}

// rowsOf is one row per container, with the facts already dug out of the
// Report so that sorting, filtering and drawing do not each have to.
export function rowsOf(report) {
  const rows = [];
  for (const workload of workloadsOf(report)) {
    for (const container of workload.containers ?? []) {
      rows.push(rowOf(workload, container));
    }
  }
  return rows;
}

function rowOf(workload, container) {
  const probes = PROBE_TYPES.map((probe) => container[probe] ?? null);
  const runtime = container.runtime ?? null;

  return {
    // key names the one container this row is about, so a re-render can find
    // the row it drew last time rather than drawing a new one.
    key: [workload.namespace ?? "", workload.displayName, container.name].join("\u0000"),
    namespace: workload.namespace ?? "",
    workload: workload.displayName,
    container: container.name,
    probes,
    drifted: probes.some((detail) => detail?.drifted === true),
    // A workload with no pods still gets a row. It says 0/0 rather than
    // claiming anything about pods that do not exist.
    ready: runtime ? `${runtime.ready}/${runtime.total}` : "0/0",
    restarts: runtime ? runtime.restarts : 0,
    // An absent count means listing events was forbidden, which is not the
    // same as zero. Nothing running is nothing to fail, and that is a zero.
    failures: runtime ? (runtime.failures ?? null) : 0,
    findings: (container.findings ?? []).length,
  };
}

// cellsOf is one row as the table draws it, in the same order as columnsFor.
export function cellsOf(row, namespaced) {
  const cells = namespaced ? [cell(row.namespace)] : [];
  cells.push(cell(row.workload), cell(row.container));
  for (const [i, probe] of PROBE_TYPES.entries()) {
    cells.push(probeCell(probe, row.probes[i]));
  }
  cells.push(
    cell(row.ready),
    cell(String(row.restarts)),
    failuresCell(row.failures),
    findingsCell(row.findings),
  );
  return cells;
}

// cell is one table cell: the text, the tone that colours it, and whether it
// wears the drift mark. The mark is kept apart from the text because only the
// mark is coloured, exactly as the CLI colours only the star.
function cell(text, tone = "", mark = false) {
  return { text, tone, mark };
}

// probeCell is a probe in one cell: how it asks, and the single duration a
// reader scanning the table needs. A probe nobody configured is a gray dash,
// and one whose running configuration differs from the template wears a star,
// explained once above the table.
//
// A detail with no config but a drift mark is a probe the template configures
// and the running pod does not, which reads as a dash with a star on it.
function probeCell(probe, detail) {
  const drifted = detail?.drifted === true;
  if (!detail?.config) {
    return cell(ABSENT, "muted", drifted);
  }
  const timing = headline(probe, detail.timing);
  const handler = handlerType(detail.config.handler);
  return cell(timing ? `${handler}:${timing}` : handler, "", drifted);
}

// headline is the one duration a probe's cell shows: for a startup probe the
// budget the container has to come up in, and for the other two how long the
// kubelet takes to notice a failure, because that is the number each of them
// is read for.
function headline(probe, timing) {
  if (!timing) {
    return "";
  }
  if (probe === "startup" && timing.startupBudget) {
    return short(timing.startupBudget);
  }
  return short(timing.failureDetection);
}

// handlerType names how a probe asks. A probe with no handler at all cannot
// come from a pod the API server accepted, only from a hand-written manifest,
// and it is reported as unknown rather than left out.
function handlerType(handler) {
  return handler?.type ? handler.type : "?";
}

// failuresCell counts the Unhealthy events the cluster reports. It is a gray
// dash, rather than a zero, where the events could not be read: an unknown
// count is not evidence of anything.
function failuresCell(failures) {
  if (failures === null) {
    return cell(ABSENT, "muted");
  }
  return failures === 0 ? cell("0") : cell(String(failures), "danger");
}

function findingsCell(findings) {
  return findings === 0 ? cell("0") : cell(String(findings), "warn");
}

// notesFor says what the table could not, once, for whatever marks it ended
// up carrying. It is asked about the rows on screen rather than the whole
// Report, because a note explains a mark a reader can see.
export function notesFor(rows) {
  const notes = [];
  if (rows.some((row) => row.drifted)) {
    notes.push(DRIFT_NOTE);
  }
  if (rows.some((row) => row.failures === null)) {
    notes.push(FAILURES_NOTE);
  }
  return notes;
}

// sortRows returns the rows in the order asked for, leaving the array it was
// given alone.
export function sortRows(rows, order) {
  return rows.slice().sort((a, b) => {
    if (order === SORT_SEVERITY && band(a) !== band(b)) {
      return band(a) - band(b);
    }
    return compareName(a, b);
  });
}

// compareName orders by namespace, workload, then container, comparing code
// unit by code unit rather than by locale: the CLI compares bytes, and two
// surfaces that disagree about where a row goes are two different tables.
function compareName(a, b) {
  return (
    compare(a.namespace, b.namespace) ||
    compare(a.workload, b.workload) ||
    compare(a.container, b.container)
  );
}

function compare(a, b) {
  if (a === b) {
    return 0;
  }
  return a < b ? -1 : 1;
}

// band is where the severity order puts a row: the containers the cluster
// reports probe failures for first, then the ones that have restarted, then
// the ones only a rule has an opinion about, and the quiet ones last. Rows
// stay alphabetical within a band.
//
// A container whose failure count could not be read is banded on the rest of
// its evidence, because an unknown count is not evidence.
function band(row) {
  if (row.failures !== null && row.failures > 0) {
    return 0;
  }
  if (row.restarts > 0) {
    return 1;
  }
  if (row.findings > 0) {
    return 2;
  }
  return 3;
}

// filterRows narrows the table to the workloads and containers whose names
// hold the text typed. It is a plain substring match, because the names are
// what a reader is looking at.
export function filterRows(rows, text) {
  const needle = text.trim().toLowerCase();
  if (needle === "") {
    return rows;
  }
  return rows.filter(
    (row) =>
      row.workload.toLowerCase().includes(needle) ||
      row.container.toLowerCase().includes(needle),
  );
}
