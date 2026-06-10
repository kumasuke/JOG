package storage

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FileSystem implements Storage using local file system.
type FileSystem struct {
	dataDir  string
	metadata *Metadata
}

// NewFileSystem creates a new file system storage backend.
func NewFileSystem(dataDir string, metadataDB string) (*FileSystem, error) {
	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Initialize metadata store
	metadata, err := NewMetadata(metadataDB)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metadata: %w", err)
	}

	return &FileSystem{
		dataDir:  dataDir,
		metadata: metadata,
	}, nil
}

// CreateBucket creates a new bucket.
func (fs *FileSystem) CreateBucket(ctx context.Context, name string) error {
	// Check if bucket already exists
	exists, err := fs.metadata.BucketExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return ErrBucketAlreadyExists
	}

	// Create bucket directory
	bucketPath := filepath.Join(fs.dataDir, name)
	if err := os.MkdirAll(bucketPath, 0755); err != nil {
		return fmt.Errorf("failed to create bucket directory: %w", err)
	}

	// Save bucket metadata
	return fs.metadata.CreateBucket(ctx, name, time.Now())
}

// DeleteBucket deletes a bucket.
func (fs *FileSystem) DeleteBucket(ctx context.Context, name string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if bucket is empty
	count, err := fs.metadata.CountObjects(ctx, name)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrBucketNotEmpty
	}

	// Delete bucket directory
	bucketPath := filepath.Join(fs.dataDir, name)
	if err := os.RemoveAll(bucketPath); err != nil {
		return fmt.Errorf("failed to delete bucket directory: %w", err)
	}

	// Delete bucket metadata
	return fs.metadata.DeleteBucket(ctx, name)
}

// HeadBucket returns bucket metadata if it exists.
func (fs *FileSystem) HeadBucket(ctx context.Context, name string) (*Bucket, error) {
	bucket, err := fs.metadata.GetBucket(ctx, name)
	if err != nil {
		return nil, err
	}
	if bucket == nil {
		return nil, ErrBucketNotFound
	}
	return bucket, nil
}

// ListBuckets returns all buckets.
func (fs *FileSystem) ListBuckets(ctx context.Context) ([]Bucket, error) {
	return fs.metadata.ListBuckets(ctx)
}

// PutObject stores an object.
func (fs *FileSystem) PutObject(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string, metadata map[string]string) (*Object, error) {
	// Validate object key to prevent path traversal
	objectPath, err := fs.validateObjectKey(bucket, key)
	if err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}
	objectDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create object directory: %w", err)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp(objectDir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) // Clean up temp file if we don't rename it
	}()

	// Write data and calculate MD5
	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)

	written, err := io.Copy(writer, body)
	if err != nil {
		return nil, fmt.Errorf("failed to write object: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Calculate ETag
	etag := hex.EncodeToString(hash.Sum(nil))

	// Rename temp file to final path
	if err := os.Rename(tmpPath, objectPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Set default content type
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Save object metadata
	obj := &Object{
		Key:          key,
		Size:         written,
		LastModified: time.Now(),
		ETag:         etag,
		ContentType:  contentType,
		Metadata:     metadata,
	}

	if err := fs.metadata.PutObject(ctx, bucket, obj); err != nil {
		return nil, err
	}

	return obj, nil
}

// GetObject retrieves an object.
func (fs *FileSystem) GetObject(ctx context.Context, bucket, key string) (*ObjectData, error) {
	// Validate object key to prevent path traversal
	objectPath, err := fs.validateObjectKey(bucket, key)
	if err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Get object metadata
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, ErrObjectNotFound
	}

	// Open object file
	file, err := os.Open(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to open object file: %w", err)
	}

	return &ObjectData{
		Object: *obj,
		Body:   file,
	}, nil
}

// GetObjectRange retrieves a range of an object.
func (fs *FileSystem) GetObjectRange(ctx context.Context, bucket, key string, start, end int64) (*ObjectData, error) {
	// Validate object key to prevent path traversal
	objectPath, err := fs.validateObjectKey(bucket, key)
	if err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Get object metadata
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, ErrObjectNotFound
	}

	// Open object file
	file, err := os.Open(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to open object file: %w", err)
	}

	// Seek to start position
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to seek: %w", err)
	}

	// Calculate size for range
	rangeSize := end - start + 1

	return &ObjectData{
		Object: Object{
			Key:          obj.Key,
			Size:         rangeSize,
			LastModified: obj.LastModified,
			ETag:         obj.ETag,
			ContentType:  obj.ContentType,
			Metadata:     obj.Metadata,
		},
		Body: &limitedReader{file, rangeSize},
	}, nil
}

// limitedReader limits reading to a specific number of bytes.
type limitedReader struct {
	r io.ReadCloser
	n int64
}

func (lr *limitedReader) Read(p []byte) (int, error) {
	if lr.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > lr.n {
		p = p[:lr.n]
	}
	n, err := lr.r.Read(p)
	lr.n -= int64(n)
	return n, err
}

func (lr *limitedReader) Close() error {
	return lr.r.Close()
}

// HeadObject returns object metadata.
func (fs *FileSystem) HeadObject(ctx context.Context, bucket, key string) (*Object, error) {
	// Validate object key to prevent path traversal
	if _, err := fs.validateObjectKey(bucket, key); err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Get object metadata
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, ErrObjectNotFound
	}

	return obj, nil
}

