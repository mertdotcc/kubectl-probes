// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatih/color"
	"sigs.k8s.io/yaml"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The manifests these tests are driven with. They hold no pods, because what
// is under test here is the flags reaching the pipeline and not the pipeline
// itself, which integration/ covers against saved API server output.
var (
	// one workload: deploy/api in prod.
	oneWorkload = []string{"-f", filepath.Join("testdata", "api.yaml")}
	// three workloads: deploy/api and sts/zookeeper in prod, deploy/edge in
	// staging. Only zookeeper has findings.
	threeWorkloads = append(append([]string{}, oneWorkload...),
		"-f", filepath.Join("testdata", "more.yaml"))
)

// Colour would put escapes in everything asserted below, and whether the test
// run happens to have a terminal or a kubeconfig must not change what the
// command prints.
func TestMain(m *testing.M) {
	color.NoColor = true
	os.Setenv("KUBECONFIG", filepath.Join(os.TempDir(), "kubectl-probes-no-such-kubeconfig"))
	os.Exit(m.Run())
}

// The Overview is what a run that named nothing gets, in the shape the flags
// asked for.
func TestRunOverview(t *testing.T) {
	tests := []struct {
		name string
		args []string
		// want and absent are substrings of stdout: the columns and rows that
		// say which surface was chosen and how it was told to print.
		want   []string
		absent []string
	}{
		{
			name: "the default table",
			args: threeWorkloads,
			want: []string{"WORKLOAD", "CONTAINER", "READINESS", "FINDINGS", "deploy/api", "sts/zookeeper"},
			// Every row carries the namespace the user already named, so the
			// column is only worth printing with -A.
			absent: []string{"NAMESPACE", "READINESS-HANDLER"},
		},
		{
			name:   "-A adds the namespace column",
			args:   append([]string{"-A"}, threeWorkloads...),
			want:   []string{"NAMESPACE", "prod", "staging", "deploy/edge"},
			absent: []string{"READINESS-HANDLER"},
		},
		{
			name: "-o wide adds the handlers and the drift column",
			args: append([]string{"-o", "wide"}, threeWorkloads...),
			want: []string{"READINESS-HANDLER", "LIVENESS-HANDLER", "DRIFT", "GET /ready:http"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := run(t, tt.args...)
			for _, want := range tt.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("the table does not mention %q:\n%s", want, stdout)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(stdout, absent) {
					t.Errorf("the table mentions %q, which these flags do not ask for:\n%s", absent, stdout)
				}
			}
			if stderr != "" {
				t.Errorf("notes = %q, want none: nothing here drifts and nothing was forbidden", stderr)
			}
		})
	}
}

// --sort severity puts the rows worth looking at first, which is the one thing
// that tells it apart from the default order here.
func TestRunSortSeverity(t *testing.T) {
	byName, _ := run(t, threeWorkloads...)
	bySeverity, _ := run(t, append([]string{"--sort", "severity"}, threeWorkloads...)...)

	// Alphabetical by namespace first, then workload, whether or not the
	// namespace column is printed.
	if got := workloadColumn(byName); !equal(got, []string{"deploy/api", "sts/zookeeper", "deploy/edge"}) {
		t.Errorf("the default order is %v, want it alphabetical", got)
	}
	// zookeeper is the only workload a rule has an opinion about, and it
	// sorts last by name.
	if got := workloadColumn(bySeverity); !equal(got, []string{"sts/zookeeper", "deploy/api", "deploy/edge"}) {
		t.Errorf("--sort severity gives %v, want the workload with findings first", got)
	}
}

// The Inspection is the view of a single workload, so it is what a run that
// named one gets, and only then.
func TestRunInspection(t *testing.T) {
	stdout, stderr := run(t, append([]string{"deploy/api"}, oneWorkload...)...)

	for _, want := range []string{"deploy/api -n prod", "container api", "Configuration", "Effective timing", "Runtime state"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the Inspection does not mention %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "WORKLOAD  CONTAINER") {
		t.Errorf("naming one workload printed the Overview:\n%s", stdout)
	}
	// The manifest is the template, so there is nothing the view could not
	// say about it.
	if stderr != "" {
		t.Errorf("notes = %q, want none", stderr)
	}
}

// An argument that does not narrow the run to one workload leaves the Overview
// in place: there is no reading of the Inspection that covers three at once.
func TestRunInspectionNeedsOneWorkload(t *testing.T) {
	stdout, _ := run(t, append([]string{"deploy/api"}, threeWorkloads...)...)
	if !strings.Contains(stdout, "WORKLOAD") || !strings.Contains(stdout, "sts/zookeeper") {
		t.Errorf("want the Overview of all three workloads:\n%s", stdout)
	}
}

