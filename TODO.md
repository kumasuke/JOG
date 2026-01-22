# JOG Implementation Tasks (TDD)

## Development Workflow

Each feature follows TDD (Test-Driven Development):

1. **Write test** → `test: add tests for <feature>`
2. **Run test** → Confirm it fails (Red)
3. **Implement** → `feat: implement <feature>`
4. **Run test** → Confirm it passes (Green)
5. **Refactor** → `refactor: <description>` (if needed)

---

## Phase 1: MVP

### Infrastructure Setup
- [x] Create CLAUDE.md
- [x] Create go.mod
- [x] Create project directory structure
- [x] Create Makefile
- [x] Create .gitignore
- [x] Create GitHub Actions workflow

### Test Infrastructure
- [ ] Write `test/testutil/server.go` - Test server helper
- [ ] Write `test/testutil/client.go` - S3 client helper

### CLI Framework
- [ ] Implement `cmd/jog/main.go` - Entry point
- [ ] Implement `internal/cli/root.go` - Root command
- [ ] Implement `internal/cli/server.go` - Server command
- [ ] Implement `internal/cli/version.go` - Version command
- [ ] Implement `internal/config/config.go` - Configuration

### HTTP Server
- [ ] Implement `internal/server/server.go` - HTTP server
- [ ] Implement `internal/server/router.go` - S3 routing
- [ ] Implement `internal/server/middleware.go` - Logging, recovery

### Bucket Operations (TDD)
- [ ] **TEST**: Write `test/s3compat/bucket_test.go`
  - [ ] TestCreateBucket
  - [ ] TestCreateBucketAlreadyExists
  - [ ] TestCreateBucketInvalidName
  - [ ] TestListBuckets
  - [ ] TestHeadBucket
  - [ ] TestHeadBucketNotFound
  - [ ] TestDeleteBucket
  - [ ] TestDeleteBucketNotEmpty
- [ ] **IMPL**: Implement `internal/api/bucket.go`
  - [ ] CreateBucket handler
  - [ ] ListBuckets handler
  - [ ] HeadBucket handler
  - [ ] DeleteBucket handler

### Object Operations (TDD)
- [ ] **TEST**: Write `test/s3compat/object_test.go`
  - [ ] TestPutObject
  - [ ] TestPutObjectWithMetadata
  - [ ] TestGetObject
  - [ ] TestGetObjectNotFound
  - [ ] TestGetObjectRange
  - [ ] TestHeadObject
  - [ ] TestDeleteObject
  - [ ] TestListObjectsV2
  - [ ] TestListObjectsV2Prefix
  - [ ] TestListObjectsV2Pagination
- [ ] **IMPL**: Implement `internal/api/object.go`
  - [ ] PutObject handler
  - [ ] GetObject handler
  - [ ] HeadObject handler
  - [ ] DeleteObject handler
  - [ ] ListObjectsV2 handler

### Storage Backend
- [ ] Implement `internal/storage/interface.go` - Storage interface
- [ ] Implement `internal/storage/filesystem.go` - File system backend
- [ ] Implement `internal/storage/metadata.go` - SQLite metadata

### Authentication (TDD)
- [ ] **TEST**: Write `test/s3compat/auth_test.go`
  - [ ] TestValidSignatureV4
  - [ ] TestInvalidSignatureV4
  - [ ] TestExpiredSignature
- [ ] **IMPL**: Implement `internal/auth/signature_v4.go`

### Error Handling (TDD)
- [ ] **TEST**: Write `test/s3compat/error_test.go`
  - [ ] TestErrorResponseFormat
  - [ ] TestErrorCodes
- [ ] **IMPL**: Implement `internal/api/errors.go` - S3 error responses

---

## Phase 2: Feature Expansion (Future)

### Multipart Upload
- [ ] TEST: Write multipart tests
- [ ] IMPL: CreateMultipartUpload
- [ ] IMPL: UploadPart
- [ ] IMPL: CompleteMultipartUpload
- [ ] IMPL: AbortMultipartUpload
- [ ] IMPL: ListParts

### Additional Operations
- [ ] CopyObject
- [ ] DeleteObjects (batch)
- [ ] GetObjectAttributes

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