// DeleteObject deletes an object.
func (fs *FileSystem) DeleteObject(ctx context.Context, bucket, key string) error {
	// Validate object key to prevent path traversal
	objectPath, err := fs.validateObjectKey(bucket, key)
	if err != nil {
		return err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Delete object file
	if err := os.Remove(objectPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete object file: %w", err)
	}

	// Delete object metadata
	return fs.metadata.DeleteObject(ctx, bucket, key)
}

// CopyObject copies an object from source to destination.
func (fs *FileSystem) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*Object, error) {
	// Validate source and destination keys to prevent path traversal
	srcPath, err := fs.validateObjectKey(srcBucket, srcKey)
	if err != nil {
		return nil, err
	}
	dstPath, err := fs.validateObjectKey(dstBucket, dstKey)
	if err != nil {
		return nil, err
	}

	// Check if source bucket exists
	exists, err := fs.metadata.BucketExists(ctx, srcBucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, &BucketNotFoundError{Bucket: srcBucket}
	}

	// Check if destination bucket exists
	exists, err = fs.metadata.BucketExists(ctx, dstBucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, &BucketNotFoundError{Bucket: dstBucket}
	}

	// Get source object metadata
	srcObj, err := fs.metadata.GetObject(ctx, srcBucket, srcKey)
	if err != nil {
		return nil, err
	}
	if srcObj == nil {
		return nil, ErrObjectNotFound
	}

	// Open source file
	srcFile, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to open source object: %w", err)
	}
	defer srcFile.Close()

	// Create destination directory
	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp(dstDir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) // Clean up temp file if we don't rename it
	}()

	// Copy file and calculate MD5
	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)

	written, err := io.Copy(writer, srcFile)
	if err != nil {
		return nil, fmt.Errorf("failed to copy object: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Calculate ETag
	etag := hex.EncodeToString(hash.Sum(nil))

	// Rename temp file to final path
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Determine metadata to use
	var finalMetadata map[string]string
	if metadata != nil {
		// REPLACE directive - use new metadata
		finalMetadata = metadata
	} else {
		// COPY directive - preserve original metadata
		finalMetadata = srcObj.Metadata
	}

	// Create new object metadata
	obj := &Object{
		Key:          dstKey,
		Size:         written,
		LastModified: time.Now(),
		ETag:         etag,
		ContentType:  srcObj.ContentType,
		Metadata:     finalMetadata,
	}

	// Save object metadata
	if err := fs.metadata.PutObject(ctx, dstBucket, obj); err != nil {
		return nil, err
	}

	return obj, nil
}

// CopyObjectVersioned copies a source object/version to a destination key on a
// versioning-enabled bucket, creating a NEW destination version under
// .versions/ without clobbering the destination's existing current version or
// its lock rows (issue #39). srcVersionID "" copies the source's current
// (null/latest) version. Returns the destination object and its new version ID.
func (fs *FileSystem) CopyObjectVersioned(ctx context.Context, srcBucket, srcKey, srcVersionID, dstBucket, dstKey string, metadata map[string]string) (*Object, string, error) {
	// Validate source and destination keys to prevent path traversal
	if _, err := fs.validateObjectKey(srcBucket, srcKey); err != nil {
		return nil, "", err
	}
	if _, err := fs.validateObjectKey(dstBucket, dstKey); err != nil {
		return nil, "", err
	}

	// Check buckets exist
	exists, err := fs.metadata.BucketExists(ctx, srcBucket)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", &BucketNotFoundError{Bucket: srcBucket}
	}
	exists, err = fs.metadata.BucketExists(ctx, dstBucket)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", &BucketNotFoundError{Bucket: dstBucket}
	}

	// Resolve the source: a specific version reads from .versions/, otherwise
	// the current object.
	var srcContentType string
	var srcMetadata map[string]string
	var srcPath string
	if srcVersionID != "" {
		version, err := fs.metadata.GetObjectVersion(ctx, srcBucket, srcKey, srcVersionID)
		if err != nil {
			return nil, "", err
		}
		if version == nil || version.IsDeleteMarker {
			return nil, "", ErrObjectNotFound
		}
		srcContentType = version.ContentType
		srcMetadata = version.Metadata
		srcPath = filepath.Join(fs.dataDir, srcBucket, ".versions", srcKey, srcVersionID)
	} else {
		srcObj, err := fs.metadata.GetObject(ctx, srcBucket, srcKey)
		if err != nil {
			return nil, "", err
		}
		if srcObj == nil {
			return nil, "", ErrObjectNotFound
		}
		srcContentType = srcObj.ContentType
		srcMetadata = srcObj.Metadata
		srcPath = filepath.Join(fs.dataDir, srcBucket, srcKey)
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrObjectNotFound
		}
		return nil, "", fmt.Errorf("failed to open source object: %w", err)
	}
	defer srcFile.Close()

	// Determine final metadata: REPLACE uses the supplied map, COPY preserves
	// the source's metadata.
	finalMetadata := srcMetadata
	if metadata != nil {
		finalMetadata = metadata
	}

	// Write the destination as a new version under .versions/.
	versionID := generateVersionID()
	objectPath := filepath.Join(fs.dataDir, dstBucket, ".versions", dstKey, versionID)
	objectDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create destination version directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(objectDir, ".tmp-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)
	written, err := io.Copy(writer, srcFile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to copy object: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close temp file: %w", err)
	}
	etag := hex.EncodeToString(hash.Sum(nil))
	if err := os.Rename(tmpPath, objectPath); err != nil {
		return nil, "", fmt.Errorf("failed to rename temp file: %w", err)
	}

	if srcContentType == "" {
		srcContentType = "application/octet-stream"
	}
	now := time.Now()

	version := &ObjectVersion{
		Key:          dstKey,
		VersionID:    versionID,
		Size:         written,
		LastModified: now,
		ETag:         etag,
		ContentType:  srcContentType,
		Metadata:     finalMetadata,
	}
	if err := fs.metadata.PutObjectVersion(ctx, dstBucket, version); err != nil {
		os.Remove(objectPath)
		return nil, "", err
	}

	obj := &Object{
		Key:          dstKey,
		Size:         written,
		LastModified: now,
		ETag:         etag,
		ContentType:  srcContentType,
		Metadata:     finalMetadata,
	}
	// Versioned write: update the current pointer WITHOUT the null-version lock
	// guard. A new version must always be allowed even when a prior version
	// (including the ” null version) is under retention/legal hold.
	if err := fs.metadata.PutObjectCurrentPointer(ctx, dstBucket, obj); err != nil {
		return nil, "", err
	}

	// Update the current pointer file.
	currentPath := filepath.Join(fs.dataDir, dstBucket, dstKey)
	if err := os.MkdirAll(filepath.Dir(currentPath), 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create current object directory: %w", err)
	}
	if err := copyFile(objectPath, currentPath); err != nil {
		return nil, "", fmt.Errorf("failed to copy version to current: %w", err)
	}

	return obj, versionID, nil
}

// ListObjectsV2 lists objects in a bucket.
func (fs *FileSystem) ListObjectsV2(ctx context.Context, input *ListObjectsInput) (*ListObjectsOutput, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, input.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Apply max keys
	maxKeys := input.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 1000
	}

	// Determine starting point
	startKey := input.StartAfter
	if input.ContinuationToken != "" {
		startKey = input.ContinuationToken
	}

	// Get objects with prefix, pagination handled by SQL
	// Request more objects when delimiter is used (we may need to skip common prefixes).
	// Use int64 arithmetic to prevent overflow before clamping back to int32 (H-14).
	var fetchLimit int64 = int64(maxKeys)
	if input.Delimiter != "" {
		// Fetch more to account for common prefixes that will be collapsed
		fetchLimit = int64(maxKeys) * 10
		if fetchLimit > 10000 {
			fetchLimit = 10000
		}
	}

	objects, err := fs.metadata.ListObjects(ctx, input.Bucket, input.Prefix, startKey, int32(fetchLimit))
	if err != nil {
		return nil, err
	}

	// Handle delimiter for common prefixes
	var resultObjects []Object
	var commonPrefixes []string
	commonPrefixMap := make(map[string]bool)

	if input.Delimiter != "" {
		for _, obj := range objects {
			// Find delimiter after prefix
			if len(obj.Key) > len(input.Prefix) {
				suffix := obj.Key[len(input.Prefix):]
				idx := strings.Index(suffix, input.Delimiter)
				if idx >= 0 {
					// This is a common prefix
					prefixKey := obj.Key[:len(input.Prefix)+idx+len(input.Delimiter)]
					if !commonPrefixMap[prefixKey] {
						commonPrefixMap[prefixKey] = true
						commonPrefixes = append(commonPrefixes, prefixKey)
					}
					continue
				}
			}
			resultObjects = append(resultObjects, obj)
		}
	} else {
		resultObjects = objects
	}

	output := &ListObjectsOutput{}

	// Check if we need pagination
	if int32(len(resultObjects)) > maxKeys {
		output.IsTruncated = true
		output.NextContinuationToken = resultObjects[maxKeys-1].Key
		resultObjects = resultObjects[:maxKeys]
	}

	output.Objects = resultObjects
	output.CommonPrefixes = commonPrefixes
	output.KeyCount = int32(len(resultObjects))

	return output, nil
}

// CreateMultipartUpload initiates a multipart upload.
func (fs *FileSystem) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (*MultipartUpload, error) {
	// Validate object key to prevent path traversal
	if _, err := fs.validateObjectKey(bucket, key); err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Generate upload ID
	uploadID := generateUploadID()

	// Set default content type
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	upload := &MultipartUpload{
		UploadID:    uploadID,
		Bucket:      bucket,
		Key:         key,
		ContentType: contentType,
		Metadata:    metadata,
		Initiated:   time.Now(),
	}

	// Create directory for parts
	partsDir := filepath.Join(fs.dataDir, ".uploads", uploadID)
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create parts directory: %w", err)
	}

	// Save upload metadata
	if err := fs.metadata.CreateMultipartUpload(ctx, upload); err != nil {
		os.RemoveAll(partsDir)
		return nil, err
	}

	return upload, nil
}

