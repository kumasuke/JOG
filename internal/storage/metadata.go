package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Metadata manages object metadata using SQLite.
type Metadata struct {
	db *sql.DB
}

// NewMetadata creates a new metadata store.
func NewMetadata(dbPath string) (*Metadata, error) {
	// Ensure directory exists
	if err := ensureDir(filepath.Dir(dbPath)); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	m := &Metadata{db: db}
	if err := m.initialize(); err != nil {
		db.Close()
		return nil, err
	}

	return m, nil
}

func (m *Metadata) initialize() error {
	// Create buckets table
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS buckets (
			name TEXT PRIMARY KEY,
			creation_date DATETIME NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create buckets table: %w", err)
	}

	// Create objects table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS objects (
			bucket TEXT NOT NULL,
			key TEXT NOT NULL,
			size INTEGER NOT NULL,
			last_modified DATETIME NOT NULL,
			etag TEXT NOT NULL,
			content_type TEXT NOT NULL,
			metadata TEXT,
			PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create objects table: %w", err)
	}

	// Create index for listing
	_, err = m.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_objects_bucket_key ON objects(bucket, key)
	`)
	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}

	// Create multipart_uploads table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS multipart_uploads (
			upload_id TEXT PRIMARY KEY,
			bucket TEXT NOT NULL,
			key TEXT NOT NULL,
			content_type TEXT NOT NULL,
			metadata TEXT,
			initiated DATETIME NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create multipart_uploads table: %w", err)
	}

	// Create parts table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS parts (
			upload_id TEXT NOT NULL,
			part_number INTEGER NOT NULL,
			size INTEGER NOT NULL,
			etag TEXT NOT NULL,
			last_modified DATETIME NOT NULL,
			PRIMARY KEY (upload_id, part_number),
			FOREIGN KEY (upload_id) REFERENCES multipart_uploads(upload_id) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create parts table: %w", err)
	}

	// Create object_tags table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS object_tags (
			bucket TEXT NOT NULL,
			key TEXT NOT NULL,
			tag_key TEXT NOT NULL,
			tag_value TEXT NOT NULL,
			PRIMARY KEY (bucket, key, tag_key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create object_tags table: %w", err)
	}

	// Create bucket_tags table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_tags (
			bucket TEXT NOT NULL,
			tag_key TEXT NOT NULL,
			tag_value TEXT NOT NULL,
			PRIMARY KEY (bucket, tag_key),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_tags table: %w", err)
	}

	// Create bucket_cors table (stores CORS config as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_cors (
			bucket TEXT PRIMARY KEY,
			cors_config TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_cors table: %w", err)
	}

	// Create bucket_versioning table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_versioning (
			bucket TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_versioning table: %w", err)
	}

	// Create object_versions table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS object_versions (
			bucket TEXT NOT NULL,
			key TEXT NOT NULL,
			version_id TEXT NOT NULL,
			size INTEGER NOT NULL,
			last_modified DATETIME NOT NULL,
			etag TEXT NOT NULL,
			content_type TEXT NOT NULL,
			metadata TEXT,
			is_delete_marker INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, key, version_id),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create object_versions table: %w", err)
	}

	// Create index for version listing
	_, err = m.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_key ON object_versions(bucket, key, last_modified DESC)
	`)
	if err != nil {
		return fmt.Errorf("failed to create version index: %w", err)
	}

	// Create bucket_acls table (stores ACL as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_acls (
			bucket TEXT PRIMARY KEY,
			acl_config TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_acls table: %w", err)
	}

	// Create object_acls table (stores ACL as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS object_acls (
			bucket TEXT NOT NULL,
			key TEXT NOT NULL,
			acl_config TEXT NOT NULL,
			PRIMARY KEY (bucket, key),
			FOREIGN KEY (bucket, key) REFERENCES objects(bucket, key) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create object_acls table: %w", err)
	}

	// Create bucket_encryption table (stores encryption config as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_encryption (
			bucket TEXT PRIMARY KEY,
			encryption_config TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_encryption table: %w", err)
	}

	return nil
}

