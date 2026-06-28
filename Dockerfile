# Stage 1: Build
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o scraper-sync ./cmd/app

# Stage 2: Runtime
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy binary and migrations
COPY --from=builder /app/scraper-sync .
COPY --from=builder /app/migrations ./migrations

# Expose health check port
EXPOSE 8080

# Run the application
CMD ["./scraper-sync"]