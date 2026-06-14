# JOG - Just Object Gateway

[![S3 API Coverage](https://img.shields.io/badge/S3_API-68%25_covered-yellow)](docs/S3_API_CHECKLIST.md)
[![GitHub Downloads](https://img.shields.io/github/downloads/kumasuke/JOG/total?color=blue)](https://github.com/kumasuke/JOG/releases)

A fast, lightweight S3-compatible object storage server written in Go.
Fully developed with AI assistance using Claude Code.

## Features

- **High performance** - Built with Go for speed and efficient concurrency
- **S3-compatible** - Works with AWS CLI, SDKs, and existing S3 tools
- **Single binary** - No external dependencies, easy deployment
- **AI-friendly** - Includes [CLAUDE.md](CLAUDE.md) for seamless AI-assisted development
- **Well tested** - Comprehensive test coverage using AWS SDK for Go v2

See [Supported S3 APIs](docs/S3_API_CHECKLIST.md) for the full list of implemented operations.

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

### Docker

```bash
docker run -p 9000:9000 ghcr.io/kumasuke/jog:latest
```

With persistent storage and custom credentials:

```bash
docker run -p 9000:9000 \
  -v jog-data:/data \
  -e JOG_AUTH_ACCESS_KEY=myaccesskey \
  -e JOG_AUTH_SECRET_KEY=mysecretkey \
  ghcr.io/kumasuke/jog:latest
```

To enable automatic metadata backup via Litestream:

```bash
docker run -p 9000:9000 \
  -v jog-data:/data \
  -e LITESTREAM_REPLICA_URL=s3://my-backup-bucket/jog \
  -e AWS_ACCESS_KEY_ID=... \
  -e AWS_SECRET_ACCESS_KEY=... \
  ghcr.io/kumasuke/jog:latest
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

- `JOG_SERVER_PORT` - Server port (default: 9000)
- `JOG_SERVER_ADDRESS` - Listen address (default: 0.0.0.0)
- `JOG_STORAGE_DATA_DIR` - Data directory (default: ./data)
- `JOG_STORAGE_METADATA_DB` - Metadata DB path (default: ./data/metadata.db)
- `JOG_AUTH_ACCESS_KEY` - Access key (default: minioadmin)
- `JOG_AUTH_SECRET_KEY` - Secret key (default: minioadmin)
- `JOG_LOGGING_LEVEL` - Log level (default: info)
- `JOG_LOGGING_FORMAT` - Log format: json or console (default: json)
- `JOG_LIFECYCLE_ENABLED` - Run the lifecycle engine (default: true)
- `JOG_LIFECYCLE_INTERVAL` - Lifecycle cycle interval, Go duration (default: 1h)
- `JOG_LIFECYCLE_MAX_ACTIONS` - Max actions per cycle (default: 10000)
- `JOG_NOTIFICATION_REGION` - `awsRegion` stamped into notification events (default: us-east-1)
- `JOG_NOTIFICATION_DELIVERY_TIMEOUT` - Per-webhook delivery timeout, Go duration (default: 10s)

Webhook delivery targets (ARN → URL) are configured only via `notification.targets` in `config.yaml`; lifecycle expiration events (`s3:LifecycleExpiration:Delete` / `:DeleteMarkerCreated`) are POSTed to the matching URL when at least one target is set.

### Lifecycle engine

JOG executes bucket lifecycle rules with a background engine (in-server ticker,
first run ~1 minute after startup). You can also drive it manually:

```bash
# Preview what a cycle would do without changing anything
jog lifecycle run --dry-run

# Run one cycle (optionally scoped to a single bucket)
jog lifecycle run --bucket my-bucket
```

Versions under COMPLIANCE/GOVERNANCE retention or legal hold are never deleted
(fail-closed). Set `JOG_LIFECYCLE_ENABLED=false` to disable the engine.

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

## Benchmark

Benchmark JOG against MinIO, rclone, and versitygw. See [benchmark/README.md](benchmark/README.md) for details.

For a detailed comparison with other S3-compatible servers, see [S3 Alternatives Comparison](benchmark/docs/S3_ALTERNATIVES_COMPARISON.md).

## Contributing

See [CLAUDE.md](CLAUDE.md) for development guidelines.

## License

MIT License
