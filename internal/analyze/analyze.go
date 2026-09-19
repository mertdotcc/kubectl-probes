// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// Options is what a run asks of Analyze beyond the facts it was given.
type Options struct {
	// NoFindings leaves the rules unrun, so the Report carries facts only.
	// It is --no-findings at the CLI, and withholding the opinions here
	// rather than in each surface is what makes the flag mean the same thing
	// in the Overview, the Inspection, and the JSON and YAML output.
	NoFindings bool
}

// Analyze turns what a run collected into the Report every surface is built
// from. It reads nothing and decides nothing about the cluster: the same
// Result always produces the same Report.
func Analyze(result *collect.Result, generatedAt time.Time, opts Options) *model.Report {
	report := model.New(generatedAt)
	for _, workload := range result.Workloads {
		report.Workloads = append(report.Workloads, workloadOf(workload, result.Gaps, opts))
	}
	return report
}

func workloadOf(workload collect.Workload, gaps collect.Gaps, opts Options) model.Workload {
	live, terminating := livePods(workload.Pods)

	out := model.Workload{
		Kind:                workload.GroupKind.Kind,
		Group:               workload.GroupKind.Group,
		Name:                workload.Name,
		Namespace:           workload.Namespace,
		DisplayName:         model.DisplayName(workload.GroupKind.Group, workload.GroupKind.Kind, workload.Name),
		PodCount:            len(live),
		TerminatingPodCount: terminating,
		TemplateAvailable:   workload.Template != nil,
	}
	for _, ref := range containersOf(live, workload.Template) {
		out.Containers = append(out.Containers, containerOf(ref, live, workload.Template, gaps, opts))
	}
	return out
}

// livePods keeps the pods the runtime numbers are aggregated over and counts the
// rest. A pod with a deletion timestamp is on its way out: its restarts and its
// probe failures are history, and counting them would make a finished rollout
// look like an ongoing problem.
func livePods(pods []collect.Pod) (live []collect.Pod, terminating int) {
	for _, pod := range pods {
		if pod.Pod.DeletionTimestamp != nil {
			terminating++
			continue
		}
		live = append(live, pod)
	}
	return live, terminating
}

// containerRef is a container of a workload: the identity the report groups by,
// which is a name and nothing else, however many pods run it.
type containerRef struct {
	name    string
	sidecar bool
}

