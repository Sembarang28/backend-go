# Syntax: Multi-stage hardened production Dockerfile
# Stage 1: Build
FROM golang:1.26.1-alpine AS builder

RUN apk --no-cache add ca-certificates git tzdata

WORKDIR /src

# Leverage Docker cache layers for dependencies
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy application source
COPY . .

# Build statically linked binary with stripped symbols
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
go build -trimpath -ldflags="-w -s" -o /bin/api ./cmd/api && \
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
go build -trimpath -ldflags="-w -s" -o /bin/migrate ./cmd/migrate

# Stage 2: Distroless/Minimal Hardened Runtime
FROM alpine:3.20

# Create unprivileged application user
RUN addgroup -S -g 10001 appgroup && \
    adduser -S -u 10001 -G appgroup -h /home/appuser -s /sbin/nologin appuser

# Copy SSL certificates, timezone data and compiled binary
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder --chown=10001:10001 /bin/api /app/api
COPY --from=builder --chown=10001:10001 /bin/migrate /app/migrate

WORKDIR /app
USER 10001:10001

EXPOSE 8080 50051 9090

ENTRYPOINT ["/app/api"]
