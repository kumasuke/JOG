package api

import (
	"encoding/xml"
	"errors"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/kumasuke/jog/internal/s3select"
	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// selectRequest is the parsed SelectObjectContentRequest body.
type selectRequest struct {
	XMLName             xml.Name                  `xml:"SelectObjectContentRequest"`
	Expression          string                    `xml:"Expression"`
	ExpressionType      string                    `xml:"ExpressionType"`
	InputSerialization  selectInputSerialization  `xml:"InputSerialization"`
	OutputSerialization selectOutputSerialization `xml:"OutputSerialization"`
}

type selectInputSerialization struct {
	CompressionType string            `xml:"CompressionType"`
	CSV             *selectCSVInput   `xml:"CSV"`
	JSON            *selectJSONInput  `xml:"JSON"`
	Parquet         *selectParquetTag `xml:"Parquet"`
}

type selectCSVInput struct {
	FileHeaderInfo string `xml:"FileHeaderInfo"`
	FieldDelimiter string `xml:"FieldDelimiter"`
}

type selectJSONInput struct {
	Type string `xml:"Type"`
}

// selectParquetTag exists only so a <Parquet> element parses; it is always
// rejected as unsupported.
type selectParquetTag struct{}

type selectOutputSerialization struct {
	CSV  *selectCSVOutput  `xml:"CSV"`
	JSON *selectJSONOutput `xml:"JSON"`
}

type selectCSVOutput struct {
	FieldDelimiter string `xml:"FieldDelimiter"`
}

// selectJSONOutput accepts a <JSON> output element. JOG always emits JSON Lines,
// so no fields are configurable yet.
type selectJSONOutput struct{}

// SelectObjectContent handles POST /{bucket}/{key}?select&select-type=2.
//
// All validation (request XML, SQL parse, serialization support, object
// existence) happens before any byte of the 200 response is written, because
// once the event stream starts the handler can no longer return an S3 XML error.
func (h *Handler) SelectObjectContent(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	key := GetKey(r)

	body, s3err := readXMLBody(w, r, MaxSelectRequestBodySize)
	if s3err != nil {
		WriteErrorWithResource(w, s3err, "/"+bucket+"/"+key)
		return
	}

	var req selectRequest
	if err := xml.Unmarshal(body, &req); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket+"/"+key)
		return
	}

	if !strings.EqualFold(req.ExpressionType, "SQL") {
		WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
		return
	}

	inCfg, outCfg, cfgErr := selectSerializations(&req)
	if cfgErr != nil {
		WriteErrorWithResource(w, cfgErr, "/"+bucket+"/"+key)
		return
	}

	query, err := s3select.ParseSQL(req.Expression)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
		return
	}

	obj, err := h.storage.GetObject(r.Context(), bucket, key)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrInvalidKey):
			WriteErrorWithResource(w, ErrInvalidArgument, "/"+bucket+"/"+key)
		case errors.Is(err, storage.ErrBucketNotFound):
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
		case errors.Is(err, storage.ErrObjectNotFound):
			WriteErrorWithResource(w, ErrNoSuchKey, "/"+bucket+"/"+key)
		default:
			WriteError(w, ErrInternalError)
		}
		return
	}
	defer obj.Body.Close()

	// Run the query entirely before writing the response: the result is small
	// relative to streaming concerns here and lets late failures still surface
	// as XML errors. Errors after this point would have to be in-stream, but the
	// supported subset only fails on input it has already validated.
	payload, stats, err := s3select.Run(obj.Body, query, inCfg, outCfg)
	if err != nil {
		WriteError(w, ErrInternalError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	enc := eventstream.NewEncoder()
	if err := writeRecordsEvent(enc, w, payload); err != nil {
		log.Error().Err(err).Msg("SelectObjectContent: write Records event")
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
	if err := writeStatsEvent(enc, w, stats); err != nil {
		log.Error().Err(err).Msg("SelectObjectContent: write Stats event")
		return
	}
	if err := writeEndEvent(enc, w); err != nil {
		log.Error().Err(err).Msg("SelectObjectContent: write End event")
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
}

// selectSerializations validates and converts the request's serialization
// settings into engine config. Parquet input, compression, and missing/ambiguous
// serializations are rejected as InvalidArgument.
func selectSerializations(req *selectRequest) (s3select.InputCfg, s3select.OutputCfg, *S3Error) {
	in := req.InputSerialization
	if in.Parquet != nil {
		return s3select.InputCfg{}, s3select.OutputCfg{}, ErrInvalidArgument
	}
	if in.CompressionType != "" && !strings.EqualFold(in.CompressionType, "NONE") {
		return s3select.InputCfg{}, s3select.OutputCfg{}, ErrInvalidArgument
	}

	var inCfg s3select.InputCfg
	switch {
	case in.CSV != nil && in.JSON == nil:
		inCfg.CSV = &s3select.CSVInput{
			FileHeaderInfo: in.CSV.FileHeaderInfo,
			FieldDelimiter: in.CSV.FieldDelimiter,
		}
	case in.JSON != nil && in.CSV == nil:
		inCfg.JSON = &s3select.JSONInput{Type: in.JSON.Type}
	default:
		return s3select.InputCfg{}, s3select.OutputCfg{}, ErrInvalidArgument
	}

	out := req.OutputSerialization
	var outCfg s3select.OutputCfg
	switch {
	case out.CSV != nil && out.JSON == nil:
		outCfg.CSV = &s3select.CSVOutput{FieldDelimiter: out.CSV.FieldDelimiter}
	case out.JSON != nil && out.CSV == nil:
		outCfg.JSON = &s3select.JSONOutput{}
	default:
		return s3select.InputCfg{}, s3select.OutputCfg{}, ErrInvalidArgument
	}

	return inCfg, outCfg, nil
}

// eventstream header helpers. The header names and values mirror what the AWS
// SDK's deserializer expects (see eventstreamapi: :message-type / :event-type /
// :content-type). The encoder computes the prelude length and CRC32 checksums.

func eventHeaders(eventType, contentType string) eventstream.Headers {
	h := eventstream.Headers{
		{Name: ":message-type", Value: eventstream.StringValue("event")},
		{Name: ":event-type", Value: eventstream.StringValue(eventType)},
	}
	if contentType != "" {
		h = append(h, eventstream.Header{
			Name:  ":content-type",
			Value: eventstream.StringValue(contentType),
		})
	}
	return h
}

func writeRecordsEvent(enc *eventstream.Encoder, w http.ResponseWriter, payload []byte) error {
	return enc.Encode(w, eventstream.Message{
		Headers: eventHeaders("Records", "application/octet-stream"),
		Payload: payload,
	})
}

func writeStatsEvent(enc *eventstream.Encoder, w http.ResponseWriter, s s3select.Stats) error {
	payload, err := xml.Marshal(statsPayload{
		BytesScanned:   s.BytesScanned,
		BytesProcessed: s.BytesProcessed,
		BytesReturned:  s.BytesReturned,
	})
	if err != nil {
		return err
	}
	return enc.Encode(w, eventstream.Message{
		Headers: eventHeaders("Stats", "text/xml"),
		Payload: payload,
	})
}

func writeEndEvent(enc *eventstream.Encoder, w http.ResponseWriter) error {
	return enc.Encode(w, eventstream.Message{
		Headers: eventHeaders("End", ""),
	})
}

// statsPayload is the <Stats> XML carried by the Stats event.
type statsPayload struct {
	XMLName        xml.Name `xml:"Stats"`
	BytesScanned   int64    `xml:"BytesScanned"`
	BytesProcessed int64    `xml:"BytesProcessed"`
	BytesReturned  int64    `xml:"BytesReturned"`
}
