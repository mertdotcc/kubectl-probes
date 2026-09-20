// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The kubelet-and-pods drawing: a heptagon per node, the pods it runs, and in
// each pod a chip per container of the workload. It draws the state
// history.stateAt decided and nothing else, so everything on screen is a fact
// the cluster reported or a derivation this file labels as one. See ADR-0005.
//
// The shapes are SVG so they scale, and every label is HTML so it wraps, can
// be selected, and is read out in words: a chip says "not ready" to a screen
// reader as well as wearing a cross, because no state here is carried by
// colour alone.
//
// The drawing is written into rather than thrown away. A Report arrives
// whenever the cluster changes, and a redraw that replaced the elements would
// take the reader's selection, their focus and any animation in flight with
// it.

import { READY, TEMPLATE, UNKNOWN, UNREADY, UNSTARTED, UNSCHEDULED } from "./history.js";
import {
  DEFAULT_PERIOD_SECONDS,
  durationMs,
  effective,
  short,
  timingSentences,
} from "./sentences.js";

// How long a pod that has left the Report takes to go, and how long a chip
// holds its flash. Both are read back by the timer that cleans up after them,
// because somebody who has asked for no motion never gets an animation to end.
const leavingMs = 400;
const flashMs = 600;
// How long a pulse takes to cross from the kubelet to the container it is
// about, which is the one thing on screen that travels.
const pulseMs = 500;

// The states a chip can be in, as a reader is told them. The glyph and the
// word carry the state; the colour only repeats it.
const chipWords = {
  [READY]: "ready",
  [UNREADY]: "not ready",
  [UNSTARTED]: "not started",
  [UNKNOWN]: "unknown",
};

// The sentence beside the traffic dot, in the legend and on the dot itself.
// It is a derivation and says so: no Service and no EndpointSlice is read, and
// readiness gating traffic is what the Ready condition means. See ADR-0005.
export const TRAFFIC_NOTE = "in service (derived from the Ready condition)";
export const SCHEDULE_NOTE = "schedule (from configuration)";
export const STILL_NOTE =
  "a healthy probe leaves no trace in the API, so nothing is drawn for it";
export const UNSCHEDULED_NOTE = "unscheduled: no node has taken these pods yet";
// What the drawing says where the CLI says "no pods": there is a template and
// nothing running it, so there is a schedule to show and no state to show.
export const NO_PODS = "no pods";
export const RECONSTRUCTED_NOTE =
  "reconstructed from cluster timestamps: the cluster reports no per-container history, so chips are unknown and the pods are the ones the first Report carried";

// A tick every periodSeconds is the point of the bar, but a probe that checks
// every second over a ten minute budget has six hundred of them and they are
// a solid block rather than a schedule. Past this many the ticks are left off
// and the period is in the label, which is the fact they were drawing.
const mostTicks = 60;