// CreateBucket creates a new bucket.
func (m *Metadata) CreateBucket(ctx context.Context, name string, creationDate time.Time) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO buckets (name, creation_date) VALUES (?, ?)
	`, name, creationDate)
	return err
}

// DeleteBucket deletes a bucket.
func (m *Metadata) DeleteBucket(ctx context.Context, name string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM buckets WHERE name = ?`, name)
	return err
}

// BucketExists checks if a bucket exists.
func (m *Metadata) BucketExists(ctx context.Context, name string) (bool, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM buckets WHERE name = ?`, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetBucket returns bucket metadata.
func (m *Metadata) GetBucket(ctx context.Context, name string) (*Bucket, error) {
	var bucket Bucket
	err := m.db.QueryRowContext(ctx, `
		SELECT name, creation_date FROM buckets WHERE name = ?
	`, name).Scan(&bucket.Name, &bucket.CreationDate)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &bucket, nil
}

// ListBuckets returns all buckets.
func (m *Metadata) ListBuckets(ctx context.Context) ([]Bucket, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT name, creation_date FROM buckets ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []Bucket
	for rows.Next() {
		var bucket Bucket
		if err := rows.Scan(&bucket.Name, &bucket.CreationDate); err != nil {
			return nil, err
		}
		buckets = append(buckets, bucket)
	}
	return buckets, rows.Err()
}

// PutObject stores object metadata.
func (m *Metadata) PutObject(ctx context.Context, bucket string, obj *Object) error {
	metadata, err := json.Marshal(obj.Metadata)
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO objects (bucket, key, size, last_modified, etag, content_type, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, bucket, obj.Key, obj.Size, obj.LastModified, obj.ETag, obj.ContentType, string(metadata))
	return err
}

// GetObject returns object metadata.
func (m *Metadata) GetObject(ctx context.Context, bucket, key string) (*Object, error) {
	var obj Object
	var metadataStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT key, size, last_modified, etag, content_type, metadata
		FROM objects WHERE bucket = ? AND key = ?
	`, bucket, key).Scan(&obj.Key, &obj.Size, &obj.LastModified, &obj.ETag, &obj.ContentType, &metadataStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &obj.Metadata); err != nil {
			return nil, err
		}
	}

	return &obj, nil
}

// DeleteObject deletes object metadata.
func (m *Metadata) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM objects WHERE bucket = ? AND key = ?`, bucket, key)
	return err
}

// CountObjects returns the number of objects in a bucket.
func (m *Metadata) CountObjects(ctx context.Context, bucket string) (int, error) {
	var count int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE bucket = ?`, bucket).Scan(&count)
	return count, err
}

// ListObjects returns objects matching a prefix.
func (m *Metadata) ListObjects(ctx context.Context, bucket, prefix string) ([]Object, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT key, size, last_modified, etag, content_type, metadata
		FROM objects
		WHERE bucket = ? AND key LIKE ?
		ORDER BY key
	`, bucket, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var obj Object
		var metadataStr string
		if err := rows.Scan(&obj.Key, &obj.Size, &obj.LastModified, &obj.ETag, &obj.ContentType, &metadataStr); err != nil {
			return nil, err
		}
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &obj.Metadata); err != nil {
				return nil, err
			}
		}
		objects = append(objects, obj)
	}
	return objects, rows.Err()
}

// CreateMultipartUpload creates a new multipart upload record.
func (m *Metadata) CreateMultipartUpload(ctx context.Context, upload *MultipartUpload) error {
	metadata, err := json.Marshal(upload.Metadata)
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, `
		INSERT INTO multipart_uploads (upload_id, bucket, key, content_type, metadata, initiated)
		VALUES (?, ?, ?, ?, ?, ?)
	`, upload.UploadID, upload.Bucket, upload.Key, upload.ContentType, string(metadata), upload.Initiated)
	return err
}

// GetMultipartUpload returns a multipart upload by ID.
func (m *Metadata) GetMultipartUpload(ctx context.Context, uploadID string) (*MultipartUpload, error) {
	var upload MultipartUpload
	var metadataStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT upload_id, bucket, key, content_type, metadata, initiated
		FROM multipart_uploads WHERE upload_id = ?
	`, uploadID).Scan(&upload.UploadID, &upload.Bucket, &upload.Key, &upload.ContentType, &metadataStr, &upload.Initiated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &upload.Metadata); err != nil {
			return nil, err
		}
	}

	return &upload, nil
}

