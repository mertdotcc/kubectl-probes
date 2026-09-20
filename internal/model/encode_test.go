// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

func TestEncodeGolden(t *testing.T) {
	tests := []struct {
		format Format
		golden string
	}{
		{format: FormatJSON, golden: "report.json"},
		{format: FormatYAML, golden: "report.yaml"},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			var buf bytes.Buffer
			if err := sampleReport().Encode(&buf, tt.format); err != nil {
				t.Fatalf("Encode(%s) failed: %v", tt.format, err)
			}

			path := filepath.Join("testdata", tt.golden)
			if *update {
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatalf("writing %s failed: %v", path, err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s failed: %v", path, err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("Encode(%s) does not match %s; rerun with -update to see the diff in git:\n%s",
					tt.format, path, buf.String())
			}
		})
	}
}

// The schema is an export, so what it writes has to read back as what it was.
func TestEncodeRoundTrip(t *testing.T) {
	for _, format := range []Format{FormatJSON, FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			var first bytes.Buffer
			if err := sampleReport().Encode(&first, format); err != nil {
				t.Fatalf("Encode(%s) failed: %v", format, err)
			}

			var decoded Report
			if err := yaml.Unmarshal(first.Bytes(), &decoded); err != nil {
				t.Fatalf("decoding %s failed: %v", format, err)
			}

			var second bytes.Buffer
			if err := decoded.Encode(&second, format); err != nil {
				t.Fatalf("re-encoding %s failed: %v", format, err)
			}
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Errorf("%s did not survive a round trip:\ngot:\n%s\nwant:\n%s",
					format, second.String(), first.String())
			}
		})
	}
}

func TestEncodeUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := sampleReport().Encode(&buf, Format("table")); err == nil {
		t.Fatalf("Encode(table) = nil, want an error")
	}
	if buf.Len() != 0 {
		t.Errorf("Encode(table) wrote %d bytes, want none", buf.Len())
	}
}

// at is the fixture's timestamp literal. The fixture is code, so a timestamp
// that does not parse is a mistake in this file and nothing a run can recover
// from.
func at(s string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("bad fixture timestamp " + s + ": " + err.Error())
	}
	return &parsed
}

