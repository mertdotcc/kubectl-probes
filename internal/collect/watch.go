// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// How long a burst of changes is allowed to run before it is rebuilt. A
// rolling update touches every pod of a workload within a few hundred
// milliseconds, and a consumer wants the state the cluster settled on rather
// than every step it took to get there.
//
// They are variables so a test can shrink them to a few milliseconds.
var (
	// coalesceQuiet is how quiet the cluster has to go before a rebuild.
	coalesceQuiet = 250 * time.Millisecond
	// coalesceCap is the longest a burst can hold a rebuild off, counted from
	// the first change in it, so a cluster that never goes quiet still reports.
	coalesceCap = time.Second
)

// Watch is Collect repeated: it emits a first Result equivalent to the one
// Collect returns, and a fresh Result every time a pod or an Unhealthy event
// in scope changes.
//
// It exists because the Dashboard shows changes at the cluster's own
// timestamps, which means watching rather than polling on a clock of its own.
// See docs/adr/0005-the-dashboard-draws-only-what-the-cluster-reported.md.
//
// The channel is closed when ctx is done, and never blocks: a consumer too
// slow to keep up is given the newest Result and never a queue of stale ones.
// The pods and events a Result points at live in the informer caches behind
// it and are read-only. Nothing rewrites them in place, so a Result already
// handed over does not change under whoever is holding it.
func Watch(ctx context.Context, o Options) (<-chan *Result, error) {
	if len(o.Filenames) > 0 {
		// A set of files is read as one cluster, but it is a cluster frozen at
		// the moment it was written: there is no runtime state in it to change.
		return nil, errors.New("-f cannot be watched: a manifest has no runtime state")
	}

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
	return watchFrom(ctx, clients.typed, newReducer(scope, clients.getObject))
}

// watcher is one run of Watch: the informer caches it reads, the reduction it
// reads them through, and the coalescing that decides when to read them.
type watcher struct {
	reducer *reducer
	pods    listersv1.PodLister
	events  listersv1.EventLister
	// changed carries one token however many changes arrive, because what the
	// loop needs to know is that something moved, not what.
	changed chan struct{}
	// eventsForbidden records that the cluster would not let the watch read
	// events, which makes failure counts unknown rather than zero, exactly as
	// being forbidden to list them does for Collect. It is written by the
	// reflector's goroutine and read by the loop.
	eventsForbidden atomic.Bool
	// refused carries a read the cluster would not allow, so a watch that can
	// never fill its cache fails instead of hanging.
	podsRefused   chan error
	eventsRefused chan error
}

// watchFrom is the watch itself, kept apart from the clients it watches
// through so a test can drive it against a fake clientset.
func watchFrom(ctx context.Context, client kubernetes.Interface, r *reducer) (<-chan *Result, error) {
	// The reflectors run until this context is done, and nobody outside this
	// function ever gets a handle on them. Hanging them off a context of our
	// own is what lets a Watch that failed to start take them down with it,
	// rather than leave them retrying against a cluster nobody is reading.
	ctx, cancel := context.WithCancel(ctx)

	w := &watcher{
		reducer:       r,
		changed:       make(chan struct{}, 1),
		podsRefused:   make(chan error, 1),
		eventsRefused: make(chan error, 1),
	}
	first, err := w.start(ctx, client)
	if err != nil {
		cancel()
		return nil, err
	}

	out := make(chan *Result, 1)
	out <- first
	go w.run(ctx, cancel, out, first)
	return out, nil
}

// start fills the informer caches and reduces them once, which is the Result
// Collect would have returned for the same scope.
func (w *watcher) start(ctx context.Context, client kubernetes.Interface) (*Result, error) {
	// Two factories because the label selector is matched against pod labels
	// and nothing else: an event carries the labels of the object that wrote
	// it, which are not the labels of the pod it is about.
	podFactory := informers.NewSharedInformerFactoryWithOptions(client, 0,
		informers.WithNamespace(w.reducer.scope.namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = w.reducer.scope.selector
		}),
	)
	eventFactory := informers.NewSharedInformerFactoryWithOptions(client, 0,
		informers.WithNamespace(w.reducer.scope.namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = "reason=" + unhealthyReason
		}),
	)
	pods := podFactory.Core().V1().Pods()
	events := eventFactory.Core().V1().Events()
	w.pods = pods.Lister()
	w.events = events.Lister()

	if err := w.follow(pods.Informer(), w.podsRefused); err != nil {
		return nil, err
	}
	if err := w.follow(events.Informer(), w.eventsRefused); err != nil {
		return nil, err
	}

	podFactory.Start(ctx.Done())
	eventFactory.Start(ctx.Done())

	if err := await(ctx, synced(ctx, pods.Informer()), w.podsRefused); err != nil {
		return nil, err
	}
	// Per ADR-0002 a gap is never fatal: the probe configuration is still
	// worth reporting without the events that count its failures.
	if err := await(ctx, synced(ctx, events.Informer()), w.eventsRefused); err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		klog.V(2).Infof("events cannot be watched, failure counts will be unknown: %v", err)
		w.eventsForbidden.Store(true)
	}

	// The initial list reaches the handlers as a change per object. The first
	// Result already holds all of it, so the token it left behind is dropped
	// rather than rebuilt.
	w.drain()
	return w.build(ctx)
}