// DeleteMultipartUpload deletes a multipart upload and its parts.
func (m *Metadata) DeleteMultipartUpload(ctx context.Context, uploadID string) error {
	// Parts will be deleted by cascade
	_, err := m.db.ExecContext(ctx, `DELETE FROM multipart_uploads WHERE upload_id = ?`, uploadID)
	return err
}

// PutPart stores or updates a part.
func (m *Metadata) PutPart(ctx context.Context, uploadID string, part *Part) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO parts (upload_id, part_number, size, etag, last_modified)
		VALUES (?, ?, ?, ?, ?)
	`, uploadID, part.PartNumber, part.Size, part.ETag, part.LastModified)
	return err
}

// GetPart returns a specific part.
func (m *Metadata) GetPart(ctx context.Context, uploadID string, partNumber int32) (*Part, error) {
	var part Part
	err := m.db.QueryRowContext(ctx, `
		SELECT part_number, size, etag, last_modified
		FROM parts WHERE upload_id = ? AND part_number = ?
	`, uploadID, partNumber).Scan(&part.PartNumber, &part.Size, &part.ETag, &part.LastModified)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &part, nil
}

// ListParts returns parts for a multipart upload.
func (m *Metadata) ListParts(ctx context.Context, uploadID string, maxParts int32, partNumberMarker int32) ([]Part, bool, int32, error) {
	if maxParts <= 0 {
		maxParts = 1000
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT part_number, size, etag, last_modified
		FROM parts
		WHERE upload_id = ? AND part_number > ?
		ORDER BY part_number
		LIMIT ?
	`, uploadID, partNumberMarker, maxParts+1)
	if err != nil {
		return nil, false, 0, err
	}
	defer rows.Close()

	var parts []Part
	for rows.Next() {
		var part Part
		if err := rows.Scan(&part.PartNumber, &part.Size, &part.ETag, &part.LastModified); err != nil {
			return nil, false, 0, err
		}
		parts = append(parts, part)
	}

	if err := rows.Err(); err != nil {
		return nil, false, 0, err
	}

	isTruncated := len(parts) > int(maxParts)
	var nextMarker int32
	if isTruncated {
		nextMarker = parts[maxParts-1].PartNumber
		parts = parts[:maxParts]
	}

	return parts, isTruncated, nextMarker, nil
}

