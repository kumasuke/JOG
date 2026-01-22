# CLAUDE.md - JOG Project Guidelines

## Project Overview

JOG (Just Object Gateway) is an S3-compatible object storage server written in Go.

## Development Approach: TDD (Test-Driven Development)

**Always follow the Red-Green-Refactor cycle:**

1. **Red**: Write a failing test first (using AWS SDK for Go v2)
2. **Green**: Write minimal code to make the test pass
3. **Refactor**: Clean up the code while keeping tests green

## Build Commands

```bash
# Build the binary
make build

# Run all tests
make test

# Run S3 compatibility tests only
make test-s3compat

# Run linter
make lint

# Clean build artifacts
make clean
```

## Test Commands

```bash
# Run unit tests
go test ./internal/...

# Run S3 compatibility tests
go test -v ./test/s3compat/...

# Run specific test
go test -v ./test/s3compat/... -run TestCreateBucket

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Project Structure

```
cmd/jog/main.go          # Entry point
internal/
  cli/                   # CLI commands (cobra)
  server/                # HTTP server
  api/                   # S3 API handlers
  auth/                  # AWS Signature V4
  storage/               # Storage backend
  config/                # Configuration
test/
  s3compat/              # S3 compatibility tests (AWS SDK)
  testutil/              # Test utilities
```

## Code Style

- Follow standard Go conventions (gofmt, golint)
- Use meaningful variable names
- Keep functions small and focused
- Error messages should be descriptive
- Use structured logging (zerolog)

## S3 API Implementation Notes

### Response Format
- Success responses: XML format matching AWS S3
- Error responses: S3 XML error format with appropriate error codes

### Error Codes (must match S3)
- `NoSuchBucket` - Bucket does not exist
- `NoSuchKey` - Object does not exist
- `BucketAlreadyExists` - Bucket already exists
- `BucketNotEmpty` - Cannot delete non-empty bucket
- `InvalidBucketName` - Invalid bucket name
- `AccessDenied` - Authentication failed
- `SignatureDoesNotMatch` - Invalid signature

### HTTP Status Codes
- 200: Success (GET, HEAD)
- 204: Success with no content (DELETE)
- 400: Bad Request
- 403: Forbidden (auth errors)
- 404: Not Found
- 409: Conflict (bucket exists, bucket not empty)
- 500: Internal Server Error

## Testing Strategy

### S3 Compatibility Tests
All tests use AWS SDK for Go v2 to ensure real S3 compatibility:

```go
// Example test pattern
func TestCreateBucket(t *testing.T) {
    ts := testutil.NewTestServer(t)
    defer ts.Cleanup()

    client := ts.S3Client(t)

    _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
        Bucket: aws.String("test-bucket"),
    })
    require.NoError(t, err)
}
```

### Test File Naming
- `bucket_test.go` - Bucket operation tests
- `object_test.go` - Object operation tests
- `auth_test.go` - Authentication tests
- `error_test.go` - Error response tests

## Configuration

Environment variables:
- `JOG_PORT` - Server port (default: 9000)
- `JOG_DATA_DIR` - Data directory (default: ./data)
- `JOG_ACCESS_KEY` - Access key (default: minioadmin)
- `JOG_SECRET_KEY` - Secret key (default: minioadmin)
- `JOG_LOG_LEVEL` - Log level (default: info)

## Git Workflow

- Write tests first, commit with message: `test: add tests for <feature>`
- Implement feature, commit with message: `feat: implement <feature>`
- Bug fixes: `fix: <description>`
- Refactoring: `refactor: <description>`

## Implementation Order (TDD)

1. Write `test/testutil/server.go` - Test infrastructure
2. Write `test/s3compat/bucket_test.go` - Bucket tests (will fail)
3. Implement bucket operations until tests pass
4. Write `test/s3compat/object_test.go` - Object tests (will fail)
5. Implement object operations until tests pass
6. Continue for each feature...

## Dependencies

Main:
- `github.com/spf13/cobra` - CLI
- `github.com/spf13/viper` - Config
- `github.com/rs/zerolog` - Logging
- `github.com/google/uuid` - UUID
- `github.com/mattn/go-sqlite3` - Metadata DB

Test:
- `github.com/aws/aws-sdk-go-v2` - S3 client for testing
- `github.com/stretchr/testify` - Assertions
