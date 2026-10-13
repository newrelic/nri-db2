package main

import (
	"errors"
	"testing"

	"github.com/newrelic/nri-db2/pkg/queries"
)

func inventoryFetch(rows map[string][]queries.Row, fail map[string]error) fetchFunc {
	return func(q string) ([]queries.Row, error) {
		if err := fail[q]; err != nil {
			return nil, err
		}
		return rows[q], nil
	}
}

func TestCollectInventory(t *testing.T) {
	good := map[string][]queries.Row{
		queries.DBMCfg:   {{"name": "svcename", "value": "50000", "value_flags": "NONE"}},
		queries.DBCfg:    {{"name": "locktimeout", "value": "-1", "value_flags": "NONE"}},
		queries.InstInfo: {{"inst_name": "db2inst1", "num_members": int64(1)}},
	}
	tests := []struct {
		name string
		fail map[string]error
		rows map[string][]queries.Row
		want []string
	}{
		{"all queries work", nil, good, []string{"dbm.svcename", "db.locktimeout", "instance"}},
		{
			"missing privilege skips one query",
			map[string]error{queries.DBCfg: dbErr("42501", -551)}, good,
			[]string{"dbm.svcename", "instance"},
		},
		{
			"connection error does not panic",
			map[string]error{queries.DBMCfg: dbErr("08506", -30108), queries.DBCfg: errors.New("gone")}, good,
			[]string{"instance"},
		},
		{"empty results publish nothing", nil, map[string][]queries.Row{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]bool{}
			set := func(key, field string, value any) error {
				got[key] = true
				return nil
			}
			collectInventory(inventoryFetch(tt.rows, tt.fail), set)
			if len(got) != len(tt.want) {
				t.Errorf("keys = %v, want %v", got, tt.want)
			}
			for _, k := range tt.want {
				if !got[k] {
					t.Errorf("missing key %s in %v", k, got)
				}
			}
		})
	}
}