// DeleteParts deletes all parts for a multipart upload.
func (m *Metadata) DeleteParts(ctx context.Context, uploadID string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM parts WHERE upload_id = ?`, uploadID)
	return err
}

// ListMultipartUploadsByBucket lists multipart uploads in a bucket with pagination.
func (m *Metadata) ListMultipartUploadsByBucket(ctx context.Context, bucket, prefix string, maxUploads int32, keyMarker, uploadIDMarker string) ([]MultipartUpload, bool, string, string, error) {
	if maxUploads <= 0 {
		maxUploads = 1000
	}

	// Build query with pagination support
	// For pagination: we need uploads > (keyMarker, uploadIDMarker)
	var rows *sql.Rows
	var err error

	if keyMarker == "" {
		// No pagination marker, just prefix filter
		rows, err = m.db.QueryContext(ctx, `
			SELECT upload_id, bucket, key, content_type, metadata, initiated
			FROM multipart_uploads
			WHERE bucket = ? AND key LIKE ?
			ORDER BY key, upload_id
			LIMIT ?
		`, bucket, prefix+"%", maxUploads+1)
	} else {
		// With pagination marker
		rows, err = m.db.QueryContext(ctx, `
			SELECT upload_id, bucket, key, content_type, metadata, initiated
			FROM multipart_uploads
			WHERE bucket = ? AND key LIKE ?
			  AND (key > ? OR (key = ? AND upload_id > ?))
			ORDER BY key, upload_id
			LIMIT ?
		`, bucket, prefix+"%", keyMarker, keyMarker, uploadIDMarker, maxUploads+1)
	}

	if err != nil {
		return nil, false, "", "", err
	}
	defer rows.Close()

	var uploads []MultipartUpload
	for rows.Next() {
		var upload MultipartUpload
		var metadataStr string
		if err := rows.Scan(&upload.UploadID, &upload.Bucket, &upload.Key, &upload.ContentType, &metadataStr, &upload.Initiated); err != nil {
			return nil, false, "", "", err
		}
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &upload.Metadata); err != nil {
				return nil, false, "", "", err
			}
		}
		uploads = append(uploads, upload)
	}

	if err := rows.Err(); err != nil {
		return nil, false, "", "", err
	}

	isTruncated := len(uploads) > int(maxUploads)
	var nextKeyMarker, nextUploadIDMarker string
	if isTruncated {
		lastUpload := uploads[maxUploads-1]
		nextKeyMarker = lastUpload.Key
		nextUploadIDMarker = lastUpload.UploadID
		uploads = uploads[:maxUploads]
	}

	return uploads, isTruncated, nextKeyMarker, nextUploadIDMarker, nil
}

// PutObjectTags stores tags for an object.
func (m *Metadata) PutObjectTags(ctx context.Context, bucket, key string, tags []Tag) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing tags
	_, err = tx.ExecContext(ctx, `DELETE FROM object_tags WHERE bucket = ? AND key = ?`, bucket, key)
	if err != nil {
		return err
	}

	// Insert new tags
	for _, tag := range tags {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO object_tags (bucket, key, tag_key, tag_value)
			VALUES (?, ?, ?, ?)
		`, bucket, key, tag.Key, tag.Value)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetObjectTags returns tags for an object.
func (m *Metadata) GetObjectTags(ctx context.Context, bucket, key string) ([]Tag, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT tag_key, tag_value FROM object_tags
		WHERE bucket = ? AND key = ?
		ORDER BY tag_key
	`, bucket, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.Key, &tag.Value); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// DeleteObjectTags deletes all tags for an object.
func (m *Metadata) DeleteObjectTags(ctx context.Context, bucket, key string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM object_tags WHERE bucket = ? AND key = ?`, bucket, key)
	return err
}

// PutBucketTags stores tags for a bucket.
func (m *Metadata) PutBucketTags(ctx context.Context, bucket string, tags []Tag) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete existing tags
	_, err = tx.ExecContext(ctx, `DELETE FROM bucket_tags WHERE bucket = ?`, bucket)
	if err != nil {
		return err
	}

	// Insert new tags
	for _, tag := range tags {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO bucket_tags (bucket, tag_key, tag_value)
			VALUES (?, ?, ?)
		`, bucket, tag.Key, tag.Value)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetBucketTags returns tags for a bucket.
func (m *Metadata) GetBucketTags(ctx context.Context, bucket string) ([]Tag, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT tag_key, tag_value FROM bucket_tags
		WHERE bucket = ?
		ORDER BY tag_key
	`, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.Key, &tag.Value); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// DeleteBucketTags deletes all tags for a bucket.
func (m *Metadata) DeleteBucketTags(ctx context.Context, bucket string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM bucket_tags WHERE bucket = ?`, bucket)
	return err
}

// PutBucketCors stores CORS configuration for a bucket.
func (m *Metadata) PutBucketCors(ctx context.Context, bucket string, corsConfig string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_cors (bucket, cors_config)
		VALUES (?, ?)
	`, bucket, corsConfig)
	return err
}