// containersOf lists the containers a workload reports: those of the pods that
// are running, and the template's own when nothing is running.
//
// Per ADR-0001 the pods are what the workload is. A container the template adds
// that no pod runs yet has no runtime state to describe, and appears once the
// rollout that creates it does.
//
// Regular containers come first and sidecars after, each in the order the spec
// writes them, so the report reads the way the spec does whichever order the
// API server listed the pods in.
func containersOf(live []collect.Pod, template *corev1.PodSpec) []containerRef {
	specs := make([]*corev1.PodSpec, 0, len(live)+1)
	for _, pod := range live {
		specs = append(specs, &pod.Pod.Spec)
	}
	if len(specs) == 0 && template != nil {
		specs = append(specs, template)
	}

	var refs []containerRef
	seen := map[string]bool{}
	for _, sidecars := range []bool{false, true} {
		for _, spec := range specs {
			for _, ref := range probeBearing(spec) {
				if ref.sidecar != sidecars || seen[ref.name] {
					continue
				}
				seen[ref.name] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// probeBearing lists the containers of a spec that can carry probes: the regular
// containers, and the init containers with restartPolicy Always, which are
// sidecars. A plain init container and an ephemeral container cannot carry a
// probe at all.
func probeBearing(spec *corev1.PodSpec) []containerRef {
	refs := make([]containerRef, 0, len(spec.Containers)+len(spec.InitContainers))
	for _, container := range spec.Containers {
		refs = append(refs, containerRef{name: container.Name})
	}
	for i := range spec.InitContainers {
		if isSidecar(&spec.InitContainers[i]) {
			refs = append(refs, containerRef{name: spec.InitContainers[i].Name, sidecar: true})
		}
	}
	return refs
}

func isSidecar(container *corev1.Container) bool {
	return container.RestartPolicy != nil && *container.RestartPolicy == corev1.ContainerRestartPolicyAlways
}

// containerOf is everything the report says about one container of a workload:
// how its probes are configured, what that means, whether the template says
// something else, and what the pods running it report.
func containerOf(ref containerRef, live []collect.Pod, template *corev1.PodSpec, gaps collect.Gaps, opts Options) model.Container {
	out := model.Container{Name: ref.name, Sidecar: ref.sidecar}

	fromTemplate := specContainer(template, ref.name)
	running := representative(live, ref.name, fromTemplate)

	// With no pod running it, the template is the only configuration there is,
	// and there is nothing to compare it against.
	configured, compare := running, true
	if running == nil {
		configured, compare = fromTemplate, false
	}

	afterStartup := probeOf(configured, model.ProbeStartup) != nil
	for _, probe := range model.ProbeTypes {
		out.SetProbe(probe, probeDetail(probe, probeOf(configured, probe), probeOf(fromTemplate, probe), compare && template != nil, afterStartup))
	}

	out.Runtime, out.Pods = aggregate(live, ref.name, gaps)

	// Findings come last because they are opinions about everything above
	// them, and they are kept in their own field so a reader can tell them
	// from the facts they were drawn from.
	if !opts.NoFindings {
		out.Findings = findingsFor(ruleInputFor(configured, &out))
	}
	return out
}

// representative is the running container whose configuration the report shows.
//
// Mid-rollout the pods of one workload disagree, and a probe the template no
// longer configures that way is exactly what the reader came for, so a pod that
// differs from the template is preferred over one that agrees with it. Pods
// arrive sorted by name, so the choice is the same on every run.
func representative(live []collect.Pod, name string, fromTemplate *corev1.Container) *corev1.Container {
	var first *corev1.Container
	for _, pod := range live {
		container := specContainer(&pod.Pod.Spec, name)
		if container == nil {
			continue
		}
		if first == nil {
			first = container
		}
		for _, probe := range model.ProbeTypes {
			if drifted(probeOf(container, probe), probeOf(fromTemplate, probe)) {
				return container
			}
		}
	}
	return first
}

func specContainer(spec *corev1.PodSpec, name string) *corev1.Container {
	if spec == nil {
		return nil
	}
	for _, containers := range [][]corev1.Container{spec.Containers, spec.InitContainers} {
		for i := range containers {
			if containers[i].Name == name {
				return &containers[i]
			}
		}
	}
	return nil
}

// probeDetail is what the report says about one of a container's three probes.
//
// It is nil for a probe nobody configures, which is the common case and the one
// the no-readiness-probe rule is about. A probe the template configures and the
// running pod does not is reported as drift with no configuration of its own,
// because there is no running configuration to report.
func probeDetail(probe model.ProbeType, running, fromTemplate *corev1.Probe, compare, afterStartup bool) *model.ProbeDetail {
	if running == nil && (!compare || fromTemplate == nil) {
		return nil
	}

	detail := &model.ProbeDetail{}
	if running != nil {
		detail.Config = probeConfig(running)
		detail.Timing = timingOf(probe, effective(running), afterStartup)
	}
	if compare && drifted(running, fromTemplate) {
		detail.Drifted = true
		detail.Template = probeConfig(fromTemplate)
	}
	return detail
}

// aggregate sums what the pods running a container report, and describes each of
// them. Both are absent for a container nothing is running: a workload scaled to
// zero has no runtime state, and saying 0/0 ready would be a claim about pods
// that do not exist.
func aggregate(live []collect.Pod, name string, gaps collect.Gaps) (*model.Runtime, []model.Pod) {
	runtime := &model.Runtime{}
	var pods []model.Pod
	var failures int32

	for _, pod := range live {
		if specContainer(&pod.Pod.Spec, name) == nil {
			continue
		}
		state, podFailures := podState(pod, name)
		runtime.Total++
		if state.Ready {
			runtime.Ready++
		}
		runtime.Restarts += state.Restarts
		failures += podFailures
		pods = append(pods, state)
	}

	if runtime.Total == 0 {
		return nil, nil
	}
	// Failures are unknown, rather than zero, when the events that count them
	// could not be read.
	if !gaps.Events {
		runtime.Failures = &failures
	}
	return runtime, pods
}
