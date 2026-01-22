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
- [x] Write `test/testutil/server.go` - Test server helper
- [x] Write `test/testutil/client.go` - S3 client helper

### CLI Framework
- [x] Implement `cmd/jog/main.go` - Entry point
- [x] Implement `internal/cli/root.go` - Root command
- [x] Implement `internal/cli/server.go` - Server command
- [x] Implement `internal/cli/version.go` - Version command
- [x] Implement `internal/config/config.go` - Configuration

### HTTP Server
- [x] Implement `internal/server/server.go` - HTTP server
- [x] Implement `internal/server/router.go` - S3 routing
- [x] Implement `internal/server/middleware.go` - Logging, recovery

### Bucket Operations (TDD)
- [x] **TEST**: Write `test/s3compat/bucket_test.go`
  - [x] TestCreateBucket
  - [x] TestCreateBucketAlreadyExists
  - [x] TestCreateBucketInvalidName
  - [x] TestListBuckets
  - [x] TestHeadBucket
  - [x] TestHeadBucketNotFound
  - [x] TestDeleteBucket
  - [x] TestDeleteBucketNotEmpty
- [x] **IMPL**: Implement `internal/api/bucket.go`
  - [x] CreateBucket handler
  - [x] ListBuckets handler
  - [x] HeadBucket handler
  - [x] DeleteBucket handler

### Object Operations (TDD)
- [x] **TEST**: Write `test/s3compat/object_test.go`
  - [x] TestPutObject
  - [x] TestPutObjectWithMetadata
  - [x] TestGetObject
  - [x] TestGetObjectNotFound
  - [x] TestGetObjectRange
  - [x] TestHeadObject
  - [x] TestDeleteObject
  - [x] TestListObjectsV2
  - [x] TestListObjectsV2Prefix
  - [x] TestListObjectsV2Pagination
- [x] **IMPL**: Implement `internal/api/object.go`
  - [x] PutObject handler
  - [x] GetObject handler
  - [x] HeadObject handler
  - [x] DeleteObject handler
  - [x] ListObjectsV2 handler

### Storage Backend
- [x] Implement `internal/storage/interface.go` - Storage interface
- [x] Implement `internal/storage/filesystem.go` - File system backend
- [x] Implement `internal/storage/metadata.go` - SQLite metadata

### Authentication (TDD)
- [x] **TEST**: Write `test/s3compat/auth_test.go`
  - [x] TestValidSignatureV4
  - [x] TestInvalidSignatureV4
  - [x] TestInvalidAccessKey
- [x] **IMPL**: Implement `internal/auth/signature_v4.go`

### Error Handling (TDD)
- [x] **TEST**: Write `test/s3compat/error_test.go`
  - [x] TestErrorResponseFormat
  - [x] TestErrorCodes
- [x] **IMPL**: Implement `internal/api/errors.go` - S3 error responses

---

## Phase 2: Feature Expansion

### Multipart Upload
- [x] TEST: Write multipart tests
- [x] IMPL: CreateMultipartUpload
- [x] IMPL: UploadPart
- [x] IMPL: CompleteMultipartUpload
- [x] IMPL: AbortMultipartUpload
- [x] IMPL: ListParts
- [ ] IMPL: ListMultipartUploads (未実装 - バケット内の進行中アップロード一覧取得)

### Additional Operations (Future)
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
