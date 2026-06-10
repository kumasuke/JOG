package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Metadata manages object metadata using SQLite.
type Metadata struct {
	db     *sql.DB
	dbPath string
}

// NewMetadata creates a new metadata store.
func NewMetadata(dbPath string) (*Metadata, error) {
	// Ensure directory exists
	if err := ensureDir(filepath.Dir(dbPath)); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	m := &Metadata{db: db, dbPath: dbPath}
	if err := m.initialize(); err != nil {
		db.Close()
		return nil, err
	}

	return m, nil
}

// escapeLikePattern escapes '%' and '_' characters in a SQLite LIKE pattern
// so they are treated as literals rather than wildcards.  The caller must
// append the ESCAPE '\' clause to the query when using this function.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
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

	// Create multipart_uploads table. The object_lock_* columns (issue #40)
	// capture the x-amz-object-lock-* headers supplied to CreateMultipartUpload
	// so they can be applied to the version finalized at CompleteMultipartUpload.
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS multipart_uploads (
			upload_id TEXT PRIMARY KEY,
			bucket TEXT NOT NULL,
			key TEXT NOT NULL,
			content_type TEXT NOT NULL,
			metadata TEXT,
			initiated DATETIME NOT NULL,
			object_lock_mode TEXT NOT NULL DEFAULT '',
			object_lock_retain_until_date DATETIME,
			object_lock_default_days INTEGER,
			object_lock_default_years INTEGER,
			object_lock_legal_hold TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create multipart_uploads table: %w", err)
	}

	// Migrate pre-#40 multipart_uploads tables that lack the object_lock_*
	// columns. ALTER TABLE ADD COLUMN is idempotent here because we add a
	// column only when PRAGMA table_info reports it missing.
	if err := m.migrateMultipartObjectLockColumns(); err != nil {
		return err
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

	// Create bucket_lifecycle table (stores lifecycle config as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_lifecycle (
			bucket TEXT PRIMARY KEY,
			lifecycle_config TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_lifecycle table: %w", err)
	}

	// Create bucket_object_lock table (stores object lock configuration)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_object_lock (
			bucket TEXT PRIMARY KEY,
			object_lock_enabled INTEGER NOT NULL DEFAULT 0,
			object_lock_config TEXT,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_object_lock table: %w", err)
	}

	// Create the per-version Object Lock tables (v2 schema) and migrate any
	// legacy (bucket, key)-keyed rows. See migrateObjectLockSchema for the
	// fail-closed migration contract. This MUST run before any lock writes.
	if err := m.migrateObjectLockSchema(); err != nil {
		return err
	}

	// Create bucket_policy table
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_policy (
			bucket TEXT PRIMARY KEY,
			policy TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_policy table: %w", err)
	}

	// Create bucket_website table (stores website config as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_website (
			bucket TEXT PRIMARY KEY,
			website_config TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_website table: %w", err)
	}

	// Create bucket_notification table (stores notification config as JSON)
	_, err = m.db.Exec(`
		CREATE TABLE IF NOT EXISTS bucket_notification (
			bucket TEXT PRIMARY KEY,
			notification_config TEXT NOT NULL,
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create bucket_notification table: %w", err)
	}

	return nil
}

// objectLockSchemaVersion is written to PRAGMA user_version once the
// per-version Object Lock tables (v2) are in place.
const objectLockSchemaVersion = 1

// createRetentionV2DDL / createLegalHoldV2DDL are the canonical v2 DDL for the
// per-version Object Lock tables. The foreign key references buckets(name)
// (NOT objects(bucket, key)) so that INSERT OR REPLACE INTO objects no longer
// cascade-deletes lock rows on overwrite — that cascade was a silent
// Object Lock bypass (issue #39, risk 1).
const createRetentionV2DDL = `
	CREATE TABLE IF NOT EXISTS object_retention (
		bucket            TEXT NOT NULL,
		key               TEXT NOT NULL,
		version_id        TEXT NOT NULL DEFAULT '',
		mode              TEXT NOT NULL CHECK (mode IN ('GOVERNANCE', 'COMPLIANCE')),
		retain_until_date DATETIME NOT NULL,
		PRIMARY KEY (bucket, key, version_id),
		FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
	)`

const createLegalHoldV2DDL = `
	CREATE TABLE IF NOT EXISTS object_legal_hold (
		bucket     TEXT NOT NULL,
		key        TEXT NOT NULL,
		version_id TEXT NOT NULL DEFAULT '',
		status     TEXT NOT NULL CHECK (status IN ('ON', 'OFF')),
		PRIMARY KEY (bucket, key, version_id),
		FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
	)`

// migrateObjectLockSchema brings the object_retention / object_legal_hold
// tables to the per-version (bucket, key, version_id) schema. It is fail-closed:
// a fresh DB gets the v2 DDL directly, a legacy DB is migrated inside a single
// BEGIN IMMEDIATE transaction with row-count / value validation, and any
// mismatch ROLLBACKs and aborts startup (better to refuse to start than to run
// with silently-lost lock data). Old tables are renamed to *_legacy_v1 rather
// than dropped, and a physical backup is taken before migrating.
// lockTableGeneration describes the Object Lock table schema generation.
type lockTableGeneration int

const (
	lockTableAbsent lockTableGeneration = iota
	lockTableLegacy
	lockTableV2
)

func (g lockTableGeneration) String() string {
	switch g {
	case lockTableAbsent:
		return "absent"
	case lockTableLegacy:
		return "legacy"
	case lockTableV2:
		return "v2"
	default:
		return "unknown"
	}
}

func (m *Metadata) migrateObjectLockSchema() error {
	retentionGen, err := m.lockTableGeneration("object_retention")
	if err != nil {
		return err
	}
	legalHoldGen, err := m.lockTableGeneration("object_legal_hold")
	if err != nil {
		return err
	}

	switch {
	case retentionGen == lockTableAbsent && legalHoldGen == lockTableAbsent:
		if _, err := m.db.Exec(createRetentionV2DDL); err != nil {
			return fmt.Errorf("failed to create object_retention table: %w", err)
		}
		if _, err := m.db.Exec(createLegalHoldV2DDL); err != nil {
			return fmt.Errorf("failed to create object_legal_hold table: %w", err)
		}
		if _, err := m.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, objectLockSchemaVersion)); err != nil {
			return fmt.Errorf("failed to set user_version: %w", err)
		}
		return nil
	case retentionGen == lockTableV2 && legalHoldGen == lockTableV2:
		var userVersion int
		if err := m.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
			return fmt.Errorf("failed to read user_version: %w", err)
		}
		if userVersion < objectLockSchemaVersion {
			if _, err := m.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, objectLockSchemaVersion)); err != nil {
				return fmt.Errorf("failed to set user_version: %w", err)
			}
		}
		return nil
	case retentionGen == lockTableLegacy && legalHoldGen == lockTableLegacy:
		return m.runObjectLockMigration()
	default:
		return fmt.Errorf(
			"object lock schema mismatch: object_retention=%s object_legal_hold=%s (fail-closed)",
			retentionGen, legalHoldGen,
		)
	}
}

// lockTableGeneration reports whether a lock table is absent, legacy
// (no version_id column), or v2 (has version_id).
func (m *Metadata) lockTableGeneration(table string) (lockTableGeneration, error) {
	rows, err := m.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return lockTableAbsent, fmt.Errorf("failed to inspect %s columns: %w", table, err)
	}
	defer rows.Close()

	tableExists := false
	hasVersionCol := false
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &primaryKey); err != nil {
			return lockTableAbsent, fmt.Errorf("failed to scan %s column info: %w", table, err)
		}
		tableExists = true
		if name == "version_id" {
			hasVersionCol = true
		}
	}
	if err := rows.Err(); err != nil {
		return lockTableAbsent, err
	}
	if !tableExists {
		return lockTableAbsent, nil
	}
	if hasVersionCol {
		return lockTableV2, nil
	}
	return lockTableLegacy, nil
}

// migrateMultipartObjectLockColumns adds the object_lock_* columns to an
// existing multipart_uploads table created before issue #40. It is a no-op for
// fresh databases (where the columns are part of the CREATE TABLE) and for
// already-migrated databases. Each column is added only when PRAGMA table_info
// reports it missing, so the migration is idempotent.
func (m *Metadata) migrateMultipartObjectLockColumns() error {
	existing := map[string]bool{}
	rows, err := m.db.Query(`PRAGMA table_info('multipart_uploads')`)
	if err != nil {
		return fmt.Errorf("failed to inspect multipart_uploads columns: %w", err)
	}
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan multipart_uploads column info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("failed to read multipart_uploads column info: %w", err)
	}
	rows.Close()

	additions := []struct {
		name string
		ddl  string
	}{
		{"object_lock_mode", `ALTER TABLE multipart_uploads ADD COLUMN object_lock_mode TEXT NOT NULL DEFAULT ''`},
		{"object_lock_retain_until_date", `ALTER TABLE multipart_uploads ADD COLUMN object_lock_retain_until_date DATETIME`},
		{"object_lock_default_days", `ALTER TABLE multipart_uploads ADD COLUMN object_lock_default_days INTEGER`},
		{"object_lock_default_years", `ALTER TABLE multipart_uploads ADD COLUMN object_lock_default_years INTEGER`},
		{"object_lock_legal_hold", `ALTER TABLE multipart_uploads ADD COLUMN object_lock_legal_hold TEXT NOT NULL DEFAULT ''`},
	}
	for _, add := range additions {
		if existing[add.name] {
			continue
		}
		if _, err := m.db.Exec(add.ddl); err != nil {
			return fmt.Errorf("failed to add multipart_uploads.%s column: %w", add.name, err)
		}
	}
	return nil
}

// runObjectLockMigration performs the legacy -> v2 migration described in the
// issue #39 design doc. The whole migration runs in BEGIN IMMEDIATE; a physical
// VACUUM INTO backup is taken first (outside the transaction).
func (m *Metadata) runObjectLockMigration() error {
	// Step 0: physical backup before touching anything.
	if m.dbPath != "" {
		backupPath := m.dbPath + ".pre-lockv2.bak"
		_ = os.Remove(backupPath)
		if _, err := m.db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
			return fmt.Errorf("object lock migration: failed to create backup %q: %w", backupPath, err)
		}
	}

	ctx := context.Background()

	// Step 2 (pre-transaction validation): reject CHECK-violating legacy
	// rows up front so the migration never silently drops data.
	var badMode int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM object_retention WHERE mode NOT IN ('GOVERNANCE','COMPLIANCE')`).Scan(&badMode); err != nil {
		return fmt.Errorf("object lock migration: failed to validate legacy retention modes: %w", err)
	}
	if badMode != 0 {
		return fmt.Errorf("object lock migration aborted: %d legacy object_retention rows have an invalid mode; manual cleanup required", badMode)
	}
	var badStatus int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM object_legal_hold WHERE status NOT IN ('ON','OFF')`).Scan(&badStatus); err != nil {
		return fmt.Errorf("object lock migration: failed to validate legacy legal hold statuses: %w", err)
	}
	if badStatus != 0 {
		return fmt.Errorf("object lock migration aborted: %d legacy object_legal_hold rows have an invalid status; manual cleanup required", badStatus)
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("object lock migration: failed to begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Acquire an immediate write lock so any future concurrent opener blocks.
	if _, err := tx.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		// modernc/sqlite already opened an implicit deferred transaction via
		// BeginTx; a nested BEGIN may error. Fall through — the BeginTx
		// transaction already provides isolation for our single-process use.
		_ = err
	}

	// Step 3: create the v2 tables under temporary names.
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE object_retention_v2 (
			bucket            TEXT NOT NULL,
			key               TEXT NOT NULL,
			version_id        TEXT NOT NULL DEFAULT '',
			mode              TEXT NOT NULL CHECK (mode IN ('GOVERNANCE', 'COMPLIANCE')),
			retain_until_date DATETIME NOT NULL,
			PRIMARY KEY (bucket, key, version_id),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)`); err != nil {
		return fmt.Errorf("object lock migration: failed to create object_retention_v2: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE object_legal_hold_v2 (
			bucket     TEXT NOT NULL,
			key        TEXT NOT NULL,
			version_id TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL CHECK (status IN ('ON', 'OFF')),
			PRIMARY KEY (bucket, key, version_id),
			FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE
		)`); err != nil {
		return fmt.Errorf("object lock migration: failed to create object_legal_hold_v2: %w", err)
	}

	// Step 4: backfill. A legacy lock row protected "the current version" of
	// the key, so bind it to the newest non-delete-marker version_id when one
	// exists, else to '' (null version). mode / status / retain_until_date are
	// copied verbatim (zero transformation = zero loss).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_retention_v2 (bucket, key, version_id, mode, retain_until_date)
		SELECT
			r.bucket,
			r.key,
			COALESCE(
				(SELECT v.version_id
				   FROM object_versions v
				  WHERE v.bucket = r.bucket
				    AND v.key    = r.key
				    AND v.is_delete_marker = 0
				  ORDER BY v.last_modified DESC, v.version_id DESC
				  LIMIT 1),
				''
			),
			r.mode,
			r.retain_until_date
		FROM object_retention r`); err != nil {
		return fmt.Errorf("object lock migration: failed to backfill object_retention_v2: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO object_legal_hold_v2 (bucket, key, version_id, status)
		SELECT
			h.bucket,
			h.key,
			COALESCE(
				(SELECT v.version_id
				   FROM object_versions v
				  WHERE v.bucket = h.bucket
				    AND v.key    = h.key
				    AND v.is_delete_marker = 0
				  ORDER BY v.last_modified DESC, v.version_id DESC
				  LIMIT 1),
				''
			),
			h.status
		FROM object_legal_hold h`); err != nil {
		return fmt.Errorf("object lock migration: failed to backfill object_legal_hold_v2: %w", err)
	}

	// Step 5: in-transaction validation. Any mismatch aborts the migration.
	if err := validateLockMigration(ctx, tx); err != nil {
		return err
	}

	// Step 6: retire the legacy tables (rename, not drop) and promote v2.
	for _, stmt := range []string{
		`ALTER TABLE object_retention   RENAME TO object_retention_legacy_v1`,
		`ALTER TABLE object_retention_v2 RENAME TO object_retention`,
		`ALTER TABLE object_legal_hold   RENAME TO object_legal_hold_legacy_v1`,
		`ALTER TABLE object_legal_hold_v2 RENAME TO object_legal_hold`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("object lock migration: rename step failed (%q): %w", stmt, err)
		}
	}

	// Step 7: stamp the schema generation (user_version is transactional).
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, objectLockSchemaVersion)); err != nil {
		return fmt.Errorf("object lock migration: failed to set user_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("object lock migration: commit failed: %w", err)
	}
	committed = true

	// Step 8 (post-commit): the renamed schema must satisfy all FK constraints.
	var fkViolations int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fkViolations); err != nil {
		return fmt.Errorf("object lock migration: foreign_key_check failed: %w", err)
	}
	if fkViolations != 0 {
		return fmt.Errorf("object lock migration: %d foreign key violations after commit", fkViolations)
	}

	return nil
}

// validateLockMigration runs the design-doc verification queries against the
// in-flight v2 tables. It returns a descriptive error on any discrepancy so
// startup fails closed.
func validateLockMigration(ctx context.Context, tx *sql.Tx) error {
	// (a) row-count parity for retention.
	var oldN, newN int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_retention`).Scan(&oldN); err != nil {
		return fmt.Errorf("object lock migration: count old retention: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_retention_v2`).Scan(&newN); err != nil {
		return fmt.Errorf("object lock migration: count new retention: %w", err)
	}
	if oldN != newN {
		return fmt.Errorf("object lock migration: retention row count mismatch (old=%d new=%d)", oldN, newN)
	}

	// (b) no value loss / mutation for retention.
	var missing int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM object_retention r
		LEFT JOIN object_retention_v2 n
		  ON  n.bucket = r.bucket AND n.key = r.key
		  AND n.mode = r.mode AND n.retain_until_date = r.retain_until_date
		WHERE n.bucket IS NULL`).Scan(&missing); err != nil {
		return fmt.Errorf("object lock migration: retention value-loss check failed: %w", err)
	}
	if missing != 0 {
		return fmt.Errorf("object lock migration: %d retention rows lost or altered during backfill", missing)
	}

	// (c) COMPLIANCE row-count parity (the strictest invariant).
	var oldCompliance, newCompliance int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_retention WHERE mode = 'COMPLIANCE'`).Scan(&oldCompliance); err != nil {
		return fmt.Errorf("object lock migration: count old compliance: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_retention_v2 WHERE mode = 'COMPLIANCE'`).Scan(&newCompliance); err != nil {
		return fmt.Errorf("object lock migration: count new compliance: %w", err)
	}
	if oldCompliance != newCompliance {
		return fmt.Errorf("object lock migration: COMPLIANCE row count mismatch (old=%d new=%d)", oldCompliance, newCompliance)
	}

	// (d) legal hold parity + value loss.
	var oldH, newH int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_legal_hold`).Scan(&oldH); err != nil {
		return fmt.Errorf("object lock migration: count old legal hold: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM object_legal_hold_v2`).Scan(&newH); err != nil {
		return fmt.Errorf("object lock migration: count new legal hold: %w", err)
	}
	if oldH != newH {
		return fmt.Errorf("object lock migration: legal hold row count mismatch (old=%d new=%d)", oldH, newH)
	}
	var missingH int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM object_legal_hold h
		LEFT JOIN object_legal_hold_v2 n
		  ON n.bucket = h.bucket AND n.key = h.key AND n.status = h.status
		WHERE n.bucket IS NULL`).Scan(&missingH); err != nil {
		return fmt.Errorf("object lock migration: legal hold value-loss check failed: %w", err)
	}
	if missingH != 0 {
		return fmt.Errorf("object lock migration: %d legal hold rows lost or altered during backfill", missingH)
	}

	// (e) orphan check: every non-'' version_id must exist in object_versions.
	var orphans int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM object_retention_v2 n
		WHERE n.version_id <> ''
		  AND NOT EXISTS (SELECT 1 FROM object_versions v
		                   WHERE v.bucket = n.bucket AND v.key = n.key
		                     AND v.version_id = n.version_id)`).Scan(&orphans); err != nil {
		return fmt.Errorf("object lock migration: retention orphan check failed: %w", err)
	}
	if orphans != 0 {
		return fmt.Errorf("object lock migration: %d retention rows reference a non-existent version", orphans)
	}

	var holdOrphans int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM object_legal_hold_v2 n
		WHERE n.version_id <> ''
		  AND NOT EXISTS (SELECT 1 FROM object_versions v
		                   WHERE v.bucket = n.bucket AND v.key = n.key
		                     AND v.version_id = n.version_id)`).Scan(&holdOrphans); err != nil {
		return fmt.Errorf("object lock migration: legal hold orphan check failed: %w", err)
	}
	if holdOrphans != 0 {
		return fmt.Errorf("object lock migration: %d legal hold rows reference a non-existent version", holdOrphans)
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

// PutObject stores object metadata for a non-versioning in-place overwrite.
//
// With the per-version Object Lock schema (issue #39), lock rows are no longer
// cascade-deleted by an overwrite: the FK now references buckets(name) and the
// unconditional DELETE of the legacy code is gone. Instead, on a non-versioning
// overwrite we apply a ” (null version) fail-closed guard — if the live null
// version still carries an active retention or legal hold, the overwrite is
// refused (ErrObjectLocked); only an expired retention has its ” row pruned so
// the object can be replaced. This is a second防壁 behind the handler's
// evaluateObjectLock check.
//
// IMPORTANT: this guard is scoped to the genuine non-versioning overwrite path
// (FileSystem.PutObject / CopyObject / CompleteMultipartUpload). Versioned write
// paths must instead use PutObjectCurrentPointer, which updates the live objects
// row WITHOUT the guard: creating a NEW version must always be allowed and must
// leave the locked prior version intact (core S3 semantic restored by #39).
func (m *Metadata) PutObject(ctx context.Context, bucket string, obj *Object) error {
	return m.putObject(ctx, bucket, obj, true)
}

// PutObjectCurrentPointer updates the live objects row (the "current version"
// pointer) without running the null-version Object Lock guard.
//
// Versioned write paths (CopyObjectVersioned, CompleteMultipartUploadVersioned,
// PutObjectVersioned) call this after persisting the new version row: the new
// write creates a brand-new version and must always succeed regardless of any
// retention or legal hold on a prior version (including the ” null version).
// Applying the overwrite guard here would wrongly reject new-version creation
// whenever the key's null version is locked.
func (m *Metadata) PutObjectCurrentPointer(ctx context.Context, bucket string, obj *Object) error {
	return m.putObject(ctx, bucket, obj, false)
}

// putObject upserts the live objects row. When guard is true it first enforces
// the null-version Object Lock guard (non-versioning overwrite path); when false
// it skips the guard (versioned current-pointer update).
func (m *Metadata) putObject(ctx context.Context, bucket string, obj *Object, guard bool) error {
	metadata, err := json.Marshal(obj.Metadata)
	if err != nil {
		return err
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if guard {
		if err := guardNullVersionLockForOverwrite(ctx, tx, bucket, obj.Key); err != nil {
			return err
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO objects (bucket, key, size, last_modified, etag, content_type, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, bucket, obj.Key, obj.Size, obj.LastModified, obj.ETag, obj.ContentType, string(metadata))
	if err != nil {
		return err
	}

	return tx.Commit()
}

// guardNullVersionLockForOverwrite enforces the ” (null version) Object Lock
// state before a non-versioning overwrite replaces the live objects row. If an
// active retention or legal hold protects the null version, the overwrite is
// rejected; an expired retention row is pruned so the overwrite may proceed.
func guardNullVersionLockForOverwrite(ctx context.Context, tx *sql.Tx, bucket, key string) error {
	// Legal hold ON on the null version blocks the overwrite outright.
	var holdStatus string
	err := tx.QueryRowContext(ctx,
		`SELECT status FROM object_legal_hold WHERE bucket = ? AND key = ? AND version_id = ''`,
		bucket, key).Scan(&holdStatus)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && holdStatus == "ON" {
		return ErrObjectLocked
	}

	// Active retention (COMPLIANCE or GOVERNANCE) on the null version blocks
	// the overwrite. An expired retention row is removed so the object can be
	// replaced; the storage handler already evaluated bypass-governance.
	var mode string
	var retainUntil time.Time
	err = tx.QueryRowContext(ctx,
		`SELECT mode, retain_until_date FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ''`,
		bucket, key).Scan(&mode, &retainUntil)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if retainUntil.After(time.Now()) {
			return ErrObjectLocked
		}
		// Expired: prune the stale null-version retention row.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ''`,
			bucket, key); err != nil {
			return err
		}
	}
	return nil
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

