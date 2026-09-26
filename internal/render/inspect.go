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

// headersSaid is the line under the Configuration table naming the headers a
// probe sends.
const headersSaid = "%s sends %s"

// The names the Configuration table's columns and the Drift section's rows go
// by. They are kubectl describe's own words for the probe's fields, short
// enough that a column of two-character values is not headed by a word ten
// times as wide.
const (
	fieldProbe   = "probe"
	fieldDelay   = "delay"
	fieldPeriod  = "period"
	fieldTimeout = "timeout"
	fieldSuccess = "success"
	fieldFailure = "failure"
	fieldGrace   = "grace"
	fieldHandler = "handler"

	// actsAfterColumn is the one column that is not a field of the probe
	// but what its fields add up to.
	actsAfterColumn = "ACTS AFTER"
)

// How far each part of the Inspection is indented. A section is under its
// container, its body is under its heading, and a failure message hangs under
// the row that counted it.
const (
	sectionIndent = "  "
	bodyIndent    = "    "
	detailIndent  = "      "
)

// Inspection writes the detailed view of a single workload: full probe
// configuration as a table, what its timing means in sentences, drift against
// the workload template, per-pod runtime state, failure evidence, and last the
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

// configurationSection is every probe's configuration as one table, a row per
// probe and a column per setting, so the three probes of a container can be
// compared down a column rather than across three lists (ADR 0010).
//
// A probe nobody configured keeps its row, so the three are always there to
// compare, and the handler is the last column, so a long exec command runs to
// the edge of the terminal without pushing a single number out of line.
func configurationSection(w io.Writer, container model.Container, _ time.Time) error {
	heading(w, headingConfiguration)
	if !anyProbe(container) {
		sayNothing(w, noProbesSaid)
		return nil
	}

	grace := graceColumn(container)
	t := &table{}
	header := configHeader(grace)
	t.add(header...)
	for _, probe := range model.ProbeTypes {
		row := configRow(probe, container.Probe(probe), grace)
		// A row cut short ends every column it has no cell in, as far as
		// tabwriter is concerned, so the rows after it would line up with
		// each other and not with the header. Empty cells keep it in them,
		// and cost nothing once the padding after the last one is trimmed.
		for len(row) < len(header) {
			row = append(row, plain(""))
		}
		t.add(row...)
	}
	// Headers are too long for a cell and too much to leave out, so they
	// hang under the table as raw lines, which take no part in its widths.
	for _, probe := range model.ProbeTypes {
		if detail := container.Probe(probe); detail != nil && detail.Config != nil {
			if headers := detail.Config.Handler.HTTPHeaders; len(headers) > 0 {
				t.addRaw(bodyIndent + fmt.Sprintf(headersSaid, probe, headerList(headers)))
			}
		}
	}
	return t.write(w)
}

// anyProbe says whether the table has anything to show. A probe only the
// template configures counts: it is a row with nothing in it but the mark
// that says the template has it, which is how a rollout adding a container's
// first probe looks while the old pods are still running.
func anyProbe(container model.Container) bool {
	for _, probe := range model.ProbeTypes {
		if detail := container.Probe(probe); detail != nil && (detail.Config != nil || detail.Drifted) {
			return true
		}
	}
	return false
}

// timingField is one of the five numeric fields every probe has: what the
// table calls it, where a Probe keeps it, and what the kubelet uses in its
// place when the spec leaves it out.
type timingField struct {
	name     string
	of       func(*model.Probe) *int32
	fallback int32
	// seconds marks a duration, printed the way the Overview prints one.
	// The two thresholds are counts, and printed bare.
	seconds bool
}

var timingFields = []timingField{
	{name: fieldDelay, of: func(p *model.Probe) *int32 { return p.InitialDelaySeconds }, fallback: model.DefaultInitialDelaySeconds, seconds: true},
	{name: fieldPeriod, of: func(p *model.Probe) *int32 { return p.PeriodSeconds }, fallback: model.DefaultPeriodSeconds, seconds: true},
	{name: fieldTimeout, of: func(p *model.Probe) *int32 { return p.TimeoutSeconds }, fallback: model.DefaultTimeoutSeconds, seconds: true},
	{name: fieldSuccess, of: func(p *model.Probe) *int32 { return p.SuccessThreshold }, fallback: model.DefaultSuccessThreshold},
	{name: fieldFailure, of: func(p *model.Probe) *int32 { return p.FailureThreshold }, fallback: model.DefaultFailureThreshold},
}

// value is the field as the kubelet uses it, and whether that is the kubelet's
// default rather than a value the spec wrote.
func (f timingField) value(probe *model.Probe) (string, bool) {
	value, isDefault := model.Effective(f.of(probe), f.fallback)
	if f.seconds {
		return short(model.Seconds(value)), isDefault
	}
	return strconv.FormatInt(int64(value), 10), isDefault
}

// graceColumn says whether the table has a GRACE column, which is only where
// one of the container's probes sets its own terminationGracePeriodSeconds.
// Few do, and a probe that does not is killed on the pod's grace period, which
// is not this tool's to report, so a column of dashes would say nothing.
//
// A grace period only the template sets is a column too, so that the drift
// mark it earns has a cell to go on.
func graceColumn(container model.Container) bool {
	for _, probe := range model.ProbeTypes {
		detail := container.Probe(probe)
		if detail == nil {
			continue
		}
		if detail.Config != nil && detail.Config.TerminationGracePeriodSeconds != nil {
			return true
		}
		if driftedFields(detail)[fieldGrace] {
			return true
		}
	}
	return false
}

