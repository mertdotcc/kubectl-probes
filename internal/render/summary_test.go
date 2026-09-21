// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// sampleSummary is the ADR's example Summary, figure for figure, so the golden
// file can be read against the layout the ADR draws.
func sampleSummary() *model.Summary {
	at := func(p, seconds int, namespace, workload, container string) model.Percentile {
		return model.Percentile{Percentile: p, FailureDetection: model.Seconds(seconds),
			Namespace: namespace, Workload: workload, Container: container}
	}
	buckets := func(counts ...int) []model.Bucket {
		var out []model.Bucket
		for i, seconds := range []int{10, 30, 60, 120, 300} {
			out = append(out, model.Bucket{UpTo: model.SecondsPtr(seconds), Count: counts[i]})
		}
		return append(out, model.Bucket{Count: counts[5]})
	}

	return &model.Summary{
		Containers: 214,
		Workloads:  61,
		Coverage: model.Coverage{
			Startup:           model.Share{Count: 29, Of: 214},
			Readiness:         model.Share{Count: 171, Of: 202},
			Liveness:          model.Share{Count: 133, Of: 214},
			None:              model.Share{Count: 31, Of: 214},
			ReadinessExcluded: 12,
		},
		FailureDetection: model.FailureDetection{
			Readiness: &model.Distribution{
				Containers: 171,
				Percentiles: []model.Percentile{
					at(100, 180, "observability", "sts/loki", "loki"),
					at(99, 150, "payments", "deploy/ledger", "ledger"),
					at(90, 90, "shop", "deploy/web", "web"),
					at(50, 30, "shop", "deploy/api", "api"),
					at(10, 10, "kube-system", "deploy/coredns", "coredns"),
					at(0, 3, "kube-system", "node/cp-1", "etcd"),
				},
				Buckets: buckets(18, 97, 31, 19, 5, 1),
			},
			Liveness: &model.Distribution{
				Containers: 133,
				Percentiles: []model.Percentile{
					at(100, 600, "observability", "sts/loki", "loki"),
					at(99, 300, "payments", "deploy/ledger", "ledger"),
					at(90, 90, "shop", "deploy/web", "web"),
					at(50, 30, "shop", "deploy/api", "api"),
					at(10, 30, "kube-system", "deploy/coredns", "coredns"),
					at(0, 10, "kube-system", "node/cp-1", "etcd"),
				},
				Buckets: buckets(20, 70, 30, 8, 4, 1),
			},
		},
	}
}

func TestSummaryGolden(t *testing.T) {
	noLiveness := sampleSummary()
	noLiveness.Coverage.Liveness.Count = 0
	noLiveness.FailureDetection.Liveness = nil

	tests := []struct {
		name    string
		summary *model.Summary
		opts    Options
		golden  string
	}{
		{name: "all namespaces", summary: sampleSummary(), opts: Options{AllNamespaces: true}, golden: "summary-all-namespaces.txt"},
		// Every row already carries the namespace the user named, so, as in
		// the table, it is only printed with -A.
		{name: "one namespace", summary: sampleSummary(), golden: "summary.txt"},
		// A probe no container has gets its coverage line and nothing else.
		{name: "no liveness probes", summary: noLiveness, opts: Options{AllNamespaces: true}, golden: "summary-no-liveness.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := sampleReport()
			report.Summary = tt.summary
			tt.opts.SummaryOnly = true

			var out, errOut bytes.Buffer
			if err := Overview(&out, &errOut, report, tt.opts); err != nil {
				t.Fatalf("Overview failed: %v", err)
			}
			checkRenderGolden(t, tt.golden, out.Bytes())
			// The notes explain marks in the table, and there is no table.
			if errOut.Len() != 0 {
				t.Errorf("notes = %q, want none without the table", errOut.String())
			}
		})
	}
}

// The Summary follows the table after one blank line, and without one the
// table is all there is.
func TestOverviewWithSummary(t *testing.T) {
	var tableOnly, errOut bytes.Buffer
	if err := Overview(&tableOnly, &errOut, sampleReport(), Options{}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}

	report := sampleReport()
	report.Summary = sampleSummary()
	var both bytes.Buffer
	errOut.Reset()
	if err := Overview(&both, &errOut, report, Options{}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}

	rest, ok := strings.CutPrefix(both.String(), tableOnly.String()+"\n")
	if !ok {
		t.Fatalf("the output does not open with the table and a blank line:\n%s", both.String())
	}
	if !strings.HasPrefix(rest, "Summary: 214 containers in 61 workloads\n") {
		t.Errorf("the Summary does not follow the blank line:\n%s", rest)
	}
	// The notes still explain the table's marks.
	if got, want := errOut.String(), driftNote+"\n"+failuresNote+"\n"; got != want {
		t.Errorf("notes = %q, want %q", got, want)
	}
}

// One container in one workload is said in the singular.
func TestSummarySingular(t *testing.T) {
	report := model.New(generatedAt())
	report.Workloads = []model.Workload{{Kind: "Job", Group: "batch", Name: "once", DisplayName: "job/once",
		Containers: []model.Container{{Name: "once"}}}}
	report.Summary = &model.Summary{
		Containers: 1,
		Workloads:  1,
		Coverage: model.Coverage{
			Startup:           model.Share{Of: 1},
			Liveness:          model.Share{Of: 1},
			None:              model.Share{Count: 1, Of: 1},
			ReadinessExcluded: 1,
		},
	}

	var out, errOut bytes.Buffer
	if err := Overview(&out, &errOut, report, Options{SummaryOnly: true}); err != nil {
		t.Fatalf("Overview failed: %v", err)
	}
	for _, want := range []string{
		"Summary: 1 container in 1 workload\n",
		// Nothing is left to count readiness over, which is no percentage
		// at all rather than 0% of nothing.
		"  readiness   0 / 0     -   (1 Job and CronJob container not counted)\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the Summary does not say %q:\n%s", want, out.String())
		}
	}
}

func checkRenderGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing %s failed: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output does not match %s; rerun with -update to see the diff in git:\n%s", path, got)
	}
}
