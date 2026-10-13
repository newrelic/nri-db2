package main

import (
	"os"
	"sync"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/integration"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/config"
	"github.com/newrelic/nri-db2/pkg/health"
)

// publishGuard lets exactly one publish through. The main path and the watchdog both go through
// it, so only one JSON document reaches stdout.
type publishGuard struct {
	mu   sync.Mutex
	done bool
}

// once runs fn unless an earlier call already did, and reports whether it ran. The lock is held
// while fn runs, so a second caller waits for the first to finish.
func (g *publishGuard) once(fn func() error) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return false, nil
	}
	g.done = true
	return true, fn()
}

// watchdog ends a run that outlives RUN_TIMEOUT. A blocking cgo call cannot be interrupted, but the
// timer goroutine still runs while the main goroutine sits in it, so the watchdog publishes a
// canConnect=0 document of its own and exits the process.
type watchdog struct {
	guard   *publishGuard
	cfg     config.Config
	timeout time.Duration
	// fire builds and publishes the canConnect=0 document; exit ends the process. Both are
	// replaced in tests.
	fire func(config.Config) error
	exit func(int)
}

func newWatchdog(guard *publishGuard, cfg config.Config) *watchdog {
	return &watchdog{
		guard:   guard,
		cfg:     cfg,
		timeout: time.Duration(cfg.RunTimeout) * time.Second,
		fire:    publishTimedOut,
		exit:    os.Exit,
	}
}

// start arms the timer and returns the function that disarms it. A timeout of 0 arms nothing.
func (w *watchdog) start() (stop func()) {
	if w.timeout <= 0 {
		return func() {}
	}
	t := time.AfterFunc(w.timeout, w.expire)
	return func() { t.Stop() }
}

// expire publishes canConnect=0 unless the main path already published, then exits. It holds the
// guard through the exit, so the main goroutine cannot publish or return in between.
func (w *watchdog) expire() {
	w.guard.mu.Lock()
	defer w.guard.mu.Unlock()
	if w.guard.done {
		return
	}
	w.guard.done = true

	log.Error("run exceeded RUN_TIMEOUT of %s: publishing canConnect=0 and exiting", w.timeout)
	if err := w.fire(w.cfg); err != nil {
		log.Error("publish after RUN_TIMEOUT: %s", err)
		w.exit(exitInternalError)
		return
	}
	w.exit(exitOK)
}

// publishTimedOut publishes the document of a run that could not reach the database: the same
// entity and Db2DatabaseSample identity as a normal run, with canConnect=0. It uses an Integration
// of its own and never touches the main one, whose maps have no locking, and it saves no state.
func publishTimedOut(cfg config.Config) error {
	i, err := integration.New(integrationName, version, integration.InMemoryStore())
	if err != nil {
		return err
	}
	e, err := newEntity(i, cfg)
	if err != nil {
		return err
	}
	database := e.NewMetricSet(databaseSample, identityAttrs(cfg)...)
	if err := health.SetCanConnect(database, false); err != nil {
		return err
	}
	return i.Publish()
}
