// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// httpGet is the least interesting handler there is, so a timing test can say
// nothing about handlers at all.
func httpGet(path string) corev1.ProbeHandler {
	return corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(8080)},
	}
}

func durationString(d *model.Duration) string {
	if d == nil {
		return "-"
	}
	return d.String()
}

func TestTimingOf(t *testing.T) {
	tests := []struct {
		name                 string
		probe                model.ProbeType
		spec                 *corev1.Probe
		afterStartup         bool
		wantFirstCheck       string
		wantFailureDetection string
		wantStartupBudget    string
		wantTrafficDelay     string
		wantAfterStartup     bool
	}{
		{
			name:                 "liveness with everything left to the defaults",
			probe:                model.ProbeLiveness,
			spec:                 &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			wantFirstCheck:       "0s",
			wantFailureDetection: "30s",
			wantStartupBudget:    "-",
			wantTrafficDelay:     "-",
		},
		{
			name:                 "readiness with everything left to the defaults",
			probe:                model.ProbeReadiness,
			spec:                 &corev1.Probe{ProbeHandler: httpGet("/ready")},
			wantFirstCheck:       "0s",
			wantFailureDetection: "30s",
			wantStartupBudget:    "-",
			// One success is enough, so traffic arrives at the first check.
			wantTrafficDelay: "-",
		},
		{
			name:                 "startup with everything left to the defaults",
			probe:                model.ProbeStartup,
			spec:                 &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			wantFirstCheck:       "0s",
			wantFailureDetection: "30s",
			wantStartupBudget:    "30s",
			wantTrafficDelay:     "-",
		},
		{
			name:  "startup with a delay and a high failure threshold",
			probe: model.ProbeStartup,
			spec: &corev1.Probe{
				ProbeHandler:        httpGet("/healthz"),
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
				FailureThreshold:    30,
			},
			wantFirstCheck:       "5s",
			wantFailureDetection: "5m0s",
			wantStartupBudget:    "5m5s",
			wantTrafficDelay:     "-",
		},
		{
			name:  "readiness with a success threshold above one",
			probe: model.ProbeReadiness,
			spec: &corev1.Probe{
				ProbeHandler:     httpGet("/ready"),
				PeriodSeconds:    5,
				SuccessThreshold: 2,
			},
			wantFirstCheck:       "0s",
			wantFailureDetection: "15s",
			wantStartupBudget:    "-",
			wantTrafficDelay:     "5s",
		},
		{
			name:  "readiness with a success threshold above one behind a delay",
			probe: model.ProbeReadiness,
			spec: &corev1.Probe{
				ProbeHandler:        httpGet("/ready"),
				InitialDelaySeconds: 10,
				PeriodSeconds:       5,
				SuccessThreshold:    3,
				FailureThreshold:    4,
			},
			wantFirstCheck:       "10s",
			wantFailureDetection: "20s",
			wantStartupBudget:    "-",
			wantTrafficDelay:     "20s",
		},
		{
			name:  "liveness with every field written out",
			probe: model.ProbeLiveness,
			spec: &corev1.Probe{
				ProbeHandler:        httpGet("/healthz"),
				InitialDelaySeconds: 60,
				PeriodSeconds:       20,
				TimeoutSeconds:      30,
				FailureThreshold:    2,
			},
			wantFirstCheck:       "1m0s",
			wantFailureDetection: "40s",
			wantStartupBudget:    "-",
			wantTrafficDelay:     "-",
		},
		{
			name:                 "liveness behind a startup probe",
			probe:                model.ProbeLiveness,
			spec:                 &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			afterStartup:         true,
			wantFirstCheck:       "0s",
			wantFailureDetection: "30s",
			wantStartupBudget:    "-",
			wantTrafficDelay:     "-",
			wantAfterStartup:     true,
		},
		{
			name:                 "readiness behind a startup probe",
			probe:                model.ProbeReadiness,
			spec:                 &corev1.Probe{ProbeHandler: httpGet("/ready"), SuccessThreshold: 2},
			afterStartup:         true,
			wantFirstCheck:       "0s",
			wantFailureDetection: "30s",
			wantStartupBudget:    "-",
			wantTrafficDelay:     "10s",
			wantAfterStartup:     true,
		},
		{
			name:                 "startup is never relative to itself",
			probe:                model.ProbeStartup,
			spec:                 &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			afterStartup:         true,
			wantFirstCheck:       "0s",
			wantFailureDetection: "30s",
			wantStartupBudget:    "30s",
			wantTrafficDelay:     "-",
			wantAfterStartup:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timing := timingOf(tt.probe, effective(tt.spec), tt.afterStartup)
			if timing == nil {
				t.Fatal("timingOf returned no timing")
			}
			if got := timing.FirstCheck.String(); got != tt.wantFirstCheck {
				t.Errorf("FirstCheck = %s, want %s", got, tt.wantFirstCheck)
			}
			if got := timing.FailureDetection.String(); got != tt.wantFailureDetection {
				t.Errorf("FailureDetection = %s, want %s", got, tt.wantFailureDetection)
			}
			if got := durationString(timing.StartupBudget); got != tt.wantStartupBudget {
				t.Errorf("StartupBudget = %s, want %s", got, tt.wantStartupBudget)
			}
			if got := durationString(timing.TrafficDelay); got != tt.wantTrafficDelay {
				t.Errorf("TrafficDelay = %s, want %s", got, tt.wantTrafficDelay)
			}
			if timing.AfterStartup != tt.wantAfterStartup {
				t.Errorf("AfterStartup = %v, want %v", timing.AfterStartup, tt.wantAfterStartup)
			}
		})
	}
}

func TestTimingOfNothing(t *testing.T) {
	if timing := timingOf(model.ProbeLiveness, effective(nil), false); timing != nil {
		t.Errorf("timingOf with no probe = %+v, want nil", timing)
	}
}
