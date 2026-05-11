package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/kumasuke/jog/internal/storage"
)

// captureMaxKeys is a storage stub that captures the MaxKeys value passed to ListObjectsV2.
type captureMaxKeys struct {
	storage.Storage
	captured int32
}

func (s *captureMaxKeys) ListObjectsV2(_ context.Context, input *storage.ListObjectsInput) (*storage.ListObjectsOutput, error) {
	s.captured = input.MaxKeys
	return &storage.ListObjectsOutput{}, nil
}

// TestListObjectsV2_MaxKeysClamp covers H-14: max-keys values outside [1, 1000] are
// clamped to that range before being forwarded to the storage layer.
func TestListObjectsV2_MaxKeysClamp(t *testing.T) {
	cases := []struct {
		name     string
		param    string
		wantKeys int32
	}{
		{"huge value", "99999999", 1000},
		{"over 1000", "9999", 1000},
		{"zero", "0", 1},
		{"negative", "-1", 1},
		{"min valid", "1", 1},
		{"max valid", "1000", 1000},
		{"mid valid", "500", 500},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stub := &captureMaxKeys{}
			h := &Handler{storage: stub}

			req := httptest.NewRequest("GET", "/b?list-type=2&max-keys="+tc.param, nil)
			req = WithBucket(req, "b")
			rr := httptest.NewRecorder()

			h.ListObjectsV2(rr, req)

			if stub.captured != tc.wantKeys {
				t.Errorf("max-keys=%s: storage received MaxKeys=%d, want %d", tc.param, stub.captured, tc.wantKeys)
			}
		})
	}
}

// TestListObjectsV1_MaxKeysClamp covers H-14 for the v1 endpoint as well.
func TestListObjectsV1_MaxKeysClamp(t *testing.T) {
	cases := []struct {
		name     string
		param    string
		wantKeys int32
	}{
		{"huge value", "99999999", 1000},
		{"zero", "0", 1},
		{"negative", "-1", 1},
		{"max valid", "1000", 1000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stub := &captureMaxKeys{}
			h := &Handler{storage: stub}

			req := httptest.NewRequest("GET", "/b?max-keys="+tc.param, nil)
			req = WithBucket(req, "b")
			rr := httptest.NewRecorder()

			h.ListObjects(rr, req)

			if stub.captured != tc.wantKeys {
				t.Errorf("max-keys=%s: storage received MaxKeys=%d, want %d", tc.param, stub.captured, tc.wantKeys)
			}
		})
	}
}
