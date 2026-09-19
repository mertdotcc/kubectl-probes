// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The rules of v0.1.0, by the kebab-case name every finding they produce
// carries. A rule name is part of the Report's schema: it is what a user greps
// for and what a script keys on, so it outlives the wording of its message.
const (
	ruleNoReadinessProbe            = "no-readiness-probe"
	ruleLivenessWithoutStartup      = "liveness-without-startup"
	ruleLivenessSameAsReadiness     = "liveness-same-as-readiness"
	ruleLivenessFasterThanReadiness = "liveness-faster-than-readiness"
	ruleTimeoutAtDefault            = "timeout-at-default"
	ruleTimeoutExceedsPeriod        = "timeout-exceeds-period"
	ruleProbeFailuresRecent         = "probe-failures-recent"
)

// slowLivenessStart is the liveness initialDelaySeconds at which
// liveness-without-startup fires. A liveness probe told to wait half a minute
// is one whose author already knew the container was slow to come up, and the
// delay is a fixed guess where a startup probe would be a measurement.
const slowLivenessStart int32 = 30

// rules is every rule there is, in the order a container reports its findings:
// what is not configured, then what is configured in a way worth a second
// look, then what is failing right now.
//
// A rule returns a finding per thing it observed, so a container whose startup
// and liveness probes both sit at the default timeout says so about each of
// them rather than once about both.
var rules = []func(ruleInput) []model.Finding{
	noReadinessProbe,
	livenessWithoutStartup,
	livenessSameAsReadiness,
	livenessFasterThanReadiness,
	timeoutAtDefault,
	timeoutExceedsPeriod,
	probeFailuresRecent,
}

// ruleInput is one container as the rules judge it: what the kubelet will
// actually do with its probes, and what the cluster reports has happened to
// them.
//
// The probes carry the kubelet's defaults, because a rule is about behaviour
// and not about wording: a probe that writes periodSeconds: 10 and one that
// leaves it unset are the same probe to every rule here.
type ruleInput struct {
	probes map[model.ProbeType]*effectiveProbe
	// failures counts the container's Unhealthy events, and is nil when the
	// events could not be read at all.
	failures *int32
	// failing lists the probes those events name, in ProbeTypes order.
	failing []model.ProbeType
}

// findingsFor runs every rule against one container.
func findingsFor(c ruleInput) []model.Finding {
	var out []model.Finding
	for _, rule := range rules {
		out = append(out, rule(c)...)
	}
	return out
}

// ruleInputFor is one container as the rules see it, read from the
// configuration the report describes and the evidence it already carries.
func ruleInputFor(configured *corev1.Container, reported *model.Container) ruleInput {
	c := ruleInput{probes: map[model.ProbeType]*effectiveProbe{}}
	for _, probe := range model.ProbeTypes {
		if e := effective(probeOf(configured, probe)); e != nil {
			c.probes[probe] = e
		}
	}

	if reported.Runtime != nil {
		c.failures = reported.Runtime.Failures
	}
	named := map[model.ProbeType]bool{}
	for _, pod := range reported.Pods {
		for _, group := range pod.Events {
			named[group.Probe] = true
		}
	}
	for _, probe := range model.ProbeTypes {
		if named[probe] {
			c.failing = append(c.failing, probe)
		}
	}
	return c
}

// noReadinessProbe fires for a container that never tells the kubelet whether
// it can serve, sidecars included: a sidecar that is not ready holds its whole
// pod out of service.
func noReadinessProbe(c ruleInput) []model.Finding {
	if c.probes[model.ProbeReadiness] != nil {
		return nil
	}
	return finding(ruleNoReadinessProbe,
		"No readiness probe is configured, so the kubelet reports this container ready the moment it starts and traffic arrives from then on.")
}

// livenessWithoutStartup fires where a fixed delay stands in for a startup
// probe: the container is known to be slow to come up, and the only thing
// covering that is a guess made when the manifest was written.
func livenessWithoutStartup(c ruleInput) []model.Finding {
	liveness := c.probes[model.ProbeLiveness]
	if liveness == nil || c.probes[model.ProbeStartup] != nil || liveness.InitialDelaySeconds < slowLivenessStart {
		return nil
	}
	return finding(ruleLivenessWithoutStartup, fmt.Sprintf(
		"Liveness waits %s before its first check and there is no startup probe, so a container slower than that to come up is restarted while it is still starting.",
		model.Seconds(liveness.InitialDelaySeconds)))
}

