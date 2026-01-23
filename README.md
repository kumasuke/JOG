# JOG - Just Object Gateway

JOG (Just Object Gateway) is an S3-compatible object storage server written in Go.

## Features

- S3 API compatibility - Compatible with AWS S3 major APIs
- Simple - Single binary, minimal dependencies
- High performance - Efficient I/O using Go's concurrency
- TDD approach - Test-Driven Development with AWS SDK for Go v2

## Quick Start

### Build

```bash
make build
```

### Run

```bash
./bin/jog server
```

The server will start on port 9000 by default.

### Configuration

Environment variables:

- `JOG_PORT` - Server port (default: 9000)
- `JOG_DATA_DIR` - Data directory (default: ./data)
- `JOG_ACCESS_KEY` - Access key (default: minioadmin)
- `JOG_SECRET_KEY` - Secret key (default: minioadmin)
- `JOG_LOG_LEVEL` - Log level (default: info)

### Docker Compose

```bash
# Start with Docker Compose
docker compose up -d

# Check logs
docker compose logs -f

# Stop
docker compose down
```

### Usage with AWS CLI

```bash
export AWS_ENDPOINT_URL=http://localhost:9000
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin

# Create bucket
aws s3 mb s3://my-bucket

# Upload file
aws s3 cp file.txt s3://my-bucket/

# List objects
aws s3 ls s3://my-bucket/

# Download file
aws s3 cp s3://my-bucket/file.txt ./downloaded.txt
```

## Development

This project follows TDD (Test-Driven Development):

1. Write tests first using AWS SDK for Go v2
2. Run tests (should fail - Red)
3. Implement features
4. Run tests again (should pass - Green)
5. Refactor

### Test

```bash
# Run all tests
make test

# Run S3 compatibility tests only
make test-s3compat

# Run specific test
go test -v ./test/s3compat/... -run TestCreateBucket
```

### Lint

```bash
make lint
```

## Benchmark

JOGとMinIOのパフォーマンス比較ができます。

```bash
cd benchmark
```

`benchmark` ディレクトリに移動後、[benchmark/README.md](benchmark/README.md) および [benchmark/CLAUDE.md](benchmark/CLAUDE.md) を参照してください。

## Documentation

- [SPEC.md](SPEC.md) - Detailed specification and architecture
- [CLAUDE.md](CLAUDE.md) - Development guidelines for Claude Code
- [TODO.md](TODO.md) - Implementation task list (TDD)
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) - Deployment guide and Litestream integration
- [docs/S3_API_CHECKLIST.md](docs/S3_API_CHECKLIST.md) - S3 API implementation status
- [benchmark/README.md](benchmark/README.md) - Benchmark execution guide

## License

Apache License 2.0
