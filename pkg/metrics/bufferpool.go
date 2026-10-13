package metrics

import (
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// NewSet creates the metric set of one dimension value, for example the Db2BufferpoolSample of
// one bufferpool. The caller owns the entity and the identity attributes.
type NewSet func(dimension string) *metric.Set

// pageTypes are the four page types of MON_GET_BUFFERPOOL: the column prefix and the metric word.
var pageTypes = []struct{ col, word string }{
	{"col", "Column"},
	{"data", "Data"},
	{"index", "Index"},
	{"xda", "Xda"},
}

// Bufferpool turns MON_GET_BUFFERPOOL rows into one sample per bufferpool.
func Bufferpool(newSet NewSet, rows []queries.Row) {
	for _, row := range rows {
		name, ok := row.String("bp_name")
		if !ok {
			log.Warn("%s returned a row without bp_name: row skipped", queries.BufferpoolName)
			continue
		}
		b := builder{ms: newSet(name), query: queries.BufferpoolName, row: row}
		b.bufferpool()
	}
}

// pageCounts are the sums for one page type. Each flag is false when a source column was NULL.
type pageCounts struct {
	physical, logical, found float64
	okPhysical, okLogical    bool
	okFound                  bool
}

// bufferpool calculates the hit percent: the hit percent numerator is the non-temporary local
// bufferpool pages found minus the asynchronous ones, while the denominator includes temporary
// table reads.
func (b builder) bufferpool() {
	var (
		allPhysical, allLogical, allFound = true, true, true
		sumPhysical, sumLogical, sumFound float64
		sumGroupLogical, sumGroupFind     float64
	)
	for _, pt := range pageTypes {
		c := b.pageCounts(pt.col, "bufferpool"+pt.word)

		if c.okPhysical {
			b.set("bufferpool"+pt.word+"ReadsPhysical", c.physical, metric.PDELTA)
			sumPhysical += c.physical
		} else {
			allPhysical = false
		}
		if c.okLogical {
			b.set("bufferpool"+pt.word+"ReadsLogical", c.logical, metric.PDELTA)
			sumLogical += c.logical
		} else {
			allLogical = false
		}
		if c.okPhysical && c.okLogical {
			b.set("bufferpool"+pt.word+"ReadsTotal", c.physical+c.logical, metric.PDELTA)
		}
		if c.okFound {
			sumFound += c.found
		} else {
			allFound = false
		}
		if c.okFound && c.okLogical {
			b.set("bufferpool"+pt.word+"HitPercent", percent(c.found, c.logical), metric.GAUGE)
		}

		// pureScale group bufferpool: NULL counts as 0 and the gauge is submitted only for a
		// non-zero GL.
		gl := b.coalesce("pool_" + pt.col + "_gbp_l_reads")
		gf := gl - b.coalesce("pool_"+pt.col+"_gbp_p_reads")
		sumGroupLogical += gl
		sumGroupFind += gf
		if gl != 0 {
			b.set("bufferpoolGroup"+pt.word+"HitPercent", percent(gf, gl), metric.GAUGE)
		}
	}

	if allPhysical {
		b.set("bufferpoolReadsPhysical", sumPhysical, metric.PDELTA)
	}
	if allLogical {
		b.set("bufferpoolReadsLogical", sumLogical, metric.PDELTA)
	}
	if allPhysical && allLogical {
		b.set("bufferpoolReadsTotal", sumPhysical+sumLogical, metric.PDELTA)
	}
	if allFound && allLogical {
		b.set("bufferpoolHitPercent", percent(sumFound, sumLogical), metric.GAUGE)
	}
	if sumGroupLogical != 0 {
		b.set("bufferpoolGroupHitPercent", percent(sumGroupFind, sumGroupLogical), metric.GAUGE)
	}
}

// pageCounts reads the columns of one page type: P = p_reads + temp_p_reads, L = l_reads +
// temp_l_reads and F = lbp_pages_found - async_lbp_pages_found. prefix names the metrics in warnings.
func (b builder) pageCounts(t, prefix string) (c pageCounts) {
	c.physical, c.okPhysical = b.combine(prefix+"Reads*", 1, "pool_"+t+"_p_reads", "pool_temp_"+t+"_p_reads")
	c.logical, c.okLogical = b.combine(prefix+"Reads*", 1, "pool_"+t+"_l_reads", "pool_temp_"+t+"_l_reads")
	c.found, c.okFound = b.combine(prefix+"HitPercent", -1, "pool_"+t+"_lbp_pages_found", "pool_async_"+t+"_lbp_pages_found")
	return c
}

// combine returns first + sign*second, or false (with a warning naming what is skipped) when
// either column is NULL or not numeric.
func (b builder) combine(what string, sign float64, first, second string) (float64, bool) {
	x, ok := b.row.Float(first)
	if !ok {
		b.skip(first, what)
		return 0, false
	}
	y, ok := b.row.Float(second)
	if !ok {
		b.skip(second, what)
		return 0, false
	}
	return x + sign*y, true
}

// coalesce is COALESCE(col, 0) for the group bufferpool columns, which are 0 or NULL off pureScale.
func (b builder) coalesce(col string) float64 {
	v, ok := b.row.Float(col)
	if !ok {
		if !b.row.Absent(col) {
			log.Warn("%s.%s has unexpected type %T: treated as 0", b.query, col, b.row[col])
		}
		return 0
	}
	return v
}

// percent is calculated by num/den*100
func percent(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den * 100
}
