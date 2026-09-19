// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

func TestHandlerOf(t *testing.T) {
	tests := []struct {
		name        string
		handler     corev1.ProbeHandler
		wantType    model.HandlerType
		wantSummary string
		wantPort    string
		wantPath    string
		wantScheme  string
	}{
		{
			name:        "http with the defaults applied",
			handler:     corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromInt32(8080)}},
			wantType:    model.HandlerHTTP,
			wantSummary: "GET /:8080",
			wantPort:    "8080",
			wantPath:    "/",
			wantScheme:  "HTTP",
		},
		{
			name: "http on a named port",
			handler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz",
				Port: intstr.FromString("http-admin"),
			}},
			wantType:    model.HandlerHTTP,
			wantSummary: "GET /healthz:http-admin",
			wantPort:    "http-admin",
			wantPath:    "/healthz",
			wantScheme:  "HTTP",
		},
		{
			name: "https and a host are worth saying out loud",
			handler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
				Path:   "/healthz",
				Port:   intstr.FromInt32(8443),
				Host:   "10.0.0.1",
				Scheme: corev1.URISchemeHTTPS,
			}},
			wantType:    model.HandlerHTTP,
			wantSummary: "GET /healthz:8443 (https, host 10.0.0.1)",
			wantPort:    "8443",
			wantPath:    "/healthz",
			wantScheme:  "HTTPS",
		},
		{
			name:        "tcp",
			handler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(5432)}},
			wantType:    model.HandlerTCP,
			wantSummary: "tcp :5432",
			wantPort:    "5432",
		},
		{
			name: "tcp against a host",
			handler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt32(5432),
				Host: "127.0.0.1",
			}},
			wantType:    model.HandlerTCP,
			wantSummary: "tcp 127.0.0.1:5432",
			wantPort:    "5432",
		},
		{
			name:        "grpc without a service",
			handler:     corev1.ProbeHandler{GRPC: &corev1.GRPCAction{Port: 9000}},
			wantType:    model.HandlerGRPC,
			wantSummary: "grpc :9000",
			wantPort:    "9000",
		},
		{
			name: "grpc naming a service",
			handler: corev1.ProbeHandler{GRPC: &corev1.GRPCAction{
				Port:    9000,
				Service: ptr.To("checkout.Health"),
			}},
			wantType:    model.HandlerGRPC,
			wantSummary: "grpc :9000 checkout.Health",
			wantPort:    "9000",
		},
		{
			name: "exec",
			handler: corev1.ProbeHandler{Exec: &corev1.ExecAction{
				Command: []string{"/bin/health", "--deep"},
			}},
			wantType:    model.HandlerExec,
			wantSummary: "exec /bin/health --deep",
		},
		{
			name:        "a probe with no handler at all",
			handler:     corev1.ProbeHandler{},
			wantType:    "",
			wantSummary: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := handlerOf(tt.handler)
			if handler.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", handler.Type, tt.wantType)
			}
			if handler.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", handler.Summary, tt.wantSummary)
			}
			if handler.Port != tt.wantPort {
				t.Errorf("Port = %q, want %q", handler.Port, tt.wantPort)
			}
			if handler.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", handler.Path, tt.wantPath)
			}
			if handler.Scheme != tt.wantScheme {
				t.Errorf("Scheme = %q, want %q", handler.Scheme, tt.wantScheme)
			}
		})
	}
}

func TestHandlerOfCopiesTheCommand(t *testing.T) {
	command := []string{"/bin/health", "--deep"}
	handler := handlerOf(corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: command}})
	command[1] = "--shallow"

	if want := []string{"/bin/health", "--deep"}; !reflect.DeepEqual(handler.Command, want) {
		t.Errorf("Command = %v, want %v: the report shares memory with the pod it was read from", handler.Command, want)
	}
}

func TestProbeConfigTellsUnsetFromWritten(t *testing.T) {
	config := probeConfig(&corev1.Probe{
		ProbeHandler:  httpGet("/healthz"),
		PeriodSeconds: 10,
		// Everything else is left unset, the way a spec that trusts the
		// kubelet's defaults writes it.
	})

	if config.PeriodSeconds == nil || *config.PeriodSeconds != 10 {
		t.Errorf("PeriodSeconds = %v, want 10", config.PeriodSeconds)
	}
	for name, field := range map[string]*int32{
		"InitialDelaySeconds": config.InitialDelaySeconds,
		"TimeoutSeconds":      config.TimeoutSeconds,
		"SuccessThreshold":    config.SuccessThreshold,
		"FailureThreshold":    config.FailureThreshold,
	} {
		if field != nil {
			t.Errorf("%s = %d, want it reported as unset", name, *field)
		}
	}
}

func TestProbeConfigOfNothing(t *testing.T) {
	if config := probeConfig(nil); config != nil {
		t.Errorf("probeConfig(nil) = %+v, want nil", config)
	}
}

func TestProbeOf(t *testing.T) {
	container := &corev1.Container{
		Name:           "api",
		StartupProbe:   &corev1.Probe{ProbeHandler: httpGet("/startup")},
		ReadinessProbe: &corev1.Probe{ProbeHandler: httpGet("/ready")},
		LivenessProbe:  &corev1.Probe{ProbeHandler: httpGet("/healthz")},
	}
	wants := map[model.ProbeType]string{
		model.ProbeStartup:   "/startup",
		model.ProbeReadiness: "/ready",
		model.ProbeLiveness:  "/healthz",
	}
	for probe, want := range wants {
		got := probeOf(container, probe)
		if got == nil || got.HTTPGet.Path != want {
			t.Errorf("probeOf(%s) = %v, want the probe on %s", probe, got, want)
		}
	}
	if got := probeOf(nil, model.ProbeLiveness); got != nil {
		t.Errorf("probeOf of no container = %v, want nil", got)
	}
}
