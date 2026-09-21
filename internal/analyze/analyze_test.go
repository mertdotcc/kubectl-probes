// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// generatedAt is when the fixture reports say they were produced.
var generatedAt = time.Date(2026, 9, 19, 14, 30, 0, 0, time.UTC)

func podFrom(t *testing.T, manifest string) *corev1.Pod {
	t.Helper()
	pod := &corev1.Pod{}
	if err := yaml.Unmarshal([]byte(manifest), pod); err != nil {
		t.Fatalf("bad fixture pod: %v", err)
	}
	return pod
}

func specFrom(t *testing.T, manifest string) *corev1.PodSpec {
	t.Helper()
	spec := &corev1.PodSpec{}
	if err := yaml.Unmarshal([]byte(manifest), spec); err != nil {
		t.Fatalf("bad fixture pod spec: %v", err)
	}
	return spec
}

// apiTemplate is what the Deployment stamps its pods out of: a container with
// all three probes, a sidecar with one, and a plain init container that cannot
// carry any.
const apiTemplate = `
containers:
- name: api
  image: api:1
  startupProbe:
    httpGet: {path: /healthz, port: 8080}
    initialDelaySeconds: 5
    failureThreshold: 30
  readinessProbe:
    httpGet: {path: /ready, port: 8080}
    periodSeconds: 5
    successThreshold: 2
  livenessProbe:
    httpGet: {path: /healthz, port: 8080}
    periodSeconds: 30
initContainers:
- name: istio-proxy
  image: proxy:1
  restartPolicy: Always
  readinessProbe:
    httpGet: {path: /healthz/ready, port: 15021}
    periodSeconds: 15
    failureThreshold: 4
- name: wait-for-db
  image: busybox:1
`

// apiPod is a pod of that Deployment, in agreement with the template.
const apiPod = `
metadata: {name: api-a, namespace: prod}
spec:
  containers:
  - name: api
    image: api:1
    startupProbe:
      httpGet: {path: /healthz, port: 8080}
      initialDelaySeconds: 5
      failureThreshold: 30
    readinessProbe:
      httpGet: {path: /ready, port: 8080}
      periodSeconds: 5
      successThreshold: 2
    livenessProbe:
      httpGet: {path: /healthz, port: 8080}
      periodSeconds: 30
  initContainers:
  - name: istio-proxy
    image: proxy:1
    restartPolicy: Always
    readinessProbe:
      httpGet: {path: /healthz/ready, port: 15021}
      periodSeconds: 15
      failureThreshold: 4
  - name: wait-for-db
    image: busybox:1
status:
  conditions:
  - {type: Ready, status: "True", lastTransitionTime: "2026-09-19T13:58:12Z"}
  - {type: ContainersReady, status: "True", lastTransitionTime: "2026-09-19T13:58:12Z"}
  containerStatuses:
  - {name: api, ready: true, started: true, restartCount: 0}
  initContainerStatuses:
  - {name: istio-proxy, ready: true, started: true, restartCount: 0}
`

// driftedPod is a pod of the same Deployment left over from before the last
// edit to the template: its liveness probe runs three times as often.
const driftedPod = `
metadata: {name: api-b, namespace: prod}
spec:
  containers:
  - name: api
    image: api:1
    startupProbe:
      httpGet: {path: /healthz, port: 8080}
      initialDelaySeconds: 5
      failureThreshold: 30
    readinessProbe:
      httpGet: {path: /ready, port: 8080}
      periodSeconds: 5
      successThreshold: 2
    livenessProbe:
      httpGet: {path: /healthz, port: 8080}
      periodSeconds: 10
  initContainers:
  - name: istio-proxy
    image: proxy:1
    restartPolicy: Always
    readinessProbe:
      httpGet: {path: /healthz/ready, port: 15021}
      periodSeconds: 15
      failureThreshold: 4
status:
  conditions:
  - {type: Ready, status: "False", lastTransitionTime: "2026-09-19T14:22:03Z"}
  - {type: ContainersReady, status: "False", lastTransitionTime: "2026-09-19T14:22:03Z"}
  containerStatuses:
  - name: api
    ready: false
    started: true
    restartCount: 4
    lastState:
      terminated: {reason: Error, exitCode: 137, signal: 9, finishedAt: "2026-09-19T14:21:47Z"}
  initContainerStatuses:
  - {name: istio-proxy, ready: true, started: true, restartCount: 0}
`

