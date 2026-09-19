// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import "time"

// The schema identifiers every Report carries so a consumer that pins to a
// version can tell when it has changed.
const (
	APIVersion = "probes.kubectl.dev/v1alpha1"
	Kind       = "ProbeReport"
)

// Report is what a run of the plugin found: the single source of truth the
// Overview, the Inspection, and the JSON and YAML output are all built from.
type Report struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	// GeneratedAt is when the report was produced, not when the cluster
	// observed any of the facts in it.
	GeneratedAt time.Time  `json:"generatedAt"`
	Workloads   []Workload `json:"workloads,omitempty"`
}

// New returns an empty Report stamped with the schema identifiers.
func New(generatedAt time.Time) *Report {
	return &Report{
		APIVersion:  APIVersion,
		Kind:        Kind,
		GeneratedAt: generatedAt.UTC(),
	}
}

// Workload is the top-most owner of a set of pods: a Deployment, a StatefulSet,
// a custom owner such as a Rollout, or a bare Pod with no owner.
type Workload struct {
	Kind string `json:"kind"`
	// Group is the API group of Kind, empty for core resources.
	Group     string `json:"group,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	// DisplayName is how the workload is printed, and typing it back into
	// kubectl probes resolves to this same workload. See DisplayName.
	DisplayName string `json:"displayName"`
	// PodCount counts the pods the runtime numbers were aggregated over, so
	// terminating pods are not part of it.
	PodCount int `json:"podCount"`
	// TerminatingPodCount counts the pods left out of PodCount because they
	// carry a deletion timestamp.
	TerminatingPodCount int `json:"terminatingPodCount,omitempty"`
	// TemplateAvailable records whether the workload's pod template could be
	// read. Without it there is nothing to compare a running pod against, so
	// no probe of this workload reports drift.
	TemplateAvailable bool        `json:"templateAvailable,omitempty"`
	Containers        []Container `json:"containers,omitempty"`
	// InitContainers names the plain init containers, which cannot carry a
	// probe at all. They are not Containers and have nothing else to report;
	// they are named so that a reader looking for one does not take its
	// absence for an omission. Sidecars are Containers and are not here.
	InitContainers []string `json:"initContainers,omitempty"`
}

// Container is a probe-bearing container definition within a workload, named
// once however many pods run it.
type Container struct {
	Name string `json:"name"`
	// Sidecar marks an init container with restartPolicy Always, which can
	// carry all three probes.
	Sidecar   bool         `json:"sidecar,omitempty"`
	Startup   *ProbeDetail `json:"startup,omitempty"`
	Readiness *ProbeDetail `json:"readiness,omitempty"`
	Liveness  *ProbeDetail `json:"liveness,omitempty"`
	// Runtime is absent for a workload with no pods, where there is nothing
	// running to describe.
	Runtime *Runtime `json:"runtime,omitempty"`
	Pods    []Pod    `json:"pods,omitempty"`
	// Findings are opinions, kept apart from the facts above so a reader can
	// tell one from the other.
	Findings []Finding `json:"findings,omitempty"`
}

// ProbeType names one of the three kubelet health checks.
type ProbeType string

const (
	ProbeStartup   ProbeType = "startup"
	ProbeReadiness ProbeType = "readiness"
	ProbeLiveness  ProbeType = "liveness"
)

// ProbeTypes lists the probes in the order every surface presents them: the
// order the kubelet puts them to work in.
var ProbeTypes = []ProbeType{ProbeStartup, ProbeReadiness, ProbeLiveness}

// Probe returns the detail for one of the container's three probes, or nil
// when the container has nothing to say about it.
func (c *Container) Probe(t ProbeType) *ProbeDetail {
	switch t {
	case ProbeStartup:
		return c.Startup
	case ProbeReadiness:
		return c.Readiness
	case ProbeLiveness:
		return c.Liveness
	}
	return nil
}

// SetProbe stores the detail for one of the container's three probes and
// reports whether t named a probe.
func (c *Container) SetProbe(t ProbeType, d *ProbeDetail) bool {
	switch t {
	case ProbeStartup:
		c.Startup = d
	case ProbeReadiness:
		c.Readiness = d
	case ProbeLiveness:
		c.Liveness = d
	default:
		return false
	}
	return true
}

// ProbeDetail is everything known about one of a container's three probes: how
// it is configured, what that configuration means in practice, and whether the
// workload template says something different.
//
// A detail with no Config but Drifted set is a probe the template configures
// and the running pod does not.
type ProbeDetail struct {
	Config *Probe  `json:"config,omitempty"`
	Timing *Timing `json:"timing,omitempty"`
	// Drifted records that the running pod's configuration differs from the
	// workload template's, which is a reason to investigate on its own.
	Drifted bool `json:"drifted,omitempty"`
	// Template is the template-side configuration, carried only when it
	// differs from Config.
	Template *Probe `json:"template,omitempty"`
}

// Probe is a probe's configuration exactly as the spec carries it. The timing
// fields are pointers because an unset field means the kubelet default, and a
// reader has to be able to tell that from a field set to the same value.
type Probe struct {
	Handler                       Handler `json:"handler"`
	InitialDelaySeconds           *int32  `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds                 *int32  `json:"periodSeconds,omitempty"`
	TimeoutSeconds                *int32  `json:"timeoutSeconds,omitempty"`
	SuccessThreshold              *int32  `json:"successThreshold,omitempty"`
	FailureThreshold              *int32  `json:"failureThreshold,omitempty"`
	TerminationGracePeriodSeconds *int64  `json:"terminationGracePeriodSeconds,omitempty"`
}

