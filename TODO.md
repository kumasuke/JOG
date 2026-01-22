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

## Phase 5: Future Enhancements

### Object Lock & Retention
- [ ] GetObjectLockConfiguration / PutObjectLockConfiguration
- [ ] GetObjectRetention / PutObjectRetention
- [ ] GetObjectLegalHold / PutObjectLegalHold

### Lifecycle Management
- [ ] GetBucketLifecycleConfiguration / PutBucketLifecycleConfiguration
- [ ] DeleteBucketLifecycle

### Encryption
- [ ] GetBucketEncryption / PutBucketEncryption
- [ ] DeleteBucketEncryption

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
