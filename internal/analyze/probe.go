// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The kubelet's defaults for the probe fields a spec can leave unset.
//
// A probe read from the API server has had these filled in already, and one
// read from a manifest has not, so the report cannot tell a field left unset
// from one written with the default value. It does not need to: both mean the
// kubelet does the same thing.
const (
	defaultInitialDelaySeconds int32 = 0
	defaultPeriodSeconds       int32 = 10
	defaultTimeoutSeconds      int32 = 1
	defaultSuccessThreshold    int32 = 1
	defaultFailureThreshold    int32 = 3
)

// probeConfig is a probe's configuration as the report carries it: the timing
// fields exactly as the spec wrote them, so a reader can see which ones the
// spec said nothing about, and a handler with its defaults already applied
// because nobody reading a handler wants to remember what an empty scheme
// means.
func probeConfig(probe *corev1.Probe) *model.Probe {
	if probe == nil {
		return nil
	}
	return &model.Probe{
		Handler:                       handlerOf(probe.ProbeHandler),
		InitialDelaySeconds:           written(probe.InitialDelaySeconds),
		PeriodSeconds:                 written(probe.PeriodSeconds),
		TimeoutSeconds:                written(probe.TimeoutSeconds),
		SuccessThreshold:              written(probe.SuccessThreshold),
		FailureThreshold:              written(probe.FailureThreshold),
		TerminationGracePeriodSeconds: clone(probe.TerminationGracePeriodSeconds),
	}
}

// written reports a timing field only when the spec set it. Zero is not a value
// any of these fields can hold, so zero is how a spec says nothing at all.
func written(value int32) *int32 {
	if value == 0 {
		return nil
	}
	return &value
}

// effectiveProbe is a probe with the kubelet's defaults filled in: what the
// kubelet will actually do. Timing is derived from it, and comparing two of
// them is what makes drift a difference in behaviour rather than in wording.
type effectiveProbe struct {
	Handler                       model.Handler
	InitialDelaySeconds           int32
	PeriodSeconds                 int32
	TimeoutSeconds                int32
	SuccessThreshold              int32
	FailureThreshold              int32
	TerminationGracePeriodSeconds *int64
}

// effective applies the kubelet's defaults to a probe, and returns nil for a
// probe that is not configured at all.
func effective(probe *corev1.Probe) *effectiveProbe {
	if probe == nil {
		return nil
	}
	return &effectiveProbe{
		Handler:                       handlerOf(probe.ProbeHandler),
		InitialDelaySeconds:           orDefault(probe.InitialDelaySeconds, defaultInitialDelaySeconds),
		PeriodSeconds:                 orDefault(probe.PeriodSeconds, defaultPeriodSeconds),
		TimeoutSeconds:                orDefault(probe.TimeoutSeconds, defaultTimeoutSeconds),
		SuccessThreshold:              orDefault(probe.SuccessThreshold, defaultSuccessThreshold),
		FailureThreshold:              orDefault(probe.FailureThreshold, defaultFailureThreshold),
		TerminationGracePeriodSeconds: clone(probe.TerminationGracePeriodSeconds),
	}
}

func orDefault(value, fallback int32) int32 {
	if value == 0 {
		return fallback
	}
	return value
}

// clone copies a pointer field so nothing in the report shares memory with the
// objects the API server returned.
func clone[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// handlerOf renders a probe's handler twice over: as the fields a rule
// compares, with defaults applied, and as the one line a person reads.
//
// A probe with no handler cannot exist in a pod the API server accepted, and
// arrives here only from a hand-written manifest. It is reported as a probe
// with a handler of no type rather than dropped, because a probe the spec
// carries is a probe the reader asked about.
func handlerOf(h corev1.ProbeHandler) model.Handler {
	switch {
	case h.HTTPGet != nil:
		handler := model.Handler{
			Type:   model.HandlerHTTP,
			Path:   orEmpty(h.HTTPGet.Path, "/"),
			Port:   h.HTTPGet.Port.String(),
			Host:   h.HTTPGet.Host,
			Scheme: orEmpty(string(h.HTTPGet.Scheme), string(corev1.URISchemeHTTP)),
		}
		for _, header := range h.HTTPGet.HTTPHeaders {
			handler.HTTPHeaders = append(handler.HTTPHeaders, model.HTTPHeader{
				Name:  header.Name,
				Value: header.Value,
			})
		}
		handler.Summary = httpSummary(handler)
		return handler

	case h.TCPSocket != nil:
		handler := model.Handler{
			Type: model.HandlerTCP,
			Port: h.TCPSocket.Port.String(),
			Host: h.TCPSocket.Host,
		}
		handler.Summary = "tcp " + handler.Host + ":" + handler.Port
		return handler

	case h.GRPC != nil:
		handler := model.Handler{
			Type: model.HandlerGRPC,
			Port: strconv.FormatInt(int64(h.GRPC.Port), 10),
		}
		if h.GRPC.Service != nil {
			handler.Service = *h.GRPC.Service
		}
		handler.Summary = "grpc :" + handler.Port
		if handler.Service != "" {
			handler.Summary += " " + handler.Service
		}
		return handler

	case h.Exec != nil:
		handler := model.Handler{
			Type:    model.HandlerExec,
			Command: slices.Clone(h.Exec.Command),
		}
		handler.Summary = strings.TrimSpace("exec " + strings.Join(handler.Command, " "))
		return handler
	}
	return model.Handler{}
}

// httpSummary is an http handler on one line: the request it makes, and a
// parenthetical for whatever about it is not the default, so the common case
// stays short enough to read in a table.
func httpSummary(handler model.Handler) string {
	summary := "GET " + handler.Path + ":" + handler.Port

	var notes []string
	if !strings.EqualFold(handler.Scheme, string(corev1.URISchemeHTTP)) {
		notes = append(notes, strings.ToLower(handler.Scheme))
	}
	if handler.Host != "" {
		notes = append(notes, "host "+handler.Host)
	}
	if len(notes) > 0 {
		summary += " (" + strings.Join(notes, ", ") + ")"
	}
	return summary
}

func orEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// probeOf returns one of a container's three probes. It takes a nil container,
// which is the workload that has no template to ask, or no pod running a
// container the template has.
func probeOf(container *corev1.Container, probe model.ProbeType) *corev1.Probe {
	if container == nil {
		return nil
	}
	switch probe {
	case model.ProbeStartup:
		return container.StartupProbe
	case model.ProbeReadiness:
		return container.ReadinessProbe
	case model.ProbeLiveness:
		return container.LivenessProbe
	}
	return nil
}
