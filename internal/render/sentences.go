// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"strings"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The wording of the effective-timing sentences, in one place.
//
// A duration is only worth printing if the reader takes the right meaning from
// it, and the meaning lives in the sentence around it rather than in a column
// heading. Keeping the wording here is what lets the tests assert the exact
// sentences the output is built from instead of a paraphrase of them.
const (
	// sentenceStartupBudget is the startup probe's headline: everything it
	// allows before the kubelet gives up on the container.
	sentenceStartupBudget = "%s gives the container %s to come up before the kubelet restarts it."
	// sentenceFailureDetection is the readiness and liveness headline: how
	// many checks have to fail, and the worst case before one acts.
	sentenceFailureDetection = "%s acts after %d consecutive failures, at worst %s after the container stops responding."
	// sentenceAfterStartup says the durations above are counted from the
	// startup probe succeeding and not from the container starting.
	sentenceAfterStartup = "%s begins after %s."
	sentenceFirstCheck   = "%s waits %s before its first check."
	// sentenceTrafficDelay is only ever about readiness, which is the one
	// probe that has to succeed more than once before it means anything.
	sentenceTrafficDelay = "Traffic can first arrive %s after %s."

	originContainerStart = "the container starts"
	originStartupSuccess = "the startup probe succeeds"
)

// timingSentences is what one probe's effective timing means, as the sentences
// the Inspection prints on a single line.
//
// The headline comes first because it is the number the reader came for, and
// everything qualifying it follows.
func timingSentences(probe model.ProbeType, detail *model.ProbeDetail) []string {
	if detail == nil || detail.Timing == nil {
		return nil
	}
	timing := detail.Timing
	name := titled(string(probe))

	origin := originContainerStart
	if timing.AfterStartup {
		origin = originStartupSuccess
	}

	var out []string
	if probe == model.ProbeStartup && timing.StartupBudget != nil {
		out = append(out, fmt.Sprintf(sentenceStartupBudget, name, short(*timing.StartupBudget)))
	} else {
		threshold, _ := model.Effective(failureThreshold(detail.Config), model.DefaultFailureThreshold)
		out = append(out, fmt.Sprintf(sentenceFailureDetection, name, threshold, short(timing.FailureDetection)))
	}
	if timing.AfterStartup {
		out = append(out, fmt.Sprintf(sentenceAfterStartup, name, originStartupSuccess))
	}
	if timing.FirstCheck > 0 {
		out = append(out, fmt.Sprintf(sentenceFirstCheck, name, short(timing.FirstCheck)))
	}
	if timing.TrafficDelay != nil {
		out = append(out, fmt.Sprintf(sentenceTrafficDelay, short(*timing.TrafficDelay), origin))
	}
	return out
}

func failureThreshold(config *model.Probe) *int32 {
	if config == nil {
		return nil
	}
	return config.FailureThreshold
}

// titled opens a sentence with a probe's name. The names are ASCII by
// construction, so the first byte is the first letter.
func titled(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
