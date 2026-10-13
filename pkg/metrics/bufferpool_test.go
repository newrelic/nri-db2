package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/integration"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// bpColumns are the 32 numeric columns of the MON_GET_BUFFERPOOL query.
var bpColumns = func() []string {
	var cols []string
	for _, t := range []string{"col", "data", "index", "xda"} {
		cols = append(cols,
			"pool_async_"+t+"_lbp_pages_found",
			"pool_"+t+"_gbp_l_reads", "pool_"+t+"_gbp_p_reads",
			"pool_"+t+"_l_reads", "pool_"+t+"_lbp_pages_found", "pool_"+t+"_p_reads",
			"pool_temp_"+t+"_l_reads", "pool_temp_"+t+"_p_reads",
		)
	}
	return cols
}()

// bpRow builds a row with every counter 0, then applies the overrides.
func bpRow(name string, set map[string]any) queries.Row {
	row := queries.Row{"bp_name": name}
	for _, c := range bpColumns {
		row[c] = int64(0)
	}
	for k, v := range set {
		row[k] = v
	}
	return row
}

// poolSets returns a NewSet that creates Db2BufferpoolSample sets on e and records them by name.
func poolSets(e *integration.Entity) (NewSet, map[string]*metric.Set) {
	sets := map[string]*metric.Set{}
	return func(name string) *metric.Set {
		ms := e.NewMetricSet("Db2BufferpoolSample",
			attribute.Attr("database", "TESTDB"), attribute.Attr("bufferpoolName", name))
		sets[name] = ms
		return ms
	}, sets
}

// bpMetricNames lists the bufferpool metrics of a set, excluding the dimension attribute.
func bpMetricNames(ms *metric.Set, prefix string) []string {
	var out []string
	for k := range ms.Metrics {
		if strings.HasPrefix(k, prefix) && k != "bufferpoolName" {
			out = append(out, k)
		}
	}
	return out
}

func groupNames(ms *metric.Set) []string { return bpMetricNames(ms, "bufferpoolGroup") }

func nonGroupNames(ms *metric.Set) []string {
	var out []string
	for _, k := range bpMetricNames(ms, "bufferpool") {
		if !strings.HasPrefix(k, "bufferpoolGroup") {
			out = append(out, k)
		}
	}
	return out
}

// worked is the IBMDEFAULTBP idle snapshot with known hit percentages.
func worked() queries.Row {
	return bpRow("IBMDEFAULTBP", map[string]any{
		"pool_data_l_reads": int64(1555), "pool_data_lbp_pages_found": int64(1412),
		"pool_async_data_lbp_pages_found": int64(57),
		"pool_index_l_reads":              int64(1526), "pool_index_lbp_pages_found": int64(1222),
		"pool_data_p_reads": int64(200), "pool_index_p_reads": int64(304),
	})
}

func TestBufferpoolWorkedExample(t *testing.T) {
	newSet, sets := poolSets(newEntity(t))
	Bufferpool(newSet, []queries.Row{worked()})

	ms := sets["IBMDEFAULTBP"]
	if ms == nil {
		t.Fatal("no sample for IBMDEFAULTBP")
	}
	// The expressions use the same operation order as the check: a / b * 100.
	want := map[string]float64{
		"bufferpoolDataHitPercent":   float64(1412-57) / float64(1555) * 100,
		"bufferpoolIndexHitPercent":  float64(1222) / float64(1526) * 100,
		"bufferpoolHitPercent":       float64(1355+1222) / float64(1555+1526) * 100,
		"bufferpoolColumnHitPercent": 0,
		"bufferpoolXdaHitPercent":    0,
		// PDELTA reports 0 on the first run.
		"bufferpoolReadsPhysical": 0, "bufferpoolReadsLogical": 0, "bufferpoolReadsTotal": 0,
	}
	for k, v := range want {
		if got, ok := ms.Metrics[k]; !ok || got != v {
			t.Errorf("%s = %v (present %t), want %v", k, got, ok, v)
		}
	}
	// Spot-check the rounded values.
	for k, v := range map[string]float64{"bufferpoolDataHitPercent": 87.138, "bufferpoolIndexHitPercent": 80.079, "bufferpoolHitPercent": 83.642} {
		if got := ms.Metrics[k].(float64); got < v-0.001 || got > v+0.001 {
			t.Errorf("%s = %v, want about %v", k, got, v)
		}
	}
	if n := len(nonGroupNames(ms)); n != 20 {
		t.Errorf("%d bufferpool metrics, want 20: %v", n, nonGroupNames(ms))
	}
	if g := groupNames(ms); len(g) != 0 {
		t.Errorf("group metrics %v on a row with no group reads", g)
	}
}