// UploadPart uploads a part for a multipart upload.
func (fs *FileSystem) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader, size int64) (*Part, error) {
	// Check if upload exists
	upload, err := fs.metadata.GetMultipartUpload(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	if upload == nil {
		return nil, ErrUploadNotFound
	}

	// Verify bucket and key match
	if upload.Bucket != bucket || upload.Key != key {
		return nil, ErrUploadNotFound
	}

	// Create part file
	partsDir := filepath.Join(fs.dataDir, ".uploads", uploadID)
	partPath := filepath.Join(partsDir, fmt.Sprintf("%d", partNumber))

	// Write to temp file first
	tmpFile, err := os.CreateTemp(partsDir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	// Write data and calculate MD5
	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)

	written, err := io.Copy(writer, body)
	if err != nil {
		return nil, fmt.Errorf("failed to write part: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Calculate ETag
	etag := hex.EncodeToString(hash.Sum(nil))

	// Rename temp file to part file
	if err := os.Rename(tmpPath, partPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	part := &Part{
		PartNumber:   partNumber,
		Size:         written,
		ETag:         etag,
		LastModified: time.Now(),
	}

	// Save part metadata
	if err := fs.metadata.PutPart(ctx, uploadID, part); err != nil {
		os.Remove(partPath)
		return nil, err
	}

	return part, nil
}

// UploadPartCopy copies data from an existing object to a part for a multipart upload.
func (fs *FileSystem) UploadPartCopy(ctx context.Context, bucket, key, uploadID string, partNumber int32, srcBucket, srcKey string, startByte, endByte *int64) (*Part, error) {
	// Validate source key to prevent path traversal
	srcPath, err := fs.validateObjectKey(srcBucket, srcKey)
	if err != nil {
		return nil, err
	}

	// Check if upload exists
	upload, err := fs.metadata.GetMultipartUpload(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	if upload == nil {
		return nil, ErrUploadNotFound
	}

	// Verify bucket and key match
	if upload.Bucket != bucket || upload.Key != key {
		return nil, ErrUploadNotFound
	}

	// Check if source bucket exists
	exists, err := fs.metadata.BucketExists(ctx, srcBucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Get source object metadata
	srcObj, err := fs.metadata.GetObject(ctx, srcBucket, srcKey)
	if err != nil {
		return nil, err
	}
	if srcObj == nil {
		return nil, ErrObjectNotFound
	}

	// Open source object file
	srcFile, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to open source object: %w", err)
	}
	defer srcFile.Close()

	// Determine start and end positions
	var start, end int64
	if startByte != nil && endByte != nil {
		start = *startByte
		end = *endByte
		// Validate range
		if start < 0 || end >= srcObj.Size || start > end {
			return nil, ErrInvalidRange
		}
	} else {
		start = 0
		end = srcObj.Size - 1
	}

	// Seek to start position
	if _, err := srcFile.Seek(start, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek: %w", err)
	}

	// Calculate copy size
	copySize := end - start + 1

	// Create part file
	partsDir := filepath.Join(fs.dataDir, ".uploads", uploadID)
	partPath := filepath.Join(partsDir, fmt.Sprintf("%d", partNumber))

	// Write to temp file first
	tmpFile, err := os.CreateTemp(partsDir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	// Copy data and calculate MD5
	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)

	// Use LimitReader to copy only the specified range
	limitedReader := io.LimitReader(srcFile, copySize)
	written, err := io.Copy(writer, limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to copy data: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Calculate ETag
	etag := hex.EncodeToString(hash.Sum(nil))

	// Rename temp file to part file
	if err := os.Rename(tmpPath, partPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	part := &Part{
		PartNumber:   partNumber,
		Size:         written,
		ETag:         etag,
		LastModified: time.Now(),
	}

	// Save part metadata
	if err := fs.metadata.PutPart(ctx, uploadID, part); err != nil {
		os.Remove(partPath)
		return nil, err
	}

	return part, nil
}

// CompleteMultipartUpload completes a multipart upload.
func (fs *FileSystem) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []Part) (*Object, error) {
	// Validate object key to prevent path traversal
	objectPath, err := fs.validateObjectKey(bucket, key)
	if err != nil {
		return nil, err
	}

	// Check if upload exists
	upload, err := fs.metadata.GetMultipartUpload(ctx, uploadID)
	if err != nil {
		return nil, err
	}
	if upload == nil {
		return nil, ErrUploadNotFound
	}

	// Verify bucket and key match
	if upload.Bucket != bucket || upload.Key != key {
		return nil, ErrUploadNotFound
	}

	// Verify all parts exist and ETags match
	partsDir := filepath.Join(fs.dataDir, ".uploads", uploadID)
	var totalSize int64
	var partETags []string

	for _, part := range parts {
		storedPart, err := fs.metadata.GetPart(ctx, uploadID, part.PartNumber)
		if err != nil {
			return nil, err
		}
		if storedPart == nil {
			return nil, ErrInvalidPart
		}

		// Clean up ETags for comparison (remove quotes if present)
		expectedETag := strings.Trim(part.ETag, "\"")
		storedETag := strings.Trim(storedPart.ETag, "\"")
		if expectedETag != storedETag {
			return nil, ErrInvalidPart
		}

		totalSize += storedPart.Size
		partETags = append(partETags, storedPart.ETag)
	}

	// Create final object directory
	objectDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create object directory: %w", err)
	}

	// Create temp file for assembled object
	tmpFile, err := os.CreateTemp(objectDir, ".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	// Concatenate parts
	for _, part := range parts {
		partPath := filepath.Join(partsDir, fmt.Sprintf("%d", part.PartNumber))
		partFile, err := os.Open(partPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open part file: %w", err)
		}
		_, err = io.Copy(tmpFile, partFile)
		partFile.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to copy part: %w", err)
		}
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Rename temp file to final path
	if err := os.Rename(tmpPath, objectPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Calculate multipart ETag (MD5 of concatenated part MD5s + "-" + part count)
	hash := md5.New()
	for _, etag := range partETags {
		data, _ := hex.DecodeString(etag)
		hash.Write(data)
	}
	etag := fmt.Sprintf("%s-%d", hex.EncodeToString(hash.Sum(nil)), len(parts))

	// Create object metadata
	obj := &Object{
		Key:          key,
		Size:         totalSize,
		LastModified: time.Now(),
		ETag:         etag,
		ContentType:  upload.ContentType,
		Metadata:     upload.Metadata,
	}

	if err := fs.metadata.PutObject(ctx, bucket, obj); err != nil {
		os.Remove(objectPath)
		return nil, err
	}

	// Clean up upload
	fs.metadata.DeleteMultipartUpload(ctx, uploadID)
	os.RemoveAll(partsDir)

	return obj, nil
}

// CompleteMultipartUploadVersioned completes a multipart upload on a
// versioning-enabled bucket, assembling the parts into a NEW object version
// under .versions/ instead of overwriting the current object in place (issue
// #39). The existing current version and its lock rows are left untouched.
// Returns the assembled object and its new version ID.
func (fs *FileSystem) CompleteMultipartUploadVersioned(ctx context.Context, bucket, key, uploadID string, parts []Part) (*Object, string, error) {
	if _, err := fs.validateObjectKey(bucket, key); err != nil {
		return nil, "", err
	}

	// Check if upload exists
	upload, err := fs.metadata.GetMultipartUpload(ctx, uploadID)
	if err != nil {
		return nil, "", err
	}
	if upload == nil {
		return nil, "", ErrUploadNotFound
	}
	if upload.Bucket != bucket || upload.Key != key {
		return nil, "", ErrUploadNotFound
	}

	// Verify all parts exist and ETags match.
	partsDir := filepath.Join(fs.dataDir, ".uploads", uploadID)
	var totalSize int64
	var partETags []string
	for _, part := range parts {
		storedPart, err := fs.metadata.GetPart(ctx, uploadID, part.PartNumber)
		if err != nil {
			return nil, "", err
		}
		if storedPart == nil {
			return nil, "", ErrInvalidPart
		}
		expectedETag := strings.Trim(part.ETag, "\"")
		storedETag := strings.Trim(storedPart.ETag, "\"")
		if expectedETag != storedETag {
			return nil, "", ErrInvalidPart
		}
		totalSize += storedPart.Size
		partETags = append(partETags, storedPart.ETag)
	}

	// Assemble into a new version file under .versions/.
	versionID := generateVersionID()
	objectPath := filepath.Join(fs.dataDir, bucket, ".versions", key, versionID)
	objectDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create version directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(objectDir, ".tmp-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	for _, part := range parts {
		partPath := filepath.Join(partsDir, fmt.Sprintf("%d", part.PartNumber))
		partFile, err := os.Open(partPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to open part file: %w", err)
		}
		_, err = io.Copy(tmpFile, partFile)
		partFile.Close()
		if err != nil {
			return nil, "", fmt.Errorf("failed to copy part: %w", err)
		}
	}
	if err := tmpFile.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, objectPath); err != nil {
		return nil, "", fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Multipart ETag = MD5 of concatenated part MD5s + "-" + part count.
	hash := md5.New()
	for _, etag := range partETags {
		data, _ := hex.DecodeString(etag)
		hash.Write(data)
	}
	etag := fmt.Sprintf("%s-%d", hex.EncodeToString(hash.Sum(nil)), len(parts))

	now := time.Now()
	version := &ObjectVersion{
		Key:          key,
		VersionID:    versionID,
		Size:         totalSize,
		LastModified: now,
		ETag:         etag,
		ContentType:  upload.ContentType,
		Metadata:     upload.Metadata,
	}
	if err := fs.metadata.PutObjectVersion(ctx, bucket, version); err != nil {
		os.Remove(objectPath)
		return nil, "", err
	}

	obj := &Object{
		Key:          key,
		Size:         totalSize,
		LastModified: now,
		ETag:         etag,
		ContentType:  upload.ContentType,
		Metadata:     upload.Metadata,
	}
	// Versioned write: update the current pointer WITHOUT the null-version lock
	// guard. A new version must always be allowed even when a prior version
	// (including the ” null version) is under retention/legal hold.
	if err := fs.metadata.PutObjectCurrentPointer(ctx, bucket, obj); err != nil {
		return nil, "", err
	}

	// Update the current pointer file.
	currentPath := filepath.Join(fs.dataDir, bucket, key)
	if err := os.MkdirAll(filepath.Dir(currentPath), 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create current object directory: %w", err)
	}
	if err := copyFile(objectPath, currentPath); err != nil {
		return nil, "", fmt.Errorf("failed to copy version to current: %w", err)
	}

	// Clean up upload.
	fs.metadata.DeleteMultipartUpload(ctx, uploadID)
	os.RemoveAll(partsDir)

	return obj, versionID, nil
}

// AbortMultipartUpload aborts a multipart upload.
func (fs *FileSystem) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	// Check if upload exists
	upload, err := fs.metadata.GetMultipartUpload(ctx, uploadID)
	if err != nil {
		return err
	}
	if upload == nil {
		return ErrUploadNotFound
	}

	// Verify bucket and key match
	if upload.Bucket != bucket || upload.Key != key {
		return ErrUploadNotFound
	}

	// Delete parts directory
	partsDir := filepath.Join(fs.dataDir, ".uploads", uploadID)
	os.RemoveAll(partsDir)

	// Delete upload metadata (parts will be deleted by cascade)
	return fs.metadata.DeleteMultipartUpload(ctx, uploadID)
}

// ListParts lists parts for a multipart upload.
func (fs *FileSystem) ListParts(ctx context.Context, input *ListPartsInput) (*ListPartsOutput, error) {
	// Check if upload exists
	upload, err := fs.metadata.GetMultipartUpload(ctx, input.UploadID)
	if err != nil {
		return nil, err
	}
	if upload == nil {
		return nil, ErrUploadNotFound
	}

	// Verify bucket and key match
	if upload.Bucket != input.Bucket || upload.Key != input.Key {
		return nil, ErrUploadNotFound
	}

	parts, isTruncated, nextMarker, err := fs.metadata.ListParts(ctx, input.UploadID, input.MaxParts, input.PartNumberMarker)
	if err != nil {
		return nil, err
	}

	return &ListPartsOutput{
		Parts:                parts,
		IsTruncated:          isTruncated,
		NextPartNumberMarker: nextMarker,
	}, nil
}

// ListMultipartUploads lists in-progress multipart uploads in a bucket.
func (fs *FileSystem) ListMultipartUploads(ctx context.Context, input *ListMultipartUploadsInput) (*ListMultipartUploadsOutput, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, input.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	uploads, isTruncated, nextKeyMarker, nextUploadIDMarker, err := fs.metadata.ListMultipartUploadsByBucket(
		ctx,
		input.Bucket,
		input.Prefix,
		input.MaxUploads,
		input.KeyMarker,
		input.UploadIdMarker,
	)
	if err != nil {
		return nil, err
	}

	return &ListMultipartUploadsOutput{
		Uploads:            uploads,
		IsTruncated:        isTruncated,
		NextKeyMarker:      nextKeyMarker,
		NextUploadIdMarker: nextUploadIDMarker,
	}, nil
}

// DeleteObjects deletes multiple objects.
func (fs *FileSystem) DeleteObjects(ctx context.Context, bucket string, keys []string) ([]DeletedObject, []DeleteError, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, ErrBucketNotFound
	}

	deleted := make([]DeletedObject, 0, len(keys))
	errs := make([]DeleteError, 0)

	// Delete each object
	for _, key := range keys {
		// Validate object key to prevent path traversal
		objectPath, err := fs.validateObjectKey(bucket, key)
		if err != nil {
			errs = append(errs, DeleteError{
				Key:     key,
				Code:    "InvalidArgument",
				Message: "Invalid object key",
			})
			continue
		}

		// Delete object file
		if err := os.Remove(objectPath); err != nil && !os.IsNotExist(err) {
			// If there's an error other than "not exists", add to error list
			errs = append(errs, DeleteError{
				Key:     key,
				Code:    "InternalError",
				Message: fmt.Sprintf("Failed to delete object: %v", err),
			})
			continue
		}

		// Delete object metadata
		if err := fs.metadata.DeleteObject(ctx, bucket, key); err != nil {
			// Even if metadata deletion fails, we still report success
			// This matches S3 behavior for DeleteObjects
		}

		// Report as deleted (even if it didn't exist, matching S3 behavior)
		deleted = append(deleted, DeletedObject{
			Key: key,
		})
	}

	return deleted, errs, nil
}

// generateUploadID generates a unique upload ID.
func generateUploadID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomHex(16))
}

// randomHex generates a random hex string of given length.
func randomHex(length int) string {
	b := make([]byte, length/2)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		// Fallback to timestamp-based ID if random generation fails
		return fmt.Sprintf("%016x", time.Now().UnixNano())[:length]
	}
	return hex.EncodeToString(b)
}

// PutObjectTagging stores tags for an object.
func (fs *FileSystem) PutObjectTagging(ctx context.Context, bucket, key string, tags []Tag) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if object exists
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return err
	}
	if obj == nil {
		return ErrObjectNotFound
	}

	return fs.metadata.PutObjectTags(ctx, bucket, key, tags)
}

