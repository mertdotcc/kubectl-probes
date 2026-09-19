// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The Inspection's sections, in the order someone debugging a probe by hand
// works through them: what is configured, what that configuration means, what
// the workload asked for instead, what is running, what has failed, and last
// the opinions.
const (
	headingConfiguration   = "Configuration"
	headingEffectiveTiming = "Effective timing"
	headingDrift           = "Drift"
	headingRuntimeState    = "Runtime state"
	headingFailureEvidence = "Failure evidence"
	headingFindings        = "Findings"
)

// What a section says where it has nothing to show. An empty section still
// prints its heading: the reader asked about probes, and "no readiness probe"
// is an answer where a missing section is a gap they have to notice.
const (
	noProbesSaid  = "no probes are configured"
	noPodsSaid    = "no pods are running this container"
	noEventsSaid  = "no Unhealthy events are recorded"
	unknownEvents = "unknown: listing events was forbidden"

	// initContainersSaid names the plain init containers, which have no probes
	// and can have none, so that a reader hunting for one does not read its
	// absence as something this tool missed.
	initContainersSaid = "init containers (no probes possible): "

	// noTemplateNote goes to errOut, because it is about what could not be
	// read rather than about the workload.
	noTemplateNote = "drift is unknown: the workload template could not be read"
)

// The note beside a probe field the spec never wrote.
const (
	defaultSaid = "(default)"
	// A probe that does not set its own terminationGracePeriodSeconds is
	// killed on the pod's, which is not this tool's to report.
	podGraceSaid = "(the pod's own)"
)

// How far each part of the Inspection is indented. A section is under its
// container, its body is under its heading, and a probe's fields are under the
// probe.
const (
	sectionIndent = "  "
	bodyIndent    = "    "
	detailIndent  = "      "
)

// Inspection writes the detailed view of a single workload: full probe
// configuration, what its timing means in sentences, drift against the
// workload template, per-pod runtime state, failure evidence, and last the
// findings.
//
// It takes the whole Report rather than a Workload because the ages it prints
// are measured against the Report's own GeneratedAt, so the same Report reads
// the same whenever it is rendered. The caller has already narrowed the Report
// to the workload the positional argument resolved to; a Report carrying more
// than one is rendered in full, one workload after another.
//
// There is no option for --no-findings. The flag is applied where the findings
// are produced, so a Report made under it carries none and the section it
// would fill is simply not there.
func Inspection(out, errOut io.Writer, report *model.Report) error {
	if report == nil || len(report.Workloads) == 0 {
		_, err := fmt.Fprintln(errOut, emptyNote)
		return err
	}

	// The whole view is built before any of it is written, so a workload is
	// never half-printed by a write that fails partway down it.
	var buf bytes.Buffer
	for i, workload := range report.Workloads {
		if i > 0 {
			fmt.Fprintln(&buf)
		}
		if err := inspectWorkload(&buf, workload, report.GeneratedAt); err != nil {
			return err
		}
	}
	if _, err := out.Write(buf.Bytes()); err != nil {
		return err
	}
	return inspectionNotes(errOut, report.Workloads)
}

func inspectWorkload(w io.Writer, workload model.Workload, now time.Time) error {
	fmt.Fprintln(w, workloadLine(workload))
	fmt.Fprintln(w, podLine(workload))

	for _, container := range workload.Containers {
		fmt.Fprintln(w)
		fmt.Fprintln(w, containerLine(container))
		if err := inspectContainer(w, container, now); err != nil {
			return err
		}
	}

	if len(workload.InitContainers) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, initContainersSaid+strings.Join(workload.InitContainers, ", "))
	}
	return nil
}

// workloadLine names the workload the way the argument that asked for it does,
// so the header reads back as the command that produced it.
func workloadLine(workload model.Workload) string {
	if workload.Namespace == "" {
		return workload.DisplayName
	}
	return workload.DisplayName + " -n " + workload.Namespace
}