// ListObjects returns objects matching a prefix with pagination support.
// startAfter specifies the key to start after (exclusive).
// maxKeys limits the number of results (0 means default 1000).
func (m *Metadata) ListObjects(ctx context.Context, bucket, prefix, startAfter string, maxKeys int32) ([]Object, error) {
	if maxKeys <= 0 {
		maxKeys = 1000
	}

	var rows *sql.Rows
	var err error

	likePrefix := escapeLikePattern(prefix) + "%"
	if startAfter != "" {
		rows, err = m.db.QueryContext(ctx, `
			SELECT key, size, last_modified, etag, content_type
			FROM objects
			WHERE bucket = ? AND key LIKE ? ESCAPE '\' AND key > ?
			ORDER BY key
			LIMIT ?
		`, bucket, likePrefix, startAfter, maxKeys+1)
	} else {
		rows, err = m.db.QueryContext(ctx, `
			SELECT key, size, last_modified, etag, content_type
			FROM objects
			WHERE bucket = ? AND key LIKE ? ESCAPE '\'
			ORDER BY key
			LIMIT ?
		`, bucket, likePrefix, maxKeys+1)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var obj Object
		if err := rows.Scan(&obj.Key, &obj.Size, &obj.LastModified, &obj.ETag, &obj.ContentType); err != nil {
			return nil, err
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
		INSERT INTO multipart_uploads (
			upload_id, bucket, key, content_type, metadata, initiated,
			object_lock_mode, object_lock_retain_until_date,
			object_lock_default_days, object_lock_default_years,
			object_lock_legal_hold
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, upload.UploadID, upload.Bucket, upload.Key, upload.ContentType, string(metadata), upload.Initiated,
		string(upload.ObjectLockMode), upload.ObjectLockRetainUntilDate,
		upload.ObjectLockDefaultDays, upload.ObjectLockDefaultYears,
		string(upload.ObjectLockLegalHold))
	return err
}

// GetMultipartUpload returns a multipart upload by ID.
func (m *Metadata) GetMultipartUpload(ctx context.Context, uploadID string) (*MultipartUpload, error) {
	var upload MultipartUpload
	var metadataStr string
	var lockMode string
	var lockRetainUntil sql.NullTime
	var lockDefaultDays, lockDefaultYears sql.NullInt32
	var lockLegalHold string
	err := m.db.QueryRowContext(ctx, `
		SELECT upload_id, bucket, key, content_type, metadata, initiated,
			object_lock_mode, object_lock_retain_until_date,
			object_lock_default_days, object_lock_default_years,
			object_lock_legal_hold
		FROM multipart_uploads WHERE upload_id = ?
	`, uploadID).Scan(
		&upload.UploadID, &upload.Bucket, &upload.Key, &upload.ContentType, &metadataStr, &upload.Initiated,
		&lockMode, &lockRetainUntil, &lockDefaultDays, &lockDefaultYears, &lockLegalHold,
	)
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

	upload.ObjectLockMode = ObjectLockRetentionMode(lockMode)
	if lockRetainUntil.Valid {
		t := lockRetainUntil.Time
		upload.ObjectLockRetainUntilDate = &t
	}
	if lockDefaultDays.Valid {
		d := lockDefaultDays.Int32
		upload.ObjectLockDefaultDays = &d
	}
	if lockDefaultYears.Valid {
		y := lockDefaultYears.Int32
		upload.ObjectLockDefaultYears = &y
	}
	upload.ObjectLockLegalHold = ObjectLegalHoldStatus(lockLegalHold)

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

	likePrefix := escapeLikePattern(prefix) + "%"
	if keyMarker == "" {
		// No pagination marker, just prefix filter
		rows, err = m.db.QueryContext(ctx, `
			SELECT upload_id, bucket, key, content_type, metadata, initiated
			FROM multipart_uploads
			WHERE bucket = ? AND key LIKE ? ESCAPE '\'
			ORDER BY key, upload_id
			LIMIT ?
		`, bucket, likePrefix, maxUploads+1)
	} else {
		// With pagination marker
		rows, err = m.db.QueryContext(ctx, `
			SELECT upload_id, bucket, key, content_type, metadata, initiated
			FROM multipart_uploads
			WHERE bucket = ? AND key LIKE ? ESCAPE '\'
			  AND (key > ? OR (key = ? AND upload_id > ?))
			ORDER BY key, upload_id
			LIMIT ?
		`, bucket, likePrefix, keyMarker, keyMarker, uploadIDMarker, maxUploads+1)
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

// GetLatestObjectVersionExcluding returns the newest version row for key,
// skipping excludeVersionID (used to preview the post-delete latest).
func (m *Metadata) GetLatestObjectVersionExcluding(ctx context.Context, bucket, key, excludeVersionID string) (*ObjectVersion, error) {
	var version ObjectVersion
	var metadataStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
		FROM object_versions WHERE bucket = ? AND key = ? AND version_id != ?
		ORDER BY last_modified DESC LIMIT 1
	`, bucket, key, excludeVersionID).Scan(&version.Key, &version.VersionID, &version.Size, &version.LastModified, &version.ETag, &version.ContentType, &metadataStr, &version.IsDeleteMarker)
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

	likePrefix := escapeLikePattern(prefix) + "%"
	if keyMarker == "" {
		rows, err = m.db.QueryContext(ctx, `
			SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
			FROM object_versions
			WHERE bucket = ? AND key LIKE ? ESCAPE '\'
			ORDER BY key, last_modified DESC
			LIMIT ?
		`, bucket, likePrefix, maxKeys+1)
	} else {
		rows, err = m.db.QueryContext(ctx, `
			SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
			FROM object_versions
			WHERE bucket = ? AND key LIKE ? ESCAPE '\'
			  AND (key > ? OR (key = ? AND version_id > ?))
			ORDER BY key, last_modified DESC
			LIMIT ?
		`, bucket, likePrefix, keyMarker, keyMarker, versionIDMarker, maxKeys+1)
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

// PutBucketLifecycle stores the lifecycle configuration for a bucket.
func (m *Metadata) PutBucketLifecycle(ctx context.Context, bucket string, lifecycleConfig string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_lifecycle (bucket, lifecycle_config)
		VALUES (?, ?)
	`, bucket, lifecycleConfig)
	return err
}

// GetBucketLifecycle returns the lifecycle configuration for a bucket.
func (m *Metadata) GetBucketLifecycle(ctx context.Context, bucket string) (string, error) {
	var lifecycleConfig string
	err := m.db.QueryRowContext(ctx, `
		SELECT lifecycle_config FROM bucket_lifecycle WHERE bucket = ?
	`, bucket).Scan(&lifecycleConfig)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return lifecycleConfig, nil
}

// DeleteBucketLifecycle deletes the lifecycle configuration for a bucket.
func (m *Metadata) DeleteBucketLifecycle(ctx context.Context, bucket string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM bucket_lifecycle WHERE bucket = ?`, bucket)
	return err
}

// SetBucketObjectLockEnabled sets the object lock enabled status for a bucket.
func (m *Metadata) SetBucketObjectLockEnabled(ctx context.Context, bucket string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_object_lock (bucket, object_lock_enabled, object_lock_config)
		VALUES (?, ?, COALESCE((SELECT object_lock_config FROM bucket_object_lock WHERE bucket = ?), NULL))
	`, bucket, enabledInt, bucket)
	return err
}

// GetBucketObjectLockEnabled returns whether object lock is enabled for a bucket.
func (m *Metadata) GetBucketObjectLockEnabled(ctx context.Context, bucket string) (bool, error) {
	var enabled int
	err := m.db.QueryRowContext(ctx, `
		SELECT object_lock_enabled FROM bucket_object_lock WHERE bucket = ?
	`, bucket).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}

// PutBucketObjectLockConfig stores the object lock configuration for a bucket.
func (m *Metadata) PutBucketObjectLockConfig(ctx context.Context, bucket string, config string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO bucket_object_lock (bucket, object_lock_enabled, object_lock_config)
		VALUES (?, 1, ?)
		ON CONFLICT(bucket) DO UPDATE SET object_lock_config = ?
	`, bucket, config, config)
	return err
}

// GetBucketObjectLockConfig returns the object lock configuration for a bucket.
func (m *Metadata) GetBucketObjectLockConfig(ctx context.Context, bucket string) (string, error) {
	var config sql.NullString
	err := m.db.QueryRowContext(ctx, `
		SELECT object_lock_config FROM bucket_object_lock WHERE bucket = ?
	`, bucket).Scan(&config)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !config.Valid {
		return "", nil
	}
	return config.String, nil
}

// ApplyObjectLockOnVersion atomically persists retention and legal hold for a
// single object version. Either field may be nil (skip). Both are written in
// one transaction so partial application cannot occur.
func (m *Metadata) ApplyObjectLockOnVersion(ctx context.Context, bucket, key, versionID string, retention *ObjectRetention, legalHold *ObjectLegalHold) error {
	if retention == nil && legalHold == nil {
		return nil
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if retention != nil && retention.RetainUntilDate != nil {
		mode := string(retention.Mode)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO object_retention (bucket, key, version_id, mode, retain_until_date)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(bucket, key, version_id) DO UPDATE SET
				mode = excluded.mode,
				retain_until_date = excluded.retain_until_date
		`, bucket, key, versionID, mode, *retention.RetainUntilDate); err != nil {
			return err
		}
	}
	if legalHold != nil {
		status := string(legalHold.Status)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO object_legal_hold (bucket, key, version_id, status)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(bucket, key, version_id) DO UPDATE SET
				status = excluded.status
		`, bucket, key, versionID, status); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetPriorObjectVersion returns the most recent object version for key excluding
// excludeVersionID. Nil when no prior version exists.
func (m *Metadata) GetPriorObjectVersion(ctx context.Context, bucket, key, excludeVersionID string) (*ObjectVersion, error) {
	var version ObjectVersion
	var metadataStr string
	err := m.db.QueryRowContext(ctx, `
		SELECT key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker
		FROM object_versions
		WHERE bucket = ? AND key = ? AND version_id != ?
		ORDER BY last_modified DESC, version_id DESC
		LIMIT 1
	`, bucket, key, excludeVersionID).Scan(
		&version.Key, &version.VersionID, &version.Size, &version.LastModified,
		&version.ETag, &version.ContentType, &metadataStr, &version.IsDeleteMarker,
	)
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

// RollbackNewObjectVersion removes a failed new version and restores the prior
// current pointer. Returns the prior version metadata for filesystem restoration.
func (m *Metadata) RollbackNewObjectVersion(ctx context.Context, bucket, key, versionID string) (*ObjectVersion, error) {
	if versionID == "" {
		return nil, fmt.Errorf("rollback requires a concrete version ID")
	}

	prior, err := m.GetPriorObjectVersion(ctx, bucket, key, versionID)
	if err != nil {
		return nil, err
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM object_legal_hold WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM object_versions WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID); err != nil {
		return nil, err
	}

	if prior != nil && !prior.IsDeleteMarker {
		metadataJSON, err := json.Marshal(prior.Metadata)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO objects (bucket, key, size, last_modified, etag, content_type, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, bucket, key, prior.Size, prior.LastModified, prior.ETag, prior.ContentType, string(metadataJSON)); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM objects WHERE bucket = ? AND key = ?`,
			bucket, key); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return prior, nil
}

