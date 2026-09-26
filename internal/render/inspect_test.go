// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"k8s.io/utils/ptr"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

func TestInspectionGolden(t *testing.T) {
	tests := []struct {
		name   string
		report *model.Report
		golden string
		notes  string
	}{
		{
			name:   "a deployment with drift and events",
			report: driftingReport(),
			golden: "inspect.txt",
		},
		{
			name:   "a workload scaled to zero",
			report: zeroPodReport(),
			golden: "inspect-zero-pods.txt",
		},
		{
			name:   "a manifest that has not been applied",
			report: manifestReport(),
			golden: "inspect-manifest.txt",
		},
		{
			// The template could not be read, so nothing can be said about
			// drift and the view says so on stderr.
			name:   "a workload with a sidecar",
			report: sidecarReport(),
			golden: "inspect-sidecar.txt",
			notes:  noTemplateNote + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := Inspection(&out, &errOut, tt.report); err != nil {
				t.Fatalf("Inspection failed: %v", err)
			}

			path := filepath.Join("testdata", tt.golden)
			if *update {
				if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
					t.Fatalf("writing %s failed: %v", path, err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s failed: %v", path, err)
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Errorf("Inspection does not match %s; rerun with -update to see the diff in git:\n%s",
					path, out.String())
			}
			if got := errOut.String(); got != tt.notes {
				t.Errorf("notes = %q, want %q", got, tt.notes)
			}
		})
	}
}

// An empty result is not an error, and it is not a header with no sections
// under it either.
func TestInspectionEmptyReport(t *testing.T) {
	for _, report := range []*model.Report{nil, model.New(generatedAt())} {
		var out, errOut bytes.Buffer
		if err := Inspection(&out, &errOut, report); err != nil {
			t.Fatalf("Inspection failed: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("view = %q, want none", out.String())
		}
		if got, want := errOut.String(), emptyNote+"\n"; got != want {
			t.Errorf("notes = %q, want %q", got, want)
		}
	}
}

// Findings are the only section that is absent rather than empty, because
// --no-findings produces a Report that carries none.
func TestInspectionWithoutFindings(t *testing.T) {
	report := driftingReport()
	for i := range report.Workloads[0].Containers {
		report.Workloads[0].Containers[i].Findings = nil
	}

	var out, errOut bytes.Buffer
	if err := Inspection(&out, &errOut, report); err != nil {
		t.Fatalf("Inspection failed: %v", err)
	}
	if strings.Contains(out.String(), headingFindings) {
		t.Errorf("view still has a %s section:\n%s", headingFindings, out.String())
	}
	// Every other section is still there, so the flag takes away opinions and
	// nothing else.
	for _, heading := range []string{headingConfiguration, headingEffectiveTiming,
		headingDrift, headingRuntimeState, headingFailureEvidence} {
		if !strings.Contains(out.String(), heading) {
			t.Errorf("view is missing the %s section:\n%s", heading, out.String())
		}
	}
}

// Drift is the other section that comes and goes, and it goes where the
// running configuration is what the template asked for.
func TestInspectionWithoutDrift(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Inspection(&out, &errOut, zeroPodReport()); err != nil {
		t.Fatalf("Inspection failed: %v", err)
	}
	if strings.Contains(out.String(), headingDrift) {
		t.Errorf("view has a %s section with nothing drifted:\n%s", headingDrift, out.String())
	}
}

// Colour must not move a column here either, and the Inspection adds raw lines
// between the rows, which is the part a measured table can get wrong.
func TestInspectionColorKeepsAlignment(t *testing.T) {
	var plainOut, errOut bytes.Buffer
	if err := Inspection(&plainOut, &errOut, driftingReport()); err != nil {
		t.Fatalf("Inspection failed: %v", err)
	}

	color.NoColor = false
	t.Cleanup(func() { color.NoColor = true })

	var colored bytes.Buffer
	errOut.Reset()
	if err := Inspection(&colored, &errOut, driftingReport()); err != nil {
		t.Fatalf("Inspection failed: %v", err)
	}

	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatalf("no colour in:\n%s", colored.String())
	}
	if got, want := stripANSI(colored.String()), plainOut.String(); got != want {
		t.Errorf("coloured view does not lay out like the plain one:\ngot:\n%s\nwant:\n%s", got, want)
	}
	// Red for what the cluster reports is wrong now, yellow for an opinion,
	// gray for what nobody configured or wrote.
	for _, want := range []string{red.Sprint("37"), red.Sprint("false"),
		yellow.Sprint(bodyIndent + "liveness-same-as-readiness"), gray.Sprint("1s")} {
		if !strings.Contains(colored.String(), want) {
			t.Errorf("view is missing %q:\n%s", want, colored.String())
		}
	}
}