// livenessSameAsReadiness fires where one answer decides both questions.
//
// The comparison is of effective handlers: two http handlers that name the
// same port agree whether or not either of them wrote down the scheme.
func livenessSameAsReadiness(c ruleInput) []model.Finding {
	liveness, readiness := c.probes[model.ProbeLiveness], c.probes[model.ProbeReadiness]
	if liveness == nil || readiness == nil || !reflect.DeepEqual(liveness.Handler, readiness.Handler) {
		return nil
	}
	return finding(ruleLivenessSameAsReadiness, fmt.Sprintf(
		"Liveness and readiness both check %s, so whatever takes this container out of service also restarts it.",
		liveness.Handler.Summary))
}

// livenessFasterThanReadiness fires where the restart comes first: the
// container is killed on its way to being taken out of service, so a
// dependency everything shares can turn one slow backend into a fleet-wide
// restart loop.
func livenessFasterThanReadiness(c ruleInput) []model.Finding {
	liveness, readiness := c.probes[model.ProbeLiveness], c.probes[model.ProbeReadiness]
	if liveness == nil || readiness == nil {
		return nil
	}
	restart, removal := failureDetection(liveness), failureDetection(readiness)
	if restart >= removal {
		return nil
	}
	return finding(ruleLivenessFasterThanReadiness, fmt.Sprintf(
		"Liveness detects failure in %s and readiness in %s, so a failing container is restarted before it is taken out of service.",
		restart, removal))
}

// timeoutAtDefault fires for a probe the kubelet gives one second to answer.
//
// It does not matter whether the spec left timeoutSeconds unset or wrote the
// default value out: the kubelet allows the same second either way, and the
// finding is about what a container has to beat, not about what the manifest
// says.
func timeoutAtDefault(c ruleInput) []model.Finding {
	var out []model.Finding
	for _, probe := range model.ProbeTypes {
		e := c.probes[probe]
		if e == nil || e.TimeoutSeconds != defaultTimeoutSeconds {
			continue
		}
		out = append(out, model.Finding{Rule: ruleTimeoutAtDefault, Message: fmt.Sprintf(
			"The %s probe allows the default %s for an answer, so a container that is only slow to answer counts as failing.",
			probe, model.Seconds(e.TimeoutSeconds))})
	}
	return out
}

// timeoutExceedsPeriod fires where a probe cannot keep to its own period: the
// kubelet does not start a check while the last one is outstanding, so failure
// takes longer to detect than the period reads like it does.
func timeoutExceedsPeriod(c ruleInput) []model.Finding {
	var out []model.Finding
	for _, probe := range model.ProbeTypes {
		e := c.probes[probe]
		if e == nil || e.TimeoutSeconds <= e.PeriodSeconds {
			continue
		}
		out = append(out, model.Finding{Rule: ruleTimeoutExceedsPeriod, Message: fmt.Sprintf(
			"The %s probe allows %s for an answer but runs every %s, so a check can still be outstanding when the next one is due.",
			probe, model.Seconds(e.TimeoutSeconds), model.Seconds(e.PeriodSeconds))})
	}
	return out
}

// probeFailuresRecent fires on evidence rather than on configuration: the
// cluster has recorded a probe of this container failing, so whatever else the
// report says, something here is failing now.
//
// Events expire, so this says nothing about a container that was failing
// yesterday, and nothing at all where events could not be read.
func probeFailuresRecent(c ruleInput) []model.Finding {
	if c.failures == nil || *c.failures == 0 {
		return nil
	}
	return finding(ruleProbeFailuresRecent, fmt.Sprintf(
		"The cluster reports %d recent Unhealthy %s for %s, so this is failing now and not only on paper.",
		*c.failures, plural("event", *c.failures), subject(c.failing)))
}

// failureDetection is the worst case between a container going bad and a probe
// acting on it: one failing check per period, failureThreshold of them.
func failureDetection(e *effectiveProbe) model.Duration {
	return model.Seconds(e.PeriodSeconds * e.FailureThreshold)
}

func finding(rule, message string) []model.Finding {
	return []model.Finding{{Rule: rule, Message: message}}
}

// subject names the probes a finding is about the way a sentence does. Events
// that name no probe leave nothing to name but the container itself, which is
// still worth saying: the failures are real either way.
func subject(probes []model.ProbeType) string {
	if len(probes) == 0 {
		return "this container"
	}
	names := make([]string, 0, len(probes))
	for _, probe := range probes {
		names = append(names, string(probe))
	}
	last := len(names) - 1
	joined := names[last]
	if last > 0 {
		joined = strings.Join(names[:last], ", ") + " and " + names[last]
	}
	return "the " + joined + " " + plural("probe", int32(len(probes)))
}

func plural[T ~int32 | ~int](word string, n T) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