// PutObjectRetention stores the retention configuration for a specific object
// version. versionID is the resolved version_id ("" means the null version).
// The write is an upsert scoped to (bucket, key, version_id) — never an
// INSERT OR REPLACE, whose internal DELETE could fire FK cascades.
func (m *Metadata) PutObjectRetention(ctx context.Context, bucket, key, versionID string, mode string, retainUntilDate time.Time) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO object_retention (bucket, key, version_id, mode, retain_until_date)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(bucket, key, version_id) DO UPDATE SET
			mode = excluded.mode,
			retain_until_date = excluded.retain_until_date
	`, bucket, key, versionID, mode, retainUntilDate)
	return err
}

// GetObjectRetention returns the retention configuration for a specific object
// version. versionID "" addresses the null version.
func (m *Metadata) GetObjectRetention(ctx context.Context, bucket, key, versionID string) (string, *time.Time, error) {
	var mode string
	var retainUntilDate time.Time
	err := m.db.QueryRowContext(ctx, `
		SELECT mode, retain_until_date FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ?
	`, bucket, key, versionID).Scan(&mode, &retainUntilDate)
	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return mode, &retainUntilDate, nil
}

// PutObjectLegalHold stores the legal hold status for a specific object version.
func (m *Metadata) PutObjectLegalHold(ctx context.Context, bucket, key, versionID string, status string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO object_legal_hold (bucket, key, version_id, status)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(bucket, key, version_id) DO UPDATE SET
			status = excluded.status
	`, bucket, key, versionID, status)
	return err
}

