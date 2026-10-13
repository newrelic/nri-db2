package logs

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/newrelic/nri-db2/pkg/queries"
)

var testID = Identity{
	EntityName: "db2-121/TESTDB", ReportingEndpoint: "db2-121:50000",
	Hostname: "db2-121", Port: "50000", Database: "TESTDB",
}

// loadFixtures reads testdata/diag_rows.jsonl: PD_GET_DIAG_HIST rows captured from db2-121 and
// db2-115 (RETCODE column removed, NUL runs shortened), keyed by "_case".
func loadFixtures(t *testing.T) map[string]queries.Row {
	t.Helper()
	f, err := os.Open("testdata/diag_rows.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]queries.Row{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		dec := json.NewDecoder(strings.NewReader(sc.Text()))
		dec.UseNumber()
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		row := queries.Row{}
		for k, v := range raw {
			k = strings.ToLower(k)
			switch x := v.(type) {
			case json.Number:
				n, err := x.Int64()
				if err != nil {
					t.Fatal(err)
				}
				row[k] = n
			default:
				row[k] = v
			}
		}
		ts, err := time.Parse("2006-01-02T15:04:05.000000", row["timestamp"].(string))
		if err != nil {
			t.Fatal(err)
		}
		row["timestamp"] = ts
		out[row["_case"].(string)] = row
	}
	return out
}

func TestNormalizeFixtures(t *testing.T) {
	fx := loadFixtures(t)
	tests := []struct {
		name string
		want map[string]any
		gone []string
	}{
		{"ei-null-level", map[string]any{
			"recordId": "I1E1840", "level": "Event", "rectype": "EI", "function": "pdLogInternal", "probe": int64(120),
			"recordHostname": "db2-121",
		}, []string{"dbName", "applicationHandle", "errorCode", "errorStack", "impact"}},
		{"dx-notify", map[string]any{"level": "Warning", "rectype": "DX", "function": "sqloPerformCPUBindingForDB2"},
			[]string{"recordId", "recordHostname", "dbName"}},
		{"error-os", map[string]any{"level": "Error", "recordId": "E145445E965", "osError": int64(13)}, []string{"dbName"}},
		{"blank-db", map[string]any{"applicationHandle": "0-7", "level": "Info"}, []string{"dbName"}},
		{"retcode-nul", map[string]any{"dbName": "TESTDB", "applicationHandle": "0-114", "level": "Error"}, nil},
		{"with-db-apphdl", map[string]any{"dbName": "TESTDB", "applicationHandle": "0-7"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(fx[tt.name], testID)
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}
			for _, k := range tt.gone {
				if v, ok := got[k]; ok {
					t.Errorf("%s = %#v, want it absent", k, v)
				}
			}
			if msg := got["message"].(string); strings.ContainsRune(msg, 0) {
				t.Errorf("message holds a NUL byte")
			}
			for k, v := range map[string]any{
				"logtype": "db2diag", "entityName": "db2-121/TESTDB", "reportingEndpoint": "db2-121:50000",
				"db2Hostname": "db2-121", "port": "50000", "database": "TESTDB",
			} {
				if got[k] != v {
					t.Errorf("%s = %#v, want %#v", k, got[k], v)
				}
			}
			if len(got) > 36 {
				t.Errorf("%d attributes, the limit is 36", len(got))
			}
		})
	}
}

// DBNAME says APPHDL when the record's DB field is blank; the value must come from the text.
func TestBlankDBIgnoresColumn(t *testing.T) {
	row := loadFixtures(t)["blank-db"]
	if row["dbname"] != "APPHDL" {
		t.Fatalf("fixture lost the wrong DBNAME column: %v", row["dbname"])
	}
	if v, ok := Normalize(row, testID)["dbName"]; ok {
		t.Fatalf("dbName = %v, want absent", v)
	}
}

func TestRetcodeParsing(t *testing.T) {
	full := "2026-10-09-15.46.49.114140+000 I429104E998           LEVEL: Error\n" +
		"PID     : 11957                TID : 1 PROC : db2sysc 0\n" +
		"INSTANCE: db2inst1             NODE : 000            DB   : TESTDB\n" +
		"APPHDL  : 0-114                APPID: *LOCAL.db2inst1.261009154659\n" +
		"FUNCTION: DB2 UDB, base sys utilities, sqeApplication::AppStartUsing, probe:143\n" +
		"RETCODE : ZRC=0x8012006D=-2146303891=SQLR_CA_BUILT\n" +
		"          ECF=0x90000002 extra\n" +
		"          more   text\n" +
		"DATA #1 : String, 4 bytes\nabcd\n"
	got := Normalize(queries.Row{"fullrec": full}, testID)
	if got["errorCode"] != "ZRC=0x8012006D=-2146303891=SQLR_CA_BUILT" {
		t.Errorf("errorCode = %v", got["errorCode"])
	}
	if want := "ZRC=0x8012006D=-2146303891=SQLR_CA_BUILT ECF=0x90000002 extra more text"; got["errorStack"] != want {
		t.Errorf("errorStack = %v, want %v", got["errorStack"], want)
	}
	if got["dbName"] != "TESTDB" || got["applicationHandle"] != "0-114" || got["recordId"] != "I429104E998" {
		t.Errorf("header fields: %v", got)
	}
}