// TestBufferpoolCounterMapping bumps one source column at a time and checks which counters move.
func TestBufferpoolCounterMapping(t *testing.T) {
	defer persist.SetNow(time.Now)
	for _, pt := range pageTypes {
		for _, kind := range []struct{ col, word string }{{"p_reads", "Physical"}, {"l_reads", "Logical"}} {
			for _, temp := range []bool{false, true} {
				col := "pool_" + pt.col + "_" + kind.col
				if temp {
					col = "pool_temp_" + pt.col + "_" + kind.col
				}
				t.Run(col, func(t *testing.T) {
					e := newEntity(t)
					run := func(sec int, v int64) *metric.Set {
						persist.SetNow(func() time.Time { return time.Unix(int64(4_000_000+sec), 0) })
						newSet, sets := poolSets(e)
						Bufferpool(newSet, []queries.Row{bpRow("BP", map[string]any{col: v})})
						return sets["BP"]
					}
					run(0, 10)
					ms := run(10, 110)
					want := map[string]float64{
						"bufferpool" + pt.word + "Reads" + kind.word: 100,
						"bufferpool" + pt.word + "ReadsTotal":        100,
						"bufferpool" + "Reads" + kind.word:           100,
						"bufferpoolReadsTotal":                       100,
					}
					for _, k := range nonGroupNames(ms) {
						if !strings.Contains(k, "Reads") {
							continue
						}
						if got := ms.Metrics[k]; got != want[k] {
							t.Errorf("%s = %v, want %v", k, got, want[k])
						}
					}
				})
			}
		}
	}
}