// GetObjectTagging returns tags for an object.
func (fs *FileSystem) GetObjectTagging(ctx context.Context, bucket, key string) ([]Tag, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Check if object exists
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, ErrObjectNotFound
	}

	return fs.metadata.GetObjectTags(ctx, bucket, key)
}

// DeleteObjectTagging deletes all tags for an object.
func (fs *FileSystem) DeleteObjectTagging(ctx context.Context, bucket, key string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if object exists
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return err
	}
	if obj == nil {
		return ErrObjectNotFound
	}

	return fs.metadata.DeleteObjectTags(ctx, bucket, key)
}

// PutBucketTagging stores tags for a bucket.
func (fs *FileSystem) PutBucketTagging(ctx context.Context, bucket string, tags []Tag) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.PutBucketTags(ctx, bucket, tags)
}

// GetBucketTagging returns tags for a bucket.
func (fs *FileSystem) GetBucketTagging(ctx context.Context, bucket string) ([]Tag, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	tags, err := fs.metadata.GetBucketTags(ctx, bucket)
	if err != nil {
		return nil, err
	}

	// S3 returns NoSuchTagSet error when no tags are set
	if len(tags) == 0 {
		return nil, ErrNoSuchTagSet
	}

	return tags, nil
}

// DeleteBucketTagging deletes all tags for a bucket.
func (fs *FileSystem) DeleteBucketTagging(ctx context.Context, bucket string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.DeleteBucketTags(ctx, bucket)
}

// PutBucketCors stores CORS configuration for a bucket.
func (fs *FileSystem) PutBucketCors(ctx context.Context, bucket string, cors *CORSConfiguration) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Serialize CORS configuration to JSON
	corsJSON, err := json.Marshal(cors)
	if err != nil {
		return err
	}

	return fs.metadata.PutBucketCors(ctx, bucket, string(corsJSON))
}

