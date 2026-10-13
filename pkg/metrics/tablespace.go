package metrics

import (
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// Tablespace turns MON_GET_TABLESPACE rows into one sample per tablespace. Sizes are bytes: pages
// times the tablespace page size. tablespaceUtilized is relative to usable pages, not total pages.
func Tablespace(newSet NewSet, rows []queries.Row) {
	for _, row := range rows {
		name, ok := row.String("tbsp_name")
		if !ok {
			log.Warn("%s returned a row without tbsp_name: row skipped", queries.TablespaceName)
			continue
		}
		b := builder{ms: newSet(name), query: queries.TablespaceName, row: row}
		b.tablespace()
	}
}

func (b builder) tablespace() {
	// The state is an attribute, not a metric; it is kept as returned (not trimmed).
	if state, ok := b.row.String("tbsp_state"); ok {
		if err := b.ms.SetMetric("tablespaceState", state, metric.ATTRIBUTE); err != nil {
			log.Warn("%s: set tablespaceState: %s", b.query, err)
		}
	} else {
		b.skip("tbsp_state", "tablespaceState")
	}

	size, okSize := b.row.Float("tbsp_page_size")
	if !okSize {
		b.skip("tbsp_page_size", "tablespaceSize, tablespaceUsable and tablespaceUsed")
	}
	total, okTotal := b.bytes(okSize, size, "tbsp_total_pages", "tablespaceSize")
	usable, okUsable := b.bytes(okSize, size, "tbsp_usable_pages", "tablespaceUsable")
	used, okUsed := b.bytes(okSize, size, "tbsp_used_pages", "tablespaceUsed")
	if okTotal {
		b.set("tablespaceSize", total, metric.GAUGE)
	}
	if okUsable {
		b.set("tablespaceUsable", usable, metric.GAUGE)
	}
	if okUsed {
		b.set("tablespaceUsed", used, metric.GAUGE)
	}
	if okUsable && okUsed {
		b.set("tablespaceUtilized", percent(used, usable), metric.GAUGE)
	}
}

// bytes is pages * page size, multiplied as int64 and then converted to float. It is false when
// the page size or the page count is unusable; the page size has already been reported.
func (b builder) bytes(okSize bool, size float64, pagesCol, name string) (float64, bool) {
	if !okSize {
		return 0, false
	}
	pages, ok := b.row.Float(pagesCol)
	if !ok {
		b.skip(pagesCol, name)
		return 0, false
	}
	return float64(int64(pages) * int64(size)), true
}
