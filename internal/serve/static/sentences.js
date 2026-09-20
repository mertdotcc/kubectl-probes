// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The CLI's wording, in the browser.
//
// internal/render decides what every sentence the Inspection shows says, and
// this file says the same things the same way, so that a reader with the
// terminal open beside the browser is reading one tool rather than two. Every
// constant here is the constant in internal/render/inspect.go or
// internal/render/sentences.go, and the durations are formatted the way
// k8s.io/apimachinery/pkg/util/duration formats them, because that is what the
// terminal prints. Nothing here is new prose.

// The section headings the Inspection borrows. The drawing takes the place of
// the Configuration and Runtime state sections, so those are not here.
export const HEADING_FAILURE_EVIDENCE = "Failure evidence";
export const HEADING_FINDINGS = "Findings";

// What a section says where it has nothing to show. An empty section still
// says so: the reader asked about probes, and "no Unhealthy events are
// recorded" is an answer where a missing section is a gap they have to notice.
export const NO_PROBES_SAID = "no probes are configured";
export const NO_PODS_SAID = "no pods are running this container";
export const NO_EVENTS_SAID = "no Unhealthy events are recorded";
export const UNKNOWN_EVENTS = "unknown: listing events was forbidden";
export const NO_TEMPLATE_NOTE = "drift is unknown: the workload template could not be read";

// The columns of the Failure evidence table, in the CLI's order.
export const EVIDENCE_COLUMNS = ["POD", "PROBE", "FAILURES", "FIRST SEEN", "LAST SEEN"];

// The wording of the effective-timing sentences. These are the formats in
// internal/render/sentences.go with the verbs Go's Sprintf would have filled
// in written out.
const sentenceStartupBudget = (name, budget) =>
  `${name} gives the container ${budget} to come up before the kubelet restarts it.`;
const sentenceFailureDetection = (name, threshold, worst) =>
  `${name} acts after ${threshold} consecutive failures, at worst ${worst} after the container stops responding.`;
const sentenceAfterStartup = (name, origin) => `${name} begins after ${origin}.`;
const sentenceFirstCheck = (name, wait) => `${name} waits ${wait} before its first check.`;
const sentenceTrafficDelay = (delay, origin) => `Traffic can first arrive ${delay} after ${origin}.`;

const originContainerStart = "the container starts";
const originStartupSuccess = "the startup probe succeeds";

// The kubelet's defaults for the fields a spec can leave unset, which are
// model.Default* in internal/model/defaults.go.
export const DEFAULT_PERIOD_SECONDS = 10;
export const DEFAULT_FAILURE_THRESHOLD = 3;

// effective is model.Effective: a probe field's value as the kubelet uses it.
export function effective(written, fallback) {
  return written === null || written === undefined ? fallback : written;
}

// timingSentences is what one probe's effective timing means, as the sentences
// the Inspection prints. The headline comes first because it is the number the
// reader came for, and everything qualifying it follows.
export function timingSentences(probe, detail) {
  const timing = detail?.timing;
  if (!timing) {
    return [];
  }
  const name = titled(probe);
  const origin = timing.afterStartup ? originStartupSuccess : originContainerStart;

  const out = [];
  if (probe === "startup" && timing.startupBudget) {
    out.push(sentenceStartupBudget(name, short(timing.startupBudget)));
  } else {
    const threshold = effective(detail.config?.failureThreshold, DEFAULT_FAILURE_THRESHOLD);
    out.push(sentenceFailureDetection(name, threshold, short(timing.failureDetection)));
  }
  if (timing.afterStartup) {
    out.push(sentenceAfterStartup(name, originStartupSuccess));
  }
  if (durationMs(timing.firstCheck) > 0) {
    out.push(sentenceFirstCheck(name, short(timing.firstCheck)));
  }
  if (timing.trafficDelay) {
    out.push(sentenceTrafficDelay(short(timing.trafficDelay), origin));
  }
  return out;
}

// titled opens a sentence with a probe's name. The names are ASCII by
// construction, so the first character is the first letter.
function titled(s) {
  return s === "" ? s : s[0].toUpperCase() + s.slice(1);
}

// short is a duration as a surface prints it: the wording the Report carries
// with the trailing zero units trimmed, so a two minute budget reads 2m rather
// than 2m0s. It is render.short, and the Overview uses it too.
export function short(duration) {
  if (!duration) {
    return "";
  }
  if (duration.endsWith("h0m0s")) {
    return duration.slice(0, -"0m0s".length);
  }
  if (duration.endsWith("m0s")) {
    return duration.slice(0, -"0s".length);
  }
  return duration;
}