// GetBucketCors returns CORS configuration for a bucket.
func (fs *FileSystem) GetBucketCors(ctx context.Context, bucket string) (*CORSConfiguration, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	corsJSON, err := fs.metadata.GetBucketCors(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if corsJSON == "" {
		return nil, ErrNoSuchCORSConfiguration
	}

	var cors CORSConfiguration
	if err := json.Unmarshal([]byte(corsJSON), &cors); err != nil {
		return nil, err
	}

	return &cors, nil
}

// DeleteBucketCors deletes CORS configuration for a bucket.
func (fs *FileSystem) DeleteBucketCors(ctx context.Context, bucket string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.DeleteBucketCors(ctx, bucket)
}

// PutBucketVersioning sets the versioning status for a bucket.
func (fs *FileSystem) PutBucketVersioning(ctx context.Context, bucket string, status VersioningStatus) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.PutBucketVersioning(ctx, bucket, string(status))
}

// GetBucketVersioning returns the versioning status for a bucket.
func (fs *FileSystem) GetBucketVersioning(ctx context.Context, bucket string) (VersioningStatus, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrBucketNotFound
	}

	status, err := fs.metadata.GetBucketVersioning(ctx, bucket)
	if err != nil {
		return "", err
	}

	return VersioningStatus(status), nil
}

// snapshotNullVersionIfNeeded preserves a pre-versioning current object as the
// null version (”) before it is overwritten by a versioned write. It is a
// no-op when the key has no current object, or when at least one version row
// already exists for the key (meaning the object was already created under
// versioning and the null version, if any, is already represented).
func (fs *FileSystem) snapshotNullVersionIfNeeded(ctx context.Context, bucket, key string) error {
	// Already has versions? Nothing to snapshot.
	existing, err := fs.metadata.GetObjectVersion(ctx, bucket, key, "")
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	// Any version at all for this key means it was created under versioning.
	latest, err := fs.metadata.GetLatestObjectVersion(ctx, bucket, key)
	if err != nil {
		return err
	}
	if latest != nil {
		return nil
	}

	// Is there a current (pre-versioning) object to preserve?
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return err
	}
	if obj == nil {
		return nil
	}

	// Copy the current file into the null-version slot under .versions/.
	currentPath := filepath.Join(fs.dataDir, bucket, key)
	nullPath := fs.versionFilePath(bucket, key, "")
	if err := os.MkdirAll(filepath.Dir(nullPath), 0755); err != nil {
		return fmt.Errorf("failed to create null version directory: %w", err)
	}
	if err := copyFile(currentPath, nullPath); err != nil {
		// If the current file is missing, there is nothing recoverable to
		// snapshot; skip rather than fail the write.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to snapshot null version: %w", err)
	}

	nullVersion := &ObjectVersion{
		Key:          key,
		VersionID:    "",
		Size:         obj.Size,
		LastModified: obj.LastModified,
		ETag:         obj.ETag,
		ContentType:  obj.ContentType,
		Metadata:     obj.Metadata,
	}
	return fs.metadata.PutObjectVersion(ctx, bucket, nullVersion)
}

// PutObjectVersioned stores a versioned object.
func (fs *FileSystem) PutObjectVersioned(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string, userMetadata map[string]string) (*Object, string, error) {
	// Validate object key to prevent path traversal
	if _, err := fs.validateObjectKey(bucket, key); err != nil {
		return nil, "", err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		return nil, "", ErrBucketNotFound
	}

	// Risk 3 (issue #39): an object that existed before versioning was enabled
	// has a row in `objects` but none in `object_versions`. Without snapshotting
	// it, this first versioned PUT would silently lose the prior content (and
	// any '' null-version lock would protect a file that no longer exists).
	// Preserve the current file as the null version ('') before writing the new
	// version, so it remains retrievable via ListObjectVersions and is covered
	// by the existing null-version lock rows.
	if err := fs.snapshotNullVersionIfNeeded(ctx, bucket, key); err != nil {
		return nil, "", err
	}

	// Generate version ID
	versionID := generateVersionID()

	// Create object path with version
	objectPath := filepath.Join(fs.dataDir, bucket, ".versions", key, versionID)
	objectDir := filepath.Dir(objectPath)
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create object directory: %w", err)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp(objectDir, ".tmp-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	// Write data and calculate MD5
	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)

	written, err := io.Copy(writer, body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to write object: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close temp file: %w", err)
	}

	// Calculate ETag
	etag := hex.EncodeToString(hash.Sum(nil))

	// Rename temp file to final path
	if err := os.Rename(tmpPath, objectPath); err != nil {
		return nil, "", fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Set default content type
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	now := time.Now()

	// Save version metadata
	version := &ObjectVersion{
		Key:          key,
		VersionID:    versionID,
		Size:         written,
		LastModified: now,
		ETag:         etag,
		ContentType:  contentType,
		Metadata:     userMetadata,
	}

	if err := fs.metadata.PutObjectVersion(ctx, bucket, version); err != nil {
		os.Remove(objectPath)
		return nil, "", err
	}

	// Also update the regular objects table for compatibility.
	// Versioned write: update the current pointer WITHOUT the null-version lock
	// guard. A new version must always be allowed even when a prior version
	// (including the ” null version) is under retention/legal hold.
	obj := &Object{
		Key:          key,
		Size:         written,
		LastModified: now,
		ETag:         etag,
		ContentType:  contentType,
		Metadata:     userMetadata,
	}

	if err := fs.metadata.PutObjectCurrentPointer(ctx, bucket, obj); err != nil {
		return nil, "", err
	}

	// Copy to current object path
	currentPath := filepath.Join(fs.dataDir, bucket, key)
	currentDir := filepath.Dir(currentPath)
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		return nil, "", fmt.Errorf("failed to create current object directory: %w", err)
	}

	// Copy version file to current
	if err := copyFile(objectPath, currentPath); err != nil {
		return nil, "", fmt.Errorf("failed to copy version to current: %w", err)
	}

	return obj, versionID, nil
}

// GetObjectVersioned retrieves a specific version of an object.
func (fs *FileSystem) GetObjectVersioned(ctx context.Context, bucket, key, versionID string) (*ObjectData, error) {
	// Validate object key to prevent path traversal
	if _, err := fs.validateObjectKey(bucket, key); err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Get version metadata
	version, err := fs.metadata.GetObjectVersion(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, ErrObjectNotFound
	}

	if version.IsDeleteMarker {
		return nil, ErrObjectNotFound
	}

	// Open version file
	objectPath := fs.versionFilePath(bucket, key, versionID)
	file, err := os.Open(objectPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to open version file: %w", err)
	}

	return &ObjectData{
		Object: Object{
			Key:          version.Key,
			Size:         version.Size,
			LastModified: version.LastModified,
			ETag:         version.ETag,
			ContentType:  version.ContentType,
			Metadata:     version.Metadata,
		},
		Body: file,
	}, nil
}

