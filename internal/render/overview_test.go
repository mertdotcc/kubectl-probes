// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"k8s.io/utils/ptr"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// The golden files are the table with colour disabled, and whether the test
// run happens to have a terminal must not change them.
func TestMain(m *testing.M) {
	color.NoColor = true
	os.Exit(m.Run())
}

func TestOverviewGolden(t *testing.T) {
	tests := []struct {
		name   string
		opts   Options
		golden string
	}{
		{name: "default", golden: "overview.txt"},
		{name: "all namespaces", opts: Options{AllNamespaces: true}, golden: "overview-all-namespaces.txt"},
		{name: "wide", opts: Options{AllNamespaces: true, Wide: true}, golden: "overview-wide.txt"},
		{name: "sort severity", opts: Options{Sort: SortSeverity}, golden: "overview-severity.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := Overview(&out, &errOut, sampleReport(), tt.opts); err != nil {
				t.Fatalf("Overview failed: %v", err)
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
				t.Errorf("Overview does not match %s; rerun with -update to see the diff in git:\n%s",
					path, out.String())
			}

			// The sample has both a drifted probe and a container whose
			// failures could not be counted, so every table says both.
			if got, want := errOut.String(), driftNote+"\n"+failuresNote+"\n"; got != want {
				t.Errorf("notes = %q, want %q", got, want)
			}
		})
	}
}

// Every column has to line up under its header however short its cells are,
// which is what the table is for.
func TestOverviewColumnsAlign(t *testing.T) {
	for _, opts := range []Options{{}, {AllNamespaces: true}, {AllNamespaces: true, Wide: true}} {
		var out, errOut bytes.Buffer
		if err := Overview(&out, &errOut, sampleReport(), opts); err != nil {
			t.Fatalf("Overview failed: %v", err)
		}

		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		want := columnStarts(lines[0])
		for _, line := range lines[1:] {
			if got := columnStarts(line); !equal(got, want) {
				t.Errorf("columns start at %v, want %v, in:\n%s", got, want, out.String())
			}
		}
	}
}

// A row with nothing in a cell is the one that tests alignment, because an
// empty cell is the one a table drops out of a line by mistake.
func TestOverviewEmptyCells(t *testing.T) {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Pod", Name: "orphan", DisplayName: "pod/orphan",
		Containers: []model.Container{{Name: "app"}},
	}}

	var out, errOut bytes.Buffer
	if err := Overview(&out, &errOut, report, Options{AllNamespaces: true}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if got, want := len(lines), 2; got != want {
		t.Fatalf("got %d lines, want %d:\n%s", got, want, out.String())
	}
	// The namespace is unknown, so the first cell is empty and every other
	// column still has to start where its header does.
	if got, want := columnStarts(lines[1]), columnStarts(lines[0]); !equal(got, want) {
		t.Errorf("columns start at %v, want %v, in:\n%s", got, want, out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("notes = %q, want none", errOut.String())
	}
}

// An empty result is not an error, and it is not a table with no rows either.
func TestOverviewEmptyReport(t *testing.T) {
	for _, report := range []*model.Report{nil, model.New(generatedAt())} {
		var out, errOut bytes.Buffer
		if err := Overview(&out, &errOut, report, Options{}); err != nil {
			t.Fatalf("Overview failed: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("table = %q, want none", out.String())
		}
		if got, want := errOut.String(), emptyNote+"\n"; got != want {
			t.Errorf("notes = %q, want %q", got, want)
		}
	}
}

// A table with nothing to explain explains nothing.
func TestOverviewQuietNotes(t *testing.T) {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{
		Kind: "Deployment", Group: "apps", Name: "api", Namespace: "prod",
		DisplayName: "deploy/api", PodCount: 1,
		Containers: []model.Container{{
			Name:      "api",
			Readiness: detail(httpHandler("/ready", "8080"), timing(model.Seconds(10))),
			Runtime:   &model.Runtime{Ready: 1, Total: 1, Failures: ptr.To(int32(0))},
		}},
	}}

	var out, errOut bytes.Buffer
	if err := Overview(&out, &errOut, report, Options{}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("notes = %q, want none", errOut.String())
	}
}

// Colour is the reason the table is measured before it is painted: a terminal
// gives an escape sequence no width, so adding colour must not move a column.
func TestOverviewColorKeepsAlignment(t *testing.T) {
	var plainOut, errOut bytes.Buffer
	if err := Overview(&plainOut, &errOut, sampleReport(), Options{AllNamespaces: true, Wide: true}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}

	color.NoColor = false
	t.Cleanup(func() { color.NoColor = true })

	var colored bytes.Buffer
	errOut.Reset()
	if err := Overview(&colored, &errOut, sampleReport(), Options{AllNamespaces: true, Wide: true}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}

	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatalf("no colour in:\n%s", colored.String())
	}
	if got, want := stripANSI(colored.String()), plainOut.String(); got != want {
		t.Errorf("coloured table does not lay out like the plain one:\ngot:\n%s\nwant:\n%s", got, want)
	}
	// Red says a container is failing now, and yellow that a rule has an
	// opinion about it.
	for _, want := range []string{red.Sprint("37"), red.Sprint(driftMark), yellow.Sprint("2"), gray.Sprint(absent)} {
		if !strings.Contains(colored.String(), want) {
			t.Errorf("table is missing %q:\n%s", want, colored.String())
		}
	}
}

