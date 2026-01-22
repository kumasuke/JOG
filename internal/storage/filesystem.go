package storage

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}

	// Create object path
	objectPath := filepath.Join(fs.dataDir, bucket, key)
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
	objectPath := filepath.Join(fs.dataDir, bucket, key)
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
	objectPath := filepath.Join(fs.dataDir, bucket, key)
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
	// Check if bucket exists
	exists, err := fs.metadata.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	// Delete object file
	objectPath := filepath.Join(fs.dataDir, bucket, key)
	if err := os.Remove(objectPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete object file: %w", err)
	}

	// Delete object metadata
	return fs.metadata.DeleteObject(ctx, bucket, key)
}

// CopyObject copies an object from source to destination.
func (fs *FileSystem) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*Object, error) {
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
	srcPath := filepath.Join(fs.dataDir, srcBucket, srcKey)
	srcFile, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to open source object: %w", err)
	}
	defer srcFile.Close()

	// Create destination path
	dstPath := filepath.Join(fs.dataDir, dstBucket, dstKey)
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

	// Get all objects with prefix
	objects, err := fs.metadata.ListObjects(ctx, input.Bucket, input.Prefix)
	if err != nil {
		return nil, err
	}

	// Sort by key
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	// Determine starting point
	startKey := input.StartAfter
	if input.ContinuationToken != "" {
		startKey = input.ContinuationToken
	}

	// Filter objects after start key
	if startKey != "" {
		filtered := make([]Object, 0, len(objects))
		for _, obj := range objects {
			if obj.Key > startKey {
				filtered = append(filtered, obj)
			}
		}
		objects = filtered
	}

	// Handle delimiter for common prefixes
	var resultObjects []Object
	var commonPrefixes []string
	commonPrefixMap := make(map[string]bool)

	if input.Delimiter != "" {
		for _, obj := range objects {
			// Find delimiter after prefix
			suffix := strings.TrimPrefix(obj.Key, input.Prefix)
			idx := strings.Index(suffix, input.Delimiter)
			if idx >= 0 {
				// This is a common prefix
				prefix := input.Prefix + suffix[:idx+len(input.Delimiter)]
				if !commonPrefixMap[prefix] {
					commonPrefixMap[prefix] = true
					commonPrefixes = append(commonPrefixes, prefix)
				}
			} else {
				resultObjects = append(resultObjects, obj)
			}
		}
	} else {
		resultObjects = objects
	}

	// Apply max keys
	maxKeys := input.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 1000
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
	srcPath := filepath.Join(fs.dataDir, srcBucket, srcKey)
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

	// Create final object path
	objectPath := filepath.Join(fs.dataDir, bucket, key)
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
		// Delete object file
		objectPath := filepath.Join(fs.dataDir, bucket, key)
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

// Close releases storage resources.
func (fs *FileSystem) Close() error {
	return fs.metadata.Close()
}

// Errors
var (
	ErrBucketNotFound      = errors.New("bucket not found")
	ErrBucketAlreadyExists = errors.New("bucket already exists")
	ErrBucketNotEmpty      = errors.New("bucket not empty")
	ErrObjectNotFound      = errors.New("object not found")
	ErrInvalidBucketName   = errors.New("invalid bucket name")
	ErrUploadNotFound      = errors.New("upload not found")
	ErrInvalidPart         = errors.New("invalid part")
	ErrInvalidRange        = errors.New("invalid range")
)

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