// DeleteObjectVersioned deletes an object version, or creates a delete marker.
//
// versionTargeted distinguishes a version-targeted permanent delete from an
// unspecified delete (issue #39): when true, the (bucket, key, versionID)
// version is physically removed — including the null version (versionID == "").
// When false, an unspecified delete on a versioning bucket creates a delete
// marker instead.
func (fs *FileSystem) DeleteObjectVersioned(ctx context.Context, bucket, key, versionID string, versionTargeted bool) (string, bool, error) {
	// Validate object key to prevent path traversal
	if _, err := fs.validateObjectKey(bucket, key); err != nil {
		return "", false, err
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, ErrBucketNotFound
	}

	// Version-targeted permanent delete (incl. the null version).
	if versionTargeted {
		// Get version to check if it's a delete marker.
		version, err := fs.metadata.GetObjectVersion(ctx, bucket, key, versionID)
		if err != nil {
			return "", false, err
		}

		isDeleteMarker := false
		if version != nil {
			isDeleteMarker = version.IsDeleteMarker
		} else if versionID != "" {
			// A non-null version that does not exist.
			return "", false, ErrObjectNotFound
		}
		// versionID == "" with no explicit version row: a pre-versioning
		// null version — fall through and remove the current object below.

		// Delete version file (null version maps to the sentinel path).
		objectPath := fs.versionFilePath(bucket, key, versionID)
		if err := os.Remove(objectPath); err != nil && !os.IsNotExist(err) {
			return "", false, fmt.Errorf("failed to delete version file: %w", err)
		}

		// Delete version metadata (no-op if the row is absent).
		if err := fs.metadata.DeleteObjectVersion(ctx, bucket, key, versionID); err != nil {
			return "", false, err
		}

		// When deleting the null version, also clear the live objects row and
		// the current file so the key no longer resolves to the deleted bytes.
		if versionID == "" {
			if err := fs.metadata.DeleteObject(ctx, bucket, key); err != nil {
				return "", false, err
			}
			os.Remove(filepath.Join(fs.dataDir, bucket, key))
		}

		// Clean up any retention / legal hold rows for this version so they
		// don't orphan after the data is gone. The lock evaluation was already
		// performed by the handler before this call (issue #39).
		if err := fs.metadata.DeleteObjectLockRows(ctx, bucket, key, versionID); err != nil {
			return "", false, err
		}

		return versionID, isDeleteMarker, nil
	}

	// Unspecified delete - create a delete marker
	deleteMarkerID := generateVersionID()
	now := time.Now()

	deleteMarker := &ObjectVersion{
		Key:            key,
		VersionID:      deleteMarkerID,
		Size:           0,
		LastModified:   now,
		ETag:           "",
		ContentType:    "",
		IsDeleteMarker: true,
	}

	if err := fs.metadata.PutObjectVersion(ctx, bucket, deleteMarker); err != nil {
		return "", false, err
	}

	// Remove from regular objects table
	if err := fs.metadata.DeleteObject(ctx, bucket, key); err != nil {
		return "", false, err
	}

	// Remove current file
	currentPath := filepath.Join(fs.dataDir, bucket, key)
	os.Remove(currentPath)

	return deleteMarkerID, true, nil
}

// ListObjectVersions lists all versions of objects in a bucket.
func (fs *FileSystem) ListObjectVersions(ctx context.Context, input *ListObjectVersionsInput) (*ListObjectVersionsOutput, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, input.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	versions, isTruncated, nextKeyMarker, nextVersionIDMarker, err := fs.metadata.ListObjectVersions(
		ctx, input.Bucket, input.Prefix, input.MaxKeys, input.KeyMarker, input.VersionIdMarker,
	)
	if err != nil {
		return nil, err
	}

	output := &ListObjectVersionsOutput{
		IsTruncated:         isTruncated,
		NextKeyMarker:       nextKeyMarker,
		NextVersionIdMarker: nextVersionIDMarker,
	}

	// Find latest version for each key
	latestVersions := make(map[string]string)
	for _, v := range versions {
		if _, exists := latestVersions[v.Key]; !exists {
			latestVersions[v.Key] = v.VersionID
		}
	}

	for _, v := range versions {
		ov := ObjectVersion{
			Key:            v.Key,
			VersionID:      v.VersionID,
			IsLatest:       v.VersionID == latestVersions[v.Key],
			LastModified:   v.LastModified,
			ETag:           v.ETag,
			Size:           v.Size,
			IsDeleteMarker: v.IsDeleteMarker,
		}
		if v.IsDeleteMarker {
			output.DeleteMarkers = append(output.DeleteMarkers, ov)
		} else {
			output.Versions = append(output.Versions, ov)
		}
	}

	return output, nil
}

// generateVersionID generates a unique version ID.
func generateVersionID() string {
	return uuid.New().String()
}

// nullVersionDirName is the on-disk directory name used for the null version
// (version_id == ""). An empty version_id cannot be a filesystem path
// component, so the null version's bytes live under this sentinel name inside
// .versions/<key>/.
const nullVersionDirName = "null"

// versionFilePath returns the on-disk path for a specific version of a key,
// mapping the null version ("") to the nullVersionDirName sentinel.
func (fs *FileSystem) versionFilePath(bucket, key, versionID string) string {
	name := versionID
	if name == "" {
		name = nullVersionDirName
	}
	return filepath.Join(fs.dataDir, bucket, ".versions", key, name)
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return dstFile.Sync()
}

// DefaultOwnerID is the default owner ID for ACLs.
const DefaultOwnerID = "default-owner-id"

// DefaultOwnerDisplay is the default owner display name for ACLs.
const DefaultOwnerDisplay = "default-owner"

// PutBucketACL stores the ACL for a bucket.
func (fs *FileSystem) PutBucketACL(ctx context.Context, bucket string, acl *ACL) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.PutBucketACL(ctx, bucket, acl)
}

// GetBucketACL returns the ACL for a bucket.
func (fs *FileSystem) GetBucketACL(ctx context.Context, bucket string) (*ACL, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	acl, err := fs.metadata.GetBucketACL(ctx, bucket)
	if err != nil {
		return nil, err
	}

	// Return default ACL if none set
	if acl == nil {
		acl = &ACL{
			OwnerID:      DefaultOwnerID,
			OwnerDisplay: DefaultOwnerDisplay,
			Grants: []ACLGrant{
				{
					Permission:  ACLPermissionFullControl,
					GranteeType: ACLGranteeTypeCanonicalUser,
					GranteeID:   DefaultOwnerID,
				},
			},
		}
	}

	return acl, nil
}

// PutObjectACL stores the ACL for an object.
func (fs *FileSystem) PutObjectACL(ctx context.Context, bucket, key string, acl *ACL) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if object exists
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return err
	}
	if obj == nil {
		return ErrObjectNotFound
	}

	return fs.metadata.PutObjectACL(ctx, bucket, key, acl)
}

// GetObjectACL returns the ACL for an object.
func (fs *FileSystem) GetObjectACL(ctx context.Context, bucket, key string) (*ACL, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Check if object exists
	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, ErrObjectNotFound
	}

	acl, err := fs.metadata.GetObjectACL(ctx, bucket, key)
	if err != nil {
		return nil, err
	}

	// Return default ACL if none set
	if acl == nil {
		acl = &ACL{
			OwnerID:      DefaultOwnerID,
			OwnerDisplay: DefaultOwnerDisplay,
			Grants: []ACLGrant{
				{
					Permission:  ACLPermissionFullControl,
					GranteeType: ACLGranteeTypeCanonicalUser,
					GranteeID:   DefaultOwnerID,
				},
			},
		}
	}

	return acl, nil
}

// CannedACLToACL converts a canned ACL to an ACL object.
func CannedACLToACL(cannedACL CannedACL, ownerID, ownerDisplay string) *ACL {
	acl := &ACL{
		OwnerID:      ownerID,
		OwnerDisplay: ownerDisplay,
		Grants: []ACLGrant{
			{
				Permission:  ACLPermissionFullControl,
				GranteeType: ACLGranteeTypeCanonicalUser,
				GranteeID:   ownerID,
			},
		},
	}

	switch cannedACL {
	case CannedACLPrivate:
		// Default - owner has FULL_CONTROL
	case CannedACLPublicRead:
		acl.Grants = append(acl.Grants, ACLGrant{
			Permission:  ACLPermissionRead,
			GranteeType: ACLGranteeTypeGroup,
			GranteeURI:  AllUsersGroupURI,
		})
	case CannedACLPublicReadWrite:
		acl.Grants = append(acl.Grants, ACLGrant{
			Permission:  ACLPermissionRead,
			GranteeType: ACLGranteeTypeGroup,
			GranteeURI:  AllUsersGroupURI,
		})
		acl.Grants = append(acl.Grants, ACLGrant{
			Permission:  ACLPermissionWrite,
			GranteeType: ACLGranteeTypeGroup,
			GranteeURI:  AllUsersGroupURI,
		})
	case CannedACLAuthenticatedRead:
		acl.Grants = append(acl.Grants, ACLGrant{
			Permission:  ACLPermissionRead,
			GranteeType: ACLGranteeTypeGroup,
			GranteeURI:  AuthenticatedUsersGroupURI,
		})
	}

	return acl
}