// reduced asks whether the reader has said they want less movement. It is
// asked each time rather than once, because the setting can change under a
// tab that is already open.
function reduced() {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

// newDiagram takes over the elements the page laid out and returns the verbs
// the Inspection has for it: draw a moment, and say that something happened.
export function newDiagram({ host, pulses, onSelectPod }) {
  // The elements drawn last time, by the name of the thing each is about, so
  // this time's drawing is the same elements moved.
  let nodes = new Map();
  let pods = new Map();

  host.addEventListener("click", (event) => {
    const head = event.target.closest("button.pod-head");
    if (head) {
      onSelectPod(head.closest(".pod").dataset.pod);
    }
  });

  return {
    draw(state, selected) {
      nodes = drawNodes(host, state, nodes, pods, selected);
    },

    // failure is an Unhealthy event arriving: a pulse leaves the kubelet that
    // reported it and reaches the container it was about, and the chip takes
    // the colour of the probe that failed. It is the one thing that travels,
    // because it is the one thing that came from somewhere.
    failure(entry) {
      const chip = chipFor(pods, entry.pod, entry.container);
      flash(chip, "failure");
      const from = nodes.get(pods.get(entry.pod)?.dataset.node ?? UNSCHEDULED);
      pulse(pulses, from?.querySelector(".heptagon") ?? null, chip, entry.probe);
    },

    // restart is a container that died and came back. Nothing travels: the
    // kubelet did not report it from anywhere, it is simply true now.
    restart(entry) {
      flash(chipFor(pods, entry.pod, entry.container), "restart");
    },
  };
}

function drawNodes(host, state, drawn, pods, selected) {
  const next = new Map();
  let cursor = host.firstElementChild;

  for (const node of state.nodes) {
    const section = drawn.get(node.name) ?? newNode(node);
    drawPods(section.querySelector(".pods"), node, state.era, pods, selected);

    if (section === cursor) {
      cursor = cursor.nextElementSibling;
    } else {
      host.insertBefore(section, cursor);
    }
    next.set(node.name, section);
  }
  while (cursor) {
    const after = cursor.nextElementSibling;
    cursor.remove();
    cursor = after;
  }
  return next;
}

function newNode(node) {
  const template = node.name === TEMPLATE;
  const section = document.createElement("section");
  section.className = "node";
  section.dataset.node = node.name;
  section.dataset.unscheduled = String(node.name === UNSCHEDULED);
  section.dataset.template = String(template);

  const head = document.createElement("h3");
  head.className = "node-head";
  // A workload with no pods has no kubelet to draw. The heading says what the
  // CLI says in its place, and there is no heptagon to suggest otherwise.
  if (!template) {
    head.append(heptagon());
  }
  head.append(label("node-name mono", nodeWords(node.name)));
  if (node.name === UNSCHEDULED) {
    head.title = UNSCHEDULED_NOTE;
  }

  const list = document.createElement("div");
  list.className = "pods";
  section.append(head, list);
  return section;
}

function nodeWords(name) {
  if (name === TEMPLATE) {
    return NO_PODS;
  }
  return name === UNSCHEDULED ? "unscheduled" : name;
}

// heptagon is the kubelet, in the shape Kubernetes draws itself with. It is
// one node's kubelet: the thing that runs these probes and reports what they
// said.
function heptagon() {
  const svg = svgElement("svg", { viewBox: "-12 -12 24 24", class: "heptagon", "aria-hidden": "true" });
  const points = [];
  for (let i = 0; i < 7; i++) {
    const angle = (-Math.PI / 2) + (i * 2 * Math.PI) / 7;
    points.push(`${(10 * Math.cos(angle)).toFixed(2)},${(10 * Math.sin(angle)).toFixed(2)}`);
  }
  svg.append(svgElement("polygon", { points: points.join(" ") }));
  return svg;
}

function drawPods(host, node, era, pods, selected) {
  const wanted = new Set(node.pods.map((pod) => pod.name));
  let cursor = host.firstElementChild;

  for (const pod of node.pods) {
    const element = pods.get(pod.name) ?? newPod(pod);
    fillPod(element, pod, era, selected);
    pods.set(pod.name, element);

    // A pod that left and came back is the same pod, not one on its way out.
    delete element.dataset.leaving;
    if (element === cursor) {
      cursor = cursor.nextElementSibling;
    } else {
      host.insertBefore(element, cursor);
    }
  }

  // Whatever the cursor did not reach is a pod the newest Report no longer
  // carries, which is a pod that has gone. It is faded out rather than taken
  // away, because a pod vanishing between two frames is the one change a
  // reader cannot see happen.
  while (cursor) {
    const after = cursor.nextElementSibling;
    if (!wanted.has(cursor.dataset.pod)) {
      leave(cursor, pods);
    }
    cursor = after;
  }
}

function leave(element, pods) {
  if (element.dataset.leaving === "true") {
    return;
  }
  element.dataset.leaving = "true";
  const name = element.dataset.pod;
  setTimeout(() => {
    // A pod that came back while it was fading is not the one that left.
    if (element.dataset.leaving === "true") {
      element.remove();
      if (pods.get(name) === element) {
        pods.delete(name);
      }
    }
  }, reduced() ? 0 : leavingMs);
}

function newPod(pod) {
  const element = document.createElement("div");
  element.className = "pod";
  element.dataset.pod = pod.name;

  const head = document.createElement("button");
  head.type = "button";
  head.className = "pod-head";
  head.id = `pod-${cssId(pod.name)}`;

  const traffic = document.createElement("span");
  traffic.className = "traffic";
  traffic.append(svgElement("svg", { viewBox: "-6 -6 12 12", "aria-hidden": "true" }, [
    svgElement("circle", { r: "4" }),
  ]));
  traffic.append(label("sr", ""));

  head.append(label("pod-name mono", pod.name), traffic);

  const chips = document.createElement("ul");
  chips.className = "chips";

  const schedule = document.createElement("div");
  schedule.className = "schedule";
  schedule.id = `schedule-${cssId(pod.name)}`;
  schedule.hidden = true;

  head.setAttribute("aria-controls", schedule.id);
  element.append(head, chips, schedule);
  return element;
}

function fillPod(element, pod, era, selected) {
  const ready = pod.conditions.Ready ?? "Unknown";
  const template = pod.template === true;
  element.dataset.node = pod.node;
  element.dataset.ready = ready;
  element.dataset.era = era;
  element.dataset.template = String(template);

  const inService = ready === "True";
  const traffic = element.querySelector(".traffic");
  traffic.dataset.inService = String(inService);
  traffic.title = inService ? TRAFFIC_NOTE : `not ${TRAFFIC_NOTE}`;
  traffic.querySelector(".sr").textContent = inService ? "in service" : "not in service";

  const head = element.querySelector(".pod-head");
  head.title = conditionTitle(pod);

  // A workload with no pods has nothing but its schedule to show, so there is
  // nothing to open and it is open.
  const open = template || pod.name === selected;
  head.hidden = template;
  head.setAttribute("aria-expanded", String(open));
  element.dataset.selected = String(open);

  drawChips(element.querySelector(".chips"), pod);

  const schedule = element.querySelector(".schedule");
  schedule.hidden = !open;
  if (open) {
    drawSchedule(schedule, pod);
  }
}

// conditionTitle is the pod's two conditions in the CLI's words, which is what
// the border and the traffic dot are drawn from.
function conditionTitle(pod) {
  return Object.entries(pod.conditions)
    .map(([type, status]) => `${type}: ${status}`)
    .join(", ");
}

function drawChips(host, pod) {
  const drawn = new Map();
  for (const chip of host.children) {
    drawn.set(chip.dataset.container, chip);
  }

  let cursor = host.firstElementChild;
  for (const container of pod.containers) {
    const chip = drawn.get(container.name) ?? newChip(container);
    fillChip(chip, container);
    if (chip === cursor) {
      cursor = cursor.nextElementSibling;
    } else {
      host.insertBefore(chip, cursor);
    }
  }
  while (cursor) {
    const after = cursor.nextElementSibling;
    cursor.remove();
    cursor = after;
  }
}

function newChip(container) {
  const chip = document.createElement("li");
  chip.className = "chip";
  chip.dataset.container = container.name;
  chip.append(
    glyph(),
    label("chip-name mono", container.name + (container.sidecar ? " (sidecar)" : "")),
    label("restarts", ""),
    label("sr", ""),
  );
  return chip;
}

// glyph is the chip's state as a shape: a check, a cross, or neither. All
// three live in the one SVG and the stylesheet shows the one the state names,
// so a chip changing state is a swap rather than a redraw.
function glyph() {
  return svgElement("svg", { viewBox: "-8 -8 16 16", class: "glyph", "aria-hidden": "true" }, [
    svgElement("path", { class: "tick", d: "M-4 0 L-1 3 L4.5 -3.5" }),
    svgElement("path", { class: "cross", d: "M-3.5 -3.5 L3.5 3.5 M3.5 -3.5 L-3.5 3.5" }),
  ]);
}

function fillChip(chip, container) {
  chip.dataset.state = container.state;
  chip.title = `${container.name}: ${chipWords[container.state]}`;

  const restarts = chip.querySelector(".restarts");
  restarts.textContent = container.restarts > 0 ? String(container.restarts) : "";
  restarts.title = container.restarts > 0 ? `${container.restarts} restarts` : "";

  chip.querySelector(".sr").textContent = chipWords[container.state];
}

// drawSchedule is the probes a container is configured with, drawn as the
// static thing they are. A bar is as long as the probe takes to act and
// carries a tick for every check in that time; it never moves, and the
// heading says it comes from configuration rather than from anything the
// cluster observed. See ADR-0005.
function drawSchedule(host, pod) {
  host.replaceChildren();
  const title = document.createElement("p");
  title.className = "schedule-title";
  title.textContent = SCHEDULE_NOTE;
  host.append(title);

  let drawn = false;
  for (const container of pod.containers) {
    if (container.probes.length === 0) {
      continue;
    }
    drawn = true;
    const longest = Math.max(...container.probes.map((probe) => spanMs(probe)));
    const group = document.createElement("div");
    group.className = "schedule-container";
    group.append(label("schedule-name mono", container.name));
    for (const probe of container.probes) {
      group.append(bar(probe, longest));
    }
    host.append(group);
  }
  if (!drawn) {
    host.append(label("schedule-empty", "no probes are configured"));
  }
}

// spanMs is how long a bar stands for: for a startup probe the budget the
// container has to come up in, and for the other two the worst case before
// the probe acts. It is the duration the Overview's cell shows, for the same
// reason: it is the number each probe is read for.
function spanMs(probe) {
  const timing = probe.detail.timing ?? {};
  if (probe.probe === "startup" && timing.startupBudget) {
    return durationMs(timing.startupBudget);
  }
  return durationMs(timing.failureDetection);
}

function bar(probe, longest) {
  const span = spanMs(probe);
  const period = effective(probe.detail.config?.periodSeconds, DEFAULT_PERIOD_SECONDS) * 1000;
  const ticks = period > 0 ? Math.floor(span / period) : 0;

  const row = document.createElement("div");
  row.className = "bar";
  row.dataset.probe = probe.probe;

  const heading = `${probe.probe} · ${probe.handler}`;
  row.append(label("bar-label mono", ticks > mostTicks ? `${heading} · every ${period / 1000}s` : heading));

  // The track is measured in seconds and stretched to whatever width the
  // window gives it, so two probes of the same container stay in proportion
  // however narrow the column is.
  const seconds = Math.max(span / 1000, 1);
  const track = svgElement("svg", {
    class: "bar-track",
    viewBox: `0 0 ${seconds} 10`,
    preserveAspectRatio: "none",
    "aria-hidden": "true",
  });
  // The bar is as wide a share of the column as the probe is long against
  // the longest of its container's, set through the CSSOM: the policy this
  // page is served under forbids a style attribute, and a Report carries text
  // the cluster wrote. See ADR-0006.
  const width = longest > 0 ? (span / longest) * 100 : 100;
  track.style.setProperty("--bar-width", `${width.toFixed(2)}%`);
  track.append(svgElement("rect", { x: "0", y: "3", width: String(seconds), height: "4", rx: "1" }));
  if (ticks <= mostTicks) {
    for (let i = 1; i <= ticks; i++) {
      track.append(
        svgElement("line", {
          x1: String((i * period) / 1000),
          x2: String((i * period) / 1000),
          y1: "1",
          y2: "9",
          "vector-effect": "non-scaling-stroke",
        }),
      );
    }
  }
  row.append(track);

  for (const sentence of timingSentences(probe.probe, probe.detail)) {
    row.append(label("bar-sentence", sentence));
  }
  return row;
}

function chipFor(pods, pod, container) {
  return pods.get(pod)?.querySelector(`.chip[data-container="${cssEscape(container)}"]`) ?? null;
}

// The timer holding each chip's flash up, so a chip that fails twice inside
// one flash gets the whole of a second one rather than the tail of the first.
const flashes = new WeakMap();

function flash(chip, kind) {
  if (chip === null) {
    return;
  }
  clearTimeout(flashes.get(chip));
  chip.removeAttribute("data-flash");
  // Reading the layout back is what restarts an animation already running.
  void chip.offsetWidth;
  chip.dataset.flash = kind;
  flashes.set(
    chip,
    setTimeout(() => chip.removeAttribute("data-flash"), flashMs),
  );
}

// pulse is the one thing on screen that travels: an Unhealthy event leaving
// the kubelet that reported it and arriving at the container it was about.
// Somebody who has asked for less movement gets none of it; the Timeline
// marker lights up for them instead.
//
// The overlay is its own coordinate system, laid over the drawing and taking
// no clicks, so a pulse can cross a pod without being part of one. Measuring
// against the overlay's own box is what keeps the two in step whatever the
// layout does with the drawing.
function pulse(host, from, to, probe) {
  if (host === null || from === null || to === null || reduced()) {
    return;
  }
  const frame = host.getBoundingClientRect();
  const start = centre(from, frame);
  const end = centre(to, frame);

  host.setAttribute("width", String(frame.width));
  host.setAttribute("height", String(frame.height));
  host.setAttribute("viewBox", `0 0 ${frame.width} ${frame.height}`);

  const dot = svgElement("circle", { class: "pulse", r: "5", cx: "0", cy: "0" });
  dot.dataset.probe = probe;
  host.append(dot);

  const travel = dot.animate(
    [
      { transform: `translate(${start.x}px, ${start.y}px)`, opacity: 0.2 },
      { transform: `translate(${end.x}px, ${end.y}px)`, opacity: 1 },
    ],
    { duration: pulseMs, easing: "ease-out" },
  );
  const done = () => dot.remove();
  travel.onfinish = done;
  travel.oncancel = done;
  // A tab hidden mid-flight never finishes the animation, and a dot left on
  // the drawing would be a failure that never arrived.
  setTimeout(done, pulseMs * 4);
}

function centre(element, frame) {
  const box = element.getBoundingClientRect();
  return { x: box.left - frame.left + box.width / 2, y: box.top - frame.top + box.height / 2 };
}

function label(className, text) {
  const span = document.createElement("span");
  span.className = className;
  span.textContent = text;
  return span;
}

function svgElement(name, attributes, children = []) {
  const element = document.createElementNS("http://www.w3.org/2000/svg", name);
  for (const [key, value] of Object.entries(attributes)) {
    element.setAttribute(key, value);
  }
  element.append(...children);
  return element;
}

// cssId turns a name into something an id can be. Pod and container names are
// DNS labels, so this changes nothing in practice; it is here so that a name
// from a cluster can never be read as a selector.
function cssId(name) {
  return name.replace(/[^a-zA-Z0-9_-]/g, "_");
}

function cssEscape(value) {
  return window.CSS?.escape ? CSS.escape(value) : value.replace(/["\\]/g, "\\$&");
}
