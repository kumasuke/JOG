package s3select

import (
	"errors"
	"fmt"
	"io"
)

// ErrUnsupported is returned for input/output serialisations outside this
// implementation's scope (e.g. Parquet, compression). The api layer maps it to
// an S3 InvalidArgument 400.
var ErrUnsupported = errors.New("unsupported serialization")

// ErrBadInput is returned when the source object cannot be parsed as the
// requested input serialisation (malformed CSV/JSON). It signals a client-supplied
// data problem, so the api layer maps it to an S3 InvalidArgument 400 rather than
// a 500.
var ErrBadInput = errors.New("bad input")

// InputCfg selects and configures the input deserialiser. Exactly one of CSV or
// JSON must be non-nil.
type InputCfg struct {
	CSV  *CSVInput
	JSON *JSONInput
}

// OutputCfg selects and configures the output serialiser. Exactly one of CSV or
// JSON must be non-nil.
type OutputCfg struct {
	CSV  *CSVOutput
	JSON *JSONOutput
}

// Stats reports the byte counts S3 Select returns in its Stats event.
type Stats struct {
	BytesScanned   int64
	BytesProcessed int64
	BytesReturned  int64
}

// outputWriter is the common interface for the CSV and JSON output serialisers.
type outputWriter interface {
	write(values, names []string) error
	bytes() ([]byte, error)
}

// Run executes the query against the input reader and returns the serialised
// result bytes (a single Records payload) and Stats. All input is read; the
// caller (the api handler) frames the result into the S3 Select event stream.
func Run(src io.Reader, q *Query, in InputCfg, out OutputCfg) ([]byte, Stats, error) {
	// Count bytes scanned/processed as we read the source.
	cr := &countingReader{r: src}

	reader, err := newReader(cr, in)
	if err != nil {
		return nil, Stats{}, err
	}
	writer, err := newWriter(out)
	if err != nil {
		return nil, Stats{}, err
	}

	var emitted int64
	for {
		if q.Limit != nil && emitted >= *q.Limit {
			break
		}
		rec, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A non-EOF error from the deserialiser means the source object could
			// not be parsed as the requested format. This is a client data problem,
			// so wrap it as ErrBadInput for a 4xx rather than a 5xx.
			return nil, Stats{}, fmt.Errorf("%w: read input: %w", ErrBadInput, err)
		}

		ok, err := match(q.Where, rec)
		if err != nil {
			return nil, Stats{}, err
		}
		if !ok {
			continue
		}

		values, err := project(q.Projection, rec)
		if err != nil {
			return nil, Stats{}, err
		}
		names := projectNames(q.Projection, rec)
		if err := writer.write(values, names); err != nil {
			return nil, Stats{}, err
		}
		emitted++
	}

	payload, err := writer.bytes()
	if err != nil {
		return nil, Stats{}, err
	}
	stats := Stats{
		BytesScanned:   cr.n,
		BytesProcessed: cr.n,
		BytesReturned:  int64(len(payload)),
	}
	return payload, stats, nil
}

func newReader(src io.Reader, in InputCfg) (RowReader, error) {
	switch {
	case in.CSV != nil:
		cr, err := newCSVReader(src, *in.CSV)
		if err != nil {
			return nil, err
		}
		if err := cr.init(*in.CSV); err != nil {
			return nil, fmt.Errorf("read csv header: %w", err)
		}
		return cr, nil
	case in.JSON != nil:
		return newJSONReader(src, *in.JSON), nil
	default:
		return nil, fmt.Errorf("%w: no input serialization", ErrUnsupported)
	}
}

func newWriter(out OutputCfg) (outputWriter, error) {
	switch {
	case out.CSV != nil:
		return newCSVWriter(*out.CSV), nil
	case out.JSON != nil:
		return newJSONWriter(*out.JSON), nil
	default:
		return nil, fmt.Errorf("%w: no output serialization", ErrUnsupported)
	}
}

// countingReader tracks how many bytes were read from the source.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