// podLine counts the pods the report is drawn from, and the ones deliberately
// left out of it. A pod on its way out has history rather than state, and
// saying so here is what keeps a finished rollout from looking like a problem.
func podLine(workload model.Workload) string {
	line := plural(workload.PodCount, "pod")
	if workload.PodCount == 0 {
		line = "no pods"
	}
	if workload.TerminatingPodCount > 0 {
		line += fmt.Sprintf(", %d terminating", workload.TerminatingPodCount)
	}
	if !workload.TemplateAvailable {
		line += ", no template"
	}
	return line
}

func containerLine(container model.Container) string {
	if container.Sidecar {
		return "container " + container.Name + " (sidecar)"
	}
	return "container " + container.Name
}

func inspectContainer(w io.Writer, container model.Container, now time.Time) error {
	sections := []func(io.Writer, model.Container, time.Time) error{
		configurationSection,
		effectiveTimingSection,
		driftSection,
		runtimeStateSection,
		failureEvidenceSection,
		findingsSection,
	}
	for _, section := range sections {
		if err := section(w, container, now); err != nil {
			return err
		}
	}
	return nil
}

// configurationSection is every raw field of every probe, with the values the
// spec never wrote shown at the kubelet's default and marked as defaults: the
// kubelet behaves the same either way, and a reader comparing two containers
// needs to see which numbers somebody chose.
//
// The probe headings are raw lines, so the field rows of all three probes share
// one set of columns and line up with each other however long a handler is.
func configurationSection(w io.Writer, container model.Container, _ time.Time) error {
	heading(w, headingConfiguration)

	t := &table{}
	configured := false
	for _, probe := range model.ProbeTypes {
		detail := container.Probe(probe)
		if detail == nil || detail.Config == nil {
			t.addRaw(probeHeading(probe, tinted(absent, gray).print()))
			continue
		}
		configured = true
		t.addRaw(probeHeading(probe, handlerLine(detail.Config.Handler)))
		for _, field := range configFields(detail.Config) {
			t.add(plain(detailIndent+field.name), field.value, field.note)
		}
	}
	if !configured {
		sayNothing(w, noProbesSaid)
		return nil
	}
	return t.write(w)
}

// probeHeading is a probe's name and how it asks, padded so the three headings
// line up with each other.
func probeHeading(probe model.ProbeType, handler string) string {
	return fmt.Sprintf("%s%-*s  %s", bodyIndent, probeNameWidth, probe, handler)
}

var probeNameWidth = widestProbeName()

func widestProbeName() int {
	widest := 0
	for _, probe := range model.ProbeTypes {
		widest = max(widest, len(probe))
	}
	return widest
}

// configField is one row of the Configuration section: the field as the spec
// spells it, the value the kubelet uses, and where that value came from.
type configField struct {
	name  string
	value cell
	note  cell
}

func configFields(probe *model.Probe) []configField {
	fields := []configField{
		intField("initialDelaySeconds", probe.InitialDelaySeconds, model.DefaultInitialDelaySeconds),
		intField("periodSeconds", probe.PeriodSeconds, model.DefaultPeriodSeconds),
		intField("timeoutSeconds", probe.TimeoutSeconds, model.DefaultTimeoutSeconds),
		intField("successThreshold", probe.SuccessThreshold, model.DefaultSuccessThreshold),
		intField("failureThreshold", probe.FailureThreshold, model.DefaultFailureThreshold),
	}

	grace := configField{
		name:  "terminationGracePeriodSeconds",
		value: tinted(absent, gray),
		note:  tinted(podGraceSaid, gray),
	}
	if probe.TerminationGracePeriodSeconds != nil {
		grace.value = plain(strconv.FormatInt(*probe.TerminationGracePeriodSeconds, 10))
		grace.note = plain("")
	}
	fields = append(fields, grace)

	// The handler is already on the probe's own heading, all but its headers,
	// which are too long to belong there and too much to leave out.
	if headers := probe.Handler.HTTPHeaders; len(headers) > 0 {
		fields = append(fields, configField{name: "httpHeaders", value: plain(headerList(headers)), note: plain("")})
	}
	return fields
}

