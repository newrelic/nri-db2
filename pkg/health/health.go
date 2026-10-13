// Package health reports the canConnect metric.
package health

import (
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
)

// MetricCanConnect is the attribute name carried on Db2DatabaseSample.
const MetricCanConnect = "canConnect"

// SetCanConnect records 1 when the session worked and 0 otherwise. The last write wins, so a
// connection failure after a successful ping overwrites an earlier 1.
func SetCanConnect(ms *metric.Set, ok bool) error {
	v := 0.0
	if ok {
		v = 1.0
	}
	return ms.SetMetric(MetricCanConnect, v, metric.GAUGE)
}

// Names carried on Db2DatabaseSample.
const (
	MetricDatabaseStatus     = "databaseStatus"
	MetricDatabaseStatusCode = "databaseStatusCode"
)

// StatusCode maps DB_STATUS to databaseStatusCode: 0 serving, 1 transitioning, 2 quiesced,
// 3 unknown. The match is exact and case-sensitive.
func StatusCode(status string) int {
	switch status {
	case "ACTIVE", "ACTIVE_STANDBY", "STANDBY":
		return 0
	case "QUIESCE_PEND", "ROLLFWD":
		return 1
	case "QUIESCED":
		return 2
	default:
		return 3
	}
}

// SetDatabaseStatus records the raw DB_STATUS as an attribute and its code as a gauge. A NULL
// status (present false) gives code 3 and omits the attribute.
func SetDatabaseStatus(ms *metric.Set, status string, present bool) error {
	if !present {
		return ms.SetMetric(MetricDatabaseStatusCode, 3.0, metric.GAUGE)
	}
	if err := ms.SetMetric(MetricDatabaseStatus, status, metric.ATTRIBUTE); err != nil {
		return err
	}
	return ms.SetMetric(MetricDatabaseStatusCode, float64(StatusCode(status)), metric.GAUGE)
}
