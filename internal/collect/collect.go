// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// pageSize is how many pods are asked for at a time. Namespaces with thousands
// of pods are the reason this is paginated at all.
const pageSize = 500

// knownKinds are the workload kinds a bare name is tried against, in the order
// a person most likely meant them.
var knownKinds = []string{"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs", "pods"}

// Options is what the command line asked for, in the terms this package needs.
type Options struct {
	ConfigFlags *genericclioptions.ConfigFlags

	// Args is the positional TYPE[/NAME] argument, if there was one.
	Args []string
	// Filenames are -f files, read locally and never from a cluster. They
	// are read the way a namespace is, so a saved dump of pods, owners, and
	// events reports what the cluster it came from did, and a manifest that
	// has not been applied reports its template alone. See
	// docs/adr/0003-a-set-of-f-files-is-read-as-one-cluster.md.
	Filenames     []string
	Selector      string
	AllNamespaces bool
}

// Collect reads what a run needs from the cluster: pods, the top-most owners
// they belong to, those owners' pod templates, and the Unhealthy events
// recorded against the pods.
//
// Per ADR-0002 it only reads, and it never exercises a probe. Per ADR-0001 the
// pods are the primary source and the templates are secondary.
func Collect(ctx context.Context, o Options) (*Result, error) {
	if len(o.Filenames) > 0 {
		return collectManifests(ctx, o)
	}
	return collectCluster(ctx, o)
}

func collectCluster(ctx context.Context, o Options) (*Result, error) {
	config, err := restConfig(o)
	if err != nil {
		return nil, err
	}
	clients, err := newClients(config, o.ConfigFlags)
	if err != nil {
		return nil, err
	}
	scope, err := resolveScope(o)
	if err != nil {
		return nil, err
	}
	return collectFrom(ctx, clients.typed, newReducer(scope, clients.getObject))
}

// collectFrom is the read itself, kept apart from the clients it reads through
// so that Watch drives the same reduction from an informer cache, and a test
// drives it from a fake one.
func collectFrom(ctx context.Context, client kubernetes.Interface, r *reducer) (*Result, error) {
	pods, err := listPods(ctx, client, r.scope.namespace, r.scope.selector)
	if err != nil {
		return nil, err
	}

	items, err := r.reduce(ctx, pods)
	if err != nil {
		return nil, err
	}

	events := listEvents(ctx, client, namespacesOf(items), &r.gaps)
	return r.result(items, events), nil
}

// restConfig is how both Collect and Watch talk to the API server.
func restConfig(o Options) (*rest.Config, error) {
	config, err := o.ConfigFlags.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	// The owner walk and the per-namespace event lists are many small reads.
	// Client-side throttling is not the limit worth respecting here; the API
	// server's own priority and fairness is.
	config.QPS = 100
	config.Burst = 200
	// The plugin prints its own diagnostics, and a deprecation warning about
	// an owner kind is not something a reader can act on.
	config.WarningHandler = rest.NoWarnings{}
	return config, nil
}

type clients struct {
	typed   kubernetes.Interface
	dynamic dynamic.Interface
	mapper  meta.RESTMapper
}

func newClients(config *rest.Config, cf *genericclioptions.ConfigFlags) (*clients, error) {
	typed, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	mapper, err := cf.ToRESTMapper()
	if err != nil {
		return nil, err
	}
	return &clients{typed: typed, dynamic: dyn, mapper: mapper}, nil
}

// getObject is the owner walk's window onto the cluster: one object, by kind
// and name, through the dynamic client so custom owners work without this tool
// knowing their types.
func (c *clients) getObject(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			// The owner's CRD is not installed, or not served at that version.
			// The reference still names the owner, so treat it the way an
			// unreadable object is treated.
			return nil, apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, name)
		}
		return nil, err
	}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		return c.dynamic.Resource(mapping.Resource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return c.dynamic.Resource(mapping.Resource).Get(ctx, name, metav1.GetOptions{})
}

// listPods reads every pod in scope, a page at a time. resourceVersion=0 lets
// the API server answer the first page from its cache, which is what makes a
// large cluster bearable.
func listPods(ctx context.Context, client kubernetes.Interface, namespace, selector string) ([]*corev1.Pod, error) {
	defer traced("listing pods")()

	var pods []*corev1.Pod
	options := metav1.ListOptions{
		LabelSelector:   selector,
		Limit:           pageSize,
		ResourceVersion: "0",
	}
	for {
		page, err := client.CoreV1().Pods(namespace).List(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("listing pods: %w", err)
		}
		for i := range page.Items {
			pods = append(pods, &page.Items[i])
		}
		if page.Continue == "" {
			return pods, nil
		}
		// A continue token carries its own resourceVersion, and sending both
		// is an error.
		options.Continue = page.Continue
		options.ResourceVersion = ""
	}
}

// listEvents reads the Unhealthy events of each namespace in scope. Being
// forbidden to read events is recorded and survived, per ADR-0002: the rest of
// the report is still worth printing.
func listEvents(ctx context.Context, client kubernetes.Interface, namespaces []string, gaps *Gaps) map[types.UID][]corev1.Event {
	defer traced("listing events")()

	var all []corev1.Event
	for _, namespace := range namespaces {
		options := metav1.ListOptions{
			FieldSelector:   "reason=" + unhealthyReason,
			Limit:           pageSize,
			ResourceVersion: "0",
		}
		for {
			page, err := client.CoreV1().Events(namespace).List(ctx, options)
			if apierrors.IsForbidden(err) {
				klog.V(2).Infof("events are forbidden in namespace %q, failure counts will be unknown", namespace)
				gaps.Events = true
				break
			}
			if err != nil {
				// Anything else is worth noting but not worth failing on: the
				// probe configuration is still readable without events.
				klog.V(2).Infof("listing events in namespace %q failed: %v", namespace, err)
				gaps.Events = true
				break
			}
			all = append(all, page.Items...)
			if page.Continue == "" {
				break
			}
			options.Continue = page.Continue
			options.ResourceVersion = ""
		}
	}
	return eventsByPod(all)
}

func namespacesOf(items []owned) []string {
	seen := map[string]bool{}
	var namespaces []string
	for _, item := range items {
		if namespace := item.pod.Namespace; !seen[namespace] {
			seen[namespace] = true
			namespaces = append(namespaces, namespace)
		}
	}
	return namespaces
}

// traced reports how long a phase took at -v=2, where a slow run is usually
// one phase and not the whole thing.
func traced(phase string) func() {
	start := time.Now()
	return func() {
		klog.V(2).Infof("%s took %v", phase, time.Since(start))
	}
}

// joinKinds renders a list of resource types the way an error message reads
// best.
func joinKinds(kinds []string) string {
	return strings.Join(kinds, ", ")
}
