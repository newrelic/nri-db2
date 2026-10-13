// Package inventory turns configuration rows into inventory items on the entity.
package inventory

import (
	"fmt"
	"strings"

	"github.com/newrelic/infra-integrations-sdk/v3/log"

	"github.com/newrelic/nri-db2/pkg/queries"
)

// Setter stores one inventory field; it is satisfied by the entity's SetInventoryItem.
type Setter func(key, field string, value any) error

// normalise returns the flag word for a parameter that Db2 tunes itself (AUTOMATIC, COMPUTED), so
// the inventory does not change every time the tuned number does. Otherwise it is the trimmed value.
func normalise(value, flags string) string {
	if f := strings.TrimSpace(flags); f != "" && !strings.EqualFold(f, "NONE") {
		return f
	}
	return strings.TrimSpace(value)
}

// Parameters writes one item per configuration parameter under the key prefix.name with the
// field value, plus deferredValue when a pending change differs from the value in effect. A NULL
// value or an empty name is skipped. It returns the number of items written.
func Parameters(prefix string, rows []queries.Row, set Setter) int {
	written := 0
	for _, row := range rows {
		name, _ := row.String("name")
		name = strings.TrimSpace(name)
		raw, ok := row.String("value")
		if name == "" || !ok {
			continue
		}
		flags, _ := row.String("value_flags")
		value := normalise(raw, flags)
		key := prefix + "." + name
		if err := set(key, "value", value); err != nil {
			log.Warn("inventory item %s skipped: %s", key, err)
			continue
		}
		written++
		if rawDef, ok := row.String("deferred_value"); ok {
			defFlags, _ := row.String("deferred_value_flags")
			if def := normalise(rawDef, defFlags); def != value {
				if err := set(key, "deferredValue", def); err != nil {
					log.Warn("inventory item %s deferredValue skipped: %s", key, err)
				}
			}
		}
	}
	return written
}

// instanceFields maps ENV_INST_INFO columns to inventory fields.
var instanceFields = []struct{ column, field string }{
	{"inst_name", "name"},
	{"service_level", "version"},
	{"release_num", "releaseNumber"},
	{"bld_level", "buildLevel"},
	{"ptf", "ptf"},
	{"fixpack_num", "fixpackNumber"},
	{"num_members", "members"},
	{"num_dbpartitions", "partitions"},
}

// Instance writes the instance item (key "instance") from the first ENV_INST_INFO row. Values are
// strings; a NULL column is skipped. It returns the number of fields written.
func Instance(rows []queries.Row, set Setter) int {
	if len(rows) == 0 {
		return 0
	}
	row := rows[0]
	written := 0
	for _, f := range instanceFields {
		v := row[f.column]
		if v == nil {
			continue
		}
		var s string
		switch x := v.(type) {
		case string:
			s = strings.TrimSpace(x)
		default:
			s = fmt.Sprint(x)
		}
		if err := set("instance", f.field, s); err != nil {
			log.Warn("inventory item instance.%s skipped: %s", f.field, err)
			continue
		}
		written++
	}
	return written
}
