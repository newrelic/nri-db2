package main

import (
	"errors"
	"testing"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"

	"github.com/newrelic/nri-db2/pkg/custom"
)

// customHarness returns a NewSet that records the created samples.
func customHarness(t *testing.T) (custom.NewSet, *[]*metric.Set) {
	t.Helper()
	i, err := integration.New("test", "0.0.0", integration.InMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.Entity("db2/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}
	var sets []*metric.Set
	return func(sample string, attrs ...attribute.Attribute) *metric.Set {
		s := e.NewMetricSet(sample, attrs...)
		sets = append(sets, s)
		return s
	}, &sets
}

func TestCollectCustom(t *testing.T) {
	ident := []attribute.Attribute{attribute.Attr("database", "TESTDB")}
	qs := []custom.Query{
		{Name: "ok1", SQL: "q1", Sample: custom.DefaultSample},
		{Name: "typo", SQL: "q2", Sample: custom.DefaultSample},
		{Name: "noperm", SQL: "q3", Sample: custom.DefaultSample},
		{Name: "baddelta", SQL: "q4", Sample: custom.DefaultSample, Types: map[string]custom.Type{"n": custom.Delta}},
		{Name: "ok2", SQL: "q5", Sample: custom.DefaultSample},
	}
	rows := [][]any{{int64(1)}}
	fetch := func(sql string) ([]string, [][]any, bool, error) {
		switch sql {
		case "q2":
			return nil, nil, false, dbErr("42704", 204)
		case "q3":
			return nil, nil, false, dbErr("42501", 551)
		}
		return []string{"n"}, rows, false, nil
	}

	newSet, sets := customHarness(t)
	if !collectCustom(fetch, qs, newSet, ident) {
		t.Fatal("non-connection failures must not clear the session")
	}
	var got []string
	for _, s := range *sets {
		got = append(got, s.Metrics["query"].(string))
	}
	if len(got) != 2 || got[0] != "ok1" || got[1] != "ok2" {
		t.Errorf("published queries = %v, want [ok1 ok2]: a broken query, a missing grant and an attribute-less delta must each skip only themselves", got)
	}
}

func TestCollectCustomConnectionLostStopsTheRest(t *testing.T) {
	calls := 0
	fetch := func(string) ([]string, [][]any, bool, error) {
		calls++
		if calls == 2 {
			return nil, nil, false, dbErr("08506", 30108)
		}
		return []string{"n"}, [][]any{{int64(1)}}, false, nil
	}
	qs := []custom.Query{{Name: "a", SQL: "a"}, {Name: "b", SQL: "b"}, {Name: "c", SQL: "c"}}
	newSet, sets := customHarness(t)
	if collectCustom(fetch, qs, newSet, nil) {
		t.Error("a connection-class error must return false so canConnect becomes 0")
	}
	if calls != 2 || len(*sets) != 1 {
		t.Errorf("calls = %d, sets = %d, want 2 and 1", calls, len(*sets))
	}
}

func TestCollectCustomNoRowsAndPlainError(t *testing.T) {
	fetch := func(sql string) ([]string, [][]any, bool, error) {
		if sql == "boom" {
			return nil, nil, false, errors.New("plain")
		}
		return []string{"n"}, nil, false, nil
	}
	newSet, sets := customHarness(t)
	qs := []custom.Query{{Name: "boom", SQL: "boom"}, {Name: "empty", SQL: "empty"}}
	if !collectCustom(fetch, qs, newSet, nil) || len(*sets) != 0 {
		t.Errorf("sets = %d", len(*sets))
	}
}

// A truncated query publishes the rows it has, and the queries after it still run.
func TestCollectCustomTruncatedQueryStillPublishes(t *testing.T) {
	fetch := func(sql string) ([]string, [][]any, bool, error) {
		return []string{"n"}, [][]any{{int64(1)}, {int64(2)}}, sql == "big", nil
	}
	newSet, sets := customHarness(t)
	qs := []custom.Query{
		{Name: "big", SQL: "big", Sample: custom.DefaultSample},
		{Name: "next", SQL: "next", Sample: custom.DefaultSample},
	}
	if !collectCustom(fetch, qs, newSet, nil) {
		t.Fatal("a truncated query must not clear the session")
	}
	if len(*sets) != 4 {
		t.Errorf("sets = %d, want 4: two rows from each query", len(*sets))
	}
}