// sampleReport is a Report built by hand rather than collected, covering what
// the schema has to survive: drift, a sidecar, a workload with no pods, an
// owner of a custom kind, and a cluster where events could not be read.
func sampleReport() *Report {
	r := New(time.Date(2026, 9, 19, 14, 30, 0, 0, time.UTC))
	r.Workloads = []Workload{
		{
			Kind:                "Deployment",
			Group:               "apps",
			Name:                "api",
			Namespace:           "prod",
			DisplayName:         DisplayName("apps", "Deployment", "api"),
			PodCount:            2,
			TerminatingPodCount: 1,
			TemplateAvailable:   true,
			Containers: []Container{
				{
					Name: "api",
					Startup: &ProbeDetail{
						Config: &Probe{
							Handler: Handler{
								Type:    HandlerHTTP,
								Summary: "GET /healthz:8080",
								Path:    "/healthz",
								Port:    "8080",
								Scheme:  "HTTP",
							},
							InitialDelaySeconds: ptr.To[int32](5),
							PeriodSeconds:       ptr.To[int32](10),
							FailureThreshold:    ptr.To[int32](30),
						},
						Timing: &Timing{
							FirstCheck:       Seconds(5),
							FailureDetection: Seconds(300),
							StartupBudget:    SecondsPtr(305),
						},
					},
					Readiness: &ProbeDetail{
						Config: &Probe{
							Handler: Handler{
								Type:    HandlerHTTP,
								Summary: "GET /ready:8080",
								Path:    "/ready",
								Port:    "8080",
								Scheme:  "HTTP",
								HTTPHeaders: []HTTPHeader{
									{Name: "Accept", Value: "application/json"},
								},
							},
							PeriodSeconds:    ptr.To[int32](5),
							SuccessThreshold: ptr.To[int32](2),
						},
						Timing: &Timing{
							FirstCheck:       Seconds(0),
							FailureDetection: Seconds(15),
							TrafficDelay:     SecondsPtr(5),
							AfterStartup:     true,
						},
					},
					// The running pods check liveness three times as often as
					// the template says they should.
					Liveness: &ProbeDetail{
						Config: &Probe{
							Handler: Handler{
								Type:    HandlerHTTP,
								Summary: "GET /healthz:8080",
								Path:    "/healthz",
								Port:    "8080",
								Scheme:  "HTTP",
							},
							PeriodSeconds:    ptr.To[int32](10),
							FailureThreshold: ptr.To[int32](3),
						},
						Timing: &Timing{
							FirstCheck:       Seconds(0),
							FailureDetection: Seconds(30),
							AfterStartup:     true,
						},
						Drifted: true,
						Template: &Probe{
							Handler: Handler{
								Type:    HandlerHTTP,
								Summary: "GET /healthz:8080",
								Path:    "/healthz",
								Port:    "8080",
								Scheme:  "HTTP",
							},
							PeriodSeconds:    ptr.To[int32](30),
							FailureThreshold: ptr.To[int32](3),
						},
					},
					Runtime: &Runtime{Ready: 1, Total: 2, Restarts: 4, Failures: ptr.To[int32](7)},
					Pods: []Pod{
						{
							Name:    "api-7d9f4c8b6d-2xk9v",
							Node:    "worker-1",
							Ready:   true,
							Started: ptr.To(true),
							Conditions: []Condition{
								{Type: ConditionReady, Status: ConditionTrue, LastTransitionTime: at("2026-09-19T13:58:12Z")},
								{Type: ConditionContainersReady, Status: ConditionTrue, LastTransitionTime: at("2026-09-19T13:58:12Z")},
							},
						},
						{
							Name:     "api-7d9f4c8b6d-9wq4t",
							Node:     "worker-2",
							Started:  ptr.To(true),
							Restarts: 4,
							LastTermination: &Termination{
								Reason:     "Error",
								ExitCode:   137,
								Signal:     9,
								FinishedAt: at("2026-09-19T14:21:47Z"),
							},
							Conditions: []Condition{
								{Type: ConditionReady, Status: ConditionFalse, LastTransitionTime: at("2026-09-19T14:22:03Z")},
								{Type: ConditionContainersReady, Status: ConditionFalse, LastTransitionTime: at("2026-09-19T14:22:03Z")},
							},
							Events: []EventGroup{
								{
									Probe:     ProbeLiveness,
									Count:     7,
									FirstSeen: at("2026-09-19T14:02:31Z"),
									LastSeen:  at("2026-09-19T14:29:11Z"),
									Message:   "Liveness probe failed: HTTP probe failed with statuscode: 503",
								},
							},
						},
					},
					Findings: []Finding{
						{
							Rule:    "timeout-at-default",
							Message: "startup, readiness, and liveness leave timeoutSeconds unset, so every check gives up after 1s.",
						},
					},
				},
				{
					Name:    "istio-proxy",
					Sidecar: true,
					Readiness: &ProbeDetail{
						Config: &Probe{
							Handler: Handler{
								Type:    HandlerHTTP,
								Summary: "GET /healthz/ready:15021",
								Path:    "/healthz/ready",
								Port:    "15021",
								Scheme:  "HTTP",
							},
							PeriodSeconds:    ptr.To[int32](15),
							TimeoutSeconds:   ptr.To[int32](3),
							FailureThreshold: ptr.To[int32](4),
						},
						Timing: &Timing{FirstCheck: Seconds(0), FailureDetection: Seconds(60)},
					},
					Runtime: &Runtime{Ready: 2, Total: 2, Failures: ptr.To[int32](0)},
					Pods: []Pod{
						{Name: "api-7d9f4c8b6d-2xk9v", Node: "worker-1", Ready: true, Started: ptr.To(true)},
						{Name: "api-7d9f4c8b6d-9wq4t", Node: "worker-2", Ready: true, Started: ptr.To(true)},
					},
				},
			},
		},
		{
			// Scaled to zero: everything here comes from the pod template, and
			// there is no runtime block pretending otherwise.
			Kind:              "StatefulSet",
			Group:             "apps",
			Name:              "archiver",
			Namespace:         "prod",
			DisplayName:       DisplayName("apps", "StatefulSet", "archiver"),
			TemplateAvailable: true,
			Containers: []Container{
				{
					Name: "archiver",
					Liveness: &ProbeDetail{
						Config: &Probe{
							Handler: Handler{
								Type:    HandlerExec,
								Summary: "exec /bin/health --deep",
								Command: []string{"/bin/health", "--deep"},
							},
							InitialDelaySeconds: ptr.To[int32](60),
							PeriodSeconds:       ptr.To[int32](20),
							TimeoutSeconds:      ptr.To[int32](30),
							FailureThreshold:    ptr.To[int32](2),
						},
						Timing: &Timing{FirstCheck: Seconds(60), FailureDetection: Seconds(40)},
					},
					Findings: []Finding{
						{
							Rule:    "no-readiness-probe",
							Message: "the container has no readiness probe, so it receives traffic as soon as it starts.",
						},
						{
							Rule:    "timeout-exceeds-period",
							Message: "liveness has a timeoutSeconds of 30 above its periodSeconds of 20, so checks overlap.",
						},
					},
				},
			},
		},
		{
			// A custom owner: no template to compare against, and a cluster
			// where events are forbidden, so failures are absent, not zero.
			Kind:        "Rollout",
			Group:       "argoproj.io",
			Name:        "checkout",
			Namespace:   "prod",
			DisplayName: DisplayName("argoproj.io", "Rollout", "checkout"),
			PodCount:    2,
			Containers: []Container{
				{
					Name: "checkout",
					Readiness: &ProbeDetail{
						Config: &Probe{
							Handler: Handler{
								Type:    HandlerGRPC,
								Summary: "grpc :9000 checkout.Health",
								Port:    "9000",
								Service: "checkout.Health",
							},
							PeriodSeconds: ptr.To[int32](10),
						},
						Timing: &Timing{FirstCheck: Seconds(0), FailureDetection: Seconds(30)},
					},
					Runtime: &Runtime{Ready: 2, Total: 2},
					Pods: []Pod{
						{Name: "checkout-64c6f8d5b9-ltz7p", Node: "worker-1", Ready: true},
						{Name: "checkout-64c6f8d5b9-v8rn2", Node: "worker-3", Ready: true},
					},
				},
			},
		},
	}
	return r
}