// PutBucketEncryption stores the encryption configuration for a bucket.
func (fs *FileSystem) PutBucketEncryption(ctx context.Context, bucket string, config *ServerSideEncryptionConfiguration) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Serialize encryption configuration to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return fs.metadata.PutBucketEncryption(ctx, bucket, string(configJSON))
}

// GetBucketEncryption returns the encryption configuration for a bucket.
func (fs *FileSystem) GetBucketEncryption(ctx context.Context, bucket string) (*ServerSideEncryptionConfiguration, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	configJSON, err := fs.metadata.GetBucketEncryption(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if configJSON == "" {
		return nil, ErrNoSuchEncryptionConfiguration
	}

	var config ServerSideEncryptionConfiguration
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// DeleteBucketEncryption deletes the encryption configuration for a bucket.
func (fs *FileSystem) DeleteBucketEncryption(ctx context.Context, bucket string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.DeleteBucketEncryption(ctx, bucket)
}

// PutBucketLifecycleConfiguration stores the lifecycle configuration for a bucket.
func (fs *FileSystem) PutBucketLifecycleConfiguration(ctx context.Context, bucket string, config *LifecycleConfiguration) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Serialize lifecycle configuration to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return fs.metadata.PutBucketLifecycle(ctx, bucket, string(configJSON))
}

// GetBucketLifecycleConfiguration returns the lifecycle configuration for a bucket.
func (fs *FileSystem) GetBucketLifecycleConfiguration(ctx context.Context, bucket string) (*LifecycleConfiguration, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	configJSON, err := fs.metadata.GetBucketLifecycle(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if configJSON == "" {
		return nil, ErrNoSuchLifecycleConfiguration
	}

	var config LifecycleConfiguration
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// DeleteBucketLifecycle deletes the lifecycle configuration for a bucket.
func (fs *FileSystem) DeleteBucketLifecycle(ctx context.Context, bucket string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.DeleteBucketLifecycle(ctx, bucket)
}

// Close releases storage resources.
func (fs *FileSystem) Close() error {
	return fs.metadata.Close()
}

// SetBucketObjectLockEnabled sets whether object lock is enabled for a bucket.
func (fs *FileSystem) SetBucketObjectLockEnabled(ctx context.Context, bucket string, enabled bool) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.SetBucketObjectLockEnabled(ctx, bucket, enabled)
}

// GetBucketObjectLockEnabled returns whether object lock is enabled for a bucket.
func (fs *FileSystem) GetBucketObjectLockEnabled(ctx context.Context, bucket string) (bool, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrBucketNotFound
	}

	return fs.metadata.GetBucketObjectLockEnabled(ctx, bucket)
}

// PutObjectLockConfiguration stores the object lock configuration for a bucket.
func (fs *FileSystem) PutObjectLockConfiguration(ctx context.Context, bucket string, config *ObjectLockConfiguration) error {
	// Validate input
	if config == nil {
		return ErrMalformedXML
	}

	// Validate default retention mode if rule exists
	if config.Rule != nil && config.Rule.DefaultRetention != nil {
		mode := string(config.Rule.DefaultRetention.Mode)
		if mode != "GOVERNANCE" && mode != "COMPLIANCE" {
			return ErrMalformedXML
		}
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if object lock is enabled for this bucket
	enabled, err := fs.metadata.GetBucketObjectLockEnabled(ctx, bucket)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrObjectLockConfigurationNotFound
	}

	// Serialize configuration to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return fs.metadata.PutBucketObjectLockConfig(ctx, bucket, string(configJSON))
}

// GetObjectLockConfiguration returns the object lock configuration for a bucket.
func (fs *FileSystem) GetObjectLockConfiguration(ctx context.Context, bucket string) (*ObjectLockConfiguration, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Check if object lock is enabled for this bucket
	enabled, err := fs.metadata.GetBucketObjectLockEnabled(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrObjectLockConfigurationNotFound
	}

	configJSON, err := fs.metadata.GetBucketObjectLockConfig(ctx, bucket)
	if err != nil {
		return nil, err
	}

	// If no config set but object lock is enabled, return basic enabled config
	if configJSON == "" {
		return &ObjectLockConfiguration{
			ObjectLockEnabled: true,
		}, nil
	}

	var config ObjectLockConfiguration
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// ResolveObjectVersion maps a client-supplied versionId selector to a concrete
// version (issue #39). requestedVersionID "" selects the current version: on a
// versioning-enabled bucket that is the newest version row (which may be a
// delete marker), otherwise the null version of the current object. A
// non-empty requestedVersionID selects that exact version.
func (fs *FileSystem) ResolveObjectVersion(ctx context.Context, bucket, key, requestedVersionID string) (string, bool, error) {
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, ErrBucketNotFound
	}

	if requestedVersionID != "" {
		version, err := fs.metadata.GetObjectVersion(ctx, bucket, key, requestedVersionID)
		if err != nil {
			return "", false, err
		}
		if version == nil {
			return "", false, ErrObjectNotFound
		}
		return requestedVersionID, version.IsDeleteMarker, nil
	}

	// Current version: prefer the newest version row when versioning has
	// produced any; fall back to the null version of the current object.
	latest, err := fs.metadata.GetLatestObjectVersion(ctx, bucket, key)
	if err != nil {
		return "", false, err
	}
	if latest != nil {
		return latest.VersionID, latest.IsDeleteMarker, nil
	}

	obj, err := fs.metadata.GetObject(ctx, bucket, key)
	if err != nil {
		return "", false, err
	}
	if obj == nil {
		return "", false, ErrObjectNotFound
	}
	// Pre-versioning current object: addressed as the null version.
	return "", false, nil
}

// objectVersionExists reports whether a (bucket, key, versionID) version row
// exists. The empty version_id ("") addresses the null version, whose presence
// is tracked by the regular objects table.
func (fs *FileSystem) objectVersionExists(ctx context.Context, bucket, key, versionID string) (bool, error) {
	// A version row (including an explicit '' null-version row created by a
	// snapshot) takes precedence.
	version, err := fs.metadata.GetObjectVersion(ctx, bucket, key, versionID)
	if err != nil {
		return false, err
	}
	if version != nil {
		return true, nil
	}
	// For the null version, also accept a pre-versioning current object that
	// has no explicit version row yet.
	if versionID == "" {
		obj, err := fs.metadata.GetObject(ctx, bucket, key)
		if err != nil {
			return false, err
		}
		return obj != nil, nil
	}
	return false, nil
}

// PutObjectRetention stores the retention settings for a specific object
// version. versionID "" targets the null version (the current object on a
// non-versioning bucket).
func (fs *FileSystem) PutObjectRetention(ctx context.Context, bucket, key, versionID string, retention *ObjectRetention) error {
	// Validate input
	if retention == nil || retention.RetainUntilDate == nil {
		return ErrMalformedXML
	}

	// Validate retention mode
	mode := string(retention.Mode)
	if mode != "GOVERNANCE" && mode != "COMPLIANCE" {
		return ErrMalformedXML
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if object lock is enabled for this bucket
	enabled, err := fs.metadata.GetBucketObjectLockEnabled(ctx, bucket)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrInvalidRequestObjectLock
	}

	// Check if the targeted version exists
	versionExists, err := fs.objectVersionExists(ctx, bucket, key, versionID)
	if err != nil {
		return err
	}
	if !versionExists {
		return ErrObjectNotFound
	}

	return fs.metadata.PutObjectRetention(ctx, bucket, key, versionID, mode, *retention.RetainUntilDate)
}

// GetObjectRetention returns the retention settings for a specific object
// version.
func (fs *FileSystem) GetObjectRetention(ctx context.Context, bucket, key, versionID string) (*ObjectRetention, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Check if the targeted version exists
	versionExists, err := fs.objectVersionExists(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}
	if !versionExists {
		return nil, ErrObjectNotFound
	}

	mode, retainUntilDate, err := fs.metadata.GetObjectRetention(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}
	if retainUntilDate == nil {
		return nil, ErrNoSuchObjectLockConfiguration
	}

	return &ObjectRetention{
		Mode:            ObjectLockRetentionMode(mode),
		RetainUntilDate: retainUntilDate,
	}, nil
}

// PutObjectLegalHold stores the legal hold status for a specific object version.
func (fs *FileSystem) PutObjectLegalHold(ctx context.Context, bucket, key, versionID string, legalHold *ObjectLegalHold) error {
	// Validate input
	if legalHold == nil {
		return ErrMalformedXML
	}

	// Validate legal hold status
	status := string(legalHold.Status)
	if status != "ON" && status != "OFF" {
		return ErrMalformedXML
	}

	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Check if object lock is enabled for this bucket
	enabled, err := fs.metadata.GetBucketObjectLockEnabled(ctx, bucket)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrInvalidRequestObjectLock
	}

	// Check if the targeted version exists
	versionExists, err := fs.objectVersionExists(ctx, bucket, key, versionID)
	if err != nil {
		return err
	}
	if !versionExists {
		return ErrObjectNotFound
	}

	return fs.metadata.PutObjectLegalHold(ctx, bucket, key, versionID, status)
}

