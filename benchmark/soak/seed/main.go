// Command seed bulk-loads a JOG data directory with backdated object versions
// for lifecycle soak testing. It writes rows directly into the metadata DB (no
// data files — the engine's noncurrent delete unlinks with ENOENT ignored), so
// seeding a million keys takes seconds instead of hours of real S3 PUTs.
//
// Because timestamps are backdated, the seeded noncurrent versions are already
// past NoncurrentDays, so the lifecycle engine deletes them as soon as it runs.
// Combined with a small max_actions_per_cycle and a short interval, that gives
// sustained, controllable deletion + scan load for a soak.
//
// The target data directory must NOT be in use by a running server while
// seeding. See benchmark/soak/seed/README.md and benchmark/docs/LIFECYCLE_SOAK.md.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/kumasuke/jog/internal/storage"
	_ "modernc.org/sqlite"
)

func main() {
	dataDir := flag.String("data-dir", "./soak-data", "JOG data directory to seed (created if missing)")
	bucket := flag.String("bucket", "soak", "bucket name")
	keys := flag.Int("keys", 100000, "number of object keys to create")
	versions := flag.Int("versions", 2, "versions per key (>=2; the newest is current, the rest are noncurrent)")
	ageDays := flag.Int("age-days", 10, "backdate timestamps this many days into the past")
	noncurrentDays := flag.Int("noncurrent-days", 1, "NoncurrentDays for the seeded NCVE rule")
	newerNoncurrent := flag.Int("newer-noncurrent", 0, "NewerNoncurrentVersions to retain (0 = expire all eligible noncurrent)")
	flag.Parse()

	if *versions < 2 {
		log.Fatal("versions must be >= 2 (need at least one current + one noncurrent)")
	}

	ctx := context.Background()
	dbPath := filepath.Join(*dataDir, "metadata.db")

	// Initialize the schema and bucket via the real storage layer.
	fs, err := storage.NewFileSystem(*dataDir, dbPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	if err := fs.CreateBucket(ctx, *bucket); err != nil {
		log.Fatalf("create bucket: %v", err)
	}
	if err := fs.PutBucketVersioning(ctx, *bucket, storage.VersioningStatusEnabled); err != nil {
		log.Fatalf("enable versioning: %v", err)
	}
	nd := int32(*noncurrentDays)
	ncve := &storage.NoncurrentVersionExpiration{NoncurrentDays: &nd}
	if *newerNoncurrent > 0 {
		nn := int32(*newerNoncurrent)
		ncve.NewerNoncurrentVersions = &nn
	}
	if err := fs.PutBucketLifecycleConfiguration(ctx, *bucket, &storage.LifecycleConfiguration{
		Rules: []storage.LifecycleRule{{
			ID:                          "soak-ncve",
			Status:                      "Enabled",
			Filter:                      &storage.LifecycleRuleFilter{Prefix: ""},
			NoncurrentVersionExpiration: ncve,
		}},
	}); err != nil {
		log.Fatalf("put lifecycle config: %v", err)
	}
	if err := fs.Close(); err != nil {
		log.Fatalf("close storage: %v", err)
	}

	// Bulk-insert version rows on a dedicated connection.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	base := time.Now().Add(-time.Duration(*ageDays) * 24 * time.Hour).UTC()
	start := time.Now()
	tx, err := db.Begin()
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO object_versions
		(bucket, key, version_id, size, last_modified, etag, content_type, metadata, is_delete_marker)
		VALUES (?, ?, ?, 1, ?, 'e', 'application/octet-stream', '', 0)`)
	if err != nil {
		log.Fatalf("prepare: %v", err)
	}
	total := 0
	for i := 0; i < *keys; i++ {
		key := fmt.Sprintf("obj-%010d", i)
		for j := 0; j < *versions; j++ {
			vid := fmt.Sprintf("%s-v%03d", key, j)
			lm := base.Add(time.Duration(j) * time.Second)
			if _, err := stmt.Exec(*bucket, key, vid, lm); err != nil {
				log.Fatalf("insert (%s,%s): %v", key, vid, err)
			}
			total++
		}
		if i > 0 && i%200000 == 0 {
			log.Printf("seeded %d keys (%d rows)...", i, total)
		}
	}
	if err := stmt.Close(); err != nil {
		log.Fatalf("stmt close: %v", err)
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}

	noncurrentPerKey := *versions - 1 - *newerNoncurrent
	if noncurrentPerKey < 0 {
		noncurrentPerKey = 0
	}
	log.Printf("done: %d keys, %d version rows, %d expected deletions in %s",
		*keys, total, *keys*noncurrentPerKey, time.Since(start).Round(time.Millisecond))
	log.Printf("data dir: %s — start JOG against it with a short JOG_LIFECYCLE_INTERVAL and a bounded JOG_LIFECYCLE_MAX_ACTIONS", *dataDir)
}
