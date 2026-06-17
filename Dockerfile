# ── Build Stage ───────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Cache dependencies in a separate layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./cmd/server

# ── Runtime Stage ─────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app

# Copy binary
COPY --from=builder /app/server /app/server

# Copy web templates (server-rendered HTML)
COPY --from=builder /app/web/templates /app/web/templates

EXPOSE 8080

ENTRYPOINT ["/app/server"]
