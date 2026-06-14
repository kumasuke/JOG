package s3select

import (
	"strings"
	"testing"
)

func TestParseSQL_Valid(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"select star", "SELECT * FROM S3Object"},
		{"positional projection", "SELECT _1, _2 FROM S3Object"},
		{"named projection", "SELECT name, age FROM S3Object"},
		{"alias projection", "SELECT s.name FROM S3Object s"},
		{"s3object star suffix", "SELECT s.id FROM S3Object[*] s"},
		{"where numeric", "SELECT * FROM S3Object WHERE _2 > 10"},
		{"where string", "SELECT * FROM S3Object WHERE _1 = 'foo'"},
		{"where and/or", "SELECT * FROM S3Object WHERE _1 = 'a' AND _2 < 5 OR _3 >= 1"},
		{"where parens", "SELECT * FROM S3Object WHERE (_1 = 'a' OR _1 = 'b') AND _2 > 0"},
		{"where not", "SELECT * FROM S3Object WHERE NOT _1 = 'x'"},
		{"limit", "SELECT * FROM S3Object LIMIT 5"},
		{"all clauses", "SELECT _1 FROM S3Object s WHERE _2 != 0 LIMIT 3"},
		{"lowercase keywords", "select * from s3object where _1 = 'a' limit 1"},
		{"not equal angle", "SELECT * FROM S3Object WHERE _1 <> 'a'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseSQL(tt.expr); err != nil {
				t.Fatalf("ParseSQL(%q) returned error: %v", tt.expr, err)
			}
		})
	}
}

func TestParseSQL_Invalid(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"empty", ""},
		{"no select", "FROM S3Object"},
		{"no from", "SELECT *"},
		{"wrong table", "SELECT * FROM Foo"},
		{"garbage", "THIS IS NOT SQL"},
		{"unterminated string", "SELECT * FROM S3Object WHERE _1 = 'foo"},
		{"trailing tokens after alias", "SELECT * FROM S3Object s EXTRA"},
		{"missing operand", "SELECT * FROM S3Object WHERE _1 ="},
		{"negative limit", "SELECT * FROM S3Object LIMIT -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseSQL(tt.expr); err == nil {
				t.Fatalf("ParseSQL(%q) expected error, got nil", tt.expr)
			}
		})
	}
}