func TestLevelColumnFallback(t *testing.T) {
	for col, want := range map[string]string{"I": "Info", "W": "Warning", "E": "Error", "S": "Severe", "C": "Critical"} {
		got := Normalize(queries.Row{"fullrec": "\nnotify record\n", "level": col}, testID)["level"]
		if got != want {
			t.Errorf("column %s: level = %v, want %v", col, got, want)
		}
	}
	if v, ok := Normalize(queries.Row{"fullrec": "x"}, testID)["level"]; ok {
		t.Errorf("level = %v, want absent", v)
	}
}

func TestFileName(t *testing.T) {
	for id, want := range map[Identity]string{
		{Hostname: "db2-121", Port: "50000", Database: "TESTDB"}:   "db2diag-db2-121_50000_TESTDB.jsonl",
		{Hostname: "../etc/x y", Port: "1", Database: "A;B"}:       "db2diag-.._etc_x_y_1_A_B.jsonl",
		{Hostname: "fe80::1%eth0", Port: "50000", Database: "DB1"}: "db2diag-fe80__1_eth0_50000_DB1.jsonl",
	} {
		if got := FileName(id); got != want {
			t.Errorf("FileName(%+v) = %q, want %q", id, got, want)
		}
	}
}

func TestDedupeKey(t *testing.T) {
	base := queries.Row{
		"timestamp": time.Date(2026, 10, 9, 15, 0, 1, 123456000, time.UTC), "pid": int64(7), "tid": int64(9),
		"rectype": "E", "function": "f", "probe": int64(3), "fullrec": "record one\n",
	}
	with := func(over queries.Row) queries.Row {
		r := queries.Row{}
		for k, v := range base {
			r[k] = v
		}
		for k, v := range over {
			r[k] = v
		}
		return r
	}
	key := dedupeKey(base)
	if !strings.HasPrefix(key, "2026-10-09T15:00:01.123456|7|9|E|f|3|") {
		t.Errorf("key = %q: the timestamp must stay the prefix and the six fields keep their order", key)
	}
	if dedupeKey(with(nil)) != key {
		t.Error("the key of an unchanged record must be the same on every read")
	}
	if dedupeKey(with(queries.Row{"fullrec": "re\x00cord one\n"})) != key {
		t.Error("NUL bytes in FULLREC must not change the key")
	}
	if dedupeKey(with(queries.Row{"fullrec": "record two\n"})) == key {
		t.Error("a different text must give a different key")
	}
	if dedupeKey(with(queries.Row{"fullrec": nil})) == key {
		t.Error("a missing FULLREC must not look like a record with text")
	}
}

// The zones are the ones measured on Db2 12.1.5: tz is the TIMEZONE column as Db2 reported it
// (wrapped into -719..+720), cur the server's CURRENT TIMEZONE as hhmmss, trueOffset the real
// offset in minutes.
func TestEpochMillisWrappedOffsets(t *testing.T) {
	utc := time.Date(2026, 10, 12, 20, 4, 44, 0, time.UTC)
	for _, tt := range []struct {
		zone       string
		tz         int64
		cur        int64
		trueOffset int64
	}{
		{"New York DST", -240, -40000, -240},
		{"Kolkata", 330, 53000, 330},
		{"St Johns", -150, -23000, -150},
		{"Fiji", 720, 120000, 720},
		{"Auckland DST (wrapped)", -660, 130000, 780},
		{"Chatham DST (wrapped)", -615, 134500, 825},
		{"Kiritimati (wrapped)", -600, 140000, 840},
		{"UTC-12 (wrapped)", 720, -120000, -720},
		{"Hawaii is not unwrapped", -600, -100000, -600},
		{"UTC-11 is not unwrapped", -660, -110000, -660},
		{"UTC", 0, 0, 0},
	} {
		t.Run(tt.zone, func(t *testing.T) {
			local := utc.Add(time.Duration(tt.trueOffset) * time.Minute)
			row := queries.Row{"timestamp": local, "timezone": tt.tz, "server_tz": tt.cur}
			if got := Normalize(row, testID)["timestamp"]; got != utc.UnixMilli() {
				t.Errorf("timestamp = %v, want %v", got, utc.UnixMilli())
			}
		})
	}
}

// Without CURRENT TIMEZONE the column is used as it is.
func TestEpochMillisWithoutServerTimezone(t *testing.T) {
	local := time.Date(2026, 10, 12, 9, 4, 44, 0, time.UTC)
	row := queries.Row{"timestamp": local, "timezone": int64(-660)}
	if got, want := Normalize(row, testID)["timestamp"], local.Add(660*time.Minute).UnixMilli(); got != want {
		t.Errorf("timestamp = %v, want %v", got, want)
	}
}