// terminatingPod is on its way out, and everything it reports is history.
const terminatingPod = `
metadata:
  name: api-c
  namespace: prod
  deletionTimestamp: "2026-09-19T14:29:00Z"
spec:
  containers:
  - name: api
    image: api:1
    livenessProbe:
      httpGet: {path: /healthz, port: 8080}
      periodSeconds: 30
status:
  containerStatuses:
  - {name: api, ready: false, restartCount: 100}
`

// apiWorkload is the Deployment as a run collects it: three pods, one of them
// terminating, and the failures the cluster recorded against them.
func apiWorkload(t *testing.T) collect.Workload {
	t.Helper()
	return collect.Workload{
		GroupKind: schema.GroupKind{Group: "apps", Kind: "Deployment"},
		Name:      "api",
		Namespace: "prod",
		Template:  specFrom(t, apiTemplate),
		Pods: []collect.Pod{
			{Pod: podFrom(t, apiPod)},
			{
				Pod: podFrom(t, driftedPod),
				Events: []corev1.Event{
					unhealthyEvent("api", "Liveness probe failed: HTTP probe failed with statuscode: 503", 7, at(2), at(29)),
					unhealthyEvent("api", "Readiness probe failed: connection refused", 2, at(3), at(28)),
				},
			},
			{
				Pod: podFrom(t, terminatingPod),
				Events: []corev1.Event{
					unhealthyEvent("api", "Liveness probe failed: HTTP probe failed with statuscode: 503", 50, at(1), at(20)),
				},
			},
		},
	}
}

