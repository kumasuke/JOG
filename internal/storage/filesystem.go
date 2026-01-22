package storage

import (
	"context"
	"crypto/md5"
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
)