// GetObjectLegalHold returns the legal hold status for a specific object version.
func (m *Metadata) GetObjectLegalHold(ctx context.Context, bucket, key, versionID string) (string, error) {
	var status string
	err := m.db.QueryRowContext(ctx, `
		SELECT status FROM object_legal_hold WHERE bucket = ? AND key = ? AND version_id = ?
	`, bucket, key, versionID).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return status, nil
}

// DeleteObjectLockRows removes the retention and legal hold rows for a specific
// object version. Always fully scoped to (bucket, key, version_id); used after
// a version's data is physically deleted to avoid orphan lock rows.
func (m *Metadata) DeleteObjectLockRows(ctx context.Context, bucket, key, versionID string) error {
	if _, err := m.db.ExecContext(ctx,
		`DELETE FROM object_retention WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID); err != nil {
		return err
	}
	if _, err := m.db.ExecContext(ctx,
		`DELETE FROM object_legal_hold WHERE bucket = ? AND key = ? AND version_id = ?`,
		bucket, key, versionID); err != nil {
		return err
	}
	return nil
}

// PutBucketPolicy stores the policy for a bucket.
func (m *Metadata) PutBucketPolicy(ctx context.Context, bucket string, policy string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_policy (bucket, policy)
		VALUES (?, ?)
	`, bucket, policy)
	return err
}

