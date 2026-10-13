package metrics

import (
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// logBlockBytes is the size of a log block: TOTAL_LOG_USED and TOTAL_LOG_AVAILABLE are in bytes.
const logBlockBytes = 4096

// infiniteLogSpace is the TOTAL_LOG_AVAILABLE value Db2 reports for infinite logging.
const infiniteLogSpace = -1

// TransactionLog sets logUsed, logAvailable, logUtilized, logReads and logWrites from
// MON_GET_TRANSACTION_LOG.
//
// logUtilized is used / (used + available) * 100. A full log (available 0, used
// above 0) therefore gives 100 and keeps all five metrics. When available is -1 (infinite log
// space) logAvailable stays -1 and logUtilized is 0. When used + available is 0 only logUtilized
// is omitted: the division would be NaN, which SetMetric rejects.
func TransactionLog(ms *metric.Set, rows []queries.Row) {
	row, ok := firstRow(queries.TransactionLogName, rows)
	if !ok {
		return
	}
	b := builder{ms: ms, query: queries.TransactionLogName, row: row}

	b.delta("log_reads", "logReads")
	b.delta("log_writes", "logWrites")
	b.gauge("total_log_used", "logUsed", 1.0/logBlockBytes)
	b.logAvailable()
}

// logAvailable sets logAvailable and logUtilized
func (b builder) logAvailable() {
	available, okAvail := b.row.Float("total_log_available")
	if !okAvail {
		b.skip("total_log_available", "logAvailable")
		b.skip("total_log_available", "logUtilized")
		return
	}
	if available == infiniteLogSpace {
		b.set("logAvailable", infiniteLogSpace, metric.GAUGE)
		b.set("logUtilized", 0.0, metric.GAUGE)
		return
	}
	b.set("logAvailable", available/logBlockBytes, metric.GAUGE)

	used, okUsed := b.row.Float("total_log_used")
	if !okUsed {
		b.skip("total_log_used", "logUtilized")
		return
	}
	total := used + available
	if total == 0 {
		log.Warn("%s: total_log_used + total_log_available is 0: logUtilized omitted", b.query)
		return
	}
	b.set("logUtilized", used/total*100, metric.GAUGE)
}