// The Configuration section is one table: a row per probe in the order the
// kubelet puts them to work, and a column per setting. Every duration reads the
// way the Overview writes one, ACTS AFTER is the Overview's headline, and the
// handler comes last.
func TestConfigurationTable(t *testing.T) {
	tests := []struct {
		name      string
		container model.Container
		want      []string
	}{
		{
			// deploy/api in integration/testdata, the example ADR 0010 draws.
			name: "every probe configured",
			container: model.Container{
				Name: "api",
				Startup: written(detail(httpHandler("/healthz", "8080"), startupTiming(model.Seconds(150))), func(p *model.Probe) {
					p.PeriodSeconds = ptr.To(int32(5))
					p.FailureThreshold = ptr.To(int32(30))
				}),
				Readiness: written(detail(httpHandler("/ready", "http"), timing(model.Seconds(90))), func(p *model.Probe) {
					p.PeriodSeconds = ptr.To(int32(30))
					p.TimeoutSeconds = ptr.To(int32(2))
					p.FailureThreshold = ptr.To(int32(3))
				}),
				Liveness: written(detail(httpHandler("/healthz", "8080"), timing(model.Seconds(30))), func(p *model.Probe) {
					p.InitialDelaySeconds = ptr.To(int32(10))
					p.PeriodSeconds = ptr.To(int32(10))
					p.FailureThreshold = ptr.To(int32(3))
				}),
			},
			want: []string{
				"    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER",
				"    startup    0s     5s      1s       1        30       2m30s       GET /healthz:8080",
				"    readiness  0s     30s     2s       1        3        1m30s       GET /ready:http",
				"    liveness   10s    10s     1s       1        3        30s         GET /healthz:8080",
			},
		},
		{
			name: "durations past a minute",
			container: model.Container{
				Name: "slow",
				Liveness: written(detail(tcpHandler("5432"), timing(model.Seconds(3600))), func(p *model.Probe) {
					p.InitialDelaySeconds = ptr.To(int32(90))
					p.PeriodSeconds = ptr.To(int32(1200))
					p.TimeoutSeconds = ptr.To(int32(60))
					p.FailureThreshold = ptr.To(int32(3))
				}),
			},
			want: []string{
				"    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER",
				"    startup    -",
				"    readiness  -",
				"    liveness   1m30s  20m     1m       1        3        1h          tcp :5432",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configuration(t, tt.container); !equalStrings(got, tt.want) {
				t.Errorf("Configuration =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

// A value the spec never wrote is gray, the colour of what nobody configured.
// There is no text marker, so with colour off a default reads like a written
// value, and the Report is where a script tells the two apart.
func TestConfigurationDefaultsAreGray(t *testing.T) {
	container := model.Container{
		Name: "api",
		Readiness: written(detail(httpHandler("/ready", "8080"), timing(model.Seconds(15))), func(p *model.Probe) {
			p.PeriodSeconds = ptr.To(int32(5))
		}),
	}

	plainLines := configuration(t, container)
	if got, want := plainLines[2], "    readiness  0s     5s      1s       1        3        15s         GET /ready:8080"; got != want {
		t.Errorf("readiness row = %q, want %q", got, want)
	}

	color.NoColor = false
	t.Cleanup(func() { color.NoColor = true })
	colored := strings.Join(configuration(t, container), "\n")

	for _, want := range []string{gray.Sprint("0s"), gray.Sprint("1s"), gray.Sprint("1"), gray.Sprint("3")} {
		if !strings.Contains(colored, want) {
			t.Errorf("Configuration is missing the default %q:\n%s", want, colored)
		}
	}
	if strings.Contains(colored, gray.Sprint("5s")) {
		t.Errorf("the period the spec wrote is gray:\n%s", colored)
	}
	if got, want := stripANSI(colored), strings.Join(plainLines, "\n"); got != want {
		t.Errorf("coloured Configuration does not lay out like the plain one:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// GRACE is a column only where a probe sets its own grace period, which is
// rare, so the common table does not carry a column of dashes for it. Where
// nothing sets one, grace is not mentioned at all.
func TestConfigurationGrace(t *testing.T) {
	readiness := func() *model.ProbeDetail {
		return written(detail(httpHandler("/ready", "8080"), timing(model.Seconds(30))), func(p *model.Probe) {
			p.PeriodSeconds = ptr.To(int32(10))
		})
	}

	t.Run("no probe sets one", func(t *testing.T) {
		view := inspectOne(t, model.Container{Name: "api", Readiness: readiness()})
		lines := section(t, view, headingConfiguration)
		if got, want := lines[0], "    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER"; got != want {
			t.Errorf("header = %q, want %q", got, want)
		}
		for _, unwanted := range []string{"GRACE", "grace", "the pod's own"} {
			if strings.Contains(view, unwanted) {
				t.Errorf("view mentions %q with no grace period set:\n%s", unwanted, view)
			}
		}
	})

	t.Run("one probe sets one", func(t *testing.T) {
		container := model.Container{
			Name:      "api",
			Readiness: readiness(),
			Liveness: written(detail(tcpHandler("8080"), timing(model.Seconds(30))), func(p *model.Probe) {
				p.PeriodSeconds = ptr.To(int32(10))
				p.TerminationGracePeriodSeconds = ptr.To(int64(90))
			}),
		}
		want := []string{
			"    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  GRACE  ACTS AFTER  HANDLER",
			"    startup    -",
			"    readiness  0s     10s     1s       1        3        -      30s         GET /ready:8080",
			"    liveness   0s     10s     1s       1        3        1m30s  30s         tcp :8080",
		}
		if got := configuration(t, container); !equalStrings(got, want) {
			t.Errorf("Configuration =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

// The handler is the last column, so a long exec command runs to the right
// edge and every number stays under its heading, including the ones on the
// rows of shorter handlers.
func TestConfigurationLongHandlerIsLast(t *testing.T) {
	container := model.Container{
		Name: "db",
		Readiness: written(detail(execHandler("/bin/sh", "-c", "pg_isready -U postgres -h 127.0.0.1"), timing(model.Seconds(30))), func(p *model.Probe) {
			p.PeriodSeconds = ptr.To(int32(10))
			p.FailureThreshold = ptr.To(int32(3))
		}),
		Liveness: written(detail(tcpHandler("5432"), timing(model.Seconds(10))), func(p *model.Probe) {
			p.PeriodSeconds = ptr.To(int32(5))
			p.FailureThreshold = ptr.To(int32(2))
		}),
	}
	want := []string{
		"    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER",
		"    startup    -",
		"    readiness  0s     10s     1s       1        3        30s         exec /bin/sh -c pg_isready -U postgres -h 127.0.0.1",
		"    liveness   0s     5s      1s       1        2        10s         tcp :5432",
	}
	if got := configuration(t, container); !equalStrings(got, want) {
		t.Errorf("Configuration =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// HTTP headers are too long for a cell and too much to leave out, so each probe
// that sends any says so on a line under the table, which takes no part in its
// column widths.
func TestConfigurationHeaders(t *testing.T) {
	readiness := detail(httpHandler("/ready", "8080"), timing(model.Seconds(30)))
	readiness.Config.Handler.HTTPHeaders = []model.HTTPHeader{
		{Name: "Host", Value: "example.com"},
		{Name: "X-Probe", Value: "1"},
	}
	container := model.Container{
		Name:      "api",
		Readiness: readiness,
		Liveness:  detail(headerHandler("/healthz", "8080"), timing(model.Seconds(30))),
	}

	want := []string{
		"    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER",
		"    startup    -",
		"    readiness  0s     10s     1s       1        3        30s         GET /ready:8080",
		"    liveness   0s     10s     1s       1        3        30s         GET /healthz:8080",
		"    readiness sends Host: example.com, X-Probe: 1",
		"    liveness sends X-Probe: kubelet",
	}
	if got := configuration(t, container); !equalStrings(got, want) {
		t.Errorf("Configuration =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Drift is the Overview's red star on the cell that drifted: a field's own
// cell, the handler's, or the probe's name where the difference is the probe
// itself or nothing in a column explains it. The table shows what the running
// pod has, and the Drift section under it what the template has instead.
func TestConfigurationDriftMarks(t *testing.T) {
	const header = "    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER"
	// A star on the probe's name widens the column it is in by one.
	const wideHeader = "    PROBE       DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  ACTS AFTER  HANDLER"

	running := func(handler model.Handler) *model.ProbeDetail {
		return written(detail(handler, timing(model.Seconds(30))), func(p *model.Probe) {
			p.PeriodSeconds = ptr.To(int32(10))
		})
	}
	template := func(handler model.Handler, set func(*model.Probe)) *model.Probe {
		return writtenProbe(&model.Probe{Handler: handler}, func(p *model.Probe) {
			p.PeriodSeconds = ptr.To(int32(10))
			set(p)
		})
	}
	ready := httpHandler("/ready", "8080")
	unchanged := func(*model.Probe) {}

	tests := []struct {
		name      string
		readiness *model.ProbeDetail
		want      []string
		// marked is the cell the red star follows, and markedGray says it
		// prints in gray, which is known only once colour is on.
		marked     string
		markedGray bool
	}{
		{
			name: "a field",
			readiness: drift(running(ready), template(ready, func(p *model.Probe) {
				p.PeriodSeconds = ptr.To(int32(30))
			})),
			want: []string{
				header,
				"    startup    -",
				"    readiness  0s     10s*    1s       1        3        30s         GET /ready:8080",
				"    liveness   -",
			},
			marked: "10s",
		},
		{
			name: "a field left at its default",
			readiness: drift(running(ready), template(ready, func(p *model.Probe) {
				p.TimeoutSeconds = ptr.To(int32(5))
			})),
			want: []string{
				header,
				"    startup    -",
				"    readiness  0s     10s     1s*      1        3        30s         GET /ready:8080",
				"    liveness   -",
			},
			marked:     "1s",
			markedGray: true,
		},
		{
			name:      "the handler",
			readiness: drift(running(ready), template(httpHandler("/readyz", "8080"), unchanged)),
			want: []string{
				header,
				"    startup    -",
				"    readiness  0s     10s     1s       1        3        30s         GET /ready:8080*",
				"    liveness   -",
			},
			marked: "GET /ready:8080",
		},
		{
			name:      "a probe the template no longer configures",
			readiness: drift(running(ready), nil),
			want: []string{
				wideHeader,
				"    startup     -",
				"    readiness*  0s     10s     1s       1        3        30s         GET /ready:8080",
				"    liveness    -",
			},
			marked: bodyIndent + "readiness",
		},
		{
			// Headers are part of the handler but not of its line, so a
			// difference in them is one no column explains.
			name:      "a difference no column explains",
			readiness: drift(running(headerHandler("/ready", "8080")), template(ready, unchanged)),
			want: []string{
				wideHeader,
				"    startup     -",
				"    readiness*  0s     10s     1s       1        3        30s         GET /ready:8080",
				"    liveness    -",
				"    readiness sends X-Probe: kubelet",
			},
			marked: bodyIndent + "readiness",
		},
		{
			// GRACE is a column for as long as it has a mark to carry, even
			// where the running pod sets no grace period of its own.
			name: "a grace period only the template sets",
			readiness: drift(running(ready), template(ready, func(p *model.Probe) {
				p.TerminationGracePeriodSeconds = ptr.To(int64(30))
			})),
			want: []string{
				"    PROBE      DELAY  PERIOD  TIMEOUT  SUCCESS  FAILURE  GRACE  ACTS AFTER  HANDLER",
				"    startup    -",
				"    readiness  0s     10s     1s       1        3        -*     30s         GET /ready:8080",
				"    liveness   -",
			},
			marked:     absent,
			markedGray: true,
		},
		{
			// A rollout adding a container's first probe: the running pod
			// has none, and the table is still printed so the probe the
			// template adds has a row to carry its mark.
			name:      "a probe only the template configures",
			readiness: &model.ProbeDetail{Drifted: true, Template: template(ready, unchanged)},
			want: []string{
				wideHeader,
				"    startup     -",
				"    readiness*  -",
				"    liveness    -",
			},
			marked: bodyIndent + "readiness",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := model.Container{Name: "api", Readiness: tt.readiness}
			if got := configuration(t, container); !equalStrings(got, tt.want) {
				t.Errorf("Configuration =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}

			color.NoColor = false
			t.Cleanup(func() { color.NoColor = true })
			colored := strings.Join(configuration(t, container), "\n")
			marked := tt.marked
			if tt.markedGray {
				marked = gray.Sprint(marked)
			}
			if want := marked + red.Sprint(driftMark); !strings.Contains(colored, want) {
				t.Errorf("Configuration has no red star after %q:\n%s", marked, colored)
			}
		})
	}
}

// The timing sentences are the wording the ticket fixed, so the test asserts
// the strings the output is built from rather than a paraphrase of them.
func TestTimingSentences(t *testing.T) {
	tests := []struct {
		name   string
		probe  model.ProbeType
		detail *model.ProbeDetail
		want   []string
	}{
		{
			name:  "a probe nobody configured has nothing to say",
			probe: model.ProbeLiveness,
		},
		{
			name:   "a probe with no timing derived",
			probe:  model.ProbeLiveness,
			detail: detail(httpHandler("/healthz", "8080"), nil),
		},
		{
			name:   "startup leads with the budget it gives the container",
			probe:  model.ProbeStartup,
			detail: detail(httpHandler("/healthz", "8080"), startupTiming(model.Seconds(300))),
			want:   []string{fmt.Sprintf(sentenceStartupBudget, "Startup", "5m")},
		},
		{
			name:  "liveness after a startup probe",
			probe: model.ProbeLiveness,
			detail: detail(httpHandler("/healthz", "8080"), &model.Timing{
				FailureDetection: model.Seconds(90),
				AfterStartup:     true,
			}),
			want: []string{
				fmt.Sprintf(sentenceFailureDetection, "Liveness", 3, "1m30s"),
				fmt.Sprintf(sentenceAfterStartup, "Liveness", originStartupSuccess),
			},
		},
		{
			name:  "a liveness probe that waits before its first check",
			probe: model.ProbeLiveness,
			detail: threshold(detail(httpHandler("/healthz", "8080"), &model.Timing{
				FirstCheck:       model.Seconds(30),
				FailureDetection: model.Seconds(60),
			}), 6),
			want: []string{
				fmt.Sprintf(sentenceFailureDetection, "Liveness", 6, "1m"),
				fmt.Sprintf(sentenceFirstCheck, "Liveness", "30s"),
			},
		},
		{
			name:  "readiness that has to succeed more than once",
			probe: model.ProbeReadiness,
			detail: detail(httpHandler("/ready", "8080"), &model.Timing{
				FailureDetection: model.Seconds(15),
				TrafficDelay:     model.SecondsPtr(5),
			}),
			want: []string{
				fmt.Sprintf(sentenceFailureDetection, "Readiness", 3, "15s"),
				fmt.Sprintf(sentenceTrafficDelay, "5s", originContainerStart),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := timingSentences(tt.probe, tt.detail)
			if !equalStrings(got, tt.want) {
				t.Errorf("timingSentences() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The example the ticket wrote the wording down as, asserted literally, so
// that reusing the constants elsewhere cannot quietly reword them.
func TestTimingSentencesWording(t *testing.T) {
	detail := detail(httpHandler("/healthz", "8080"), &model.Timing{
		FailureDetection: model.Seconds(90),
		AfterStartup:     true,
	})
	got := strings.Join(timingSentences(model.ProbeLiveness, detail), " ")
	want := "Liveness acts after 3 consecutive failures, at worst 1m30s after the container stops responding. " +
		"Liveness begins after the startup probe succeeds."
	if got != want {
		t.Errorf("timing sentences =\n%s\nwant\n%s", got, want)
	}
}

// A probe configured on one side only is drift about the probe itself, which
// is the shape a rollout that adds or removes one leaves behind.
func TestDriftRows(t *testing.T) {
	handler := httpHandler("/healthz", "8080")
	tests := []struct {
		name   string
		detail *model.ProbeDetail
		want   []string
	}{
		{
			name:   "a probe only the template configures",
			detail: &model.ProbeDetail{Drifted: true, Template: &model.Probe{Handler: handler}},
			want:   []string{"probe " + absent + " GET /healthz:8080"},
		},
		{
			name:   "a probe the template no longer configures",
			detail: drift(detail(handler, nil), nil),
			want:   []string{"probe GET /healthz:8080 " + absent},
		},
		{
			// The Drift section names the fields the way the Configuration
			// table heads its columns, and writes their values the same way.
			name: "the fields the two sides disagree about, and only those",
			detail: drift(written(detail(handler, nil), func(p *model.Probe) {
				p.PeriodSeconds = ptr.To(int32(10))
				p.TimeoutSeconds = ptr.To(int32(5))
				p.FailureThreshold = ptr.To(int32(3))
				p.TerminationGracePeriodSeconds = ptr.To(int64(90))
			}), writtenProbe(&model.Probe{Handler: httpHandler("/live", "8080")}, func(p *model.Probe) {
				p.InitialDelaySeconds = ptr.To(int32(60))
				p.PeriodSeconds = ptr.To(int32(30))
				p.TimeoutSeconds = ptr.To(int32(5))
				p.SuccessThreshold = ptr.To(int32(2))
				p.FailureThreshold = ptr.To(int32(5))
			})),
			want: []string{
				"handler GET /healthz:8080 GET /live:8080",
				"delay 0s 1m",
				"period 10s 30s",
				"success 1 2",
				"failure 3 5",
				"grace 1m30s " + absent,
			},
		},
		{
			// Drift was decided on the whole effective probe, so a difference
			// the field list does not cover still has to say something.
			name:   "a difference the fields do not reach",
			detail: drift(detail(handler, nil), &model.Probe{Handler: handler}),
			want:   []string{"probe GET /healthz:8080 GET /healthz:8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, row := range driftRows(tt.detail) {
				got = append(got, strings.Join([]string{row.name, row.running.text, row.template.text}, " "))
			}
			if !equalStrings(got, tt.want) {
				t.Errorf("driftRows() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A bare pod has no workload above it and a manifest need not name a
// namespace, so the header is whatever there is of both.
func TestInspectionHeaders(t *testing.T) {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{
		{Kind: "Pod", Name: "orphan", DisplayName: "pod/orphan", TemplateAvailable: true,
			Containers: []model.Container{{Name: "app"}}},
		{Kind: "Deployment", Group: "apps", Name: "api", Namespace: "prod",
			DisplayName: "deploy/api", PodCount: 1, TemplateAvailable: true,
			Containers: []model.Container{{Name: "api"}}},
	}

	var out, errOut bytes.Buffer
	if err := Inspection(&out, &errOut, report); err != nil {
		t.Fatalf("Inspection failed: %v", err)
	}
	lines := strings.Split(out.String(), "\n")
	for _, want := range []string{"pod/orphan", "no pods", "deploy/api -n prod", "1 pod"} {
		if !contains(lines, want) {
			t.Errorf("view has no line %q:\n%s", want, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("notes = %q, want none", errOut.String())
	}
}

// configuration is the body of the Configuration section an Inspection prints
// for a workload of one container.
func configuration(t *testing.T, container model.Container) []string {
	t.Helper()
	return section(t, inspectOne(t, container), headingConfiguration)
}

func inspectOne(t *testing.T, container model.Container) string {
	t.Helper()
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Deployment", Group: "apps", Name: container.Name, Namespace: "prod",
		DisplayName: "deploy/" + container.Name, PodCount: 1, TemplateAvailable: true,
		Containers: []model.Container{container},
	}}
	var out, errOut bytes.Buffer
	if err := Inspection(&out, &errOut, report); err != nil {
		t.Fatalf("Inspection failed: %v", err)
	}
	return out.String()
}

// section is the body of one of the Inspection's sections: the lines after its
// heading, up to the next heading or the end of the container.
func section(t *testing.T, view, heading string) []string {
	t.Helper()
	var body []string
	in := false
	for _, line := range strings.Split(view, "\n") {
		if line == sectionIndent+heading {
			in = true
			continue
		}
		if in && !strings.HasPrefix(line, bodyIndent) {
			return body
		}
		if in {
			body = append(body, line)
		}
	}
	if !in {
		t.Fatalf("view has no %s section:\n%s", heading, view)
	}
	return body
}

func contains(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

// A raw line takes no part in the column widths, which is what lets a probe's
// failure message hang under the row that counted it without stretching it.
func TestTableRawLines(t *testing.T) {
	tbl := &table{}
	tbl.add(plain("POD"), plain("FAILURES"))
	tbl.add(plain("api-1"), plain("37"))
	tbl.addRaw("  a message far longer than any column in this table")
	tbl.add(plain("api-2"), plain("1"))

	var out bytes.Buffer
	if err := tbl.write(&out); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if got, want := len(lines), 4; got != want {
		t.Fatalf("got %d lines, want %d:\n%s", got, want, out.String())
	}
	if got, want := lines[2], "  a message far longer than any column in this table"; got != want {
		t.Errorf("raw line = %q, want %q", got, want)
	}
	// The rows on either side of the raw line still share their columns.
	for _, i := range []int{1, 3} {
		if got, want := columnStarts(lines[i]), columnStarts(lines[0]); !equal(got, want) {
			t.Errorf("columns start at %v, want %v, in:\n%s", got, want, out.String())
		}
	}
}

// driftingReport is the case the Inspection exists for: a Deployment mid
// rollout, whose running liveness probe is not the one the template asks for,
// with the cluster reporting that probe failing and a container that has been
// restarted for it.
func driftingReport() *model.Report {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Deployment", Group: "apps", Name: "api", Namespace: "prod",
		DisplayName: "deploy/api", PodCount: 2, TerminatingPodCount: 1, TemplateAvailable: true,
		Containers: []model.Container{{
			Name: "api",
			Startup: written(detail(httpHandler("/healthz", "8080"), &model.Timing{
				FailureDetection: model.Seconds(300),
				StartupBudget:    model.SecondsPtr(300),
			}), func(p *model.Probe) { p.FailureThreshold = ptr.To(int32(30)) }),
			Readiness: written(detail(headerHandler("/ready", "8080"), &model.Timing{
				FailureDetection: model.Seconds(15),
				TrafficDelay:     model.SecondsPtr(5),
				AfterStartup:     true,
			}), func(p *model.Probe) {
				p.PeriodSeconds = ptr.To(int32(5))
				p.SuccessThreshold = ptr.To(int32(2))
			}),
			// The rollout halved the liveness period, and the pods still
			// running have the template's own value.
			Liveness: drift(written(detail(httpHandler("/healthz", "8080"), &model.Timing{
				FirstCheck:       model.Seconds(10),
				FailureDetection: model.Seconds(30),
				AfterStartup:     true,
			}), func(p *model.Probe) {
				p.InitialDelaySeconds = ptr.To(int32(10))
				p.PeriodSeconds = ptr.To(int32(10))
				p.TerminationGracePeriodSeconds = ptr.To(int64(30))
			}), writtenProbe(&model.Probe{Handler: httpHandler("/healthz", "8080")}, func(p *model.Probe) {
				p.InitialDelaySeconds = ptr.To(int32(10))
				p.PeriodSeconds = ptr.To(int32(30))
			})),
			Runtime: &model.Runtime{Ready: 1, Total: 2, Restarts: 12, Failures: ptr.To(int32(37))},
			Pods: []model.Pod{
				{
					Name: "api-6c9f7d4b8-2xklm", Ready: true, Started: ptr.To(true),
					Conditions: conditions(model.ConditionTrue, ago(2*time.Hour)),
				},
				{
					Name: "api-6c9f7d4b8-9wqzt", Started: ptr.To(true), Restarts: 12,
					LastTermination: &model.Termination{
						Reason: "OOMKilled", ExitCode: 137, Signal: 9, FinishedAt: ago(3 * time.Hour),
					},
					Conditions: conditions(model.ConditionFalse, ago(5*time.Minute)),
					Events: []model.EventGroup{{
						Probe: model.ProbeLiveness, Count: 37,
						FirstSeen: ago(3 * time.Hour), LastSeen: ago(2 * time.Minute),
						Message: "Liveness probe failed: HTTP probe failed with statuscode: 503",
					}},
				},
			},
			Findings: []model.Finding{
				{Rule: "liveness-same-as-readiness", Message: "Liveness and readiness both check GET /healthz:8080, so whatever takes this container out of service also restarts it."},
				{Rule: "probe-failures-recent", Message: "The cluster reports 37 recent Unhealthy events for the liveness probe, so this is failing now and not only on paper."},
			},
		}},
		InitContainers: []string{"migrate", "seed"},
	}}
	return report
}

// zeroPodReport is a workload scaled to zero: the template is the only
// configuration there is, there is nothing running to describe, and nothing to
// compare against either, so no probe of it can drift.
func zeroPodReport() *model.Report {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Deployment", Group: "apps", Name: "batch", Namespace: "prod",
		DisplayName: "deploy/batch", TemplateAvailable: true,
		Containers: []model.Container{{
			Name: "batch",
			Readiness: written(detail(execHandler("cat", "/tmp/ready"), &model.Timing{
				FailureDetection: model.Seconds(60),
			}), func(p *model.Probe) {
				p.PeriodSeconds = ptr.To(int32(20))
				p.TimeoutSeconds = ptr.To(int32(5))
			}),
			Findings: []model.Finding{{
				Rule:    "no-readiness-probe",
				Message: "No readiness probe is configured, so the kubelet reports this container ready the moment it starts and traffic arrives from then on.",
			}},
		}},
	}}
	return report
}

// manifestReport is what -f reads: a bare Pod nobody has applied. Its probe
// writes none of its own timing, so every field it has comes from the kubelet
// and is marked as such.
func manifestReport() *model.Report {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Pod", Name: "canary", Namespace: "default",
		DisplayName: "pod/canary", TemplateAvailable: true,
		Containers: []model.Container{{
			Name:     "canary",
			Liveness: detail(tcpHandler("8080"), timing(model.Seconds(30))),
		}},
		InitContainers: []string{"wait-for-db"},
	}}
	return report
}

// sidecarReport is a workload whose owner could not be read: a sidecar carries
// all three probes and is in scope, the sidecar beside it carries none, and
// without a template there is nothing to call drift.
func sidecarReport() *model.Report {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Rollout", Group: "argoproj.io", Name: "web", Namespace: "prod",
		DisplayName: "rollout.argoproj.io/web", PodCount: 1,
		Containers: []model.Container{
			{
				Name:      "web",
				Readiness: detail(grpcHandler("7070"), timing(model.Seconds(30))),
				Runtime:   &model.Runtime{Ready: 1, Total: 1, Failures: ptr.To(int32(0))},
				Pods: []model.Pod{{
					Name: "web-0", Ready: true, Started: ptr.To(true),
					Conditions: conditions(model.ConditionTrue, ago(45*time.Minute)),
				}},
			},
			{
				Name: "istio-proxy", Sidecar: true,
				// Events could not be read, so nothing can be said about
				// whether this one has been failing.
				Runtime: &model.Runtime{Ready: 1, Total: 1},
				Pods: []model.Pod{{
					Name: "web-0", Ready: true,
					Conditions: conditions(model.ConditionTrue, ago(45*time.Minute)),
				}},
				Findings: []model.Finding{{
					Rule:    "no-readiness-probe",
					Message: "No readiness probe is configured, so the kubelet reports this container ready the moment it starts and traffic arrives from then on.",
				}},
			},
		},
	}}
	return report
}

// written fills in the timing fields a spec actually wrote, leaving the rest
// for the Inspection to report at the kubelet's default.
func written(detail *model.ProbeDetail, set func(*model.Probe)) *model.ProbeDetail {
	set(detail.Config)
	return detail
}

// writtenProbe is written for a template's side of a drifted probe, which has
// no detail of its own.
func writtenProbe(probe *model.Probe, set func(*model.Probe)) *model.Probe {
	set(probe)
	return probe
}

// drift is a probe the template configures differently from the pod running it.
func drift(detail *model.ProbeDetail, template *model.Probe) *model.ProbeDetail {
	detail.Drifted = true
	detail.Template = template
	return detail
}

// threshold is a probe whose failureThreshold the spec wrote, which is what
// the sentence about consecutive failures counts.
func threshold(detail *model.ProbeDetail, failures int32) *model.ProbeDetail {
	detail.Config.FailureThreshold = &failures
	return detail
}

func conditions(status model.ConditionStatus, at *time.Time) []model.Condition {
	return []model.Condition{
		{Type: model.ConditionReady, Status: status, LastTransitionTime: at},
		{Type: model.ConditionContainersReady, Status: status, LastTransitionTime: at},
	}
}

func ago(d time.Duration) *time.Time {
	at := generatedAt().Add(-d)
	return &at
}

func headerHandler(path, port string) model.Handler {
	handler := httpHandler(path, port)
	handler.HTTPHeaders = []model.HTTPHeader{{Name: "X-Probe", Value: "kubelet"}}
	return handler
}
