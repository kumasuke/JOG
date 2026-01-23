# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /jog ./cmd/jog

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache sqlite-libs ca-certificates

COPY --from=builder /jog /usr/local/bin/jog

VOLUME /data
EXPOSE 9000

CMD ["jog", "server"]