func intField(name string, written *int32, fallback int32) configField {
	value, isDefault := model.Effective(written, fallback)
	field := configField{
		name:  name,
		value: plain(strconv.FormatInt(int64(value), 10)),
		note:  plain(""),
	}
	if isDefault {
		field.note = tinted(defaultSaid, gray)
	}
	return field
}

// effectiveTimingSection is what the numbers above mean, in sentences. The
// sentences of one probe go on one line, so the three probes stay comparable
// at a glance.
func effectiveTimingSection(w io.Writer, container model.Container, _ time.Time) error {
	heading(w, headingEffectiveTiming)

	said := false
	for _, probe := range model.ProbeTypes {
		sentences := timingSentences(probe, container.Probe(probe))
		if len(sentences) == 0 {
			continue
		}
		said = true
		fmt.Fprintln(w, bodyIndent+strings.Join(sentences, " "))
	}
	if !said {
		sayNothing(w, noProbesSaid)
	}
	return nil
}

// driftSection is what the running pod and the workload template disagree
// about, field by field, side by side. It is left out entirely where nothing
// has drifted, which is the ordinary case and not worth a heading.
//
// The values are effective ones, defaults filled in, because that is what drift
// was decided on: a template that writes periodSeconds: 10 and a pod that leaves
// it unset are not in disagreement.
func driftSection(w io.Writer, container model.Container, _ time.Time) error {
	t := &table{}
	drifted := false
	for _, probe := range model.ProbeTypes {
		detail := container.Probe(probe)
		if detail == nil || !detail.Drifted {
			continue
		}
		if !drifted {
			drifted = true
			t.add(plain(bodyIndent+"PROBE"), plain("FIELD"), plain("RUNNING"), plain("TEMPLATE"))
		}
		for _, row := range driftRows(detail) {
			t.add(plain(bodyIndent+string(probe)), plain(row.name), row.running, row.template)
		}
	}
	if !drifted {
		return nil
	}
	heading(w, headingDrift)
	return t.write(w)
}

// driftRow is one field the two sides disagree about.
type driftRow struct {
	name     string
	running  cell
	template cell
}

func driftRows(detail *model.ProbeDetail) []driftRow {
	// A probe configured on one side only is drift about the probe itself,
	// and comparing fields it does not have would say the same thing six
	// times over.
	switch {
	case detail.Config == nil:
		return []driftRow{{name: "probe", running: tinted(absent, gray), template: plain(handlerLine(detail.Template.Handler))}}
	case detail.Template == nil:
		return []driftRow{{name: "probe", running: plain(handlerLine(detail.Config.Handler)), template: tinted(absent, gray)}}
	}

	running, template := detail.Config, detail.Template
	rows := differing(driftRow{
		name:     "handler",
		running:  plain(handlerLine(running.Handler)),
		template: plain(handlerLine(template.Handler)),
	})
	for _, field := range []struct {
		name              string
		running, template *int32
		fallback          int32
	}{
		{"initialDelaySeconds", running.InitialDelaySeconds, template.InitialDelaySeconds, model.DefaultInitialDelaySeconds},
		{"periodSeconds", running.PeriodSeconds, template.PeriodSeconds, model.DefaultPeriodSeconds},
		{"timeoutSeconds", running.TimeoutSeconds, template.TimeoutSeconds, model.DefaultTimeoutSeconds},
		{"successThreshold", running.SuccessThreshold, template.SuccessThreshold, model.DefaultSuccessThreshold},
		{"failureThreshold", running.FailureThreshold, template.FailureThreshold, model.DefaultFailureThreshold},
	} {
		left, _ := model.Effective(field.running, field.fallback)
		right, _ := model.Effective(field.template, field.fallback)
		rows = append(rows, differing(driftRow{
			name:     field.name,
			running:  plain(strconv.FormatInt(int64(left), 10)),
			template: plain(strconv.FormatInt(int64(right), 10)),
		})...)
	}
	rows = append(rows, differing(driftRow{
		name:     "terminationGracePeriodSeconds",
		running:  graceCell(running.TerminationGracePeriodSeconds),
		template: graceCell(template.TerminationGracePeriodSeconds),
	})...)

	if len(rows) == 0 {
		// Drift was decided on the whole effective probe, so a difference this
		// list does not cover is still a difference. Saying the probe drifted
		// without saying where is better than printing an empty section.
		return []driftRow{{
			name:     "probe",
			running:  plain(handlerLine(running.Handler)),
			template: plain(handlerLine(template.Handler)),
		}}
	}
	return rows
}

