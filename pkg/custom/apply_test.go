package custom

import (
	"strings"
	"testing"

	"github.com/newrelic/infra-integrations-sdk/v3/data/attribute"
	"github.com/newrelic/infra-integrations-sdk/v3/data/metric"
	"github.com/newrelic/infra-integrations-sdk/v3/persist"
)

func TestApplySkipsReservedLabels(t *testing.T) {
	tests := []struct {
		label    string
		reserved bool
	}{
		{"event_type", true}, {"entityKey", true}, {"db2Hostname", true}, {"query", true},
		{"hostname", true}, {"host", true}, {"fullHostname", true}, {"agentName", true},
		{"displayName", true}, {"entityGuid", true}, {"integrationVersion", true}, {"timestamp", true},
		{"eventType", true}, {"eventId", true}, {"externalKey", true},
		// Db2 upper-cases an unquoted alias.
		{"HOSTNAME", true}, {"DB2HOSTNAME", true}, {"ENTITYKEY", true}, {"TIMESTAMP", true}, {"PORT", true},
		{"Database", true},
		{"hostCount", false}, {"hosts", false}, {"connectionsActive", false},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			var sets []*metric.Set
			newSet := func(sample string, attrs ...attribute.Attribute) *metric.Set {
				ms := metric.NewSet(sample, persist.NewInMemoryStore(), attrs...)
				sets = append(sets, ms)
				return ms
			}
			q := Query{Name: "q", Sample: DefaultSample}
			if err := Apply(q, []string{tt.label}, [][]any{{int64(7)}}, newSet, nil); err != nil {
				t.Fatal(err)
			}
			if len(sets) != 1 {
				t.Fatalf("sets = %d, want 1", len(sets))
			}
			// event_type and query exist on every sample, so look for the value 7 instead.
			v, ok := sets[0].Metrics[tt.label]
			published := ok && v == float64(7)
			if published == tt.reserved {
				t.Errorf("label %q: published = %v, want %v", tt.label, published, !tt.reserved)
			}
		})
	}
}

func TestReservedSetIsLowerCased(t *testing.T) {
	for l := range reserved {
		if l != strings.ToLower(l) {
			t.Errorf("reserved key %q is not lower case", l)
		}
	}
}
