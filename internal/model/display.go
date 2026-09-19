// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import "strings"

// shortNames are the kubectl short names for the workload kinds that have one.
// Keyed by group and kind together, so a Deployment in somebody else's API
// group is not mistaken for the one in apps.
var shortNames = map[string]string{
	"apps/Deployment":  "deploy",
	"apps/StatefulSet": "sts",
	"apps/DaemonSet":   "ds",
	"batch/Job":        "job",
	"batch/CronJob":    "cj",
	"/Pod":             "pod",
}

// DisplayName is how a workload of this group and kind is printed: the kubectl
// short name where there is one, and kind.group otherwise, both lowercase.
//
// The result is also an argument: pasting it into kubectl probes resolves to
// the same workload, which is the point of printing it this way.
func DisplayName(group, kind, name string) string {
	if short, ok := shortNames[group+"/"+kind]; ok {
		return short + "/" + name
	}
	resource := strings.ToLower(kind)
	if group != "" {
		resource += "." + strings.ToLower(group)
	}
	return resource + "/" + name
}
