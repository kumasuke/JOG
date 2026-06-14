package s3select

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// JSONInput configures JSON parsing of the source object.
type JSONInput struct {
	Type string // DOCUMENT or LINES (default LINES)
}

// JSONOutput configures JSON serialisation of the result. The output is always
// JSON Lines (one object per record), matching S3 Select's behaviour.
type JSONOutput struct{}

// jsonReader implements RowReader over JSON input. Both DOCUMENT and LINES are
// decoded as a stream of top-level JSON values via json.Decoder, which reads
// consecutive values for LINES and the single root value for DOCUMENT.
type jsonReader struct {
	dec *json.Decoder
}

func newJSONReader(src io.Reader, _ JSONInput) *jsonReader {
	return &jsonReader{dec: json.NewDecoder(src)}
}

func (j *jsonReader) Next() (Record, error) {
	// Read the next top-level value token-by-token so field order is preserved
	// (Go map iteration is unordered, which would shuffle columns across rows for
	// SELECT * and positional references _1/_2/...).
	tok, err := j.dec.Token()
	if err != nil {
		return Record{}, err // includes io.EOF
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return Record{}, fmt.Errorf("s3select json: expected object, got %v", tok)
	}

	var names, values []string
	for j.dec.More() {
		keyTok, err := j.dec.Token()
		if err != nil {
			return Record{}, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return Record{}, fmt.Errorf("s3select json: expected object key, got %v", keyTok)
		}
		var raw json.RawMessage
		if err := j.dec.Decode(&raw); err != nil {
			return Record{}, err
		}
		names = append(names, key)
		values = append(values, scalarString(raw))
	}
	// Consume the closing '}'.
	if _, err := j.dec.Token(); err != nil {
		return Record{}, err
	}
	return Record{Names: names, Values: values}, nil
}

// scalarString renders a JSON value as the string the SQL layer compares
// against. Strings lose their quotes; numbers/bools render canonically; objects
// and arrays render as their raw JSON text.
func scalarString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return strconv.FormatBool(b)
	}
	return string(raw)
}

// jsonWriter serialises projected rows to JSON Lines. Output objects preserve
// the projection's column order, so the writer cannot use a plain map (whose
// iteration order is non-deterministic) and instead emits keys in order.
type jsonWriter struct {
	buf *bytes.Buffer
}

func newJSONWriter(_ JSONOutput) *jsonWriter {
	return &jsonWriter{buf: &bytes.Buffer{}}
}

// write emits one JSON object per record. When names are available each value is
// keyed by its column name; otherwise positional keys (_1, _2, ...) are used.
// Keys are emitted in projection order (not via a map, whose marshalling would
// reorder them) so SELECT * round-trips JSON column order faithfully.
func (j *jsonWriter) write(values, names []string) error {
	j.buf.WriteByte('{')
	for i, v := range values {
		if i > 0 {
			j.buf.WriteByte(',')
		}
		key := "_" + strconv.Itoa(i+1)
		if i < len(names) && names[i] != "" {
			key = names[i]
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return fmt.Errorf("json output key: %w", err)
		}
		j.buf.Write(keyJSON)
		j.buf.WriteByte(':')
		valJSON, err := json.Marshal(jsonScalar(v))
		if err != nil {
			return fmt.Errorf("json output value: %w", err)
		}
		j.buf.Write(valJSON)
	}
	j.buf.WriteString("}\n")
	return nil
}

// jsonScalar reproduces numbers and booleans as native JSON types, leaving
// everything else as a string.
func jsonScalar(v string) any {
	if f, ok := asNumber(v); ok {
		return f
	}
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	return v
}

func (j *jsonWriter) bytes() ([]byte, error) {
	return j.buf.Bytes(), nil
}
