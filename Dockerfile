# Stage 1: Build
FROM golang:1.26.1-alpine AS builder

RUN apk add --no-cache chromium

WORKDIR /app

# کپی کل پروژه (شامل پوشه vendor)
COPY . .

# ساخت با استفاده از vendor (بدون نیاز به دانلود)
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -o tgsc ./cmd/app

# Stage 2: Runtime
FROM alpine:3.19

RUN apk add --no-cache chromium ca-certificates tzdata

WORKDIR /root/
COPY --from=builder /app/tgsc .
COPY --from=builder /app/migrations ./migrations

EXPOSE 8080
CMD ["./tgsc"]