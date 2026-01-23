# JOG - Just Object Gateway

JOG (Just Object Gateway) is an S3-compatible object storage server written in Go.

## Features

- S3-compatible - Works with standard AWS S3 tools and SDKs
- Simple - Single binary, minimal dependencies
- High performance - Efficient I/O using Go's concurrency
- Thoroughly tested - Full test coverage using AWS SDK for Go v2

## Installation

### Download from GitHub Releases

Download the latest binary for your platform from [GitHub Releases](https://github.com/kumasuke/jog/releases).

```bash
# Linux (amd64)
curl -LO https://github.com/kumasuke/jog/releases/latest/download/jog_linux_amd64.tar.gz
tar xzf jog_linux_amd64.tar.gz
sudo mv jog /usr/local/bin/

# macOS (Apple Silicon)
curl -LO https://github.com/kumasuke/jog/releases/latest/download/jog_darwin_arm64.tar.gz
tar xzf jog_darwin_arm64.tar.gz
sudo mv jog /usr/local/bin/

# macOS (Intel)
curl -LO https://github.com/kumasuke/jog/releases/latest/download/jog_darwin_amd64.tar.gz
tar xzf jog_darwin_amd64.tar.gz
sudo mv jog /usr/local/bin/
```

### Install with Go

```bash
go install github.com/kumasuke/jog/cmd/jog@latest
```

## Quick Start

### Build

```bash
make build
```

### Run

```bash
./bin/jog server
```

The server starts on port 9000 by default.

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

### Release

To create a new release:

```bash
# 1. Update CHANGELOG.md with release notes

# 2. Commit changes
git add .
git commit -m "chore: prepare release v0.x.0"

# 3. Create and push tag
git tag v0.x.0
git push origin main --tags
```

GitHub Actions will automatically build binaries for all platforms and create a GitHub Release.

## Benchmark

Benchmark JOG against MinIO. See [benchmark/README.md](benchmark/README.md) for details.

## Documentation

- [SPEC.md](SPEC.md) - Detailed specification and architecture
- [CLAUDE.md](CLAUDE.md) - Development guidelines for Claude Code
- [TODO.md](TODO.md) - Implementation task list (TDD)
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) - Deployment guide and Litestream integration
- [docs/S3_API_CHECKLIST.md](docs/S3_API_CHECKLIST.md) - S3 API implementation status
- [benchmark/README.md](benchmark/README.md) - Benchmark execution guide
- [benchmark/CLAUDE.md](benchmark/CLAUDE.md) - Benchmark guidelines for Claude Code

## License

MIT License