func TestAnalyze(t *testing.T) {
	report := Analyze(&collect.Result{Workloads: []collect.Workload{apiWorkload(t)}}, generatedAt, Options{})

	if report.APIVersion != model.APIVersion || report.Kind != model.Kind {
		t.Errorf("report = %s/%s, want %s/%s", report.APIVersion, report.Kind, model.APIVersion, model.Kind)
	}
	if !report.GeneratedAt.Equal(generatedAt) {
		t.Errorf("GeneratedAt = %v, want %v", report.GeneratedAt, generatedAt)
	}
	if len(report.Workloads) != 1 {
		t.Fatalf("report has %d workloads, want 1", len(report.Workloads))
	}

	workload := report.Workloads[0]
	if workload.Kind != "Deployment" || workload.Group != "apps" || workload.Name != "api" || workload.Namespace != "prod" {
		t.Errorf("workload = %+v, want the prod Deployment api", workload)
	}
	if workload.DisplayName != "deploy/api" {
		t.Errorf("DisplayName = %q, want deploy/api", workload.DisplayName)
	}
	if workload.PodCount != 2 || workload.TerminatingPodCount != 1 {
		t.Errorf("PodCount = %d, TerminatingPodCount = %d, want 2 and 1", workload.PodCount, workload.TerminatingPodCount)
	}
	if !workload.TemplateAvailable {
		t.Error("TemplateAvailable = false, want the template reported as read")
	}

	// The plain init container carries no probes and is not a container of the
	// workload; the sidecar is, and comes after the regular containers.
	var names []string
	for _, container := range workload.Containers {
		names = append(names, container.Name)
	}
	if len(names) != 2 || names[0] != "api" || names[1] != "istio-proxy" {
		t.Fatalf("containers = %v, want api and istio-proxy", names)
	}

	api := workload.Containers[0]
	if api.Sidecar {
		t.Error("api reports as a sidecar, want a regular container")
	}

	if api.Startup == nil || api.Startup.Timing == nil {
		t.Fatal("api has no startup probe, want the one the template configures")
	}
	if got := durationString(api.Startup.Timing.StartupBudget); got != "5m5s" {
		t.Errorf("startup budget = %s, want 5m5s", got)
	}
	if api.Startup.Drifted {
		t.Error("startup reports drift, want none")
	}
	if api.Startup.Timing.AfterStartup {
		t.Error("startup timing is relative to the startup probe, want relative to the container starting")
	}

	if api.Readiness == nil || api.Readiness.Timing == nil {
		t.Fatal("api has no readiness probe, want the one the template configures")
	}
	if got := durationString(api.Readiness.Timing.TrafficDelay); got != "5s" {
		t.Errorf("traffic delay = %s, want 5s", got)
	}
	if !api.Readiness.Timing.AfterStartup {
		t.Error("readiness timing is not relative to the startup probe, want it to be")
	}

	// The pod left over from before the last edit is the one worth showing.
	if api.Liveness == nil || api.Liveness.Config == nil || api.Liveness.Config.PeriodSeconds == nil {
		t.Fatal("api has no liveness configuration, want the running pod's")
	}
	if *api.Liveness.Config.PeriodSeconds != 10 {
		t.Errorf("liveness periodSeconds = %d, want the drifting pod's 10", *api.Liveness.Config.PeriodSeconds)
	}
	if !api.Liveness.Drifted {
		t.Error("liveness reports no drift, want the difference from the template reported")
	}
	if api.Liveness.Template == nil || api.Liveness.Template.PeriodSeconds == nil || *api.Liveness.Template.PeriodSeconds != 30 {
		t.Errorf("liveness template = %+v, want periodSeconds 30", api.Liveness.Template)
	}
	if got := api.Liveness.Timing.FailureDetection.String(); got != "30s" {
		t.Errorf("liveness failure detection = %s, want 30s", got)
	}

	// Everything the terminating pod reports is left out of the aggregate.
	if api.Runtime == nil {
		t.Fatal("api has no runtime state, want it aggregated over the live pods")
	}
	if api.Runtime.Ready != 1 || api.Runtime.Total != 2 {
		t.Errorf("ready = %d/%d, want 1/2", api.Runtime.Ready, api.Runtime.Total)
	}
	if api.Runtime.Restarts != 4 {
		t.Errorf("restarts = %d, want 4: the terminating pod's 100 are history", api.Runtime.Restarts)
	}
	if api.Runtime.Failures == nil || *api.Runtime.Failures != 9 {
		t.Errorf("failures = %v, want 9: the terminating pod's 50 are history", api.Runtime.Failures)
	}

	if len(api.Pods) != 2 || api.Pods[0].Name != "api-a" || api.Pods[1].Name != "api-b" {
		t.Fatalf("pods = %+v, want the two live pods", api.Pods)
	}
	if len(api.Pods[0].Events) != 0 {
		t.Errorf("api-a has events %+v, want none", api.Pods[0].Events)
	}
	if len(api.Pods[1].Events) != 2 {
		t.Fatalf("api-b has events %+v, want one group per probe that failed", api.Pods[1].Events)
	}
	if api.Pods[1].LastTermination == nil || api.Pods[1].LastTermination.ExitCode != 137 {
		t.Errorf("api-b last termination = %+v, want exit code 137", api.Pods[1].LastTermination)
	}

	sidecar := workload.Containers[1]
	if !sidecar.Sidecar {
		t.Error("istio-proxy reports as a regular container, want a sidecar")
	}
	if sidecar.Startup != nil || sidecar.Liveness != nil {
		t.Errorf("istio-proxy = %+v, want readiness only", sidecar)
	}
	if sidecar.Readiness == nil || sidecar.Readiness.Drifted {
		t.Errorf("istio-proxy readiness = %+v, want the template's, undrifted", sidecar.Readiness)
	}
	if sidecar.Readiness.Timing.AfterStartup {
		t.Error("istio-proxy readiness is relative to a startup probe it does not have")
	}
	if sidecar.Runtime == nil || sidecar.Runtime.Ready != 2 || sidecar.Runtime.Total != 2 {
		t.Errorf("istio-proxy runtime = %+v, want 2/2 ready", sidecar.Runtime)
	}
	if sidecar.Runtime.Failures == nil || *sidecar.Runtime.Failures != 0 {
		t.Errorf("istio-proxy failures = %v, want none", sidecar.Runtime.Failures)
	}
	// The pod that never ran the sidecar says nothing about it.
	if len(sidecar.Pods) != 2 {
		t.Errorf("istio-proxy pods = %+v, want the two pods running it", sidecar.Pods)
	}
}

