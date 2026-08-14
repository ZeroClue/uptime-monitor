# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /uptime-monitor ./cmd/monitor

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs openssh-client

WORKDIR /app

COPY --from=builder /uptime-monitor .

EXPOSE 8080

ENTRYPOINT ["/app/uptime-monitor"]