// TestBufferpoolHitPercentFormula performs temporary reads counting in the
// denominator but their pages found do not count in the numerator.
func TestBufferpoolHitPercentFormula(t *testing.T) {
	newSet, sets := poolSets(newEntity(t))
	Bufferpool(newSet, []queries.Row{bpRow("BP", map[string]any{
		"pool_data_l_reads": int64(100), "pool_temp_data_l_reads": int64(100),
		"pool_data_lbp_pages_found": int64(70), "pool_async_data_lbp_pages_found": int64(20),
		"pool_xda_l_reads": int64(10), "pool_xda_lbp_pages_found": int64(5),
		"pool_col_l_reads": int64(40), "pool_col_lbp_pages_found": int64(30),
	})})
	ms := sets["BP"]
	for k, v := range map[string]float64{
		"bufferpoolDataHitPercent":   25, // (70-20)/(100+100)
		"bufferpoolXdaHitPercent":    50,
		"bufferpoolColumnHitPercent": 75,
		"bufferpoolIndexHitPercent":  0,
		"bufferpoolHitPercent":       float64(50+5+30) / float64(200+10+40) * 100,
	} {
		if got := ms.Metrics[k]; got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
}

func TestBufferpoolGroup(t *testing.T) {
	t.Run("pureScale row emits the group gauges whose GL is non-zero", func(t *testing.T) {
		newSet, sets := poolSets(newEntity(t))
		Bufferpool(newSet, []queries.Row{bpRow("BP", map[string]any{
			"pool_col_gbp_l_reads": int64(10), "pool_col_gbp_p_reads": int64(2),
			"pool_index_gbp_l_reads": int64(20), "pool_index_gbp_p_reads": int64(5),
			"pool_xda_gbp_l_reads": int64(8), "pool_xda_gbp_p_reads": nil, // NULL counts as 0
			// data: GL = 0 and a stray GP read: no gauge
			"pool_data_gbp_p_reads": int64(3),
		})})
		ms := sets["BP"]
		want := map[string]float64{
			"bufferpoolGroupColumnHitPercent": float64(8) / float64(10) * 100,
			"bufferpoolGroupIndexHitPercent":  float64(15) / float64(20) * 100,
			"bufferpoolGroupXdaHitPercent":    100,
			// GF sum = 8 + (-3) + 15 + 8, GL sum = 10 + 0 + 20 + 8
			"bufferpoolGroupHitPercent": float64(8-3+15+8) / float64(38) * 100,
		}
		for k, v := range want {
			if got, ok := ms.Metrics[k]; !ok || got != v {
				t.Errorf("%s = %v (present %t), want %v", k, got, ok, v)
			}
		}
		if _, ok := ms.Metrics["bufferpoolGroupDataHitPercent"]; ok {
			t.Error("bufferpoolGroupDataHitPercent must be absent when GL is 0")
		}
		if n := len(groupNames(ms)); n != 4 {
			t.Errorf("%d group metrics, want 4: %v", n, groupNames(ms))
		}
	})
	t.Run("all five when every GL is non-zero", func(t *testing.T) {
		set := map[string]any{}
		for _, pt := range pageTypes {
			set["pool_"+pt.col+"_gbp_l_reads"] = int64(4)
		}
		newSet, sets := poolSets(newEntity(t))
		Bufferpool(newSet, []queries.Row{bpRow("BP", set)})
		if n := len(groupNames(sets["BP"])); n != 5 {
			t.Errorf("%d group metrics, want 5: %v", n, groupNames(sets["BP"]))
		}
	})
	t.Run("NULL group columns emit nothing and break nothing", func(t *testing.T) {
		set := map[string]any{"pool_data_l_reads": int64(10), "pool_data_lbp_pages_found": int64(9)}
		for _, pt := range pageTypes {
			set["pool_"+pt.col+"_gbp_l_reads"] = nil
			set["pool_"+pt.col+"_gbp_p_reads"] = nil
		}
		newSet, sets := poolSets(newEntity(t))
		Bufferpool(newSet, []queries.Row{bpRow("BP", set)})
		ms := sets["BP"]
		if g := groupNames(ms); len(g) != 0 {
			t.Errorf("group metrics %v from NULL columns", g)
		}
		if got := ms.Metrics["bufferpoolDataHitPercent"]; got != 90.0 {
			t.Errorf("bufferpoolDataHitPercent = %v, want 90", got)
		}
	})
}

func TestBufferpoolNullColumns(t *testing.T) {
	t.Run("NULL data logical reads skips only what needs it", func(t *testing.T) {
		row := worked()
		row["pool_data_l_reads"] = nil
		newSet, sets := poolSets(newEntity(t))
		Bufferpool(newSet, []queries.Row{row})
		ms := sets["IBMDEFAULTBP"]
		for _, k := range []string{
			"bufferpoolDataReadsLogical", "bufferpoolDataReadsTotal", "bufferpoolDataHitPercent",
			"bufferpoolReadsLogical", "bufferpoolReadsTotal", "bufferpoolHitPercent",
		} {
			if _, ok := ms.Metrics[k]; ok {
				t.Errorf("%s must be absent", k)
			}
		}
		for _, k := range []string{
			"bufferpoolDataReadsPhysical", "bufferpoolReadsPhysical", "bufferpoolIndexHitPercent",
			"bufferpoolIndexReadsTotal", "bufferpoolColumnHitPercent",
		} {
			if _, ok := ms.Metrics[k]; !ok {
				t.Errorf("%s missing", k)
			}
		}
	})
	t.Run("unexpected type skips the metric", func(t *testing.T) {
		row := worked()
		row["pool_index_p_reads"] = "abc"
		newSet, sets := poolSets(newEntity(t))
		Bufferpool(newSet, []queries.Row{row})
		if _, ok := sets["IBMDEFAULTBP"].Metrics["bufferpoolIndexReadsPhysical"]; ok {
			t.Error("bufferpoolIndexReadsPhysical must be absent")
		}
	})
}

func TestBufferpoolRows(t *testing.T) {
	t.Run("a row without bp_name creates no sample", func(t *testing.T) {
		var calls []string
		newSet := func(name string) *metric.Set { calls = append(calls, name); return nil }
		row := worked()
		row["bp_name"] = nil
		Bufferpool(newSet, []queries.Row{row})
		if len(calls) != 0 {
			t.Errorf("newSet called for %v", calls)
		}
	})
	t.Run("no rows creates no sample", func(t *testing.T) {
		newSet := func(name string) *metric.Set { t.Errorf("newSet(%q) called", name); return nil }
		Bufferpool(newSet, nil)
	})
	t.Run("each pool has its own PDELTA baseline", func(t *testing.T) {
		defer persist.SetNow(time.Now)
		e := newEntity(t)
		run := func(sec int, a, b int64) map[string]*metric.Set {
			persist.SetNow(func() time.Time { return time.Unix(int64(5_000_000+sec), 0) })
			newSet, sets := poolSets(e)
			Bufferpool(newSet, []queries.Row{
				bpRow("A", map[string]any{"pool_data_p_reads": a}),
				bpRow("B", map[string]any{"pool_data_p_reads": b}),
			})
			return sets
		}
		run(0, 1000, 5)
		sets := run(10, 1100, 7)
		if got := sets["A"].Metrics["bufferpoolDataReadsPhysical"]; got != 100.0 {
			t.Errorf("A = %v, want 100", got)
		}
		if got := sets["B"].Metrics["bufferpoolDataReadsPhysical"]; got != 2.0 {
			t.Errorf("B = %v, want 2", got)
		}
	})
}
