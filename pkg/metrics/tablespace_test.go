package metrics

import (
	"testing"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"

	"github.com/newrelic/nri-db2/pkg/queries"
)

func tbspSets(e *integration.Entity) (NewSet, map[string]*metric.Set) {
	sets := map[string]*metric.Set{}
	return func(name string) *metric.Set {
		ms := e.NewMetricSet("Db2TablespaceSample",
			attribute.Attr("database", "TESTDB"), attribute.Attr("tablespaceName", name))
		sets[name] = ms
		return ms
	}, sets
}

func tbspRow(name string, pageSize, state any, total, usable, used any) queries.Row {
	return queries.Row{
		"tbsp_name": name, "tbsp_page_size": pageSize, "tbsp_state": state,
		"tbsp_total_pages": total, "tbsp_usable_pages": usable, "tbsp_used_pages": used,
	}
}

func TestTablespaceValues(t *testing.T) {
	tests := []struct {
		name string
		row  queries.Row
		want map[string]float64
	}{
		{
			"USERSPACE1 idle", tbspRow("USERSPACE1", int64(4096), "NORMAL", int64(8192), int64(8160), int64(96)),
			map[string]float64{
				"tablespaceSize": 33554432, "tablespaceUsable": 8160 * 4096, "tablespaceUsed": 393216,
				"tablespaceUtilized": float64(96) / float64(8160) * 100,
			},
		},
		{
			"SYSCATSPACE", tbspRow("SYSCATSPACE", int64(4096), "NORMAL", int64(32768), int64(32764), int64(31572)),
			map[string]float64{"tablespaceUtilized": float64(31572) / float64(32764) * 100},
		},
		{
			"TEMPSPACE1 reports 1/1/1", tbspRow("TEMPSPACE1", int64(4096), "NORMAL", int64(1), int64(1), int64(1)),
			map[string]float64{"tablespaceSize": 4096, "tablespaceUtilized": 100},
		},
		{
			"no usable pages gives 0", tbspRow("EMPTY", int64(4096), "NORMAL", int64(0), int64(0), int64(0)),
			map[string]float64{"tablespaceUtilized": 0, "tablespaceSize": 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newSet, sets := tbspSets(newEntity(t))
			Tablespace(newSet, []queries.Row{tt.row})
			ms := sets[tt.row["tbsp_name"].(string)]
			for k, v := range tt.want {
				if got, ok := ms.Metrics[k]; !ok || got != v {
					t.Errorf("%s = %v (present %t), want %v", k, got, ok, v)
				}
			}
			if got := ms.Metrics["tablespaceState"]; got != "NORMAL" {
				t.Errorf("tablespaceState = %v, want NORMAL", got)
			}
		})
	}
}

// Distinct values per column: a swapped column gives a wrong result.
func TestTablespaceColumnMapping(t *testing.T) {
	newSet, sets := tbspSets(newEntity(t))
	Tablespace(newSet, []queries.Row{tbspRow("T", int64(8192), "NORMAL", int64(1000), int64(900), int64(90))})
	ms := sets["T"]
	for k, v := range map[string]float64{
		"tablespaceSize": 1000 * 8192, "tablespaceUsable": 900 * 8192, "tablespaceUsed": 90 * 8192, "tablespaceUtilized": 10,
	} {
		if got := ms.Metrics[k]; got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
}

func TestTablespaceStateIsKeptAsReturned(t *testing.T) {
	newSet, sets := tbspSets(newEntity(t))
	Tablespace(newSet, []queries.Row{tbspRow("T", int64(4096), "QUIESCED_SHARE ", int64(1), int64(1), int64(1))})
	if got := sets["T"].Metrics["tablespaceState"]; got != "QUIESCED_SHARE " {
		t.Errorf("tablespaceState = %q, want it untrimmed", got)
	}
}

func TestTablespaceNulls(t *testing.T) {
	tests := []struct {
		name    string
		row     queries.Row
		present []string
		absent  []string
	}{
		{
			"NULL state omits only the attribute",
			tbspRow("T", int64(4096), nil, int64(4), int64(3), int64(1)),
			[]string{"tablespaceSize", "tablespaceUsable", "tablespaceUsed", "tablespaceUtilized"}, []string{"tablespaceState"},
		},
		{
			"NULL used pages skips used and utilized",
			tbspRow("T", int64(4096), "NORMAL", int64(4), int64(3), nil),
			[]string{"tablespaceSize", "tablespaceUsable", "tablespaceState"}, []string{"tablespaceUsed", "tablespaceUtilized"},
		},
		{
			"NULL usable pages skips usable and utilized",
			tbspRow("T", int64(4096), "NORMAL", int64(4), nil, int64(1)),
			[]string{"tablespaceSize", "tablespaceUsed"}, []string{"tablespaceUsable", "tablespaceUtilized"},
		},
		{
			"NULL page size skips every size",
			tbspRow("T", nil, "NORMAL", int64(4), int64(3), int64(1)),
			[]string{"tablespaceState"}, []string{"tablespaceSize", "tablespaceUsable", "tablespaceUsed", "tablespaceUtilized"},
		},
		{
			"unexpected type skips the dependent metrics",
			tbspRow("T", int64(4096), "NORMAL", struct{}{}, int64(3), int64(1)),
			[]string{"tablespaceUsable", "tablespaceUsed", "tablespaceUtilized"}, []string{"tablespaceSize"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newSet, sets := tbspSets(newEntity(t))
			Tablespace(newSet, []queries.Row{tt.row})
			ms := sets["T"]
			for _, k := range tt.present {
				if _, ok := ms.Metrics[k]; !ok {
					t.Errorf("%s missing", k)
				}
			}
			for _, k := range tt.absent {
				if v, ok := ms.Metrics[k]; ok {
					t.Errorf("%s = %v, want it absent", k, v)
				}
			}
		})
	}
}

func TestTablespaceRows(t *testing.T) {
	newSet, sets := tbspSets(newEntity(t))
	Tablespace(newSet, []queries.Row{
		{"tbsp_state": "NORMAL"}, // no tbsp_name
		tbspRow("A", int64(4096), "NORMAL", int64(1), int64(1), int64(1)),
		tbspRow("B", int64(4096), "NORMAL", int64(1), int64(1), int64(1)),
	})
	if len(sets) != 2 || sets["A"] == nil || sets["B"] == nil {
		t.Errorf("sets = %v, want A and B only", sets)
	}
}