// GetObjectLegalHold returns the legal hold status for a specific object
// version.
func (fs *FileSystem) GetObjectLegalHold(ctx context.Context, bucket, key, versionID string) (*ObjectLegalHold, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Check if the targeted version exists
	versionExists, err := fs.objectVersionExists(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}
	if !versionExists {
		return nil, ErrObjectNotFound
	}

	status, err := fs.metadata.GetObjectLegalHold(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}
	if status == "" {
		return nil, ErrNoSuchObjectLockConfiguration
	}

	return &ObjectLegalHold{
		Status: ObjectLegalHoldStatus(status),
	}, nil
}

// PutBucketPolicy stores the policy for a bucket.
func (fs *FileSystem) PutBucketPolicy(ctx context.Context, bucket string, policy string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.PutBucketPolicy(ctx, bucket, policy)
}

// GetBucketPolicy returns the policy for a bucket.
func (fs *FileSystem) GetBucketPolicy(ctx context.Context, bucket string) (string, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrBucketNotFound
	}

	policy, err := fs.metadata.GetBucketPolicy(ctx, bucket)
	if err != nil {
		return "", err
	}
	if policy == "" {
		return "", ErrNoSuchBucketPolicy
	}

	return policy, nil
}

// DeleteBucketPolicy deletes the policy for a bucket.
func (fs *FileSystem) DeleteBucketPolicy(ctx context.Context, bucket string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.DeleteBucketPolicy(ctx, bucket)
}

// PutBucketWebsite stores the website configuration for a bucket.
func (fs *FileSystem) PutBucketWebsite(ctx context.Context, bucket string, config *WebsiteConfiguration) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return fs.metadata.PutBucketWebsite(ctx, bucket, string(configJSON))
}

// GetBucketWebsite returns the website configuration for a bucket.
func (fs *FileSystem) GetBucketWebsite(ctx context.Context, bucket string) (*WebsiteConfiguration, error) {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	configJSON, err := fs.metadata.GetBucketWebsite(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if configJSON == "" {
		return nil, ErrNoSuchWebsiteConfiguration
	}

	var config WebsiteConfiguration
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// DeleteBucketWebsite deletes the website configuration for a bucket.
func (fs *FileSystem) DeleteBucketWebsite(ctx context.Context, bucket string) error {
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	return fs.metadata.DeleteBucketWebsite(ctx, bucket)
}

// PutBucketNotification stores bucket notification configuration.
func (fs *FileSystem) PutBucketNotification(ctx context.Context, bucket string, config *NotificationConfiguration) error {
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return fs.metadata.PutBucketNotification(ctx, bucket, string(data))
}

// GetBucketNotification returns bucket notification configuration.
func (fs *FileSystem) GetBucketNotification(ctx context.Context, bucket string) (*NotificationConfiguration, error) {
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	data, err := fs.metadata.GetBucketNotification(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if data == "" {
		return &NotificationConfiguration{}, nil
	}

	var config NotificationConfiguration
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// Errors
var (
	ErrBucketNotFound                  = errors.New("bucket not found")
	ErrBucketAlreadyExists             = errors.New("bucket already exists")
	ErrBucketNotEmpty                  = errors.New("bucket not empty")
	ErrObjectNotFound                  = errors.New("object not found")
	ErrInvalidBucketName               = errors.New("invalid bucket name")
	ErrInvalidKey                      = errors.New("invalid object key")
	ErrUploadNotFound                  = errors.New("upload not found")
	ErrInvalidPart                     = errors.New("invalid part")
	ErrInvalidRange                    = errors.New("invalid range")
	ErrNoSuchTagSet                    = errors.New("no such tag set")
	ErrNoSuchCORSConfiguration         = errors.New("no such CORS configuration")
	ErrNoSuchEncryptionConfiguration   = errors.New("no such encryption configuration")
	ErrNoSuchLifecycleConfiguration    = errors.New("no such lifecycle configuration")
	ErrObjectLockConfigurationNotFound = errors.New("object lock configuration not found")
	ErrNoSuchObjectLockConfiguration   = errors.New("no such object lock configuration")
	ErrInvalidRequestObjectLock        = errors.New("bucket is not object lock enabled")
	ErrMalformedXML                    = errors.New("malformed XML")
	ErrNoSuchBucketPolicy              = errors.New("no such bucket policy")
	ErrNoSuchWebsiteConfiguration      = errors.New("no such website configuration")
	// ErrObjectLocked is returned when a write would overwrite or remove an
	// object version that is still protected by an active retention or legal
	// hold. It is the storage-layer fail-closed companion to the handler's
	// evaluateObjectLock check (issue #39, 論点B).
	ErrObjectLocked = errors.New("object is locked by retention or legal hold")
)

// validateObjectKey validates the object key to prevent path traversal attacks.
// It returns the validated and cleaned path, or an error if the key is invalid.
func (fs *FileSystem) validateObjectKey(bucket, key string) (string, error) {
	// Reject empty keys
	if key == "" {
		return "", ErrInvalidKey
	}

	// Reject control characters that can be used to smuggle commands or
	// bypass log sanitisation (\x00, \r, \n).
	for _, c := range key {
		if c == 0x00 || c == '\r' || c == '\n' {
			return "", ErrInvalidKey
		}
	}

	// Reject backslash — on POSIX filesystems it is a valid filename
	// character but it creates normalisation confusion on Windows and in
	// URLs.
	if strings.Contains(key, "\\") {
		return "", ErrInvalidKey
	}

	// Reject normalisation-collision patterns: "./" prefix, "/." suffix,
	// and double slashes anywhere.  These can survive filepath.Clean if the
	// component resolves to the same directory after cleaning.
	if strings.HasPrefix(key, "./") || strings.HasSuffix(key, "/.") ||
		strings.Contains(key, "//") {
		return "", ErrInvalidKey
	}

	// Reject keys containing path traversal sequences (".." components).
	if key == ".." || strings.HasPrefix(key, "../") || strings.HasSuffix(key, "/..") || strings.Contains(key, "/../") {
		return "", ErrInvalidKey
	}

	// Build the full path
	objectPath := filepath.Join(fs.dataDir, bucket, key)

	// Clean the path to resolve any remaining traversal
	cleanPath := filepath.Clean(objectPath)

	// Verify the cleaned path is within the bucket directory
	bucketPath := filepath.Clean(filepath.Join(fs.dataDir, bucket))

	// The clean path must be inside the bucket directory (not equal to it).
	// filepath.IsLocal provides an additional deep-defence layer on top of
	// our explicit checks above.
	if !strings.HasPrefix(cleanPath, bucketPath+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	if !filepath.IsLocal(key) {
		return "", ErrInvalidKey
	}

	return cleanPath, nil
}

// BucketNotFoundError is an error that includes the bucket name.
type BucketNotFoundError struct {
	Bucket string
}

func (e *BucketNotFoundError) Error() string {
	return fmt.Sprintf("bucket not found: %s", e.Bucket)
}

// Is implements errors.Is for BucketNotFoundError.
func (e *BucketNotFoundError) Is(target error) bool {
	return target == ErrBucketNotFound
}
