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
