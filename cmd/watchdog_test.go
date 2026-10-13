package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/newrelic/nri-db2/pkg/config"
)

func watchdogConfig(runTimeout int) config.Config {
	return config.Config{Hostname: "db2.example.com", Port: 50000, Database: "sample", RunTimeout: runTimeout}
}

// captureStdout runs fn and returns what it wrote to os.Stdout, which is where the SDK publishes.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	func() {
		defer func() { os.Stdout = old }()
		fn()
	}()
	_ = w.Close()
	return <-done
}

// The watchdog fires once, publishes through the injected function and exits with exitOK.
func TestWatchdogFiresOnce(t *testing.T) {
	var fired, exitCode atomic.Int32
	exitCode.Store(-1)
	w := newWatchdog(&publishGuard{}, watchdogConfig(1))
	w.timeout = 10 * time.Millisecond
	w.fire = func(config.Config) error { fired.Add(1); return nil }
	w.exit = func(c int) { exitCode.Store(int32(c)) }

	w.start()
	time.Sleep(200 * time.Millisecond)
	w.expire() // a second expiry does nothing
	if fired.Load() != 1 {
		t.Errorf("fired %d times, want 1", fired.Load())
	}
	if exitCode.Load() != exitOK {
		t.Errorf("exit code %d, want %d", exitCode.Load(), exitOK)
	}
}

func TestWatchdogPublishFailureExitsWithInternalError(t *testing.T) {
	exitCode := -1
	w := newWatchdog(&publishGuard{}, watchdogConfig(1))
	w.fire = func(config.Config) error { return errors.New("stdout closed") }
	w.exit = func(c int) { exitCode = c }
	w.expire()
	if exitCode != exitInternalError {
		t.Errorf("exit code %d, want %d", exitCode, exitInternalError)
	}
}

// Once the watchdog has published, the main path's publish is suppressed.
func TestMainPublishSuppressedAfterFire(t *testing.T) {
	guard := &publishGuard{}
	w := newWatchdog(guard, watchdogConfig(1))
	w.fire = func(config.Config) error { return nil }
	w.exit = func(int) {}
	w.expire()

	ran, err := guard.once(func() error { t.Error("main publish ran after the watchdog fired"); return nil })
	if ran || err != nil {
		t.Errorf("once = %t, %v; want false, nil", ran, err)
	}
}

// The other way round: after the main path published, the timer does nothing.
func TestWatchdogSilentAfterMainPublish(t *testing.T) {
	guard := &publishGuard{}
	if ran, _ := guard.once(func() error { return nil }); !ran {
		t.Fatal("main publish did not run")
	}
	w := newWatchdog(guard, watchdogConfig(1))
	w.fire = func(config.Config) error { t.Error("fired after the main publish"); return nil }
	w.exit = func(int) { t.Error("exited after the main publish") }
	w.expire()
}

// A timeout of 0 arms nothing.
func TestWatchdogDisabled(t *testing.T) {
	w := newWatchdog(&publishGuard{}, watchdogConfig(0))
	w.fire = func(config.Config) error { t.Error("fired although disabled"); return nil }
	w.exit = func(int) { t.Error("exited although disabled") }
	stop := w.start()
	time.Sleep(50 * time.Millisecond)
	stop()
}

// Stopping the watchdog before the deadline prevents the firing.
func TestWatchdogStop(t *testing.T) {
	w := newWatchdog(&publishGuard{}, watchdogConfig(1))
	w.timeout = 50 * time.Millisecond
	w.fire = func(config.Config) error { t.Error("fired after stop"); return nil }
	w.exit = func(int) { t.Error("exited after stop") }
	w.start()()
	time.Sleep(150 * time.Millisecond)
}

type timeoutDoc struct {
	Name            string `json:"name"`
	ProtocolVersion string `json:"protocol_version"`
	Data            []struct {
		Entity struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entity"`
		Metrics []map[string]any `json:"metrics"`
	} `json:"data"`
}

// The real document: protocol 3, the normal entity and identity attributes, canConnect=0.
func TestPublishTimedOutDocument(t *testing.T) {
	out := captureStdout(t, func() {
		if err := publishTimedOut(watchdogConfig(1)); err != nil {
			t.Errorf("publishTimedOut: %s", err)
		}
	})
	var doc timeoutDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not JSON: %s\n%s", err, out)
	}
	if doc.Name != integrationName || doc.ProtocolVersion != "3" {
		t.Errorf("name=%q protocol=%q", doc.Name, doc.ProtocolVersion)
	}
	if len(doc.Data) != 1 {
		t.Fatalf("%d entities, want 1: %s", len(doc.Data), out)
	}
	d := doc.Data[0]
	if d.Entity.Name != "db2.example.com/SAMPLE" || d.Entity.Type != entityNamespace {
		t.Errorf("entity = %+v", d.Entity)
	}
	if len(d.Metrics) != 1 {
		t.Fatalf("%d metric sets, want 1", len(d.Metrics))
	}
	m := d.Metrics[0]
	want := map[string]any{
		"event_type": databaseSample, "canConnect": float64(0),
		"db2Hostname": "db2.example.com", "port": "50000", "database": "SAMPLE",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if _, bare := m["hostname"]; bare {
		t.Error("document carries a bare hostname attribute")
	}
}
