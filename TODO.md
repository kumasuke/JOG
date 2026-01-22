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

## Phase 3: Extended Features

### GetBucketLocation
- [ ] TEST: Write GetBucketLocation tests
- [ ] IMPL: GetBucketLocation handler (return region)

### Tagging - Object
- [ ] TEST: Write object tagging tests
  - [ ] TestPutObjectTagging
  - [ ] TestGetObjectTagging
  - [ ] TestDeleteObjectTagging
  - [ ] TestPutObjectWithTagging (x-amz-tagging header)
- [ ] IMPL: PutObjectTagging handler
- [ ] IMPL: GetObjectTagging handler
- [ ] IMPL: DeleteObjectTagging handler
- [ ] IMPL: Support x-amz-tagging header in PutObject

### Tagging - Bucket
- [ ] TEST: Write bucket tagging tests
  - [ ] TestPutBucketTagging
  - [ ] TestGetBucketTagging
  - [ ] TestDeleteBucketTagging
- [ ] IMPL: PutBucketTagging handler
- [ ] IMPL: GetBucketTagging handler
- [ ] IMPL: DeleteBucketTagging handler

### CORS Configuration
- [ ] TEST: Write CORS tests
  - [ ] TestPutBucketCors
  - [ ] TestGetBucketCors
  - [ ] TestDeleteBucketCors
  - [ ] TestCorsPreflightRequest (OPTIONS)
- [ ] IMPL: PutBucketCors handler
- [ ] IMPL: GetBucketCors handler
- [ ] IMPL: DeleteBucketCors handler
- [ ] IMPL: CORS preflight handling (OPTIONS request)

### Versioning
- [ ] TEST: Write versioning tests
  - [ ] TestPutBucketVersioning (enable/suspend)
  - [ ] TestGetBucketVersioning
  - [ ] TestPutObjectVersioned
  - [ ] TestGetObjectVersioned
  - [ ] TestDeleteObjectVersioned
  - [ ] TestListObjectVersions
- [ ] IMPL: PutBucketVersioning handler
- [ ] IMPL: GetBucketVersioning handler
- [ ] IMPL: Storage backend versioning support
- [ ] IMPL: ListObjectVersions handler
- [ ] IMPL: Version-aware Get/Put/Delete operations

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
