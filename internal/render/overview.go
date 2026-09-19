// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// SortOrder is the order the Overview's rows come in, matching the --sort
// values that select one.
type SortOrder string

const (
	// SortName is alphabetical by namespace, workload, then container, and
	// is what an empty Options asks for.
	SortName SortOrder = "name"
	// SortSeverity puts the rows worth looking at first.
	SortSeverity SortOrder = "severity"
)

// Options is what the flags mean to the Overview.
type Options struct {
	// AllNamespaces adds the NAMESPACE column, and is -A at the CLI. Without
	// it every row would carry the one namespace the user already named.
	AllNamespaces bool
	// Wide adds a handler detail column beside each probe and a drift column
	// at the end, and is -o wide at the CLI.
	Wide bool
	// Sort defaults to SortName.
	Sort SortOrder
}

// The marks a cell can carry, and the sentences on stderr that explain them.
// Each note opens with its own mark so a reader who sees one in the table can
// find the line that says what it means.
const (
	absent    = "-"
	driftMark = "*"

	driftNote    = "* running config differs from workload template"
	failuresNote = "- failures are unknown: listing events was forbidden"
	emptyNote    = "No workloads found."
)

// Overview writes the default output: one row per container, with each probe's
// handler and headline timing, what the pods running it report, and how many
// findings it has.
//
// The table goes to out and everything the table could not say goes to errOut,
// so a pipe carries the table and nothing else. An empty report is not an
// error: it says so on errOut and writes no table at all.
func Overview(out, errOut io.Writer, report *model.Report, opts Options) error {
	rows := rowsOf(report)
	if len(rows) == 0 {
		_, err := fmt.Fprintln(errOut, emptyNote)
		return err
	}
	sortRows(rows, opts.Sort)

	t := &table{}
	t.add(header(opts)...)
	for _, r := range rows {
		t.add(r.cells(opts)...)
	}
	if err := t.write(out); err != nil {
		return err
	}
	return writeNotes(errOut, rows)
}

// row is one container as the Overview prints it, with the facts already dug
// out of the Report so that sorting and printing do not each have to.
type row struct {
	namespace string
	workload  string
	container string
	// probes are the container's three probes in model.ProbeTypes order, nil
	// where the container has none.
	probes []*model.ProbeDetail
	drift  []model.ProbeType
	ready  string
	// restarts and failures are what the pods running the container report.
	// failures is nil when the events that count them could not be read.
	restarts int32
	failures *int32
	findings int
}

func rowsOf(report *model.Report) []row {
	if report == nil {
		return nil
	}
	var rows []row
	for _, workload := range report.Workloads {
		for _, container := range workload.Containers {
			rows = append(rows, rowOf(workload, container))
		}
	}
	return rows
}

func rowOf(workload model.Workload, container model.Container) row {
	r := row{
		namespace: workload.Namespace,
		workload:  workload.DisplayName,
		container: container.Name,
		// A workload with no pods still gets a row. It says 0/0 rather than
		// claiming anything about pods that do not exist.
		ready:    "0/0",
		findings: len(container.Findings),
	}

	for _, probe := range model.ProbeTypes {
		detail := container.Probe(probe)
		r.probes = append(r.probes, detail)
		if detail != nil && detail.Drifted {
			r.drift = append(r.drift, probe)
		}
	}

	if runtime := container.Runtime; runtime != nil {
		r.ready = fmt.Sprintf("%d/%d", runtime.Ready, runtime.Total)
		r.restarts = runtime.Restarts
		r.failures = runtime.Failures
	} else {
		// Nothing running is nothing to fail, which is a count of zero and
		// not the unknown that a forbidden event list leaves behind.
		none := int32(0)
		r.failures = &none
	}
	return r
}

func header(opts Options) []cell {
	var cells []cell
	if opts.AllNamespaces {
		cells = append(cells, plain("NAMESPACE"))
	}
	cells = append(cells, plain("WORKLOAD"), plain("CONTAINER"))
	for _, probe := range model.ProbeTypes {
		name := strings.ToUpper(string(probe))
		cells = append(cells, plain(name))
		if opts.Wide {
			cells = append(cells, plain(name+"-HANDLER"))
		}
	}
	cells = append(cells, plain("READY"), plain("RESTARTS"), plain("FAILURES"), plain("FINDINGS"))
	if opts.Wide {
		cells = append(cells, plain("DRIFT"))
	}
	return cells
}

func (r row) cells(opts Options) []cell {
	var cells []cell
	if opts.AllNamespaces {
		cells = append(cells, plain(r.namespace))
	}
	cells = append(cells, plain(r.workload), plain(r.container))
	for i, probe := range model.ProbeTypes {
		cells = append(cells, probeCell(probe, r.probes[i]))
		if opts.Wide {
			cells = append(cells, handlerCell(r.probes[i]))
		}
	}
	cells = append(cells,
		plain(r.ready),
		plain(strconv.FormatInt(int64(r.restarts), 10)),
		failuresCell(r.failures),
		findingsCell(r.findings),
	)
	if opts.Wide {
		cells = append(cells, driftCell(r.drift))
	}
	return cells
}

