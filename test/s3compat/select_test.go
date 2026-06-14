package s3compat

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainRecords runs a SelectObjectContent query and returns the concatenated
// Records payload. It fails the test if the SDK reports an event-stream decode
// error (which is what surfaces a malformed frame / bad CRC from the server).
func drainRecords(t *testing.T, client *s3.Client, in *s3.SelectObjectContentInput) []byte {
	t.Helper()
	ctx := context.Background()

	out, err := client.SelectObjectContent(ctx, in)
	require.NoError(t, err)
	stream := out.GetStream()
	defer stream.Close()

	var buf []byte
	for event := range stream.Events() {
		if rec, ok := event.(*types.SelectObjectContentEventStreamMemberRecords); ok {
			buf = append(buf, rec.Value.Payload...)
		}
	}
	require.NoError(t, stream.Err(), "event stream decode error (framing/CRC)")
	return buf
}

// putObject is a small helper to seed object content.
func putSelectObject(t *testing.T, client *s3.Client, bucket, key, content string) {
	t.Helper()
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(content),
	})
	require.NoError(t, err)
}

// parseCSVRows parses CSV bytes into rows for logical (not byte-for-byte)
// comparison, since the engine re-serialises CSV (normalising quoting/newlines).
func parseCSVRows(t *testing.T, b []byte) [][]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(string(b)))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	require.NoError(t, err)
	return rows
}

func newSelectBucket(t *testing.T, ts *testutil.TestServer, client *s3.Client) string {
	t.Helper()
	bucket := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucket)
	t.Cleanup(cleanup)
	return bucket
}

func TestSelectObjectContent_CSV_SelectStar_Passthrough(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	content := "a,1,x\nb,2,y\nc,3,z\n"
	putSelectObject(t, client, bucket, key, content)

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT * FROM S3Object"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoNone},
		},
		OutputSerialization: &types.OutputSerialization{
			CSV: &types.CSVOutput{},
		},
	})

	rows := parseCSVRows(t, got)
	require.Equal(t, [][]string{{"a", "1", "x"}, {"b", "2", "y"}, {"c", "3", "z"}}, rows)
}

func TestSelectObjectContent_CSV_ProjectionByPosition(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, "a,1,x\nb,2,y\n")

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT _1, _3 FROM S3Object"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoNone},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})

	rows := parseCSVRows(t, got)
	require.Equal(t, [][]string{{"a", "x"}, {"b", "y"}}, rows)
}

func TestSelectObjectContent_CSV_HeaderNamesUSE(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, "name,age\nalice,30\nbob,25\n")

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT s.name FROM S3Object s"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoUse},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})

	rows := parseCSVRows(t, got)
	require.Equal(t, [][]string{{"alice"}, {"bob"}}, rows)
}

func TestSelectObjectContent_CSV_Where(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, "foo,5\nfoo,15\nbar,20\n")

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT _1 FROM S3Object WHERE _2 > 10 AND _1 = 'foo'"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoNone},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})

	rows := parseCSVRows(t, got)
	require.Equal(t, [][]string{{"foo"}}, rows)
}

func TestSelectObjectContent_CSV_Limit(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, "a\nb\nc\nd\n")

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT * FROM S3Object LIMIT 2"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoNone},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})

	rows := parseCSVRows(t, got)
	require.Equal(t, [][]string{{"a"}, {"b"}}, rows)
}

func TestSelectObjectContent_JSONLines_Projection(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	content := `{"id":1,"name":"alice"}
{"id":2,"name":"bob"}
`
	putSelectObject(t, client, bucket, key, content)

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT s.id FROM S3Object[*] s"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			JSON: &types.JSONInput{Type: types.JSONTypeLines},
		},
		OutputSerialization: &types.OutputSerialization{JSON: &types.JSONOutput{}},
	})

	ids := parseJSONLineField(t, got, "id")
	sort.Strings(ids)
	require.Equal(t, []string{"1", "2"}, ids)
}

func TestSelectObjectContent_JSONDocument(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, `{"id":42,"name":"solo"}`)

	got := drainRecords(t, client, &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT s.name FROM S3Object s"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			JSON: &types.JSONInput{Type: types.JSONTypeDocument},
		},
		OutputSerialization: &types.OutputSerialization{JSON: &types.JSONOutput{}},
	})

	names := parseJSONLineField(t, got, "name")
	require.Equal(t, []string{"solo"}, names)
}

// parseJSONLineField extracts the given field from each JSON-Lines object,
// rendered as a string for comparison.
func parseJSONLineField(t *testing.T, b []byte, field string) []string {
	t.Helper()
	var out []string
	dec := json.NewDecoder(strings.NewReader(string(b)))
	for {
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			break
		}
		v, ok := obj[field]
		require.True(t, ok, "field %q missing in %v", field, obj)
		out = append(out, renderJSONScalar(v))
	}
	return out
}

func renderJSONScalar(v any) string {
	switch x := v.(type) {
	case float64:
		// Integers come back without a decimal point.
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

func TestSelectObjectContent_NoSuchKey(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)

	_, err := client.SelectObjectContent(context.Background(), &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String("does-not-exist"),
		Expression:     aws.String("SELECT * FROM S3Object"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoNone},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})
	requireAPIErrorCode(t, err, "NoSuchKey")
}

func TestSelectObjectContent_InvalidSQL(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, "a,b\n")

	_, err := client.SelectObjectContent(context.Background(), &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("THIS IS NOT SQL"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			CSV: &types.CSVInput{FileHeaderInfo: types.FileHeaderInfoNone},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})
	requireAPIErrorCode(t, err, "InvalidArgument")
}

func TestSelectObjectContent_UnsupportedFormat(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	putSelectObject(t, client, bucket, key, "a,b\n")

	_, err := client.SelectObjectContent(context.Background(), &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT * FROM S3Object"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			Parquet: &types.ParquetInput{},
		},
		OutputSerialization: &types.OutputSerialization{CSV: &types.CSVOutput{}},
	})
	requireAPIErrorCode(t, err, "InvalidArgument")
}

func TestSelectObjectContent_MalformedInput(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()
	client := ts.S3Client(t)

	bucket := newSelectBucket(t, ts, client)
	key := testutil.RandomObjectKey()
	// Not valid JSON: a bare token that cannot be decoded as a JSON object.
	putSelectObject(t, client, bucket, key, "{this is not json}\n")

	_, err := client.SelectObjectContent(context.Background(), &s3.SelectObjectContentInput{
		Bucket:         aws.String(bucket),
		Key:            aws.String(key),
		Expression:     aws.String("SELECT * FROM S3Object[*]"),
		ExpressionType: types.ExpressionTypeSql,
		InputSerialization: &types.InputSerialization{
			JSON: &types.JSONInput{Type: types.JSONTypeLines},
		},
		OutputSerialization: &types.OutputSerialization{JSON: &types.JSONOutput{}},
	})
	// Client-supplied malformed object content must not surface as a 500;
	// it is a 4xx InvalidArgument (the input data could not be parsed).
	requireAPIErrorCode(t, err, "InvalidArgument")
}

// requireAPIErrorCode asserts that err carries the given S3 API error code.
func requireAPIErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, code, apiErr.ErrorCode())
}
