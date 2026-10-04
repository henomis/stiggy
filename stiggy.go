// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stiggy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/worker"

	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/compile"
	"github.com/henomis/stiggy/internal/stepio"
	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

// Errors returned by an App.
var (
	ErrNilConn     = errors.New("stiggy: nil NATS connection")
	ErrAlreadyRun  = errors.New("stiggy: Run called twice")
	ErrNotReady    = errors.New("stiggy: stopped before it was ready")
	ErrWriteClash  = errors.New("stiggy: activity writes a channel its step also writes")
	errWorkerEnded = errors.New("stiggy: worker stopped")
)

// Retry bounds for workers that start before the engine has provisioned the
// namespace.
const (
	provisionRetryFirst = 200 * time.Millisecond
	provisionRetryMax   = 5 * time.Second

	provisionRetryFactor = 2
)

// App is a deployed fleet: the packtrail engine and the workers that run its
// agent and activity steps, as selected by its [Role].
type App struct {
	nc     *nats.Conn
	opts   *options
	fleet  *spec.Fleet
	plan   *compile.Plan
	client *packtrail.Client
	logger *slog.Logger

	ready       chan struct{}
	ran         atomic.Bool
	stopWorkers context.CancelFunc // set by startWorkers, used by Run only
	stopServers context.CancelFunc // set by start, used by Run only
}

// New validates and compiles the fleet the options describe. It does no I/O:
// models and tools are built, and NATS is used, only by [App.Run].
func New(nc *nats.Conn, opts ...Option) (*App, error) {
	if nc == nil {
		return nil, ErrNilConn
	}

	o := newOptions()
	for _, opt := range opts {
		if err := opt(o); err != nil {
			return nil, err
		}
	}

	if o.registry == nil {
		o.registry = registry.New()
	}

	if o.logger == nil {
		o.logger = slog.Default()
	}

	for _, name := range sortedKeys(o.activities) {
		if err := o.registry.RegisterActivity(name, o.activities[name]); err != nil {
			return nil, err
		}
	}

	fleet := o.fleet

	plan, err := compile.Compile(&fleet, o.registry)
	if err != nil {
		return nil, err
	}

	if err = applyOnly(plan, &fleet, o.only); err != nil {
		return nil, err
	}

	client, err := packtrail.NewClient(nc, packtrail.WithClientNamespace(plan.Namespace))
	if err != nil {
		return nil, fmt.Errorf("stiggy: %w", err)
	}

	return &App{
		nc: nc, opts: o, fleet: &fleet, plan: plan, client: client, logger: o.logger,
		ready: make(chan struct{}),
	}, nil
}

// Plan returns the compiled fleet.
func (a *App) Plan() *compile.Plan { return a.plan }

// Client returns a packtrail client on the fleet's namespace, to start,
// signal, inspect, fork or cancel executions. It is usable before Run.
func (a *App) Client() *packtrail.Client { return a.client }

// Ready is closed once everything the role runs is serving: the engine is
// provisioned and every worker is pulling jobs.
func (a *App) Ready() <-chan struct{} { return a.ready }

// Run serves the fleet until ctx is done, then shuts down in order: workers
// drain their in-flight jobs first, then the engine stops. It returns nil on
// a clean shutdown, or the first error that stopped a component.
func (a *App) Run(ctx context.Context) error {
	if !a.ran.CompareAndSwap(false, true) {
		return ErrAlreadyRun
	}

	errc := make(chan error, len(a.plan.Workers)+exposedCount(a.fleet)+1)

	// The engine outlives the workers: it must keep accepting their
	// completions while they drain.
	engCtx, stopEngine := context.WithCancel(context.WithoutCancel(ctx))
	defer stopEngine()

	var engWG, wkWG, srvWG sync.WaitGroup

	asm := newAssembly(a)

	err := a.start(ctx, engCtx, &engWG, &wkWG, &srvWG, errc, asm)
	if err == nil {
		close(a.ready)

		select {
		case <-ctx.Done():
		case err = <-errc:
		}
	}

	// Shut down from the outside in: exposed servers stop taking prompts and
	// finish theirs, then workers drain their jobs, then the engine stops.
	// Each watches ctx; a component failure must stop them too.
	if a.stopServers != nil {
		a.stopServers()
	}

	srvWG.Wait()

	if a.stopWorkers != nil {
		a.stopWorkers()
	}

	wkWG.Wait()
	stopEngine()
	engWG.Wait()

	// MCP sessions and database pools outlive every job.
	if cerr := asm.close(); cerr != nil {
		a.logger.Warn("stiggy: cleanup", "error", cerr)
	}

	if err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}

	return err
}