// -o json and -o yaml carry the same Report, which is the point of both of
// them being encodings of one model.
func TestRunStructuredOutput(t *testing.T) {
	stdout, stderr := run(t, append([]string{"-o", "json"}, threeWorkloads...)...)
	fromJSON := decode(t, "json", stdout)
	if stderr != "" {
		t.Errorf("stderr = %q, want a pipe carrying the report and nothing else", stderr)
	}

	if fromJSON.APIVersion != model.APIVersion || fromJSON.Kind != model.Kind {
		t.Errorf("the report is %s %s, want %s %s",
			fromJSON.APIVersion, fromJSON.Kind, model.APIVersion, model.Kind)
	}
	if len(fromJSON.Workloads) != 3 {
		t.Fatalf("the report has %d workloads, want the 3 in the manifests", len(fromJSON.Workloads))
	}

	yamlOut, _ := run(t, append([]string{"-o", "yaml"}, threeWorkloads...)...)
	fromYAML := decode(t, "yaml", yamlOut)
	if !fromYAML.GeneratedAt.Equal(fromJSON.GeneratedAt) {
		// Both runs stamp their own moment, so only the shape is comparable.
		fromYAML.GeneratedAt = fromJSON.GeneratedAt
	}
	if !equal(names(fromYAML), names(fromJSON)) {
		t.Errorf("yaml carries %v and json carries %v", names(fromYAML), names(fromJSON))
	}
}

// --no-findings is applied where the findings are produced, so a run under it
// carries none in any format.
func TestRunNoFindings(t *testing.T) {
	withFindings := decode(t, "json", mustRun(t, append([]string{"-o", "json"}, threeWorkloads...)...))
	if findings(withFindings) == 0 {
		t.Fatal("the manifests produce no findings, so --no-findings proves nothing")
	}

	without := decode(t, "json", mustRun(t, append([]string{"-o", "json", "--no-findings"}, threeWorkloads...)...))
	if got := findings(without); got != 0 {
		t.Errorf("--no-findings left %d findings in the report", got)
	}

	// The table counts them too, and counts none.
	stdout, _ := run(t, append([]string{"--no-findings"}, threeWorkloads...)...)
	for _, line := range workloadRows(stdout) {
		if fields := strings.Fields(line); fields[len(fields)-1] != "0" {
			t.Errorf("--no-findings still counts findings in %q", line)
		}
	}
}

// A file with nothing probe-bearing in it is not an error. The note goes to
// stderr so that a pipe carries the table and nothing else.
func TestRunNoWorkloads(t *testing.T) {
	stdout, stderr := run(t, "-f", filepath.Join("testdata", "no-workloads.yaml"))
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing where there is no table to print", stdout)
	}
	if stderr != "No workloads found.\n" {
		t.Errorf("stderr = %q, want the empty note", stderr)
	}
}

// A bad flag value fails before anything is read, and says what it would have
// accepted.
func TestRunRejectsUnknownFlagValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "output", args: []string{"-o", "bogus"}, want: `invalid value "bogus" for --output`},
		{name: "color", args: []string{"-c", "bogus"}, want: `invalid value "bogus" for --color`},
		{name: "sort", args: []string{"--sort", "bogus"}, want: `invalid value "bogus" for --sort`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := execute(append(tt.args, oneWorkload...)...)
			if err == nil {
				t.Fatal("a bad flag value was accepted")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// flagUsage is the line the flag list gives a flag, which is where the help
// for it is. The examples above it mention flags too, so it is the listing
// that is looked for and not the name.
func flagUsage(help, flag string) (string, bool) {
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), flag+" ") {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

// execute drives the root command the way main does, with both streams
// captured. Nothing here touches a cluster: every test passes -f.
func execute(args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()

	return out.String(), errOut.String(), err
}

func run(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	out, errOut, err := execute(args...)
	if err != nil {
		t.Fatalf("kubectl probes %s failed: %v", strings.Join(args, " "), err)
	}
	return out, errOut
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	stdout, _ := run(t, args...)
	return stdout
}

func decode(t *testing.T, format, in string) *model.Report {
	t.Helper()
	report := &model.Report{}
	var err error
	if format == "json" {
		err = json.Unmarshal([]byte(in), report)
	} else {
		err = yaml.Unmarshal([]byte(in), report)
	}
	if err != nil {
		t.Fatalf("the %s output does not parse: %v\n%s", format, err, in)
	}
	return report
}

// workloadRows are the table's rows without its header, and workloadColumn is
// the workload each one names, which is the order the rows came in.
func workloadRows(table string) []string {
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if line != "" && !strings.HasPrefix(line, "WORKLOAD") && !strings.HasPrefix(line, "NAMESPACE") {
			rows = append(rows, line)
		}
	}
	return rows
}

func workloadColumn(table string) []string {
	var workloads []string
	for _, row := range workloadRows(table) {
		workloads = append(workloads, strings.Fields(row)[0])
	}
	return workloads
}

func names(report *model.Report) []string {
	out := make([]string, 0, len(report.Workloads))
	for _, workload := range report.Workloads {
		out = append(out, workload.DisplayName)
	}
	return out
}

func findings(report *model.Report) int {
	var count int
	for _, workload := range report.Workloads {
		for _, container := range workload.Containers {
			count += len(container.Findings)
		}
	}
	return count
}

func equal(a, b []string) bool {
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
