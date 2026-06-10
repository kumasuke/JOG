package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// objectLockBucketStorage records SetBucketObjectLockEnabled calls so the
// CreateBucket handler tests can assert the header parsing branch.
type objectLockBucketStorage struct {
	mockStorage
	createCalled         bool
	setLockEnabledCall   *bool
	versioningStatusCall *storage.VersioningStatus
}

func (s *objectLockBucketStorage) CreateBucket(ctx context.Context, bucket string) error {
	s.createCalled = true
	return nil
}

func (s *objectLockBucketStorage) SetBucketObjectLockEnabled(ctx context.Context, bucket string, enabled bool) error {
	s.setLockEnabledCall = &enabled
	return nil
}

// PutBucketVersioning records the auto-versioning that CreateBucket performs
// for Object Lock buckets (#39).
func (s *objectLockBucketStorage) PutBucketVersioning(ctx context.Context, bucket string, status storage.VersioningStatus) error {
	s.versioningStatusCall = &status
	return nil
}

// TestCreateBucket_ObjectLockHeaderCaseInsensitive verifies the
// x-amz-bucket-object-lock-enabled header is parsed case-insensitively. AWS
// CLI / boto3 send "True" (capitalized) while the Go SDK sends "true"; a
// strict equality check silently dropped the flag for AWS CLI users, leaving
// Object Lock unenforced. (CR-5 follow-up — caught during Docker manual
// verification.)
func TestCreateBucket_ObjectLockHeaderCaseInsensitive(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"go-sdk lowercase", "true", true},
		{"aws-cli capitalized", "True", true},
		{"upper", "TRUE", true},
		{"absent", "", false},
		{"explicit false", "false", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &objectLockBucketStorage{}
			h := &Handler{storage: st}

			req := httptest.NewRequest("PUT", "/mybucket", nil)
			req = setContext(req, "mybucket", "")
			if tc.header != "" {
				req.Header.Set("x-amz-bucket-object-lock-enabled", tc.header)
			}
			w := httptest.NewRecorder()

			h.CreateBucket(w, req)

			require.True(t, st.createCalled, "CreateBucket should be invoked")
			if tc.want {
				require.NotNil(t, st.setLockEnabledCall,
					"SetBucketObjectLockEnabled should be called for header %q", tc.header)
				assert.True(t, *st.setLockEnabledCall)
				// #39: Object Lock buckets must auto-enable versioning.
				require.NotNil(t, st.versioningStatusCall,
					"PutBucketVersioning should be called for object lock bucket %q", tc.header)
				assert.Equal(t, storage.VersioningStatusEnabled, *st.versioningStatusCall)
			} else {
				assert.Nil(t, st.setLockEnabledCall,
					"SetBucketObjectLockEnabled should NOT be called for header %q", tc.header)
				assert.Nil(t, st.versioningStatusCall,
					"PutBucketVersioning should NOT be called for non-lock bucket %q", tc.header)
			}
		})
	}
}