// The units a Go duration string can be written in. Probe timing is whole
// seconds, so only the last three ever turn up, but a Duration is a Duration
// and parsing one wrongly would be a silent wrong answer.
const unitMs = {
  ns: 1e-6,
  us: 1e-3,
  "µs": 1e-3,
  ms: 1,
  s: 1000,
  m: 60000,
  h: 3600000,
};

// The longer units come first so that "ms" is never read as "m" and a stray
// "s". Go writes "1m30s" and "500ms" with the same alphabet.
const durationPattern = /(\d+(?:\.\d+)?)(ns|us|µs|ms|h|m|s)/g;

// durationMs is a Go duration string as a number of milliseconds, which is
// what a bar has to be as long as. A string that parses to nothing is zero:
// the only thing on the other side of it is a bar of no width.
export function durationMs(duration) {
  if (!duration) {
    return 0;
  }
  let total = 0;
  for (const [, amount, unit] of duration.matchAll(durationPattern)) {
    total += Number(amount) * unitMs[unit];
  }
  return duration.startsWith("-") ? -total : total;
}

// humanDuration is duration.HumanDuration from k8s.io/apimachinery, which is
// what the CLI measures every age with. It is ported rather than approximated
// because "2m" and "2m3s" are the difference between the browser and the
// terminal agreeing about a timestamp and not.
export function humanDuration(ms) {
  const seconds = Math.trunc(ms / 1000);
  if (seconds < -1) {
    return "<invalid>";
  }
  if (seconds < 0) {
    return "0s";
  }
  if (seconds < 60 * 2) {
    return `${seconds}s`;
  }
  const minutes = Math.trunc(ms / 60000);
  if (minutes < 10) {
    const s = seconds % 60;
    return s === 0 ? `${minutes}m` : `${minutes}m${s}s`;
  }
  if (minutes < 60 * 3) {
    return `${minutes}m`;
  }
  const hours = Math.trunc(ms / 3600000);
  if (hours < 8) {
    const m = minutes % 60;
    return m === 0 ? `${hours}h` : `${hours}h${m}m`;
  }
  if (hours < 48) {
    return `${hours}h`;
  }
  const days = Math.trunc(hours / 24);
  if (hours < 24 * 8) {
    const h = hours % 24;
    return h === 0 ? `${days}d` : `${days}d${h}h`;
  }
  if (hours < 24 * 365 * 2) {
    return `${days}d`;
  }
  const years = Math.trunc(days / 365);
  if (hours < 24 * 365 * 8) {
    const d = days % 365;
    return d === 0 ? `${years}y` : `${years}y${d}d`;
  }
  return `${years}y`;
}

// age is how long ago something happened, measured against the Report's own
// generatedAt so that drawing the same Report twice reads the same both times.
// A timestamp the cluster never set has no age.
export function age(now, at) {
  const when = timeOf(at);
  return when === null ? "" : `${humanDuration(now - when)} ago`;
}

// timeOf is a Report timestamp as a number of milliseconds, or null where the
// cluster never set one. An unparseable timestamp is treated as unset: a
// timeline cannot place it, and a wrong place is worse than no place.
export function timeOf(at) {
  if (!at) {
    return null;
  }
  const ms = Date.parse(at);
  return Number.isNaN(ms) ? null : ms;
}

// plural is render.plural: a count and its noun, agreeing.
export function plural(count, noun) {
  return count === 1 ? `1 ${noun}` : `${count} ${noun}s`;
}

// podLine counts the pods the Report is drawn from and the ones deliberately
// left out of it. A pod on its way out has history rather than state, and
// saying so is what keeps a finished rollout from looking like a problem.
export function podLine(workload) {
  const count = workload?.podCount ?? 0;
  let line = count === 0 ? "no pods" : plural(count, "pod");
  const terminating = workload?.terminatingPodCount ?? 0;
  if (terminating > 0) {
    line += `, ${terminating} terminating`;
  }
  if (workload && workload.templateAvailable !== true) {
    line += ", no template";
  }
  return line;
}

// handlerLine is how a probe asks, on one line. A probe with no handler at all
// comes from a hand-written manifest the API server never saw, and is reported
// as unknown rather than left blank.
export function handlerLine(handler) {
  if (handler?.summary) {
    return handler.summary;
  }
  return handler?.type ? handler.type : "?";
}