func TestProbeCell(t *testing.T) {
	tests := []struct {
		name   string
		probe  model.ProbeType
		detail *model.ProbeDetail
		want   string
	}{
		{
			name:  "probe nobody configured",
			probe: model.ProbeLiveness,
			want:  absent,
		},
		{
			name:   "startup shows the budget it gives the container",
			probe:  model.ProbeStartup,
			detail: detail(httpHandler("/healthz", "8080"), startupTiming(model.Seconds(300))),
			want:   "http:5m",
		},
		{
			name:   "liveness shows how long a failure takes to notice",
			probe:  model.ProbeLiveness,
			detail: detail(httpHandler("/healthz", "8080"), timing(model.Seconds(90))),
			want:   "http:1m30s",
		},
		{
			name:   "exec handler names itself",
			probe:  model.ProbeReadiness,
			detail: detail(execHandler("pg_isready"), timing(model.Seconds(30))),
			want:   "exec:30s",
		},
		{
			name:   "a probe with no handler at all",
			probe:  model.ProbeReadiness,
			detail: detail(model.Handler{}, timing(model.Seconds(30))),
			want:   "?:30s",
		},
		{
			name:   "a probe with no timing derived",
			probe:  model.ProbeReadiness,
			detail: detail(httpHandler("/ready", "8080"), nil),
			want:   "http",
		},
		{
			name:   "a drifted probe wears a star",
			probe:  model.ProbeReadiness,
			detail: drifted(detail(httpHandler("/ready", "8080"), timing(model.Seconds(30)))),
			want:   "http:30s" + driftMark,
		},
		{
			name:   "a probe only the template configures",
			probe:  model.ProbeStartup,
			detail: &model.ProbeDetail{Drifted: true, Template: &model.Probe{Handler: httpHandler("/healthz", "8080")}},
			want:   absent + driftMark,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probeCell(tt.probe, tt.detail).text; got != tt.want {
				t.Errorf("probeCell() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShort(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{seconds: 0, want: "0s"},
		{seconds: 10, want: "10s"},
		{seconds: 30, want: "30s"},
		{seconds: 60, want: "1m"},
		{seconds: 90, want: "1m30s"},
		{seconds: 300, want: "5m"},
		{seconds: 3600, want: "1h"},
		{seconds: 5400, want: "1h30m"},
	}

	for _, tt := range tests {
		if got := short(model.Seconds(tt.seconds)); got != tt.want {
			t.Errorf("short(%ds) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestSortRows(t *testing.T) {
	tests := []struct {
		name  string
		order SortOrder
		want  []string
	}{
		{
			name:  "alphabetical by namespace, workload, then container",
			order: SortName,
			want:  []string{"api", "envoy", "db", "worker", "agent"},
		},
		{
			// Failures first, then restarts, then findings, then the quiet
			// rows, alphabetical within each band. db has an unknown failure
			// count, which is not evidence, so it bands with the quiet rows.
			name:  "severity bands, alphabetical within each",
			order: SortSeverity,
			want:  []string{"api", "agent", "envoy", "worker", "db"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := rowsOf(sampleReport())
			sortRows(rows, tt.order)

			var got []string
			for _, r := range rows {
				got = append(got, r.container)
			}
			if !equalStrings(got, tt.want) {
				t.Errorf("sortRows(%s) = %v, want %v", tt.order, got, tt.want)
			}
		})
	}
}

// sampleReport is a Report built by hand, carrying one of everything the
// Overview has to say something about: each handler type, a drifted probe, a
// probe only the template configures, probes nobody configured, a container
// whose failures could not be counted, and a workload with no pods at all.
//
// It spans two namespaces so that -A has something to show. A real run without
// -A sees one namespace, and the table simply leaves the column out.
func sampleReport() *model.Report {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{
		{
			Kind: "Deployment", Group: "apps", Name: "api", Namespace: "prod",
			DisplayName: "deploy/api", PodCount: 5, TemplateAvailable: true,
			Containers: []model.Container{
				{
					Name:      "api",
					Startup:   detail(httpHandler("/healthz", "8080"), startupTiming(model.Seconds(30))),
					Readiness: detail(httpHandler("/ready", "8080"), timing(model.Seconds(10))),
					Liveness:  drifted(detail(httpHandler("/healthz", "8080"), timing(model.Seconds(60)))),
					Runtime:   &model.Runtime{Ready: 4, Total: 5, Restarts: 12, Failures: ptr.To(int32(37))},
					Findings: []model.Finding{
						{Rule: "liveness-same-as-readiness", Message: "liveness and readiness ask the same question"},
						{Rule: "timeout-at-default", Message: "liveness times out after 1s"},
					},
				},
				{
					Name: "envoy", Sidecar: true,
					Readiness: detail(tcpHandler("9901"), timing(model.Seconds(15))),
					Runtime:   &model.Runtime{Ready: 5, Total: 5, Failures: ptr.To(int32(0))},
					Findings:  []model.Finding{{Rule: "no-readiness-probe", Message: "nothing keeps traffic away"}},
				},
			},
		},
		{
			Kind: "StatefulSet", Group: "apps", Name: "db", Namespace: "prod",
			DisplayName: "sts/db", PodCount: 3, TemplateAvailable: true,
			Containers: []model.Container{{
				Name:      "db",
				Startup:   detail(execHandler("pg_isready"), startupTiming(model.Seconds(300))),
				Readiness: detail(execHandler("pg_isready"), timing(model.Seconds(30))),
				// Events were forbidden, so the failure count is unknown.
				Runtime: &model.Runtime{Ready: 3, Total: 3},
			}},
		},
		{
			Kind: "Deployment", Group: "apps", Name: "worker", Namespace: "staging",
			DisplayName: "deploy/worker", TemplateAvailable: true,
			Containers: []model.Container{{
				Name:     "worker",
				Findings: []model.Finding{{Rule: "no-readiness-probe", Message: "nothing keeps traffic away"}},
			}},
		},
		{
			Kind: "DaemonSet", Group: "apps", Name: "agent", Namespace: "staging",
			DisplayName: "ds/agent", PodCount: 8, TemplateAvailable: true,
			Containers: []model.Container{{
				Name: "agent",
				// The template configures a startup probe the running pods do
				// not have, which is drift with no configuration of its own.
				Startup:   &model.ProbeDetail{Drifted: true, Template: &model.Probe{Handler: grpcHandler("7070")}},
				Readiness: detail(grpcHandler("7070"), timing(model.Seconds(30))),
				Liveness:  detail(grpcHandler("7070"), timing(model.Seconds(90))),
				Runtime:   &model.Runtime{Ready: 8, Total: 8, Restarts: 3, Failures: ptr.To(int32(0))},
			}},
		},
	}
	return report
}

func generatedAt() time.Time {
	return time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
}

func detail(handler model.Handler, timing *model.Timing) *model.ProbeDetail {
	return &model.ProbeDetail{Config: &model.Probe{Handler: handler}, Timing: timing}
}

func drifted(detail *model.ProbeDetail) *model.ProbeDetail {
	detail.Drifted = true
	detail.Template = &model.Probe{Handler: detail.Config.Handler}
	return detail
}

func timing(detection model.Duration) *model.Timing {
	return &model.Timing{FailureDetection: detection}
}

func startupTiming(budget model.Duration) *model.Timing {
	return &model.Timing{FailureDetection: budget, StartupBudget: &budget}
}

func httpHandler(path, port string) model.Handler {
	return model.Handler{
		Type: model.HandlerHTTP, Path: path, Port: port, Scheme: "HTTP",
		Summary: "GET " + path + ":" + port,
	}
}

func tcpHandler(port string) model.Handler {
	return model.Handler{Type: model.HandlerTCP, Port: port, Summary: "tcp :" + port}
}

func grpcHandler(port string) model.Handler {
	return model.Handler{Type: model.HandlerGRPC, Port: port, Summary: "grpc :" + port}
}

func execHandler(command ...string) model.Handler {
	return model.Handler{
		Type: model.HandlerExec, Command: command,
		Summary: "exec " + strings.Join(command, " "),
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// columnStarts is the offset of every column in an aligned line: the start of
// the line, and everything that follows the two spaces or more a table puts
// between cells. A single space is one inside a cell, such as the one in an
// exec command or an http handler.
func columnStarts(line string) []int {
	starts := []int{0}
	for i := 2; i < len(line); i++ {
		if line[i] != ' ' && line[i-1] == ' ' && line[i-2] == ' ' {
			starts = append(starts, i)
		}
	}
	return starts
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
