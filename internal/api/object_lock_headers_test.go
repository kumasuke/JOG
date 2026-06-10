package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errSimulatedLockConfig = errors.New("simulated lock-config backend failure")

type lockConfigFailingStorage struct {
	mockStorage
}

func (s *lockConfigFailingStorage) GetObjectLockConfiguration(ctx context.Context, bucket string) (*storage.ObjectLockConfiguration, error) {
	return nil, errSimulatedLockConfig
}

func TestResolveObjectLockOnWrite_ConfigReadFailureFailClosed(t *testing.T) {
	h := &Handler{storage: &lockConfigFailingStorage{}}
	req := httptest.NewRequest(http.MethodPut, "/bucket/key", nil)

	intent, s3Err := h.resolveObjectLockOnWrite(context.Background(), "bucket", req, false)
	require.Nil(t, intent)
	require.NotNil(t, s3Err)
	assert.Equal(t, ErrInternalError, s3Err)
}

type lockConfigInvalidDefaultStorage struct {
	mockStorage
	dr *storage.DefaultRetention
}

func (s *lockConfigInvalidDefaultStorage) GetObjectLockConfiguration(ctx context.Context, bucket string) (*storage.ObjectLockConfiguration, error) {
	return &storage.ObjectLockConfiguration{
		ObjectLockEnabled: true,
		Rule: &storage.ObjectLockRule{
			DefaultRetention: s.dr,
		},
	}, nil
}

func TestResolveObjectLockOnWrite_InvalidDefaultRetention(t *testing.T) {
	tests := []struct {
		name string
		dr   *storage.DefaultRetention
	}{
		{
			name: "invalid mode",
			dr: &storage.DefaultRetention{
				Mode: storage.ObjectLockRetentionMode("INVALID"),
				Days: int32Ptr(7),
			},
		},
		{
			name: "both days and years",
			dr: &storage.DefaultRetention{
				Mode:  storage.ObjectLockRetentionModeGovernance,
				Days:  int32Ptr(7),
				Years: int32Ptr(1),
			},
		},
		{
			name: "neither days nor years",
			dr: &storage.DefaultRetention{
				Mode: storage.ObjectLockRetentionModeGovernance,
			},
		},
		{
			name: "zero days",
			dr: &storage.DefaultRetention{
				Mode: storage.ObjectLockRetentionModeGovernance,
				Days: int32Ptr(0),
			},
		},
		{
			name: "zero years",
			dr: &storage.DefaultRetention{
				Mode:  storage.ObjectLockRetentionModeGovernance,
				Years: int32Ptr(0),
			},
		},
		{
			name: "negative days",
			dr: &storage.DefaultRetention{
				Mode: storage.ObjectLockRetentionModeGovernance,
				Days: int32Ptr(-1),
			},
		},
		{
			name: "negative years",
			dr: &storage.DefaultRetention{
				Mode:  storage.ObjectLockRetentionModeGovernance,
				Years: int32Ptr(-1),
			},
		},
		{
			name: "days with zero years",
			dr: &storage.DefaultRetention{
				Mode:  storage.ObjectLockRetentionModeGovernance,
				Days:  int32Ptr(7),
				Years: int32Ptr(0),
			},
		},
		{
			name: "days with negative years",
			dr: &storage.DefaultRetention{
				Mode:  storage.ObjectLockRetentionModeGovernance,
				Days:  int32Ptr(7),
				Years: int32Ptr(-1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{storage: &lockConfigInvalidDefaultStorage{dr: tt.dr}}
			req := httptest.NewRequest(http.MethodPut, "/bucket/key", nil)

			intent, s3Err := h.resolveObjectLockOnWrite(context.Background(), "bucket", req, false)
			require.Nil(t, intent)
			require.NotNil(t, s3Err)
			assert.Equal(t, ErrInternalError, s3Err)
		})
	}
}

type multipartUploadFailingStorage struct {
	mockStorage
}

func (s *multipartUploadFailingStorage) GetMultipartUpload(ctx context.Context, uploadID string) (*storage.MultipartUpload, error) {
	return nil, errSimulatedLockConfig
}

func TestMultipartLockIntent_ReadError(t *testing.T) {
	h := &Handler{storage: &multipartUploadFailingStorage{}}
	intent, err := h.multipartLockIntent(context.Background(), "upload-1")
	require.Nil(t, intent)
	require.Error(t, err)
	assert.ErrorIs(t, err, errSimulatedLockConfig)
}

type versioningDisabledStorage struct {
	mockStorage
}

func (s *versioningDisabledStorage) GetBucketVersioning(ctx context.Context, bucket string) (storage.VersioningStatus, error) {
	return storage.VersioningStatusDisabled, nil
}

func TestValidateObjectLockIntentVersioning_InvalidBucketState(t *testing.T) {
	h := &Handler{storage: &versioningDisabledStorage{}}
	intent := &objectLockWriteIntent{
		retention: &storage.ObjectRetention{
			Mode:            storage.ObjectLockRetentionModeGovernance,
			RetainUntilDate: timePtr(time.Now().Add(time.Hour)),
		},
	}
	s3Err := h.validateObjectLockIntentVersioning(context.Background(), "bucket", intent)
	require.NotNil(t, s3Err)
	assert.Equal(t, ErrInvalidBucketState, s3Err)
}

type applyLockFailingStorage struct {
	mockStorage
	rollbackCalled    bool
	rollbackVersionID string
}

func (s *applyLockFailingStorage) GetBucketVersioning(ctx context.Context, bucket string) (storage.VersioningStatus, error) {
	return storage.VersioningStatusEnabled, nil
}

func (s *applyLockFailingStorage) ApplyObjectLockOnVersion(ctx context.Context, bucket, key, versionID string, retention *storage.ObjectRetention, legalHold *storage.ObjectLegalHold) error {
	return errSimulatedLockConfig
}

func (s *applyLockFailingStorage) RollbackNewObjectVersion(ctx context.Context, bucket, key, versionID string) error {
	s.rollbackCalled = true
	s.rollbackVersionID = versionID
	return nil
}

func TestApplyObjectLockOnWrite_RollbackOnFailure(t *testing.T) {
	store := &applyLockFailingStorage{}
	h := &Handler{storage: store}
	intent := &objectLockWriteIntent{
		retention: &storage.ObjectRetention{
			Mode:            storage.ObjectLockRetentionModeGovernance,
			RetainUntilDate: timePtr(time.Now().Add(time.Hour)),
		},
		legalHold: &storage.ObjectLegalHold{Status: storage.ObjectLegalHoldStatusOn},
	}
	err := h.applyObjectLockOnWrite(context.Background(), "bucket", "key", "v-new", intent)
	require.Error(t, err)
	assert.True(t, store.rollbackCalled, "rollback must run when lock apply fails")
	assert.Equal(t, "v-new", store.rollbackVersionID)
}

func int32Ptr(v int32) *int32 { return &v }

func timePtr(t time.Time) *time.Time { return &t }
