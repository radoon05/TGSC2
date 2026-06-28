# Scraper + WooCommerce Sync Robot

A production-grade scraper that syncs products from an external source to WooCommerce using a pipeline + state machine architecture.

## Features

- Scrapes products from external API with rate limiting, retry, and pagination.
- Normalizes price, stock, and title with pure business logic.
- Change detection via fingerprint hashing (NEW / DIRTY / UNCHANGED).
- State machine for sync jobs (PENDING → RUNNING → SUCCESS/FAILED/DEAD_LETTER).
- Batch create/update to WooCommerce with partial success handling.
- Worker pool for concurrent processing.
- PostgreSQL as queue and persistent storage.
- Prometheus metrics endpoint.
- Graceful shutdown.

## Architecture

See [Phase 0 design](docs/architecture.md) – Pipeline + Queue + State Machine.

## Quick Start

### Prerequisites

- Go 1.21+
- PostgreSQL
- (Optional) Docker & Docker Compose

### Using Docker Compose

```bash
cp .env.example .env
# edit .env with your WooCommerce keys and scraper URL
docker-compose up -d