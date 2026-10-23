package extras

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStripLiterals(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"no literals", "SELECT A, B FROM S.T WHERE C = ?", "SELECT A, B FROM S.T WHERE C = ?"},
		{"string", "SELECT * FROM T WHERE NAME = 'Jane Doe'", "SELECT * FROM T WHERE NAME = ?"},
		{"escaped quote", "WHERE N = 'O''Brien' AND M = 'x'", "WHERE N = ? AND M = ?"},
		{"only escaped quotes", "WHERE N = ''''", "WHERE N = ?"},
		{"empty string", "WHERE N = ''", "WHERE N = ?"},
		{"multiline string", "WHERE N = 'a\nb\n--c'", "WHERE N = ?"},
		{"integer", "WHERE ID = 12345 AND X > -7", "WHERE ID = ? AND X > -?"},
		{"decimal", "WHERE P = 12.50 AND Q = .5 AND R = 3.", "WHERE P = ? AND Q = ? AND R = ?"},
		{"exponent", "WHERE P = 1.5E+3 AND Q = 2e10 AND R = 4E", "WHERE P = ? AND Q = ? AND R = ?E"},
		{"identifier digits kept", "SELECT COL_2, T1.C3 FROM S1.T1 A2", "SELECT COL_2, T1.C3 FROM S1.T1 A2"},
		{"special chars in identifier", "SELECT A#1, B$2, C@3 FROM T", "SELECT A#1, B$2, C@3 FROM T"},
		{"non-ascii identifier", "SELECT Ünï1 FROM T WHERE N = 'é'", "SELECT Ünï1 FROM T WHERE N = ?"},
		{"hex literal", "WHERE B = X'DEADBEEF' AND C = x'00'", "WHERE B = ? AND C = ?"},
		{"national and graphic", "WHERE A = N'x' AND B = G'y' AND C = GX'00' AND D = BX'01' AND E = UX'02'", "WHERE A = ? AND B = ? AND C = ? AND D = ? AND E = ?"},
		{"unicode literal", "WHERE A = U&'d\\0061t' AND B = u&'x'", "WHERE A = ? AND B = ?"},
		{"typed literal", "WHERE D = TIMESTAMP '2024-01-01 00:00:00' AND E = DATE '2024-01-01'", "WHERE D = TIMESTAMP ? AND E = DATE ?"},
		{"column named like a prefix", "SELECT X, N FROM T WHERE X = 1", "SELECT X, N FROM T WHERE X = ?"},
		{"delimited identifier kept", `SELECT "My Col", "A'B" FROM "S"."T 1"`, `SELECT "My Col", "A'B" FROM "S"."T 1"`},
		{"delimited identifier with comment marks", `SELECT "A--B", "C/*D" FROM T WHERE X = 'v'`, `SELECT "A--B", "C/*D" FROM T WHERE X = ?`},
		{"escaped double quote", `SELECT "A""B'" FROM T WHERE X = 5`, `SELECT "A""B'" FROM T WHERE X = ?`},
		{"host variable", "WHERE A = :p1 AND B = ?", "WHERE A = :p1 AND B = ?"},
		{"line comment", "SELECT 1 -- secret 'x' 42\nFROM T", "SELECT ? FROM T"},
		{"line comment at end", "SELECT A FROM T -- user jdoe", "SELECT A FROM T"},
		{"block comment", "SELECT /* user=jdoe 'x' */ A FROM T", "SELECT A FROM T"},
		{"block comment between tokens", "SELECT A/*c*/FROM T", "SELECT A FROM T"},
		{"leading comment", "/* nri-db2 */ SELECT A FROM T", "SELECT A FROM T"},
		{"comment only", "/* a */ -- b", ""},
		{"quote in comment", "SELECT A FROM T /* it's */ WHERE B = 'x'", "SELECT A FROM T WHERE B = ?"},
		{"comment mark in string", "WHERE A = '--x' AND B = '/*' AND C = 2", "WHERE A = ? AND B = ? AND C = ?"},
		{"unterminated string", "WHERE A = 1 AND B = 'secret val", "WHERE A = ? AND B = ?"},
		{"unterminated block comment", "SELECT A FROM T /* secret", "SELECT A FROM T"},
		{"unterminated delimited identifier", `SELECT "AB`, `SELECT "AB`},
		{"in list", "WHERE A IN (1, 2, 3) AND B NOT IN ('a','b')", "WHERE A IN (?) AND B NOT IN (?)"},
		{"in list lower case and spacing", "where a in(1 ,2,\n3 )", "where a in (?)"},
		{"in list of markers", "WHERE A IN (?, ?, ?)", "WHERE A IN (?)"},
		{"single value in list", "WHERE A IN (5)", "WHERE A IN (?)"},
		{"in list with expression kept", "WHERE A IN (1, B, 3)", "WHERE A IN (?, B, ?)"},
		{"in subquery kept", "WHERE A IN (SELECT B FROM T WHERE C = 1)", "WHERE A IN (SELECT B FROM T WHERE C = ?)"},
		{"other lists kept", "INSERT INTO T (A, B) VALUES (1, 'x'), (2, 'y')", "INSERT INTO T (A, B) VALUES (?, ?), (?, ?)"},
		{"join is not in", "FROM A JOIN(1,2)", "FROM A JOIN(?,?)"},
		{"invalid utf-8 passes", "SELECT \xff FROM T WHERE A = 'x'", "SELECT \xff FROM T WHERE A = ?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripLiterals(tt.in)
			if got != tt.want {
				t.Errorf("StripLiterals(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := StripLiterals(got); again != got {
				t.Errorf("not idempotent: %q then %q", got, again)
			}
		})
	}
}

// The secret planted in every kind of literal and comment never survives.
func TestStripLiteralsRemovesSecrets(t *testing.T) {
	const secret = "s3cr3tV4lue"
	wrap := []string{
		"SELECT * FROM T WHERE A = '%s'",
		"SELECT * FROM T WHERE A = N'%s'",
		"SELECT * FROM T WHERE A = X'%s'",
		"SELECT * FROM T WHERE A = U&'%s'",
		"SELECT * FROM T WHERE A = 'x''%s''y'",
		"SELECT * FROM T WHERE A = 'multi\n%s\nline'",
		"SELECT * FROM T /* %s */",
		"SELECT * FROM T -- %s",
		"SELECT * FROM T WHERE A = '%s",
		"SELECT * FROM T /* %s",
		"SELECT * FROM T WHERE A IN ('%s', '%s')",
	}
	for _, w := range wrap {
		in := strings.ReplaceAll(w, "%s", secret)
		if got := StripLiterals(in); strings.Contains(got, secret) {
			t.Errorf("StripLiterals(%q) = %q still has the secret", in, got)
		}
	}
}

func FuzzStripLiterals(f *testing.F) {
	for _, s := range []string{
		"", "SELECT 1", "'", "''", "'''", "/*", "/**/", "--", "-- x\n", "X'", "U&'", "\"", "\"a\"\"",
		"IN (1,2)", "in(?,?", "1e", "1.e+", ".", "a'b'c", "SELECT /* a */ 'b' -- c\n, 12.5E3",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := StripLiterals(in)
		if len(got) > len(in) {
			t.Errorf("StripLiterals(%q) = %q is longer than the input", in, got)
		}
		if utf8.ValidString(in) && !utf8.ValidString(got) {
			t.Errorf("StripLiterals(%q) = %q is not valid UTF-8", in, got)
		}
		if again := StripLiterals(got); again != got {
			t.Errorf("StripLiterals(%q) = %q, stripped again %q", in, got, again)
		}
	})
}
