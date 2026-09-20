// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The Dashboard's entry point: take the Reports off the stream, keep the hour
// of them the server replays, and draw the newest as the Overview. Every
// Report is whole, so the newest one is the picture and there is no state
// here the cluster did not put there. See ADR-0005 and ADR-0006.

import * as overview from "./overview.js";
import { newInspection } from "./inspection.js";
import { newTable } from "./table.js";

// timelineWindow is how far back the Reports kept here reach: the server's
// own ring window, because that is as far back as it will replay. It is the
// observed part of the Timeline, which #35 draws.
const timelineWindow = 60 * 60 * 1000;

// What the connection indicator says, in a word, and the sentence behind it.
const connections = {
  connecting: "Waiting for the plugin to answer.",
  live: "Connected. Every change the cluster reports arrives here.",
  reconnecting: "Disconnected. The plugin may have exited; the browser keeps trying.",
  static: "This run was read from files, so there is nothing to change.",
  lost: "The plugin is not answering.",
};

const elements = {
  connection: document.querySelector("#connection"),
  connectionState: document.querySelector("#connection-state"),
  notes: document.querySelector("#notes"),
  filter: document.querySelector("#filter"),
  sort: document.querySelector("#sort"),
};

const state = {
  // reports is the hour of Reports the server replayed and has sent since,
  // oldest first. latest is the one being drawn.
  reports: [],
  latest: null,
  // initial is the workload --serve was given to open on, until it has been
  // opened on or found not to name one.
  initial: "",
  sort: overview.SORT_NAME,
  filter: "",
  // selected is the workload the hash names, empty for the Overview alone.
  // A tab opened on a hash is already on that workload, before anything has
  // been asked of the plugin.
  selected: selectedInHash(),
};

const drawTable = newTable({
  table: document.querySelector("#overview"),
  head: document.querySelector("#overview-head"),
  body: document.querySelector("#overview-body"),
  empty: document.querySelector("#empty"),
  onSelect: show,
});

const drawInspection = newInspection({
  panel: document.querySelector("#inspection"),
  name: document.querySelector("#inspection-name"),
  note: document.querySelector("#inspection-note"),
  body: document.querySelector("#inspection-body"),
  close: document.querySelector("#inspection-close"),
  onClose: () => show(""),
});

// The hash is the whole of the navigation: a row sets it, the back button
// sets it, and one listener turns either into the panel that is open.
window.addEventListener("hashchange", () => {
  state.selected = selectedInHash();
  draw();
});

elements.filter.addEventListener("input", () => {
  state.filter = elements.filter.value;
  draw();
});

elements.sort.addEventListener("click", () => {
  const severity = state.sort !== overview.SORT_SEVERITY;
  state.sort = severity ? overview.SORT_SEVERITY : overview.SORT_NAME;
  elements.sort.setAttribute("aria-pressed", String(severity));
  draw();
});

draw();
connect();

// connect asks what kind of run this is before it asks for anything else. A
// run served from -f is a cluster as it was written down: one Report, and no
// stream to wait on. See ADR-0003.
async function connect() {
  say("connecting");
  try {
    const meta = await (await fetch("/api/meta")).json();
    state.initial = meta.initial ?? "";
    if (!meta.static) {
      listen();
      return;
    }
    say("static");
    received(await (await fetch("/api/report")).json());
  } catch {
    say("lost");
  }
}

function listen() {
  const stream = new EventSource("/api/events");
  stream.addEventListener("open", () => say("live"));
  stream.addEventListener("report", (message) => {
    say("live");
    received(JSON.parse(message.data));
  });
  // The browser reconnects on its own and is replayed the Reports it missed,
  // so there is nothing to do here but say what has happened.
  stream.addEventListener("error", () => say("reconnecting"));
}

function received(report) {
  remember(report);
  openInitial();
  draw();
}

// remember keeps the Reports that arrived, dropping the ones the newest has
// aged out. The window is measured against the Report being added rather than
// the clock, so a tab left open holds the last hour of reporting and not the
// last hour.
function remember(report) {
  state.latest = report;
  state.reports.push(report);

  const cutoff = Date.parse(report.generatedAt) - timelineWindow;
  let drop = 0;
  while (drop < state.reports.length && Date.parse(state.reports[drop].generatedAt) < cutoff) {
    drop += 1;
  }
  if (drop > 0) {
    state.reports.splice(0, drop);
  }
}

// openInitial opens the Inspection the run was told to open on, once, and
// only once a Report has arrived to say that name is a workload at all: a run
// given a type rather than a name covers more than one workload and opens the
// Overview, exactly as the CLI does.
function openInitial() {
  const initial = state.initial;
  state.initial = "";
  if (initial === "" || state.selected !== "") {
    return;
  }
  if (!overview.workloadNamed(state.latest, initial)) {
    return;
  }
  // Opening on a workload is the state the tab started in, not a step away
  // from the Overview, so it replaces the entry rather than adding one and
  // the back button still leaves the page.
  history.replaceState(null, "", `#${initial}`);
  state.selected = initial;
}

// show is what choosing a workload comes to. Choosing one writes the hash and
// nothing else, because the listener above draws whether the hash was written
// here or by the back button.
//
// Leaving one cannot: an emptied hash leaves a bare # in the address bar and
// is not worth the step back it costs. Pushing the page's own address is the
// same step with nothing left over, and it is not heard as a hash changing,
// so it says here what the listener would have said.
function show(workload) {
  if (workload !== "") {
    location.hash = `#${workload}`;
    return;
  }
  history.pushState(null, "", location.pathname + location.search);
  state.selected = "";
  draw();
}

function selectedInHash() {
  const hash = location.hash.slice(1);
  try {
    return decodeURIComponent(hash);
  } catch {
    // A hash that is not valid escaping is still a name somebody typed, and
    // it resolves to no workload either way.
    return hash;
  }
}

function say(connection) {
  elements.connection.dataset.state = connection;
  elements.connection.title = connections[connection];
  elements.connectionState.textContent = connection;
}

function draw() {
  const namespaced = overview.spansNamespaces(state.latest);
  const all = overview.rowsOf(state.latest);
  const rows = overview.sortRows(overview.filterRows(all, state.filter), state.sort);

  drawNotes(overview.notesFor(rows));
  drawTable({ rows, namespaced, selected: state.selected, note: emptyNote(all, rows) });
  drawInspection(state.latest, state.selected);
}

// emptyNote is what stands where the table would have been. A Report with no
// workloads in it says what the CLI says; a filter that matched none of them
// says so instead, because the workloads are there and the reader hid them;
// and no Report at all is neither, so it says that.
function emptyNote(all, rows) {
  if (rows.length > 0) {
    return "";
  }
  if (state.latest === null) {
    return "Waiting for the first Report…";
  }
  if (all.length === 0) {
    return overview.EMPTY_NOTE;
  }
  return `No workload or container matches “${state.filter.trim()}”.`;
}

function drawNotes(notes) {
  elements.notes.replaceChildren(
    ...notes.map((note) => {
      const p = document.createElement("p");
      p.textContent = note;
      return p;
    }),
  );
}