// probeCell is a probe in one cell: how it asks, and the single duration a
// reader scanning the table needs. A probe nobody configured is a gray dash,
// and one whose running configuration differs from the template wears a red
// star, explained once below the table.
func probeCell(probe model.ProbeType, detail *model.ProbeDetail) cell {
	body := tinted(absent, gray)
	if detail != nil && detail.Config != nil {
		text := handlerType(detail.Config.Handler)
		if timing := headline(probe, detail.Timing); timing != "" {
			text += ":" + timing
		}
		body = plain(text)
	}
	if detail != nil && detail.Drifted {
		return join(body, tinted(driftMark, red))
	}
	return body
}

// headline is the one duration a probe's cell shows: for a startup probe the
// budget the container has to come up in, and for the other two how long the
// kubelet takes to notice a failure, because that is the number each of them
// is read for.
func headline(probe model.ProbeType, timing *model.Timing) string {
	if timing == nil {
		return ""
	}
	if probe == model.ProbeStartup && timing.StartupBudget != nil {
		return short(*timing.StartupBudget)
	}
	return short(timing.FailureDetection)
}

// short is a duration as a table prints it: Go's own wording with the trailing
// zero units trimmed, so a two minute budget reads 2m rather than 2m0s.
func short(d model.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "h0m0s") {
		return strings.TrimSuffix(s, "0m0s")
	}
	if strings.HasSuffix(s, "m0s") {
		return strings.TrimSuffix(s, "0s")
	}
	return s
}

// handlerType names how a probe asks. A probe with no handler at all cannot
// come from a pod the API server accepted, only from a hand-written manifest,
// and it is reported as unknown rather than left out.
func handlerType(handler model.Handler) string {
	if handler.Type == "" {
		return "?"
	}
	return string(handler.Type)
}

// handlerCell is the handler in full, which -o wide adds beside each probe:
// the request an http probe makes, the port a tcp or grpc probe opens, or the
// command an exec probe runs.
func handlerCell(detail *model.ProbeDetail) cell {
	if detail == nil || detail.Config == nil || detail.Config.Handler.Summary == "" {
		return tinted(absent, gray)
	}
	return plain(detail.Config.Handler.Summary)
}

// failuresCell counts the Unhealthy events the cluster reports. It is a gray
// dash, rather than a zero, where the events could not be read: an unknown
// count is not evidence of anything.
func failuresCell(failures *int32) cell {
	if failures == nil {
		return tinted(absent, gray)
	}
	if *failures == 0 {
		return plain("0")
	}
	return tinted(strconv.FormatInt(int64(*failures), 10), red)
}

func findingsCell(findings int) cell {
	if findings == 0 {
		return plain("0")
	}
	return tinted(strconv.Itoa(findings), yellow)
}

// driftCell names the probes whose running configuration differs from the
// template. It is the star of the probe cells spelled out, so it is red for
// the same reason.
func driftCell(drift []model.ProbeType) cell {
	if len(drift) == 0 {
		return tinted(absent, gray)
	}
	names := make([]string, 0, len(drift))
	for _, probe := range drift {
		names = append(names, string(probe))
	}
	return tinted(strings.Join(names, ","), red)
}

// writeNotes says what the table could not, once, for whatever marks it ended
// up carrying.
func writeNotes(errOut io.Writer, rows []row) error {
	var drifted, unknown bool
	for _, r := range rows {
		drifted = drifted || len(r.drift) > 0
		unknown = unknown || r.failures == nil
	}
	for _, note := range []struct {
		when bool
		text string
	}{
		{drifted, driftNote},
		{unknown, failuresNote},
	} {
		if !note.when {
			continue
		}
		if _, err := fmt.Fprintln(errOut, note.text); err != nil {
			return err
		}
	}
	return nil
}

func sortRows(rows []row, order SortOrder) {
	slices.SortStableFunc(rows, func(a, b row) int {
		if order == SortSeverity {
			if c := cmp.Compare(band(a), band(b)); c != 0 {
				return c
			}
		}
		return a.compareName(b)
	})
}

func (r row) compareName(other row) int {
	if c := strings.Compare(r.namespace, other.namespace); c != 0 {
		return c
	}
	if c := strings.Compare(r.workload, other.workload); c != 0 {
		return c
	}
	return strings.Compare(r.container, other.container)
}

// band is where --sort severity puts a row: the containers the cluster reports
// probe failures for first, then the ones that have restarted, then the ones
// only a rule has an opinion about, and the quiet ones last. Rows stay
// alphabetical within a band.
//
// A container whose failure count could not be read is banded on the rest of
// its evidence, because an unknown count is not evidence.
func band(r row) int {
	switch {
	case r.failures != nil && *r.failures > 0:
		return 0
	case r.restarts > 0:
		return 1
	case r.findings > 0:
		return 2
	}
	return 3
}