func (a *App) start(ctx, engCtx context.Context, engWG, wkWG, srvWG *sync.WaitGroup, errc chan error,
	asm *assembly,
) error {
	if a.opts.role.runsEngine() {
		if err := asm.ingest(ctx, a.logger); err != nil {
			return fmt.Errorf("stiggy: %w", err)
		}

		if err := a.startEngine(ctx, engCtx, engWG, errc); err != nil {
			return err
		}
	}

	if !a.opts.role.runsWorkers() {
		return nil
	}

	if err := a.startWorkers(ctx, wkWG, errc, asm); err != nil {
		return err
	}

	srvCtx, cancel := context.WithCancel(ctx)
	a.stopServers = cancel

	return a.startServers(srvCtx, srvWG, errc, asm)
}

// ErrOnlyUnknown is returned when WithOnly names nothing the fleet runs.
var ErrOnlyUnknown = errors.New("stiggy: only names no worker of the fleet")

// applyOnly keeps the workers and exposed agents named by only (all when
// only is empty).
func applyOnly(plan *compile.Plan, fleet *spec.Fleet, only []string) error {
	if len(only) == 0 {
		return nil
	}

	var kept []compile.Worker

	for _, w := range plan.Workers {
		if slices.Contains(only, w.Agent+w.Activity+w.Remote) {
			kept = append(kept, w)
		}
	}

	var unknown []string

	for _, n := range only {
		_, exposed := fleet.Agents[n]
		runs := slices.ContainsFunc(kept, func(w compile.Worker) bool { return w.Agent+w.Activity+w.Remote == n })
		serves := exposed && fleet.Expose != nil && slices.Contains(fleet.Expose.Agents, n)

		if !runs && !serves {
			unknown = append(unknown, n)
		}
	}

	if len(unknown) > 0 {
		return fmt.Errorf("%w: %v", ErrOnlyUnknown, unknown)
	}

	plan.Workers = kept

	if e := fleet.Expose; e != nil {
		agents := slices.DeleteFunc(slices.Clone(e.Agents), func(a string) bool { return !slices.Contains(only, a) })
		fleet.Expose = &spec.Expose{Owner: e.Owner, Agents: agents, Flows: e.Flows}
	}

	return nil
}

func exposedCount(f *spec.Fleet) int {
	if f.Expose == nil {
		return 0
	}

	return len(f.Expose.Agents) + len(f.Expose.Flows)
}

func (a *App) startEngine(ctx, engCtx context.Context, wg *sync.WaitGroup, errc chan error) error {
	opts := make([]packtrail.Option, 0, 2+len(a.plan.Flows)+len(a.plan.Schedules)+len(a.opts.engineOpts))
	opts = append(opts, packtrail.WithNamespace(a.plan.Namespace), packtrail.WithLogger(a.logger))

	for _, f := range a.plan.Flows {
		opts = append(opts, packtrail.WithFlow(f))
	}

	for _, s := range a.plan.Schedules {
		opts = append(opts, packtrail.WithSchedule(s.Name, s.Flow, s.Cron, s.Input))
	}

	eng, err := packtrail.New(a.nc, append(opts, a.opts.engineOpts...)...)
	if err != nil {
		return fmt.Errorf("stiggy: engine: %w", err)
	}

	done := make(chan struct{})

	wg.Go(func() {
		defer close(done)

		if rerr := eng.Run(engCtx); rerr != nil {
			errc <- fmt.Errorf("stiggy: engine: %w", rerr)
		}
	})

	select {
	case <-eng.Ready():
		return nil
	case <-done:
		return <-errc
	case <-ctx.Done():
		return ErrNotReady
	}
}

