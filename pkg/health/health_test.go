package health

import (
	"testing"

	"github.com/newrelic/infra-integrations-sdk/v3/integration"
)

func TestSetCanConnect(t *testing.T) {
	i, err := integration.New("test", "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.Entity("db2/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}
	ms := e.NewMetricSet("Db2DatabaseSample")

	for _, tt := range []struct {
		ok   bool
		want float64
	}{{true, 1}, {false, 0}, {true, 1}} { // last write wins
		if err := SetCanConnect(ms, tt.ok); err != nil {
			t.Fatal(err)
		}
		if got := ms.Metrics[MetricCanConnect]; got != tt.want {
			t.Errorf("canConnect after SetCanConnect(%t) = %v, want %v", tt.ok, got, tt.want)
		}
	}
}

func TestStatusCode(t *testing.T) {
	tests := []struct {
		status string
		want   int
	}{
		{"ACTIVE", 0}, {"ACTIVE_STANDBY", 0}, {"STANDBY", 0},
		{"QUIESCE_PEND", 1}, {"ROLLFWD", 1},
		{"QUIESCED", 2},
		{"ACTIVE_PEND", 3}, {"active", 3}, {"", 3},
	}
	for _, tt := range tests {
		if got := StatusCode(tt.status); got != tt.want {
			t.Errorf("StatusCode(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestSetDatabaseStatus(t *testing.T) {
	i, err := integration.New("test", "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	e, err := i.Entity("db2/TESTDB", "db2-instance")
	if err != nil {
		t.Fatal(err)
	}

	ms := e.NewMetricSet("Db2DatabaseSample")
	if err := SetDatabaseStatus(ms, "QUIESCED", true); err != nil {
		t.Fatal(err)
	}
	if got := ms.Metrics[MetricDatabaseStatus]; got != "QUIESCED" {
		t.Errorf("databaseStatus = %v, want QUIESCED", got)
	}
	if got := ms.Metrics[MetricDatabaseStatusCode]; got != 2.0 {
		t.Errorf("databaseStatusCode = %v, want 2", got)
	}

	null := e.NewMetricSet("Db2DatabaseSample")
	if err := SetDatabaseStatus(null, "", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := null.Metrics[MetricDatabaseStatus]; ok {
		t.Error("databaseStatus must be omitted for NULL")
	}
	if got := null.Metrics[MetricDatabaseStatusCode]; got != 3.0 {
		t.Errorf("databaseStatusCode = %v, want 3", got)
	}
}
