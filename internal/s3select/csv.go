package s3select

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// CSVInput configures CSV parsing of the source object. The quote character is
// fixed at '"' because encoding/csv does not support customising it.
type CSVInput struct {
	FileHeaderInfo string // USE, IGNORE, or NONE (default NONE)
	FieldDelimiter string // default ","
}

// CSVOutput configures CSV serialisation of the result.
type CSVOutput struct {
	FieldDelimiter string // default ","
}

// csvReader implements RowReader over CSV input.
type csvReader struct {
	r       *csv.Reader
	header  []string // non-nil when FileHeaderInfo=USE
	started bool
}

func newCSVReader(src io.Reader, cfg CSVInput) (*csvReader, error) {
	r := csv.NewReader(src)
	r.FieldsPerRecord = -1 // allow ragged rows; S3 Select tolerates them
	r.LazyQuotes = true
	if d := []rune(cfg.FieldDelimiter); len(d) == 1 {
		r.Comma = d[0]
	}
	return &csvReader{r: r, header: nil}, nil
}

// init consumes the header row when configured, on first Next call.
func (c *csvReader) init(cfg CSVInput) error {
	if c.started {
		return nil
	}
	c.started = true
	switch strings.ToUpper(cfg.FileHeaderInfo) {
	case "USE":
		row, err := c.r.Read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		c.header = row
	case "IGNORE":
		if _, err := c.r.Read(); err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}

func (c *csvReader) Next() (Record, error) {
	row, err := c.r.Read()
	if err != nil {
		return Record{}, err // includes io.EOF
	}
	return Record{Names: c.header, Values: row}, nil
}

// RowReader yields decoded input records until io.EOF.
type RowReader interface {
	Next() (Record, error)
}

// csvWriter serialises projected rows back to CSV.
type csvWriter struct {
	buf *bytes.Buffer
	w   *csv.Writer
}

func newCSVWriter(cfg CSVOutput) *csvWriter {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	if d := []rune(cfg.FieldDelimiter); len(d) == 1 {
		w.Comma = d[0]
	}
	return &csvWriter{buf: buf, w: w}
}

func (c *csvWriter) write(values, _ []string) error {
	return c.w.Write(values)
}

func (c *csvWriter) bytes() ([]byte, error) {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		return nil, fmt.Errorf("csv output: %w", err)
	}
	return c.buf.Bytes(), nil
}
