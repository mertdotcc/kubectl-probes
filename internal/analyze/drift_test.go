// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func TestDrifted(t *testing.T) {
	tests := []struct {
		name     string
		running  *corev1.Probe
		template *corev1.Probe
		want     bool
	}{
		{
			name: "identical",
			running: &corev1.Probe{
				ProbeHandler:  httpGet("/healthz"),
				PeriodSeconds: 10,
			},
			template: &corev1.Probe{
				ProbeHandler:  httpGet("/healthz"),
				PeriodSeconds: 10,
			},
		},
		{
			name: "one field differs",
			running: &corev1.Probe{
				ProbeHandler:  httpGet("/healthz"),
				PeriodSeconds: 10,
			},
			template: &corev1.Probe{
				ProbeHandler:  httpGet("/healthz"),
				PeriodSeconds: 30,
			},
			want: true,
		},
		{
			name:    "the running pod leaves written out what the template writes as the default",
			running: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			template: &corev1.Probe{
				ProbeHandler:        httpGet("/healthz"),
				InitialDelaySeconds: 0,
				PeriodSeconds:       10,
				TimeoutSeconds:      1,
				SuccessThreshold:    1,
				FailureThreshold:    3,
			},
		},
		{
			name: "the template leaves the handler's defaults unwritten",
			running: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstr.FromInt32(8080), Scheme: corev1.URISchemeHTTP},
			}},
			template: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromInt32(8080)},
			}},
		},
		{
			name:    "the template has no probe there",
			running: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			want:    true,
		},
		{
			name:     "the running pod has no probe there",
			template: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			want:     true,
		},
		{
			name: "neither configures the probe",
		},
		{
			name:     "the handler's path differs",
			running:  &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			template: &corev1.Probe{ProbeHandler: httpGet("/livez")},
			want:     true,
		},
		{
			name:    "the handler's type differs",
			running: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			template: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(8080)},
			}},
			want: true,
		},
		{
			name: "the handler's port is written as a name on one side",
			running: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromString("http")},
			}},
			template: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			want:     true,
		},
		{
			name: "a header the template does not send",
			running: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:        "/healthz",
					Port:        intstr.FromInt32(8080),
					HTTPHeaders: []corev1.HTTPHeader{{Name: "Accept", Value: "application/json"}},
				},
			}},
			template: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			want:     true,
		},
		{
			name: "an exec command that differs in one argument",
			running: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{Command: []string{"/bin/health", "--deep"}},
			}},
			template: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{Command: []string{"/bin/health"}},
			}},
			want: true,
		},
		{
			name: "a grpc service the template does not name",
			running: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				GRPC: &corev1.GRPCAction{Port: 9000, Service: ptr.To("checkout.Health")},
			}},
			template: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				GRPC: &corev1.GRPCAction{Port: 9000},
			}},
			want: true,
		},
		{
			name: "a termination grace period the template does not set",
			running: &corev1.Probe{
				ProbeHandler:                  httpGet("/healthz"),
				TerminationGracePeriodSeconds: ptr.To(int64(5)),
			},
			template: &corev1.Probe{ProbeHandler: httpGet("/healthz")},
			want:     true,
		},
		{
			name: "the same termination grace period on both sides",
			running: &corev1.Probe{
				ProbeHandler:                  httpGet("/healthz"),
				TerminationGracePeriodSeconds: ptr.To(int64(5)),
			},
			template: &corev1.Probe{
				ProbeHandler:                  httpGet("/healthz"),
				TerminationGracePeriodSeconds: ptr.To(int64(5)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := drifted(tt.running, tt.template); got != tt.want {
				t.Errorf("drifted = %v, want %v", got, tt.want)
			}
		})
	}
}
