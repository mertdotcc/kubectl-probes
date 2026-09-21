// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Package integration runs the whole plugin end to end against saved API
// server output, with no cluster and no network.
//
// The files in testdata/cluster are one namespace as `kubectl get -o json`
// would have written it: pods, the ReplicaSets, Deployments, StatefulSets,
// Job, CronJob and Rollout that own them, and the events recorded against
// them. They go in through the same resource.Builder the plugin uses for -f,
// then through collect's owner reduction, analyze, and render, so a change to
// any of those shows up here as a golden diff.
//
// The ReplicaSets carry a deliberately bare pod template. A ReplicaSet is
// never a workload, so nothing should ever read it, and a reduction that
// broke would print those bare containers instead of the ones below.
package integration

import (
	"bytes"
	"context"
	"flag"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fatih/color"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/mertdotcc/kubectl-probes/internal/analyze"
	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
	"github.com/mertdotcc/kubectl-probes/internal/render"
)

// generatedAt is the moment every golden file is written as of. The ages the
// Inspection prints are counted from it, so it is fixed here and the fixture
// timestamps are written relative to it.
var generatedAt = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// The golden files are what the plugin writes with colour off, and neither a
// terminal nor a kubeconfig on the machine running the tests may change them.
func TestMain(m *testing.M) {
	color.NoColor = true
	os.Setenv("KUBECONFIG", filepath.Join(os.TempDir(), "kubectl-probes-no-such-kubeconfig"))
	os.Exit(m.Run())
}

func TestPipeline(t *testing.T) {
	report := pipeline(t, analyze.Options{})

	// The Overview carries the Summary unless --summary=hide asks it not to,
	// and --summary=only is the Summary with no workloads beside it. The
	// Inspection never has one, so it is built from the report without.
	overview := *report
	overview.Summary = analyze.Summarize(report)
	summaryOnly := overview
	summaryOnly.Workloads = nil

	tests := []struct {
		name   string
		golden string
		// notes is what the surface writes to stderr, which is everything it
		// could not say in the output itself.
		notes string
		write func(out, errOut io.Writer) error
	}{
		{
			name:   "overview",
			golden: "overview.txt",
			notes:  "* running config differs from workload template\n",
			write: func(out, errOut io.Writer) error {
				return render.Overview(out, errOut, &overview, render.Options{})
			},
		},
		{
			// The notes explain marks in the table, and there is no table.
			name:   "summary only",
			golden: "summary-only.txt",
			write: func(out, errOut io.Writer) error {
				return render.Overview(out, errOut, &overview, render.Options{SummaryOnly: true})
			},
		},
		{
			name:   "overview wide",
			golden: "overview-wide.txt",
			notes:  "* running config differs from workload template\n",
			write: func(out, errOut io.Writer) error {
				return render.Overview(out, errOut, &overview, render.Options{Wide: true})
			},
		},
		{
			name:   "json report",
			golden: "report.json",
			write: func(out, _ io.Writer) error {
				return overview.Encode(out, model.FormatJSON)
			},
		},
		{
			name:   "json summary only",
			golden: "summary-only.json",
			write: func(out, _ io.Writer) error {
				return summaryOnly.Encode(out, model.FormatJSON)
			},
		},
		{
			name:   "yaml report",
			golden: "report.yaml",
			write: func(out, _ io.Writer) error {
				return overview.Encode(out, model.FormatYAML)
			},
		},
		{
			// A Deployment mid-rollout: one pod on the new ReplicaSet, one
			// still on the old one with a readiness probe the template no
			// longer asks for, and one on its way out.
			name:   "inspect a drifting deployment",
			golden: "inspect-api.txt",
			write: func(out, errOut io.Writer) error {
				return render.Inspection(out, errOut, only(t, report, "deploy/api"))
			},
		},
		{
			// A StatefulSet restart looping, with the Unhealthy events that
			// say why.
			name:   "inspect a restart looping statefulset",
			golden: "inspect-db.txt",
			write: func(out, errOut io.Writer) error {
				return render.Inspection(out, errOut, only(t, report, "sts/db"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := tt.write(&out, &errOut); err != nil {
				t.Fatalf("rendering failed: %v", err)
			}
			checkGolden(t, tt.golden, out.Bytes())
			if got := errOut.String(); got != tt.notes {
				t.Errorf("notes = %q, want %q", got, tt.notes)
			}
		})
	}
}

// workloadSummary is a workload as the reduction leaves it: which pods ended
// up under it, and which containers it reports.
type workloadSummary struct {
	pods        int
	terminating int
	containers  []string
}

// The reduction is what the goldens below are all built on, and a failure here
// says which workload went wrong where a golden diff would only say that
// everything moved.
func TestPipelineReducesPodsToTopMostOwners(t *testing.T) {
	report := pipeline(t, analyze.Options{})

	want := map[string]workloadSummary{
		// The two ReplicaSets collapse into the Deployment, and the pod on
		// its way out is counted apart from the two that are running.
		"deploy/api": {pods: 2, terminating: 1, containers: []string{"api"}},
		// Scaled to zero, and present only because its template is.
		"sts/cache": {containers: []string{"cache"}},
		// A custom owner, reached through the ReplicaSet it owns.
		"rollout.argoproj.io/checkout": {pods: 1, containers: []string{"checkout"}},
		"sts/db":                       {pods: 2, containers: []string{"db"}},
		// A pod with no owner is its own workload.
		"pod/debug":  {pods: 1, containers: []string{"debug"}},
		"job/import": {pods: 1, containers: []string{"import"}},
		// The Job collapses into the CronJob that created it.
		"cj/nightly": {pods: 1, containers: []string{"report"}},
		// The sidecar is a container of the workload; the plain init
		// container is named but is not one.
		"deploy/web": {pods: 1, containers: []string{"web", "istio-proxy"}},
	}

	got := map[string]workloadSummary{}
	for _, w := range report.Workloads {
		containers := make([]string, 0, len(w.Containers))
		for _, container := range w.Containers {
			containers = append(containers, container.Name)
		}
		got[w.DisplayName] = workloadSummary{pods: w.PodCount, terminating: w.TerminatingPodCount, containers: containers}
	}

	if len(got) != len(want) {
		t.Errorf("the report has %d workloads, want %d: %v", len(got), len(want), names(got))
	}
	for name, want := range want {
		have, ok := got[name]
		if !ok {
			t.Errorf("%s is not in the report; it has %v", name, names(got))
			continue
		}
		if have.pods != want.pods || have.terminating != want.terminating {
			t.Errorf("%s has %d pods and %d terminating, want %d and %d",
				name, have.pods, have.terminating, want.pods, want.terminating)
		}
		if !slices.Equal(have.containers, want.containers) {
			t.Errorf("%s has containers %v, want %v", name, have.containers, want.containers)
		}
	}
}

// --no-findings is applied where the findings are produced, so a report made
// under it carries none in any format.
func TestPipelineNoFindings(t *testing.T) {
	report := pipeline(t, analyze.Options{NoFindings: true})
	for _, workload := range report.Workloads {
		for _, container := range workload.Containers {
			if len(container.Findings) > 0 {
				t.Errorf("%s/%s carries %d findings under --no-findings",
					workload.DisplayName, container.Name, len(container.Findings))
			}
		}
	}
}

// pipeline is the run itself: every fixture file through the Builder, the
// owner reduction, and the rules, exactly as a -f run of the plugin does it.
func pipeline(t *testing.T, opts analyze.Options) *model.Report {
	t.Helper()

	result, err := collect.Collect(context.Background(), collect.Options{
		ConfigFlags: genericclioptions.NewConfigFlags(true),
		Filenames:   fixtures(t),
	})
	if err != nil {
		t.Fatalf("collecting the fixtures failed: %v", err)
	}
	if result.Gaps.Events || len(result.Gaps.Owners) > 0 {
		t.Fatalf("the fixtures reported gaps the files do not have: %+v", result.Gaps)
	}
	return analyze.Analyze(result, generatedAt, opts)
}

func fixtures(t *testing.T) []string {
	t.Helper()
	// Sorted, because the file order is what the Builder hands back and a
	// report that depends on it is a report that changes for no reason.
	files, err := filepath.Glob(filepath.Join("testdata", "cluster", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in testdata/cluster: %v", err)
	}
	return files
}

// only narrows the report to one workload, the way the command narrows it when
// the positional argument names one.
func only(t *testing.T, report *model.Report, displayName string) *model.Report {
	t.Helper()
	for _, workload := range report.Workloads {
		if workload.DisplayName == displayName {
			narrowed := *report
			narrowed.Workloads = []model.Workload{workload}
			return &narrowed
		}
	}
	t.Fatalf("%s is not in the report", displayName)
	return nil
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)

	if *update || os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing %s failed: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v; rerun with UPDATE_GOLDEN=1 to write it", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output does not match %s; rerun with UPDATE_GOLDEN=1 to see the diff in git:\n%s",
			path, got)
	}
}