// differing keeps a row only where the two sides actually say something
// different, because a drift section listing the fields that agree buries the
// one that does not.
func differing(row driftRow) []driftRow {
	if row.running.text == row.template.text {
		return nil
	}
	return []driftRow{row}
}

func graceCell(seconds *int64) cell {
	if seconds == nil {
		return tinted(absent, gray)
	}
	return plain(strconv.FormatInt(*seconds, 10))
}

// runtimeStateSection is what the kubelet reports about the container in each
// pod, one row per pod, and what that did to the pod's own conditions.
func runtimeStateSection(w io.Writer, container model.Container, now time.Time) error {
	heading(w, headingRuntimeState)
	if len(container.Pods) == 0 {
		sayNothing(w, noPodsSaid)
		return nil
	}

	t := &table{}
	t.add(plain(bodyIndent+"POD"), plain("READY"), plain("STARTED"), plain("RESTARTS"),
		plain("LAST TERMINATION"), plain("POD-READY"), plain("CONTAINERS-READY"))
	for _, pod := range container.Pods {
		t.add(
			plain(bodyIndent+pod.Name),
			boolCell(&pod.Ready),
			boolCell(pod.Started),
			restartsCell(pod.Restarts),
			terminationCell(pod.LastTermination, now),
			conditionCell(pod, model.ConditionReady, now),
			conditionCell(pod, model.ConditionContainersReady, now),
		)
	}
	return t.write(w)
}

// boolCell prints what the kubelet says, with false in red: an unready or
// unstarted container is the fact the reader came for. A kubelet that does not
// report started at all leaves a gray dash rather than a guess.
func boolCell(value *bool) cell {
	if value == nil {
		return tinted(absent, gray)
	}
	if !*value {
		return tinted("false", red)
	}
	return plain("true")
}

func restartsCell(restarts int32) cell {
	if restarts == 0 {
		return plain("0")
	}
	return tinted(strconv.FormatInt(int64(restarts), 10), red)
}

// terminationCell is why the container last died: the reason, the exit code,
// the signal where there was one, and how long ago.
func terminationCell(termination *model.Termination, now time.Time) cell {
	if termination == nil {
		return tinted(absent, gray)
	}
	parts := []string{}
	if termination.Reason != "" {
		parts = append(parts, termination.Reason)
	}
	parts = append(parts, "exit "+strconv.FormatInt(int64(termination.ExitCode), 10))
	if termination.Signal != 0 {
		parts = append(parts, "signal "+strconv.FormatInt(int64(termination.Signal), 10))
	}
	if age := age(now, termination.FinishedAt); age != "" {
		parts = append(parts, age)
	}
	return tinted(strings.Join(parts, ", "), red)
}

