// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// probed is a container as the rules see it, with the kubelet's defaults
// applied to whatever probes a case configures.
func probed(container corev1.Container) ruleInput {
	return ruleInputFor(&container, &model.Container{})
}

// messages is what a rule said, checked to carry the rule's own name, which is
// the part of a finding a script keys on.
func messages(t *testing.T, rule string, findings []model.Finding) []string {
	t.Helper()
	var out []string
	for _, finding := range findings {
		if finding.Rule != rule {
			t.Errorf("finding names rule %q, want %q", finding.Rule, rule)
		}
		out = append(out, finding.Message)
	}
	return out
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

// tcpSocket is a handler that is not an httpGet, for the cases that are about
// handlers rather than about timing.
func tcpSocket(port int32) corev1.ProbeHandler {
	return corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(port)}}
}

func exec(command ...string) corev1.ProbeHandler {
	return corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: command}}
}

func TestNoReadinessProbe(t *testing.T) {
	const message = "No readiness probe is configured, so the kubelet reports this container ready the moment it starts and traffic arrives from then on."

	tests := []struct {
		name      string
		container corev1.Container
		want      []string
	}{
		{
			name:      "a container with no probes at all",
			container: corev1.Container{Name: "api"},
			want:      []string{message},
		},
		{
			name: "a container watched for death but not for readiness",
			container: corev1.Container{
				Name:          "api",
				LivenessProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
				StartupProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			},
			want: []string{message},
		},
		{
			name: "a container with a readiness probe",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messages(t, ruleNoReadinessProbe, noReadinessProbe(probed(tt.container)))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLivenessWithoutStartup(t *testing.T) {
	tests := []struct {
		name      string
		container corev1.Container
		want      []string
	}{
		{
			name: "liveness held off for half a minute with nothing watching the start",
			container: corev1.Container{
				Name: "api",
				LivenessProbe: &corev1.Probe{
					ProbeHandler:        httpGet("/healthz"),
					InitialDelaySeconds: 30,
				},
			},
			want: []string{"Liveness waits 30s before its first check and there is no startup probe, so a container slower than that to come up is restarted while it is still starting."},
		},
		{
			name: "liveness held off for two minutes",
			container: corev1.Container{
				Name: "api",
				LivenessProbe: &corev1.Probe{
					ProbeHandler:        httpGet("/healthz"),
					InitialDelaySeconds: 120,
				},
			},
			want: []string{"Liveness waits 2m0s before its first check and there is no startup probe, so a container slower than that to come up is restarted while it is still starting."},
		},
		{
			name: "the same delay with a startup probe watching the start",
			container: corev1.Container{
				Name:         "api",
				StartupProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz"), FailureThreshold: 30},
				LivenessProbe: &corev1.Probe{
					ProbeHandler:        httpGet("/healthz"),
					InitialDelaySeconds: 120,
				},
			},
		},
		{
			name: "a delay just short of half a minute",
			container: corev1.Container{
				Name: "api",
				LivenessProbe: &corev1.Probe{
					ProbeHandler:        httpGet("/healthz"),
					InitialDelaySeconds: 29,
				},
			},
		},
		{
			name:      "no liveness probe to delay",
			container: corev1.Container{Name: "api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messages(t, ruleLivenessWithoutStartup, livenessWithoutStartup(probed(tt.container)))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLivenessSameAsReadiness(t *testing.T) {
	tests := []struct {
		name      string
		container corev1.Container
		want      []string
	}{
		{
			name: "one endpoint answering both questions",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz"), PeriodSeconds: 30},
			},
			want: []string{"Liveness and readiness both check GET /healthz:8080, so whatever takes this container out of service also restarts it."},
		},
		{
			// Only the wording differs: the kubelet makes the same request.
			name: "the same request with the defaults written out on one side",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromInt32(8080)}}},
				LivenessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
					Path:   "/",
					Port:   intstr.FromInt32(8080),
					Scheme: corev1.URISchemeHTTP,
				}}},
			},
			want: []string{"Liveness and readiness both check GET /:8080, so whatever takes this container out of service also restarts it."},
		},
		{
			name: "the same command run twice",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: exec("/bin/health", "--all")},
				LivenessProbe:  &corev1.Probe{ProbeHandler: exec("/bin/health", "--all")},
			},
			want: []string{"Liveness and readiness both check exec /bin/health --all, so whatever takes this container out of service also restarts it."},
		},
		{
			name: "two questions asked of two endpoints",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready")},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			},
		},
		{
			name: "the same port reached two ways",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: tcpSocket(8080)},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			},
		},
		{
			name: "liveness with no readiness to match",
			container: corev1.Container{
				Name:          "api",
				LivenessProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messages(t, ruleLivenessSameAsReadiness, livenessSameAsReadiness(probed(tt.container)))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLivenessFasterThanReadiness(t *testing.T) {
	tests := []struct {
		name      string
		container corev1.Container
		want      []string
	}{
		{
			name: "a restart that arrives before the endpoint is removed",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready")},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz"), PeriodSeconds: 5},
			},
			want: []string{"Liveness detects failure in 15s and readiness in 30s, so a failing container is restarted before it is taken out of service."},
		},
		{
			// The same period on both sides, but liveness gives up sooner.
			name: "a liveness threshold below the readiness one",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready"), PeriodSeconds: 10, FailureThreshold: 6},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz"), PeriodSeconds: 10, FailureThreshold: 2},
			},
			want: []string{"Liveness detects failure in 20s and readiness in 1m0s, so a failing container is restarted before it is taken out of service."},
		},
		{
			name: "both probes acting at the same moment",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready")},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			},
		},
		{
			name: "readiness acting first",
			container: corev1.Container{
				Name:           "api",
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready"), PeriodSeconds: 5},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz"), PeriodSeconds: 30},
			},
		},
		{
			// With no readiness probe there is no removal to come first, and
			// no-readiness-probe is the finding that says so.
			name: "liveness with no readiness to outrun",
			container: corev1.Container{
				Name:          "api",
				LivenessProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz"), PeriodSeconds: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messages(t, ruleLivenessFasterThanReadiness, livenessFasterThanReadiness(probed(tt.container)))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTimeoutAtDefault(t *testing.T) {
	message := func(probe model.ProbeType) string {
		return "The " + string(probe) + " probe allows the default 1s for an answer, so a container that is only slow to answer counts as failing."
	}

	tests := []struct {
		name      string
		container corev1.Container
		want      []string
	}{
		{
			name: "a probe that says nothing about its timeout",
			container: corev1.Container{
				Name:          "api",
				LivenessProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			},
			want: []string{message(model.ProbeLiveness)},
		},
		{
			// A spec that writes the default out asks the kubelet for exactly
			// what leaving it unset does.
			name: "one probe left unset and one written out at the default",
			container: corev1.Container{
				Name:           "api",
				StartupProbe:   &corev1.Probe{ProbeHandler: httpGet("/healthz"), TimeoutSeconds: 5},
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready")},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz"), TimeoutSeconds: 1},
			},
			want: []string{message(model.ProbeReadiness), message(model.ProbeLiveness)},
		},
		{
			name: "every probe given room to answer",
			container: corev1.Container{
				Name:           "api",
				StartupProbe:   &corev1.Probe{ProbeHandler: httpGet("/healthz"), TimeoutSeconds: 3},
				ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready"), TimeoutSeconds: 2},
				LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz"), TimeoutSeconds: 5},
			},
		},
		{
			name:      "no probes to time out",
			container: corev1.Container{Name: "api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messages(t, ruleTimeoutAtDefault, timeoutAtDefault(probed(tt.container)))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTimeoutExceedsPeriod(t *testing.T) {
	tests := []struct {
		name      string
		container corev1.Container
		want      []string
	}{
		{
			name: "a probe given longer to answer than it has between checks",
			container: corev1.Container{
				Name: "api",
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:   httpGet("/ready"),
					PeriodSeconds:  10,
					TimeoutSeconds: 15,
				},
			},
			want: []string{"The readiness probe allows 15s for an answer but runs every 10s, so a check can still be outstanding when the next one is due."},
		},
		{
			// The period is unset, so the kubelet's 10s is what it runs on.
			name: "a timeout above the default period",
			container: corev1.Container{
				Name:         "api",
				StartupProbe: &corev1.Probe{ProbeHandler: httpGet("/healthz"), TimeoutSeconds: 30},
			},
			want: []string{"The startup probe allows 30s for an answer but runs every 10s, so a check can still be outstanding when the next one is due."},
		},
		{
			name: "a timeout exactly as long as the period",
			container: corev1.Container{
				Name: "api",
				LivenessProbe: &corev1.Probe{
					ProbeHandler:   httpGet("/healthz"),
					PeriodSeconds:  10,
					TimeoutSeconds: 10,
				},
			},
		},
		{
			name: "a timeout comfortably inside the period",
			container: corev1.Container{
				Name: "api",
				LivenessProbe: &corev1.Probe{
					ProbeHandler:   httpGet("/healthz"),
					PeriodSeconds:  30,
					TimeoutSeconds: 5,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := messages(t, ruleTimeoutExceedsPeriod, timeoutExceedsPeriod(probed(tt.container)))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProbeFailuresRecent(t *testing.T) {
	failures := func(n int32) *int32 { return &n }

	tests := []struct {
		name     string
		failures *int32
		failing  []model.ProbeType
		want     []string
	}{
		{
			name:     "two probes failing over and over",
			failures: failures(9),
			failing:  []model.ProbeType{model.ProbeReadiness, model.ProbeLiveness},
			want:     []string{"The cluster reports 9 recent Unhealthy events for the readiness and liveness probes, so this is failing now and not only on paper."},
		},
		{
			name:     "one failure, from one probe",
			failures: failures(1),
			failing:  []model.ProbeType{model.ProbeLiveness},
			want:     []string{"The cluster reports 1 recent Unhealthy event for the liveness probe, so this is failing now and not only on paper."},
		},
		{
			name:     "all three probes failing",
			failures: failures(12),
			failing:  model.ProbeTypes,
			want:     []string{"The cluster reports 12 recent Unhealthy events for the startup, readiness and liveness probes, so this is failing now and not only on paper."},
		},
		{
			// An event whose message names no probe is still a failure the
			// cluster recorded against this container.
			name:     "failures the events name no probe for",
			failures: failures(3),
			want:     []string{"The cluster reports 3 recent Unhealthy events for this container, so this is failing now and not only on paper."},
		},
		{
			name:     "a container nothing has complained about",
			failures: failures(0),
		},
		{
			// Forbidden events leave the count unknown, and an unknown count
			// is not evidence of anything.
			name: "events that could not be read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := ruleInput{failures: tt.failures, failing: tt.failing}
			got := messages(t, ruleProbeFailuresRecent, probeFailuresRecent(in))
			if !equalStrings(got, tt.want) {
				t.Errorf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

// The evidence the rules judge is the evidence the report already carries: the
// failure count the runtime state aggregates, and the probes the pods' event
// groups name.
func TestRuleInputForReadsTheReportedEvidence(t *testing.T) {
	failures := int32(9)
	reported := &model.Container{
		Name:    "api",
		Runtime: &model.Runtime{Total: 2, Failures: &failures},
		Pods: []model.Pod{
			{Name: "api-a"},
			{Name: "api-b", Events: []model.EventGroup{
				{Probe: model.ProbeLiveness, Count: 7},
				{Probe: model.ProbeReadiness, Count: 2},
			}},
		},
	}

	in := ruleInputFor(&corev1.Container{Name: "api"}, reported)
	if in.failures == nil || *in.failures != 9 {
		t.Errorf("failures = %v, want 9", in.failures)
	}
	if len(in.failing) != 2 || in.failing[0] != model.ProbeReadiness || in.failing[1] != model.ProbeLiveness {
		t.Errorf("failing = %v, want readiness then liveness", in.failing)
	}
}

// Findings ride the Report, in rule order, for every container of every
// workload.
func TestAnalyzeFindings(t *testing.T) {
	report := Analyze(&collect.Result{Workloads: []collect.Workload{apiWorkload(t)}}, generatedAt, Options{})
	containers := report.Workloads[0].Containers

	var fired []string
	for _, finding := range containers[0].Findings {
		fired = append(fired, finding.Rule)
	}
	want := []string{
		ruleTimeoutAtDefault,
		ruleTimeoutAtDefault,
		ruleTimeoutAtDefault,
		ruleProbeFailuresRecent,
	}
	if !equalStrings(fired, want) {
		t.Errorf("api findings = %v, want %v", fired, want)
	}

	// The terminating pod's 50 failures are history, so the count the finding
	// quotes is the one the runtime state reports.
	last := containers[0].Findings[3].Message
	if want := "The cluster reports 9 recent Unhealthy events for the readiness and liveness probes, so this is failing now and not only on paper."; last != want {
		t.Errorf("failure finding = %q, want %q", last, want)
	}

	// The sidecar has a readiness probe and nothing failing, so the only
	// opinion left is about its timeout.
	fired = nil
	for _, finding := range containers[1].Findings {
		fired = append(fired, finding.Rule)
	}
	if !equalStrings(fired, []string{ruleTimeoutAtDefault}) {
		t.Errorf("istio-proxy findings = %v, want the default timeout only", fired)
	}
}

// --no-findings leaves the rules unrun, so no surface has an opinion to print
// and the facts are untouched.
func TestAnalyzeWithoutFindings(t *testing.T) {
	result := &collect.Result{Workloads: []collect.Workload{apiWorkload(t)}}

	report := Analyze(result, generatedAt, Options{NoFindings: true})
	for _, workload := range report.Workloads {
		for _, container := range workload.Containers {
			if len(container.Findings) != 0 {
				t.Errorf("%s has findings %+v, want none", container.Name, container.Findings)
			}
		}
	}

	withFindings := Analyze(result, generatedAt, Options{})
	for i, workload := range report.Workloads {
		for j, container := range withFindings.Workloads[i].Containers {
			got := report.Workloads[i].Containers[j]
			container.Findings = nil
			if !reflect.DeepEqual(got, container) {
				t.Errorf("%s reports different facts without findings", workload.Containers[j].Name)
			}
		}
	}
}