// GetBucketCors returns CORS configuration for a bucket.
func (m *Metadata) GetBucketCors(ctx context.Context, bucket string) (string, error) {
	var corsConfig string
	err := m.db.QueryRowContext(ctx, `
		SELECT cors_config FROM bucket_cors WHERE bucket = ?
	`, bucket).Scan(&corsConfig)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return corsConfig, nil
}

// DeleteBucketCors deletes CORS configuration for a bucket.
func (m *Metadata) DeleteBucketCors(ctx context.Context, bucket string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM bucket_cors WHERE bucket = ?`, bucket)
	return err
}

// PutBucketVersioning sets the versioning status for a bucket.
func (m *Metadata) PutBucketVersioning(ctx context.Context, bucket, status string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_versioning (bucket, status)
		VALUES (?, ?)
	`, bucket, status)
	return err
}

// GetBucketVersioning returns the versioning status for a bucket.
func (m *Metadata) GetBucketVersioning(ctx context.Context, bucket string) (string, error) {
	var status string
	err := m.db.QueryRowContext(ctx, `
		SELECT status FROM bucket_versioning WHERE bucket = ?
	`, bucket).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return status, nil
}

// PutObjectVersion stores a new version of an object.
func (m *Metadata) PutObjectVersion(ctx context.Context, bucket string, version *ObjectVersion) error {
	metadata, err := json.Marshal(version.Metadata)
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, `
		INSERT INTO object_versions (bucket, key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, bucket, version.Key, version.VersionID, version.Size, version.LastModified, version.ETag, version.ContentType, string(metadata), version.IsDeleteMarker)
	return err
}


// GetObjectVersion returns a specific version of an object.
func (m *Metadata) GetObjectVersion(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error) {
	var version ObjectVersion
	var metadataStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
		FROM object_versions WHERE bucket = ? AND key = ? AND version_id = ?
	`, bucket, key, versionID).Scan(&version.Key, &version.VersionID, &version.Size, &version.LastModified, &version.ETag, &version.ContentType, &metadataStr, &version.IsDeleteMarker)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &version.Metadata); err != nil {
			return nil, err
		}
	}

	return &version, nil
}

// GetLatestObjectVersion returns the latest version of an object.
func (m *Metadata) GetLatestObjectVersion(ctx context.Context, bucket, key string) (*ObjectVersion, error) {
	var version ObjectVersion
	var metadataStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
		FROM object_versions WHERE bucket = ? AND key = ?
		ORDER BY last_modified DESC LIMIT 1
	`, bucket, key).Scan(&version.Key, &version.VersionID, &version.Size, &version.LastModified, &version.ETag, &version.ContentType, &metadataStr, &version.IsDeleteMarker)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &version.Metadata); err != nil {
			return nil, err
		}
	}

	return &version, nil
}

// DeleteObjectVersion deletes a specific version of an object.
func (m *Metadata) DeleteObjectVersion(ctx context.Context, bucket, key, versionID string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM object_versions WHERE bucket = ? AND key = ? AND version_id = ?`, bucket, key, versionID)
	return err
}