// A workload scaled to zero is still a workload: the template is all there is
// to report, and there is no runtime state to claim.
// A plain init container cannot carry a probe, so it is not a container of the
// workload. It is still named, because a reader who goes looking for it has to
// be able to tell "cannot have probes" from "this tool missed it".
func TestAnalyzeNamesPlainInitContainers(t *testing.T) {
	report := Analyze(&collect.Result{Workloads: []collect.Workload{apiWorkload(t)}}, generatedAt, Options{})

	got := report.Workloads[0].InitContainers
	if len(got) != 1 || got[0] != "wait-for-db" {
		t.Errorf("InitContainers = %v, want [wait-for-db]", got)
	}
	// The sidecar is a container of the workload and is not named here twice.
	for _, container := range report.Workloads[0].Containers {
		for _, name := range got {
			if container.Name == name {
				t.Errorf("%s is reported both as a container and as a plain init container", name)
			}
		}
	}
}

func TestAnalyzeWithoutPods(t *testing.T) {
	workload := collect.Workload{
		GroupKind: schema.GroupKind{Group: "apps", Kind: "StatefulSet"},
		Name:      "archiver",
		Namespace: "prod",
		Template:  specFrom(t, apiTemplate),
	}

	report := Analyze(&collect.Result{Workloads: []collect.Workload{workload}}, generatedAt, Options{})
	got := report.Workloads[0]

	if got.DisplayName != "sts/archiver" || got.PodCount != 0 || got.TerminatingPodCount != 0 {
		t.Errorf("workload = %+v, want sts/archiver with no pods", got)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("containers = %+v, want the template's api and istio-proxy", got.Containers)
	}
	api := got.Containers[0]
	if api.Runtime != nil || api.Pods != nil {
		t.Errorf("api = %+v, want no runtime state at all", api)
	}
	if api.Liveness == nil || api.Liveness.Timing == nil {
		t.Fatal("api has no liveness timing, want the template's")
	}
	if got := api.Liveness.Timing.FailureDetection.String(); got != "1m30s" {
		t.Errorf("liveness failure detection = %s, want 1m30s", got)
	}
	// There is nothing running to differ from the template.
	for _, probe := range model.ProbeTypes {
		if detail := api.Probe(probe); detail != nil && detail.Drifted {
			t.Errorf("%s reports drift against the template it was read from", probe)
		}
	}
}

// Every pod on its way out is every pod gone: the template describes what is
// coming back.
func TestAnalyzeWithEveryPodTerminating(t *testing.T) {
	workload := apiWorkload(t)
	workload.Pods = workload.Pods[2:]

	got := Analyze(&collect.Result{Workloads: []collect.Workload{workload}}, generatedAt, Options{}).Workloads[0]
	if got.PodCount != 0 || got.TerminatingPodCount != 1 {
		t.Errorf("PodCount = %d, TerminatingPodCount = %d, want 0 and 1", got.PodCount, got.TerminatingPodCount)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("containers = %+v, want the template's", got.Containers)
	}
	if got.Containers[0].Runtime != nil {
		t.Errorf("api runtime = %+v, want none: nothing is running", got.Containers[0].Runtime)
	}
}

