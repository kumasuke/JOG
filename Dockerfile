# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies for CGO (SQLite requires CGO)
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with CGO enabled for SQLite support
RUN CGO_ENABLED=1 go build -ldflags "-s -w" -o /jog ./cmd/jog

# Runtime stage
FROM alpine:3.21

# Install runtime dependencies
RUN apk add --no-cache sqlite-libs ca-certificates

# Install Litestream for SQLite replication
ADD https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64-static.tar.gz /tmp/litestream.tar.gz
RUN tar -xzf /tmp/litestream.tar.gz -C /usr/local/bin && rm /tmp/litestream.tar.gz

# Copy binary from builder
COPY --from=builder /jog /usr/local/bin/jog

# Copy Docker-specific configuration files
COPY docker/litestream.yml /etc/litestream.yml
COPY docker/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

# Create data directory
RUN mkdir -p /data

# Default environment variables
ENV JOG_PORT=9000
ENV JOG_DATA_DIR=/data
ENV JOG_LOG_LEVEL=info

VOLUME /data
EXPOSE 9000

ENTRYPOINT ["/entrypoint.sh"]