// ListObjectVersions returns all versions of objects in a bucket.
func (m *Metadata) ListObjectVersions(ctx context.Context, bucket, prefix string, maxKeys int32, keyMarker, versionIDMarker string) ([]ObjectVersion, bool, string, string, error) {
	if maxKeys <= 0 {
		maxKeys = 1000
	}

	var rows *sql.Rows
	var err error

	if keyMarker == "" {
		rows, err = m.db.QueryContext(ctx, `
			SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
			FROM object_versions
			WHERE bucket = ? AND key LIKE ?
			ORDER BY key, last_modified DESC
			LIMIT ?
		`, bucket, prefix+"%", maxKeys+1)
	} else {
		rows, err = m.db.QueryContext(ctx, `
			SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
			FROM object_versions
			WHERE bucket = ? AND key LIKE ?
			  AND (key > ? OR (key = ? AND version_id > ?))
			ORDER BY key, last_modified DESC
			LIMIT ?
		`, bucket, prefix+"%", keyMarker, keyMarker, versionIDMarker, maxKeys+1)
	}

	if err != nil {
		return nil, false, "", "", err
	}
	defer rows.Close()

	var versions []ObjectVersion
	for rows.Next() {
		var version ObjectVersion
		var metadataStr string
		if err := rows.Scan(&version.Key, &version.VersionID, &version.Size, &version.LastModified, &version.ETag, &version.ContentType, &metadataStr, &version.IsDeleteMarker); err != nil {
			return nil, false, "", "", err
		}
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &version.Metadata); err != nil {
				return nil, false, "", "", err
			}
		}
		versions = append(versions, version)
	}

	if err := rows.Err(); err != nil {
		return nil, false, "", "", err
	}

	isTruncated := len(versions) > int(maxKeys)
	var nextKeyMarker, nextVersionIDMarker string
	if isTruncated {
		lastVersion := versions[maxKeys-1]
		nextKeyMarker = lastVersion.Key
		nextVersionIDMarker = lastVersion.VersionID
		versions = versions[:maxKeys]
	}

	return versions, isTruncated, nextKeyMarker, nextVersionIDMarker, nil
}

// PutBucketACL stores the ACL for a bucket.
func (m *Metadata) PutBucketACL(ctx context.Context, bucket string, acl *ACL) error {
	aclJSON, err := json.Marshal(acl)
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_acls (bucket, acl_config) VALUES (?, ?)
	`, bucket, string(aclJSON))
	return err
}

// GetBucketACL returns the ACL for a bucket.
func (m *Metadata) GetBucketACL(ctx context.Context, bucket string) (*ACL, error) {
	var aclJSON string
	err := m.db.QueryRowContext(ctx, `
		SELECT acl_config FROM bucket_acls WHERE bucket = ?
	`, bucket).Scan(&aclJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var acl ACL
	if err := json.Unmarshal([]byte(aclJSON), &acl); err != nil {
		return nil, err
	}

	return &acl, nil
}

// PutObjectACL stores the ACL for an object.
func (m *Metadata) PutObjectACL(ctx context.Context, bucket, key string, acl *ACL) error {
	aclJSON, err := json.Marshal(acl)
	if err != nil {
		return err
	}

	_, err = m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO object_acls (bucket, key, acl_config) VALUES (?, ?, ?)
	`, bucket, key, string(aclJSON))
	return err
}

// GetObjectACL returns the ACL for an object.
func (m *Metadata) GetObjectACL(ctx context.Context, bucket, key string) (*ACL, error) {
	var aclJSON string
	err := m.db.QueryRowContext(ctx, `
		SELECT acl_config FROM object_acls WHERE bucket = ? AND key = ?
	`, bucket, key).Scan(&aclJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var acl ACL
	if err := json.Unmarshal([]byte(aclJSON), &acl); err != nil {
		return nil, err
	}

	return &acl, nil
}

// PutBucketEncryption stores the encryption configuration for a bucket.
func (m *Metadata) PutBucketEncryption(ctx context.Context, bucket string, encryptionConfig string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_encryption (bucket, encryption_config)
		VALUES (?, ?)
	`, bucket, encryptionConfig)
	return err
}

// GetBucketEncryption returns the encryption configuration for a bucket.
func (m *Metadata) GetBucketEncryption(ctx context.Context, bucket string) (string, error) {
	var encryptionConfig string
	err := m.db.QueryRowContext(ctx, `
		SELECT encryption_config FROM bucket_encryption WHERE bucket = ?
	`, bucket).Scan(&encryptionConfig)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return encryptionConfig, nil
}

// DeleteBucketEncryption deletes the encryption configuration for a bucket.
func (m *Metadata) DeleteBucketEncryption(ctx context.Context, bucket string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM bucket_encryption WHERE bucket = ?`, bucket)
	return err
}

// Close closes the database connection.
func (m *Metadata) Close() error {
	return m.db.Close()
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}
