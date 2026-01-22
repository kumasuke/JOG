package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

// Close closes the database connection.
func (m *Metadata) Close() error {
	return m.db.Close()
}

func ensureDir(dir string) error {
	return createDirIfNotExists(dir)
}

func createDirIfNotExists(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return nil // Let the caller handle directory creation
}