// GetBucketPolicy returns the policy for a bucket.
func (m *Metadata) GetBucketPolicy(ctx context.Context, bucket string) (string, error) {
	var policy string
	err := m.db.QueryRowContext(ctx, `
		SELECT policy FROM bucket_policy WHERE bucket = ?
	`, bucket).Scan(&policy)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return policy, nil
}

// DeleteBucketPolicy deletes the policy for a bucket.
func (m *Metadata) DeleteBucketPolicy(ctx context.Context, bucket string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM bucket_policy WHERE bucket = ?`, bucket)
	return err
}

// PutBucketWebsite stores the website configuration for a bucket.
func (m *Metadata) PutBucketWebsite(ctx context.Context, bucket string, websiteConfig string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_website (bucket, website_config)
		VALUES (?, ?)
	`, bucket, websiteConfig)
	return err
}

// GetBucketWebsite returns the website configuration for a bucket.
func (m *Metadata) GetBucketWebsite(ctx context.Context, bucket string) (string, error) {
	var websiteConfig string
	err := m.db.QueryRowContext(ctx, `
		SELECT website_config FROM bucket_website WHERE bucket = ?
	`, bucket).Scan(&websiteConfig)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return websiteConfig, nil
}

// DeleteBucketWebsite deletes the website configuration for a bucket.
func (m *Metadata) DeleteBucketWebsite(ctx context.Context, bucket string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM bucket_website WHERE bucket = ?`, bucket)
	return err
}

// PutBucketNotification stores the notification configuration for a bucket.
func (m *Metadata) PutBucketNotification(ctx context.Context, bucket string, notificationConfig string) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO bucket_notification (bucket, notification_config)
		VALUES (?, ?)
	`, bucket, notificationConfig)
	return err
}

// GetBucketNotification returns the notification configuration for a bucket.
func (m *Metadata) GetBucketNotification(ctx context.Context, bucket string) (string, error) {
	var notificationConfig string
	err := m.db.QueryRowContext(ctx, `
		SELECT notification_config FROM bucket_notification WHERE bucket = ?
	`, bucket).Scan(&notificationConfig)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return notificationConfig, nil
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