// follow puts an informer's changes on the watcher's one channel and takes
// over its error reporting.
func (w *watcher) follow(informer cache.SharedIndexInformer, refused chan<- error) error {
	err := informer.SetWatchErrorHandlerWithContext(func(_ context.Context, _ *cache.Reflector, err error) {
		if apierrors.IsForbidden(err) {
			select {
			case refused <- err:
			default:
			}
			return
		}
		// Anything else is a reconnect the reflector makes on its own, and
		// nothing a reader of the report can act on.
		klog.V(2).Infof("a watch failed and will be retried: %v", err)
	})
	if err != nil {
		return err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { w.notify() },
		UpdateFunc: func(any, any) { w.notify() },
		DeleteFunc: func(any) { w.notify() },
	})
	return err
}

func (w *watcher) notify() {
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (w *watcher) drain() {
	select {
	case <-w.changed:
	default:
	}
}

// run rebuilds the Result after every burst of changes, until ctx is done.
func (w *watcher) run(ctx context.Context, cancel context.CancelFunc, out chan *Result, latest *Result) {
	defer close(out)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.changed:
		}
		if !w.settle(ctx) {
			return
		}

		result, err := w.build(ctx)
		if err != nil {
			// A rebuild that failed is not a reason to stop watching: the next
			// change tries again, and until then the consumer has the Result
			// it was last given.
			klog.V(2).Infof("rebuilding after a change failed: %v", err)
			continue
		}
		// A change the report does not show is not a change. Steady state is
		// still, and a consumer that redraws on every snapshot should not be
		// made to redraw the same picture because a watch reconnected.
		if reflect.DeepEqual(result, latest) {
			continue
		}
		latest = result
		emit(out, result)
	}
}

// settle waits out a burst of changes: until the cluster has been quiet for
// coalesceQuiet, or until coalesceCap has passed since the burst began. It
// reports whether there is still a watch to rebuild.
func (w *watcher) settle(ctx context.Context) bool {
	quiet := time.NewTimer(coalesceQuiet)
	defer quiet.Stop()
	capped := time.NewTimer(coalesceCap)
	defer capped.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-w.changed:
			quiet.Stop()
			quiet.Reset(coalesceQuiet)
		case <-quiet.C:
			return true
		case <-capped.C:
			return true
		}
	}
}

// build reads the informer caches the way Collect reads the API server, and
// reduces what it finds through the same code path.
func (w *watcher) build(ctx context.Context) (*Result, error) {
	pods, err := w.pods.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	// The caches are maps, so the order they list in is not an order at all.
	// Sorting restores the one the API server lists in, which is the order
	// Collect saw and the one the report has to stay stable against.
	inAPIOrder(pods)

	items, err := w.reducer.reduce(ctx, pods)
	if err != nil {
		return nil, err
	}

	events, err := w.events.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	inAPIOrder(events)

	result := w.reducer.result(items, eventsByPod(copies(events)))
	if w.eventsForbidden.Load() {
		result.Gaps.Events = true
	}
	return result, nil
}

// emit hands over the newest Result, dropping one the consumer has not taken
// yet. A slow consumer must never hold a watch up, and the Result it skipped
// is a state the cluster has already left behind.
func emit(out chan *Result, result *Result) {
	for {
		select {
		case out <- result:
			return
		default:
		}
		select {
		case <-out:
		default:
		}
	}
}

// synced reports an informer's cache having filled, and says nothing at all
// when it never does.
func synced(ctx context.Context, informer cache.SharedIndexInformer) <-chan struct{} {
	ready := make(chan struct{})
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			close(ready)
		}
	}()
	return ready
}

// await blocks until a cache has filled, the read behind it was refused, or
// the context ended.
func await(ctx context.Context, ready <-chan struct{}, refused <-chan error) error {
	select {
	case <-ready:
		return nil
	case err := <-refused:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// inAPIOrder sorts cached objects the way the API server lists them, by
// namespace and then by name.
func inAPIOrder[T metav1.Object](objects []T) {
	slices.SortFunc(objects, func(a, b T) int {
		if order := strings.Compare(a.GetNamespace(), b.GetNamespace()); order != 0 {
			return order
		}
		return strings.Compare(a.GetName(), b.GetName())
	})
}

// copies takes the events out of the informer cache by value, because a Result
// carries its events and the cache's copies are not this package's to hold.
func copies(events []*corev1.Event) []corev1.Event {
	out := make([]corev1.Event, 0, len(events))
	for _, event := range events {
		out = append(out, *event)
	}
	return out
}