func configHeader(grace bool) []cell {
	cells := []cell{plain(bodyIndent + strings.ToUpper(fieldProbe))}
	for _, field := range timingFields {
		cells = append(cells, plain(strings.ToUpper(field.name)))
	}
	if grace {
		cells = append(cells, plain(strings.ToUpper(fieldGrace)))
	}
	return append(cells, plain(actsAfterColumn), plain(strings.ToUpper(fieldHandler)))
}

// configRow is one probe's row: its fields as the running pod has them, per
// ADR 0001, what they add up to, and how it asks. A probe the pod does not
// configure is its name and a dash.
//
// Every cell the template disagrees about wears the Overview's drift mark. A
// probe on one side only, or a difference no column explains, marks the
// probe's own name.
func configRow(probe model.ProbeType, detail *model.ProbeDetail, grace bool) []cell {
	drifted := driftedFields(detail)
	name := marked(plain(bodyIndent+string(probe)), drifted[fieldProbe])
	if detail == nil || detail.Config == nil {
		return []cell{name, tinted(absent, gray)}
	}

	config := detail.Config
	cells := []cell{name}
	for _, field := range timingFields {
		cells = append(cells, marked(fieldCell(field, config), drifted[field.name]))
	}
	if grace {
		cells = append(cells, marked(graceCell(config.TerminationGracePeriodSeconds), drifted[fieldGrace]))
	}
	return append(cells,
		actsAfterCell(probe, detail.Timing),
		marked(plain(handlerLine(config.Handler)), drifted[fieldHandler]),
	)
}

// driftedFields is the set of names driftRows gives a probe's drift, so the
// table marks exactly what the Drift section lists and the two cannot
// disagree.
func driftedFields(detail *model.ProbeDetail) map[string]bool {
	if detail == nil || !detail.Drifted {
		return nil
	}
	fields := map[string]bool{}
	for _, row := range driftRows(detail) {
		fields[row.name] = true
	}
	return fields
}

// marked is a cell with the red drift mark after it where it drifted, each
// part keeping its own colour, so a default the template disagrees about is
// still gray.
func marked(c cell, drifted bool) cell {
	if !drifted {
		return c
	}
	return join(c, tinted(driftMark, red))
}

// fieldCell is one field's value, in gray where the spec never wrote it: the
// kubelet behaves the same either way, and a reader comparing two containers
// needs to see which numbers somebody chose. Gray costs the column no width,
// which a text marker beside every default would not (ADR 0010).
func fieldCell(field timingField, probe *model.Probe) cell {
	text, isDefault := field.value(probe)
	if isDefault {
		return tinted(text, gray)
	}
	return plain(text)
}

// actsAfterCell is the Overview's headline for the probe: how long a startup
// probe gives the container to come up, and how long the other two take to act
// on a failure. Where the count starts is the Effective timing sentences' to
// say.
func actsAfterCell(probe model.ProbeType, timing *model.Timing) cell {
	if text := headline(probe, timing); text != "" {
		return plain(text)
	}
	return tinted(absent, gray)
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

// driftRow is one field the two sides disagree about, named the way the
// Configuration table heads its column and valued the way that column prints
// it, so a row here reads as a cell above.
type driftRow struct {
	name     string
	running  cell
	template cell
}

// driftRows is the one comparison of a drifted probe's two sides. The Drift
// section prints its rows, and the Configuration table marks the cells they
// name, which is what keeps the two from ever disagreeing.
func driftRows(detail *model.ProbeDetail) []driftRow {
	// A probe configured on one side only is drift about the probe itself,
	// and comparing fields it does not have would say the same thing six
	// times over.
	switch {
	case detail.Config == nil:
		return []driftRow{{name: fieldProbe, running: tinted(absent, gray), template: plain(handlerLine(detail.Template.Handler))}}
	case detail.Template == nil:
		return []driftRow{{name: fieldProbe, running: plain(handlerLine(detail.Config.Handler)), template: tinted(absent, gray)}}
	}

	running, template := detail.Config, detail.Template
	rows := differing(driftRow{
		name:     fieldHandler,
		running:  plain(handlerLine(running.Handler)),
		template: plain(handlerLine(template.Handler)),
	})
	for _, field := range timingFields {
		left, _ := field.value(running)
		right, _ := field.value(template)
		rows = append(rows, differing(driftRow{name: field.name, running: plain(left), template: plain(right)})...)
	}
	rows = append(rows, differing(driftRow{
		name:     fieldGrace,
		running:  graceCell(running.TerminationGracePeriodSeconds),
		template: graceCell(template.TerminationGracePeriodSeconds),
	})...)

	if len(rows) == 0 {
		// Drift was decided on the whole effective probe, so a difference this
		// list does not cover is still a difference. Saying the probe drifted
		// without saying where is better than printing an empty section.
		return []driftRow{{
			name:     fieldProbe,
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

// graceCell is a probe's own grace period, or a gray dash for a probe that
// leaves it to the pod.
func graceCell(seconds *int64) cell {
	if seconds == nil {
		return tinted(absent, gray)
	}
	return plain(short(model.Seconds(*seconds)))
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
