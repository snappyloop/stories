FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build API binary
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /stories-api ./cmd/api

# Build Worker binary
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /stories-worker ./cmd/worker

# Build Webhook Dispatcher binary
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /stories-dispatcher ./cmd/dispatcher

# Build Agents binary (gRPC + MCP)
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /stories-agents ./cmd/agents

# Final stage
FROM alpine:latest

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

# Copy all binaries
COPY --from=builder /stories-api .
COPY --from=builder /stories-worker .
COPY --from=builder /stories-dispatcher .
COPY --from=builder /stories-agents .

RUN adduser -D appuser
USER appuser

EXPOSE 8080 9090 9091

# Default to API server, can be overridden
CMD ["./stories-api"]
