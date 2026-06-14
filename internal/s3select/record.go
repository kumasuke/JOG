// Package s3select implements a minimal subset of Amazon S3 Select: it parses a
// restricted SQL dialect (projection + WHERE + LIMIT) and evaluates it against
// CSV or JSON input, producing CSV or JSON-Lines output. It is transport
// agnostic — it does not import net/http or the event-stream wire format; the
// api package wraps Run's output in the S3 Select event stream.
package s3select

import "strconv"

// Record is one input row, decoded into named and/or positional columns.
//
// CSV without a header (FileHeaderInfo NONE/IGNORE) populates Values only and
// leaves Names nil, so columns are addressable by position (_1, _2, ...). CSV
// with FileHeaderInfo=USE and JSON populate both Names and Values so columns are
// addressable by name (s.col / "col") as well as by position.
type Record struct {
	Names  []string
	Values []string
}

// ByPosition returns the value of the n-th column (1-based, matching the _N
// syntax). The second return is false when n is out of range.
func (r Record) ByPosition(n int) (string, bool) {
	if n < 1 || n > len(r.Values) {
		return "", false
	}
	return r.Values[n-1], true
}

// ByName returns the value of the column with the given name. The second return
// is false when the record has no such named column.
func (r Record) ByName(name string) (string, bool) {
	for i, n := range r.Names {
		if n == name && i < len(r.Values) {
			return r.Values[i], true
		}
	}
	return "", false
}

// asNumber parses s as a float; ok is false when s is not numeric. Used for the
// weakly-typed comparison semantics S3 Select applies to CSV/JSON scalars.
func asNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