// An owner the user may not read leaves the pods without a template, and a
// report with no template claims no drift.
func TestAnalyzeWithoutATemplate(t *testing.T) {
	workload := apiWorkload(t)
	workload.Template = nil

	got := Analyze(&collect.Result{Workloads: []collect.Workload{workload}}, generatedAt, Options{}).Workloads[0]
	if got.TemplateAvailable {
		t.Error("TemplateAvailable = true, want false")
	}
	for _, container := range got.Containers {
		for _, probe := range model.ProbeTypes {
			detail := container.Probe(probe)
			if detail == nil {
				continue
			}
			if detail.Drifted || detail.Template != nil {
				t.Errorf("%s %s = %+v, want no claim about a template that could not be read", container.Name, probe, detail)
			}
		}
	}
}

// A probe the template configures and the running pod does not is drift with no
// running configuration to report.
func TestAnalyzeProbeMissingFromTheRunningPod(t *testing.T) {
	workload := apiWorkload(t)
	workload.Pods = workload.Pods[:1]
	workload.Pods[0].Pod.Spec.Containers[0].LivenessProbe = nil

	got := Analyze(&collect.Result{Workloads: []collect.Workload{workload}}, generatedAt, Options{}).Workloads[0]
	liveness := got.Containers[0].Liveness
	if liveness == nil {
		t.Fatal("liveness is absent entirely, want the template's probe reported as drift")
	}
	if !liveness.Drifted || liveness.Config != nil || liveness.Timing != nil {
		t.Errorf("liveness = %+v, want drift with no running configuration", liveness)
	}
	if liveness.Template == nil || liveness.Template.Handler.Summary != "GET /healthz:8080" {
		t.Errorf("liveness template = %+v, want the template's handler", liveness.Template)
	}
}

// Events the run was not allowed to read make failures unknown, not zero.
func TestAnalyzeWithoutEvents(t *testing.T) {
	result := &collect.Result{
		Workloads: []collect.Workload{apiWorkload(t)},
		Gaps:      collect.Gaps{Events: true},
	}

	got := Analyze(result, generatedAt, Options{}).Workloads[0]
	for _, container := range got.Containers {
		if container.Runtime == nil {
			t.Fatalf("%s has no runtime state", container.Name)
		}
		if container.Runtime.Failures != nil {
			t.Errorf("%s failures = %d, want unknown", container.Name, *container.Runtime.Failures)
		}
	}
}

func TestAnalyzeWithoutWorkloads(t *testing.T) {
	report := Analyze(&collect.Result{}, generatedAt, Options{})
	if len(report.Workloads) != 0 {
		t.Errorf("workloads = %+v, want none", report.Workloads)
	}
	if report.APIVersion != model.APIVersion {
		t.Errorf("APIVersion = %q, want it stamped even on an empty report", report.APIVersion)
	}
}

// A Job's pods run to completion and nothing sends them traffic, so readiness
// has no consumer there and no-readiness-probe has nothing to say. A CronJob
// is the same pods one owner further up.
func TestNoReadinessProbeSkipsBatchWorkloads(t *testing.T) {
	template := specFrom(t, `
containers:
- name: backup
  image: backup:1
`)
	for _, kind := range []string{"Job", "CronJob", "Deployment"} {
		t.Run(kind, func(t *testing.T) {
			group := "batch"
			if kind == "Deployment" {
				group = "apps"
			}
			report := Analyze(&collect.Result{Workloads: []collect.Workload{{
				GroupKind: schema.GroupKind{Group: group, Kind: kind},
				Name:      "backup",
				Namespace: "prod",
				Template:  template,
			}}}, generatedAt, Options{})

			var fired bool
			for _, finding := range report.Workloads[0].Containers[0].Findings {
				fired = fired || finding.Rule == ruleNoReadinessProbe
			}
			if want := kind == "Deployment"; fired != want {
				t.Errorf("no-readiness-probe fired = %v on a %s, want %v", fired, kind, want)
			}
		})
	}
}