func names(m map[string]workloadSummary) []string {
	return slices.Sorted(maps.Keys(m))
}

// A -f run against a manifest rather than a dump: there are no pods to reduce,
// and the template is the only thing there is to report.
func TestManifestWithoutPods(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		workload  string
		container string
	}{
		{
			// The Service in the same file has no probes to report and is
			// not an error.
			name:      "a deployment that has not been applied",
			file:      "unapplied.yaml",
			workload:  "deploy/shipping",
			container: "shipping",
		},
		{
			// Nothing in the file owns it, so it is all the file has to say.
			name:      "a replicaset without its deployment",
			file:      "orphan-replicaset.yaml",
			workload:  "replicaset.apps/legacy-6f4b9c",
			container: "legacy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := collect.Collect(context.Background(), collect.Options{
				ConfigFlags: genericclioptions.NewConfigFlags(true),
				Filenames:   []string{filepath.Join("testdata", "manifest", tt.file)},
			})
			if err != nil {
				t.Fatalf("collecting %s failed: %v", tt.file, err)
			}

			report := analyze.Analyze(result, generatedAt, analyze.Options{})
			if len(report.Workloads) != 1 {
				t.Fatalf("%s produced %d workloads, want 1", tt.file, len(report.Workloads))
			}
			workload := report.Workloads[0]
			if workload.DisplayName != tt.workload {
				t.Errorf("workload = %s, want %s", workload.DisplayName, tt.workload)
			}
			if workload.PodCount != 0 {
				t.Errorf("%s has %d pods, want none: the file holds no pods", tt.workload, workload.PodCount)
			}
			if !workload.TemplateAvailable {
				t.Errorf("%s reports no template, which is the only thing a manifest has", tt.workload)
			}
			if len(workload.Containers) != 1 || workload.Containers[0].Name != tt.container {
				t.Fatalf("%s has containers %+v, want one named %s", tt.workload, workload.Containers, tt.container)
			}
			// Nothing is running it, so there is nothing to compare against
			// and nothing to report as runtime state.
			if container := workload.Containers[0]; container.Runtime != nil {
				t.Errorf("%s reports runtime state for a workload with no pods: %+v", tt.container, container.Runtime)
			}
		})
	}
}
