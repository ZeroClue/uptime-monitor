# Build stage
ARG CACHE_BUST=5
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /uptime-monitor ./cmd/monitor

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs openssh-client tailscale

WORKDIR /app

COPY --from=builder /uptime-monitor .
COPY --from=builder /app/internal ./internal

EXPOSE 8080

# Entrypoint script handles optional Tailscale startup
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENTRYPOINT ["/docker-entrypoint.sh"]