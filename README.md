# JOG - Just Object Gateway

[![S3 API Coverage](https://img.shields.io/badge/S3_API-66%25_covered-yellow)](docs/S3_API_CHECKLIST.md)
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

## Benchmark

Benchmark JOG against MinIO, rclone, and versitygw. See [benchmark/README.md](benchmark/README.md) for details.

For a detailed comparison with other S3-compatible servers, see [S3 Alternatives Comparison](benchmark/docs/S3_ALTERNATIVES_COMPARISON.md).

## Contributing

See [CLAUDE.md](CLAUDE.md) for development guidelines.

## License

MIT License
