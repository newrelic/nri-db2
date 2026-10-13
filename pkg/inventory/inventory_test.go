package inventory

import (
	"errors"
	"reflect"
	"testing"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// recorder collects what a Setter received, keyed by "key/field".
type recorder map[string]any

func (r recorder) set(key, field string, value any) error {
	r[key+"/"+field] = value
	return nil
}

func param(name string, value any, flags string, deferred any, defFlags string) queries.Row {
	return queries.Row{
		"name": name, "value": value, "value_flags": flags,
		"deferred_value": deferred, "deferred_value_flags": defFlags,
	}
}

func TestParameters(t *testing.T) {
	tests := []struct {
		name string
		rows []queries.Row
		want recorder
		n    int
	}{
		{
			"plain value, no deferred difference",
			[]queries.Row{param("locktimeout", "-1", "NONE", "-1", "NONE")},
			recorder{"db.locktimeout/value": "-1"}, 1,
		},
		{
			"AUTOMATIC reports the flag word, equal deferred is omitted",
			[]queries.Row{param("logbufsz", "2149", "AUTOMATIC", "2149", "AUTOMATIC")},
			recorder{"db.logbufsz/value": "AUTOMATIC"}, 1,
		},
		{
			"COMPUTED flag word",
			[]queries.Row{param("sheapthres_shr", "1000", "COMPUTED", nil, "")},
			recorder{"db.sheapthres_shr/value": "COMPUTED"}, 1,
		},
		{
			"deferred differs",
			[]queries.Row{param("self_tuning_mem", "ON (Active)", "", "ON", "")},
			recorder{"db.self_tuning_mem/value": "ON (Active)", "db.self_tuning_mem/deferredValue": "ON"}, 1,
		},
		{
			"NULL value is skipped",
			[]queries.Row{param("x", nil, "", nil, "")},
			recorder{}, 0,
		},
		{
			"empty name is skipped",
			[]queries.Row{param("  ", "1", "", "1", "")},
			recorder{}, 0,
		},
		{
			"name and value are trimmed",
			[]queries.Row{param(" mon_heap_sz ", " 90 ", "", "90", "")},
			recorder{"db.mon_heap_sz/value": "90"}, 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recorder{}
			if n := Parameters("db", tt.rows, got.set); n != tt.n {
				t.Errorf("written = %d, want %d", n, tt.n)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("items = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParametersSetterErrorContinues(t *testing.T) {
	calls := 0
	set := func(key, field string, value any) error {
		calls++
		if key == "dbm.a" {
			return errors.New("boom")
		}
		return nil
	}
	rows := []queries.Row{param("a", "1", "", "1", ""), param("b", "2", "", "2", "")}
	if n := Parameters("dbm", rows, set); n != 1 {
		t.Errorf("written = %d, want 1", n)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestInstance(t *testing.T) {
	row := queries.Row{
		"inst_name": "db2inst1", "service_level": "DB2 v11.5.9.0", "release_num": "060A010F",
		"bld_level": "s2310", "ptf": nil, "fixpack_num": int64(0),
		"num_members": int64(1), "num_dbpartitions": int64(1),
	}
	got := recorder{}
	if n := Instance([]queries.Row{row}, got.set); n != 7 {
		t.Errorf("written = %d, want 7", n)
	}
	want := recorder{
		"instance/name": "db2inst1", "instance/version": "DB2 v11.5.9.0",
		"instance/releaseNumber": "060A010F", "instance/buildLevel": "s2310",
		"instance/fixpackNumber": "0", "instance/members": "1", "instance/partitions": "1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
	if n := Instance(nil, got.set); n != 0 {
		t.Errorf("empty rows written = %d, want 0", n)
	}
}
