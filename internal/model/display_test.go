// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import "testing"

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		group string
		kind  string
		obj   string
		want  string
	}{
		{name: "deployment", group: "apps", kind: "Deployment", obj: "api", want: "deploy/api"},
		{name: "statefulset", group: "apps", kind: "StatefulSet", obj: "db", want: "sts/db"},
		{name: "daemonset", group: "apps", kind: "DaemonSet", obj: "node-exporter", want: "ds/node-exporter"},
		{name: "job", group: "batch", kind: "Job", obj: "backup", want: "job/backup"},
		{name: "cronjob", group: "batch", kind: "CronJob", obj: "nightly", want: "cj/nightly"},
		{name: "pod", group: "", kind: "Pod", obj: "shell", want: "pod/shell"},
		{
			name:  "custom kind keeps its group",
			group: "argoproj.io", kind: "Rollout", obj: "checkout",
			want: "rollout.argoproj.io/checkout",
		},
		{
			name:  "mixed case group is lowercased",
			group: "Acme.IO", kind: "MachineSet", obj: "workers",
			want: "machineset.acme.io/workers",
		},
		{
			name:  "core kind without a short name",
			group: "", kind: "ReplicationController", obj: "legacy",
			want: "replicationcontroller/legacy",
		},
		{
			name:  "short names are not borrowed by another group",
			group: "acme.io", kind: "Deployment", obj: "api",
			want: "deployment.acme.io/api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayName(tt.group, tt.kind, tt.obj); got != tt.want {
				t.Errorf("DisplayName(%q, %q, %q) = %q, want %q", tt.group, tt.kind, tt.obj, got, tt.want)
			}
		})
	}
}
