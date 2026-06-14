# JOG Implementation Tasks (TDD)

## Development Workflow

Each feature follows TDD (Test-Driven Development):

1. **Write test** → `test: add tests for <feature>`
2. **Run test** → Confirm it fails (Red)
3. **Implement** → `feat: implement <feature>`
4. **Run test** → Confirm it passes (Green)
5. **Refactor** → `refactor: <description>` (if needed)

---

## Completed Phases

### Phase 1: MVP ✅

**Infrastructure & CLI**
- Project setup (go.mod, Makefile, .gitignore, GitHub Actions)
- CLI framework (cobra): root, server, version commands
- HTTP server with S3 routing and middleware

**Core S3 Operations**
- Bucket: CreateBucket, ListBuckets, HeadBucket, DeleteBucket
- Object: PutObject, GetObject, HeadObject, DeleteObject, ListObjectsV2
- Storage: Filesystem backend with SQLite metadata
- Auth: AWS Signature V4
- Error handling: S3-compatible XML error responses

### Phase 2: Feature Expansion ✅

**Multipart Upload**
- CreateMultipartUpload, UploadPart, UploadPartCopy
- CompleteMultipartUpload, AbortMultipartUpload
- ListParts, ListMultipartUploads

**Additional Operations**
- CopyObject, DeleteObjects (batch), GetObjectAttributes

---

### Phase 3: Extended Features ✅

**GetBucketLocation**
- GetBucketLocation handler (returns region)

**Tagging - Object**
- PutObjectTagging, GetObjectTagging, DeleteObjectTagging
- Support x-amz-tagging header in PutObject

**Tagging - Bucket**
- PutBucketTagging, GetBucketTagging, DeleteBucketTagging

**CORS Configuration**
- PutBucketCors, GetBucketCors, DeleteBucketCors
- CORS preflight handling (OPTIONS request)

**Versioning**
- PutBucketVersioning, GetBucketVersioning
- ListObjectVersions
- Version-aware Get/Put/Delete operations
- Delete markers support

---

### Phase 4: ACL (Access Control Lists) ✅

**Bucket ACL**
- GetBucketAcl, PutBucketAcl
- Support for canned ACLs (private, public-read, etc.)

**Object ACL**
- GetObjectAcl, PutObjectAcl
- Support for x-amz-acl header in PutObject

---

### Phase 5: Encryption & Lifecycle ✅

**Encryption**
- GetBucketEncryption, PutBucketEncryption, DeleteBucketEncryption
- Support for AES256 and KMS encryption algorithms
- BucketKeyEnabled option

**Lifecycle Management**
- GetBucketLifecycleConfiguration, PutBucketLifecycleConfiguration, DeleteBucketLifecycle
- Support for expiration rules with Days/Date
- Storage class transitions
- Noncurrent version expiration
- Abort incomplete multipart upload
- Filter by prefix, tag, or object size
- **Lifecycle execution engine** (`internal/lifecycle`): rules are now actually
  executed (previously CRUD-only). In-server ticker + `jog lifecycle run
  [--dry-run]` CLI. Expiration (delete marker on versioned / physical delete on
  non-versioned), NoncurrentVersionExpiration, ExpiredObjectDeleteMarker, and
  AbortIncompleteMultipartUpload. Fail-closed against retention / legal hold via
  guarded `BEGIN IMMEDIATE` transactions. Transition is a no-op on single-node.
- **Overlapping lifecycle rules (NCVE / AIMU) merge evaluation** (#53): when
  multiple NoncurrentVersionExpiration or AbortIncompleteMultipartUpload rules
  overlap the same key/upload, the engine now evaluates each rule independently
  and unions the verdicts (a version/upload is acted on when ANY rule applies),
  matching S3's shortest-expiration semantics — replacing the prior first-match
  behavior. (Current-object `Expiration` overlap is still first-match; tracked
  separately.)
- **EODM validation at PUT time** (#55): `PutBucketLifecycleConfiguration`
  rejects a rule combining `ExpiredObjectDeleteMarker` with
  `Expiration.Days` / `Expiration.Date` (400 `InvalidRequest`).

**Request Limits & Hardening**
- **XML / object body size limits** (#34): all XML subresource endpoints plus
  `DeleteObjects` / `CompleteMultipartUpload` cap the request body (AWS-aligned
  sizes) and return 413 `EntityTooLarge`; object data capped at 5 GiB per PUT /
  UploadPart.
- **Delete-path contention** (#54): write-contended deletes return 503
  `SlowDown` for client backoff/retry instead of a misleading 204/500.

---

### Phase 6: Object Lock & Retention ✅

**Object Lock Configuration**
- GetObjectLockConfiguration, PutObjectLockConfiguration
- Support for ObjectLockEnabled flag at bucket creation
- Default retention rules (GOVERNANCE/COMPLIANCE mode with Days/Years)

**Object Retention**
- PutObjectRetention, GetObjectRetention
- Support for GOVERNANCE and COMPLIANCE modes
- RetainUntilDate configuration

**Object Legal Hold**
- PutObjectLegalHold, GetObjectLegalHold
- Support for ON/OFF status

---

### Phase 7: Additional Compatibility ✅

**Legacy Compatibility**
- ListObjects (v1) - Legacy list objects API for older tools/SDKs

**Bucket Policy**
- GetBucketPolicy, PutBucketPolicy, DeleteBucketPolicy

**Website Hosting**
- GetBucketWebsite, PutBucketWebsite, DeleteBucketWebsite

---

## Phase 8: Future Enhancements (Optional)

### Not Prioritized
- [x] Bucket Notification configuration API (GetBucketNotification / PutBucketNotification)
- [x] Bucket Notification event delivery (Webhook / HTTP POST) — lifecycle expiration events `s3:LifecycleExpiration:Delete` / `:DeleteMarkerCreated` (#56)
- [ ] Bucket Notification event delivery (SNS/SQS/Lambda/EventBridge)
- [x] Object Select (SelectObjectContent) — CSV/JSON 入力、射影+WHERE+LIMIT、eventstream 出力。Parquet・gzip等圧縮・集約関数は未対応

---

## Quick Reference

```bash
# Run tests (should fail initially - Red)
make test-s3compat

# After implementation (should pass - Green)
make test-s3compat

# Build
make build

# Run server
make run
```