// HandlerType is how a probe asks a container whether it is healthy.
type HandlerType string

const (
	HandlerHTTP HandlerType = "http"
	HandlerTCP  HandlerType = "tcp"
	HandlerGRPC HandlerType = "grpc"
	HandlerExec HandlerType = "exec"
)

// Handler is a probe's handler, both as a line a person reads and as the
// fields a rule compares.
type Handler struct {
	Type HandlerType `json:"type"`
	// Summary is the handler on one line, such as "GET /healthz:8080".
	Summary string `json:"summary"`
	Path    string `json:"path,omitempty"`
	// Port is a port number or a named container port, as written.
	Port        string       `json:"port,omitempty"`
	Host        string       `json:"host,omitempty"`
	Scheme      string       `json:"scheme,omitempty"`
	HTTPHeaders []HTTPHeader `json:"httpHeaders,omitempty"`
	Command     []string     `json:"command,omitempty"`
	// Service is the gRPC service name a grpc handler checks.
	Service string `json:"service,omitempty"`
}

// HTTPHeader is one header an http handler sends.
type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Timing is what a probe's raw fields mean in practice, with kubelet defaults
// already applied.
type Timing struct {
	// FirstCheck is how long after the container starts the first check runs.
	FirstCheck Duration `json:"firstCheck"`
	// FailureDetection is the worst case between a container going bad and
	// the probe acting on it.
	FailureDetection Duration `json:"failureDetection"`
	// StartupBudget is how long a startup probe gives a container to come up
	// before the kubelet restarts it. Startup probes only.
	StartupBudget *Duration `json:"startupBudget,omitempty"`
	// TrafficDelay is how long after the container starts traffic can first
	// arrive. Readiness probes only.
	TrafficDelay *Duration `json:"trafficDelay,omitempty"`
	// AfterStartup records that these durations are counted from the moment
	// the startup probe succeeds, not from container start.
	AfterStartup bool `json:"afterStartup,omitempty"`
}

// Runtime is what the cluster reports about a container across the pods that
// run it, with terminating pods left out.
type Runtime struct {
	// Ready counts the pods reporting this container ready.
	Ready int `json:"ready"`
	Total int `json:"total"`
	// Restarts sums the container's restart count over those pods.
	Restarts int32 `json:"restarts"`
	// Failures sums the container's Unhealthy events. It is absent, rather
	// than zero, when events could not be read.
	Failures *int32 `json:"failures,omitempty"`
}

// Pod is one running instance of a container, and the failure evidence the
// cluster reports for it.
type Pod struct {
	Name string `json:"name"`
	// Ready and Started are the kubelet's container status. Started is absent
	// on a kubelet that does not report it.
	Ready           bool         `json:"ready,omitempty"`
	Started         *bool        `json:"started,omitempty"`
	Restarts        int32        `json:"restarts,omitempty"`
	LastTermination *Termination `json:"lastTermination,omitempty"`
	// Conditions carries the pod's Ready and ContainersReady conditions.
	Conditions []Condition `json:"conditions,omitempty"`
	// Events are the pod's Unhealthy events, grouped by probe.
	Events []EventGroup `json:"events,omitempty"`
}

// Termination is why the container last died.
type Termination struct {
	Reason     string     `json:"reason,omitempty"`
	ExitCode   int32      `json:"exitCode"`
	Signal     int32      `json:"signal,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// ConditionStatus mirrors the tri-state a pod condition carries.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// The pod conditions a probe can move.
const (
	ConditionReady           = "Ready"
	ConditionContainersReady = "ContainersReady"
)

// Condition is one pod condition and when it last changed.
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	LastTransitionTime *time.Time      `json:"lastTransitionTime,omitempty"`
}

// EventGroup is every Unhealthy event a pod reports for one probe, collapsed
// into the shape a reader needs: how often, how long, and what it last said.
type EventGroup struct {
	Probe     ProbeType  `json:"probe"`
	Count     int32      `json:"count"`
	FirstSeen *time.Time `json:"firstSeen,omitempty"`
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
	// Message is the latest probe failure message, verbatim.
	Message string `json:"message,omitempty"`
}

// Finding is a rule's opinion about a container. It says what was observed and
// why it is worth a look; it never proposes a fix.
type Finding struct {
	// Rule is the kebab-case name of the rule that produced the finding.
	Rule    string `json:"rule"`
	Message string `json:"message"`
}
