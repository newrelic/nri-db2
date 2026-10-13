package main

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"

	"github.com/newrelic/nri-db2/pkg/config"
	"github.com/newrelic/nri-db2/pkg/extras"
	"github.com/newrelic/nri-db2/pkg/queries"
)

func TestCollectExtras(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	cfg := config.Config{TopQueriesLimit: 5, LongRunningThresholdSeconds: 60}
	top, long, locks := queries.TopQueries(5), queries.LongRunning(60), queries.LockWaits
	good := map[string][]queries.Row{
		top: {{"member": int64(0), "executable_id": "AA", "num_executions": int64(2)}},
		long: {{
			"member": int64(0), "application_handle": int64(9), "uow_id": int64(1), "activity_id": int64(1),
			"local_start_time": now.Add(-time.Hour), "now_ts": now,
		}},
		locks: {{
			"req_application_handle": int64(3), "lock_wait_start_time": now.Add(-time.Second), "now_ts": now,
		}},
	}
	all := []string{top, long, locks}
	tests := []struct {
		name        string
		enable      func(*config.Config)
		failOn      map[string]error
		wantOK      bool
		wantQueries []string
		wantSamples []string
	}{
		{"all flags off run nothing", func(*config.Config) {}, nil, true, nil, nil},
		{
			"top queries only", func(c *config.Config) { c.CollectTopQueries = true }, nil,
			true, []string{top}, []string{extras.Db2TopQuerySample},
		},
		{
			"long running only", func(c *config.Config) { c.CollectLongRunningQueries = true }, nil,
			true, []string{long}, []string{extras.Db2LongRunningQuerySample},
		},
		{
			"lock waits only", func(c *config.Config) { c.CollectLockWaits = true }, nil,
			true, []string{locks}, []string{extras.Db2LockWaitSample},
		},
		{
			"all on, in order", func(c *config.Config) {
				c.CollectTopQueries, c.CollectLongRunningQueries, c.CollectLockWaits = true, true, true
			}, nil,
			true, all, []string{extras.Db2TopQuerySample, extras.Db2LongRunningQuerySample, extras.Db2LockWaitSample},
		},
		{
			"missing grant on one leaves the others published", func(c *config.Config) {
				c.CollectTopQueries, c.CollectLongRunningQueries, c.CollectLockWaits = true, true, true
			},
			map[string]error{long: dbErr("42501", -551)},
			true, all, []string{extras.Db2TopQuerySample, extras.Db2LockWaitSample},
		},
		{
			"any other error also skips only that query", func(c *config.Config) {
				c.CollectTopQueries, c.CollectLockWaits = true, true
			},
			map[string]error{top: errors.New("boom")},
			true, []string{top, locks}, []string{extras.Db2LockWaitSample},
		},
		{
			"connection error loses the session and skips the rest", func(c *config.Config) {
				c.CollectTopQueries, c.CollectLongRunningQueries, c.CollectLockWaits = true, true, true
			},
			map[string]error{long: dbErr("08506", -30108)},
			false, []string{top, long}, []string{extras.Db2TopQuerySample},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i, err := integration.New("test", "0.0.0", integration.InMemoryStore())
			if err != nil {
				t.Fatal(err)
			}
			e, err := i.Entity("db2/TESTDB", "db2-instance")
			if err != nil {
				t.Fatal(err)
			}
			var samples []string
			newSet := func(sample string, dims ...attribute.Attribute) *metric.Set {
				samples = append(samples, sample)
				return e.NewMetricSet(sample, dims...)
			}
			var ran []string
			fetch := func(q string) ([]queries.Row, error) {
				ran = append(ran, q)
				if err := tt.failOn[q]; err != nil {
					return nil, err
				}
				return good[q], nil
			}
			c := cfg
			tt.enable(&c)
			if got := collectExtras(fetch, c, newSet); got != tt.wantOK {
				t.Errorf("collectExtras() = %t, want %t", got, tt.wantOK)
			}
			if !slices.Equal(ran, tt.wantQueries) {
				t.Errorf("ran %v, want %v", ran, tt.wantQueries)
			}
			if !slices.Equal(samples, tt.wantSamples) {
				t.Errorf("samples = %v, want %v", samples, tt.wantSamples)
			}
		})
	}
}

// An empty result is normal: nothing is written and the session stays good.
func TestCollectExtrasEmpty(t *testing.T) {
	cfg := config.Config{
		CollectTopQueries: true, CollectLongRunningQueries: true, CollectLockWaits: true,
		TopQueriesLimit: 5, LongRunningThresholdSeconds: 60,
	}
	newSet := func(sample string, _ ...attribute.Attribute) *metric.Set {
		t.Errorf("unexpected sample %s", sample)
		return nil
	}
	fetch := func(string) ([]queries.Row, error) { return nil, nil }
	if !collectExtras(fetch, cfg, newSet) {
		t.Error("collectExtras() = false, want true")
	}
}