// runCSVtoCSV is a helper: parse expr, run over CSV input, return CSV output.
func runCSVtoCSV(t *testing.T, expr, input string, header string) string {
	t.Helper()
	q, err := ParseSQL(expr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, _, err := Run(
		strings.NewReader(input),
		q,
		InputCfg{CSV: &CSVInput{FileHeaderInfo: header}},
		OutputCfg{CSV: &CSVOutput{}},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return string(out)
}

func TestRun_CSV(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		input  string
		header string
		want   string
	}{
		{
			name:  "select star",
			expr:  "SELECT * FROM S3Object",
			input: "a,1\nb,2\n",
			want:  "a,1\nb,2\n",
		},
		{
			name:  "positional projection",
			expr:  "SELECT _2, _1 FROM S3Object",
			input: "a,1\nb,2\n",
			want:  "1,a\n2,b\n",
		},
		{
			name:  "where numeric gt",
			expr:  "SELECT _1 FROM S3Object WHERE _2 > 1",
			input: "a,1\nb,2\nc,3\n",
			want:  "b\nc\n",
		},
		{
			name:  "where string eq",
			expr:  "SELECT _2 FROM S3Object WHERE _1 = 'b'",
			input: "a,1\nb,2\n",
			want:  "2\n",
		},
		{
			name:  "and",
			expr:  "SELECT _1 FROM S3Object WHERE _2 > 1 AND _1 = 'c'",
			input: "a,1\nb,2\nc,3\n",
			want:  "c\n",
		},
		{
			name:  "or",
			expr:  "SELECT _1 FROM S3Object WHERE _1 = 'a' OR _1 = 'c'",
			input: "a,1\nb,2\nc,3\n",
			want:  "a\nc\n",
		},
		{
			name:  "not",
			expr:  "SELECT _1 FROM S3Object WHERE NOT _1 = 'b'",
			input: "a,1\nb,2\nc,3\n",
			want:  "a\nc\n",
		},
		{
			name:  "limit",
			expr:  "SELECT _1 FROM S3Object LIMIT 2",
			input: "a\nb\nc\n",
			want:  "a\nb\n",
		},
		{
			name:   "header USE named ref",
			expr:   "SELECT name FROM S3Object WHERE age > 26",
			input:  "name,age\nalice,30\nbob,25\n",
			header: "USE",
			want:   "alice\n",
		},
		{
			name:   "header IGNORE skips first row",
			expr:   "SELECT _1 FROM S3Object",
			input:  "h1,h2\na,1\nb,2\n",
			header: "IGNORE",
			want:   "a\nb\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCSVtoCSV(t, tt.expr, tt.input, tt.header)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRun_JSONLines(t *testing.T) {
	q, err := ParseSQL("SELECT s.id FROM S3Object[*] s WHERE s.id > 1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	input := `{"id":1,"name":"a"}
{"id":2,"name":"b"}
{"id":3,"name":"c"}
`
	out, _, err := Run(
		strings.NewReader(input),
		q,
		InputCfg{JSON: &JSONInput{Type: "LINES"}},
		OutputCfg{JSON: &JSONOutput{}},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := string(out)
	// Two records (id 2 and 3) should pass the filter.
	if n := strings.Count(strings.TrimSpace(got), "\n") + 1; n != 2 {
		t.Fatalf("expected 2 output rows, got %d: %q", n, got)
	}
	if !strings.Contains(got, `"id":2`) || !strings.Contains(got, `"id":3`) {
		t.Fatalf("expected ids 2 and 3 in %q", got)
	}
}

// TestRun_JSON_SelectStar_ColumnOrder guards against non-deterministic field
// ordering: SELECT * on multi-field JSON objects must preserve input field order
// across every row (a map-based decode would shuffle columns).
func TestRun_JSON_SelectStar_ColumnOrder(t *testing.T) {
	q, err := ParseSQL("SELECT * FROM S3Object[*]")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	input := `{"a":1,"b":2,"c":3}
{"a":4,"b":5,"c":6}
`
	// JSON input -> CSV output: column order must be a,b,c on every row.
	out, _, err := Run(
		strings.NewReader(input),
		q,
		InputCfg{JSON: &JSONInput{Type: "LINES"}},
		OutputCfg{CSV: &CSVOutput{}},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := string(out); got != "1,2,3\n4,5,6\n" {
		t.Fatalf("column order not preserved: got %q", got)
	}

	// JSON input -> JSON output: keys must stay in input order.
	q2, _ := ParseSQL("SELECT * FROM S3Object[*]")
	out2, _, err := Run(
		strings.NewReader(input),
		q2,
		InputCfg{JSON: &JSONInput{Type: "LINES"}},
		OutputCfg{JSON: &JSONOutput{}},
	)
	if err != nil {
		t.Fatalf("run json out: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out2)), "\n") {
		if !strings.HasPrefix(line, `{"a":`) ||
			strings.Index(line, `"b":`) < strings.Index(line, `"a":`) ||
			strings.Index(line, `"c":`) < strings.Index(line, `"b":`) {
			t.Fatalf("json key order not preserved: %q", line)
		}
	}
}

func TestRun_UnsupportedSerialization(t *testing.T) {
	q, _ := ParseSQL("SELECT * FROM S3Object")
	if _, _, err := Run(strings.NewReader(""), q, InputCfg{}, OutputCfg{CSV: &CSVOutput{}}); err == nil {
		t.Fatal("expected error for missing input serialization")
	}
	if _, _, err := Run(strings.NewReader("a\n"), q, InputCfg{CSV: &CSVInput{}}, OutputCfg{}); err == nil {
		t.Fatal("expected error for missing output serialization")
	}
}

func TestRun_Stats(t *testing.T) {
	q, _ := ParseSQL("SELECT * FROM S3Object")
	input := "a,1\nb,2\n"
	out, stats, err := Run(
		strings.NewReader(input),
		q,
		InputCfg{CSV: &CSVInput{}},
		OutputCfg{CSV: &CSVOutput{}},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stats.BytesScanned != int64(len(input)) {
		t.Errorf("BytesScanned = %d, want %d", stats.BytesScanned, len(input))
	}
	if stats.BytesReturned != int64(len(out)) {
		t.Errorf("BytesReturned = %d, want %d", stats.BytesReturned, len(out))
	}
}
