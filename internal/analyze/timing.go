// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import "github.com/mertdotcc/kubectl-probes/internal/model"

// timingOf is what a probe's fields mean in practice, once the kubelet's
// defaults are filled in.
//
// The durations are worst cases, which is what a person debugging a restart
// loop needs: the kubelet checks on a period, so the moment a container goes
// bad falls somewhere inside one, and the failing check that starts the count
// can be a whole period away.
//
// afterStartup says the container also has a startup probe. Readiness and
// liveness are not run until it succeeds, so their durations are counted from
// that moment and not from the container starting.
func timingOf(probe model.ProbeType, e *effectiveProbe, afterStartup bool) *model.Timing {
	if e == nil {
		return nil
	}

	timing := &model.Timing{
		// The first check waits out initialDelaySeconds and nothing else.
		FirstCheck: model.Seconds(e.InitialDelaySeconds),
		// It takes failureThreshold consecutive failures to act, one per
		// period.
		FailureDetection: failureDetection(e),
		AfterStartup:     afterStartup && probe != model.ProbeStartup,
	}

	switch probe {
	case model.ProbeStartup:
		// Everything the startup probe allows before the kubelet gives up on
		// the container and restarts it.
		budget := model.Seconds(e.InitialDelaySeconds) + failureDetection(e)
		timing.StartupBudget = &budget
	case model.ProbeReadiness:
		// Traffic waits for successThreshold consecutive successes. At one,
		// the default, that is the first check and FirstCheck already says so.
		if e.SuccessThreshold > 1 {
			timing.TrafficDelay = model.SecondsPtr(e.InitialDelaySeconds + e.PeriodSeconds*(e.SuccessThreshold-1))
		}
	}
	return timing
}