func (a *App) startWorkers(ctx context.Context, wg *sync.WaitGroup, errc chan error, asm *assembly) error {
	handlers, err := a.handlers(ctx, asm)
	if err != nil {
		return err
	}

	wkCtx, cancel := context.WithCancel(ctx)
	a.stopWorkers = cancel

	readies := make([]chan struct{}, 0, len(handlers))

	for _, kind := range sortedKeys(handlers) {
		ready := make(chan struct{})
		readies = append(readies, ready)

		wg.Go(func() {
			if rerr := a.runWorker(wkCtx, kind, handlers[kind], ready); rerr != nil {
				errc <- rerr
			}
		})
	}

	for _, ready := range readies {
		select {
		case <-ready:
		case err = <-errc:
			return err
		case <-ctx.Done():
			return ErrNotReady
		}
	}

	return nil
}

// runWorker runs one worker kind. A worker started before the engine has
// provisioned the namespace is retried with backoff until it has.
func (a *App) runWorker(ctx context.Context, kind string, h worker.Handler, ready chan struct{}) error {
	opts := append([]worker.Option{
		worker.WithNamespace(a.plan.Namespace), worker.WithLogger(a.logger),
	}, a.opts.workerOpts...)

	var once sync.Once

	delay := provisionRetryFirst

	for {
		w, err := worker.New(a.nc, kind, h, opts...)
		if err != nil {
			return fmt.Errorf("stiggy: worker %s: %w", kind, err)
		}

		stop := make(chan struct{})

		go func() {
			select {
			case <-w.Ready():
				once.Do(func() { close(ready) })
			case <-stop:
			}
		}()

		err = w.Run(ctx)

		close(stop)

		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, worker.ErrNotProvisioned):
			a.logger.Info("stiggy: waiting for the engine to provision the namespace",
				"kind", kind, "namespace", a.plan.Namespace, "retry_in", delay)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil
			}

			delay = min(delay*provisionRetryFactor, provisionRetryMax)
		case err != nil:
			return fmt.Errorf("stiggy: worker %s: %w", kind, err)
		default:
			return fmt.Errorf("%w: %s", errWorkerEnded, kind)
		}
	}
}

// handlers builds the handlers of the plan's workers.
func (a *App) handlers(ctx context.Context, asm *assembly) (map[string]worker.Handler, error) {
	out := make(map[string]worker.Handler, len(a.plan.Workers))

	for _, wk := range a.plan.Workers {
		if wk.Activity != "" {
			h, _ := a.opts.registry.Activity(wk.Activity)
			out[wk.Kind] = activityHandler(h)

			continue
		}

		if wk.Remote != "" {
			r, err := asm.agentResolver()
			if err != nil {
				return nil, err
			}

			rm := agentrun.Remote{Name: wk.Remote, Spec: a.fleet.Remotes[wk.Remote], Resolver: r}
			out[wk.Kind] = agentrun.RemoteHandler(rm, agentrun.WithFlows(a.client))

			continue
		}

		ag, err := asm.agent(ctx, wk.Agent)
		if err != nil {
			return nil, fmt.Errorf("stiggy: %w", err)
		}

		out[wk.Kind] = agentrun.Handler(ag, agentrun.WithFlows(a.client))
	}

	return out, nil
}

// activityHandler runs a registered activity and applies its step's writes
// to the output, like agent steps.
func activityHandler(h worker.Handler) worker.Handler {
	return func(ctx context.Context, job *worker.Job) (*worker.Result, error) {
		step, err := stepio.Step(job)
		if err != nil {
			return nil, err
		}

		res, err := h(ctx, job)
		if err != nil || res == nil || len(step.Writes) == 0 {
			return res, err
		}

		writes, err := stepio.Writes(step, res.Output)
		if err != nil {
			return nil, err
		}

		if res.Writes == nil {
			res.Writes = map[string]any{}
		}

		for ch, v := range writes {
			if _, clash := res.Writes[ch]; clash {
				return nil, worker.Permanent(fmt.Errorf("%w: %q", ErrWriteClash, ch))
			}

			res.Writes[ch] = v
		}

		return res, nil
	}
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