// conditionCell is one pod condition and how long it has held: a pod that went
// unready an hour ago and one that went unready a moment ago are different
// problems.
func conditionCell(pod model.Pod, want string, now time.Time) cell {
	for _, condition := range pod.Conditions {
		if condition.Type != want {
			continue
		}
		text := string(condition.Status)
		if age := age(now, condition.LastTransitionTime); age != "" {
			text += " (" + age + ")"
		}
		if condition.Status == model.ConditionTrue {
			return plain(text)
		}
		return tinted(text, red)
	}
	return tinted(absent, gray)
}

// failureEvidenceSection is the Unhealthy events the cluster reports, grouped
// by pod and probe: how often, over what span, and what the probe last said.
//
// The message hangs under the row that counted it as a raw line, so a message
// of any length costs the table no width at all.
func failureEvidenceSection(w io.Writer, container model.Container, now time.Time) error {
	heading(w, headingFailureEvidence)
	switch {
	case container.Runtime == nil:
		sayNothing(w, noPodsSaid)
		return nil
	case container.Runtime.Failures == nil:
		sayNothing(w, unknownEvents)
		return nil
	}

	t := &table{}
	reported := false
	for _, pod := range container.Pods {
		for _, group := range pod.Events {
			if !reported {
				reported = true
				t.add(plain(bodyIndent+"POD"), plain("PROBE"), plain("FAILURES"),
					plain("FIRST SEEN"), plain("LAST SEEN"))
			}
			t.add(
				plain(bodyIndent+pod.Name),
				plain(string(group.Probe)),
				tinted(strconv.FormatInt(int64(group.Count), 10), red),
				seenCell(now, group.FirstSeen),
				seenCell(now, group.LastSeen),
			)
			if group.Message != "" {
				t.addRaw(detailIndent + group.Message)
			}
		}
	}
	if !reported {
		sayNothing(w, noEventsSaid)
		return nil
	}
	return t.write(w)
}

func seenCell(now time.Time, at *time.Time) cell {
	if age := age(now, at); age != "" {
		return plain(age)
	}
	return tinted(absent, gray)
}

// findingsSection is the opinions, last and on their own, so nothing above it
// can be mistaken for one. It is absent for a container no rule had anything
// to say about, and for every container under --no-findings.
func findingsSection(w io.Writer, container model.Container, _ time.Time) error {
	if len(container.Findings) == 0 {
		return nil
	}
	heading(w, headingFindings)

	t := &table{}
	for _, finding := range container.Findings {
		t.add(tinted(bodyIndent+finding.Rule, yellow), plain(finding.Message))
	}
	return t.write(w)
}

// inspectionNotes says what the view could not, once, on errOut.
func inspectionNotes(errOut io.Writer, workloads []model.Workload) error {
	for _, workload := range workloads {
		if workload.TemplateAvailable {
			continue
		}
		if _, err := fmt.Fprintln(errOut, noTemplateNote); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func heading(w io.Writer, text string) {
	fmt.Fprintln(w, sectionIndent+text)
}

// sayNothing is how a section reports having nothing to report, in gray,
// because it is the absence of a fact rather than a fact.
func sayNothing(w io.Writer, text string) {
	fmt.Fprintln(w, bodyIndent+tinted(text, gray).print())
}

// handlerLine is how a probe asks, on one line. A probe with no handler at all
// comes from a hand-written manifest the API server never saw, and is reported
// as unknown rather than left blank.
func handlerLine(handler model.Handler) string {
	if handler.Summary == "" {
		return handlerType(handler)
	}
	return handler.Summary
}

func headerList(headers []model.HTTPHeader) string {
	parts := make([]string, 0, len(headers))
	for _, header := range headers {
		parts = append(parts, header.Name+": "+header.Value)
	}
	return strings.Join(parts, ", ")
}

// age is how long ago something happened, measured against the Report's own
// GeneratedAt so that rendering the same Report twice reads the same both
// times. A timestamp the cluster never set has no age.
func age(now time.Time, at *time.Time) string {
	if at == nil {
		return ""
	}
	return duration.HumanDuration(now.Sub(*at)) + " ago"
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}
