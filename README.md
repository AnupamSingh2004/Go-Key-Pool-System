# Key Pool System

A production-grade **API Key Pooling System** built in Go. It manages multiple API keys, distributes outbound requests across them using pluggable strategies, and handles failures automatically with circuit breakers, retries, and priority queuing.

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐     ┌──────────────┐
│  HTTP API   │────▶│ Priority     │────▶│  Worker Pool    │────▶│  External    │
│  (net/http) │     │ Queue        │     │  (N goroutines) │     │  APIs        │
└─────────────┘     └──────────────┘     └────────┬────────┘     └──────────────┘
                                                  │
                                         ┌────────▼────────┐
                                         │  Key Pool Mgr   │
                                         │  ┌────────────┐  │
                                         │  │ Strategy   │  │
                                         │  │ (RR / WRR) │  │
                                         │  └────────────┘  │
                                         │  ┌────────────┐  │
                                         │  │ Circuit    │  │
                                         │  │ Breakers   │  │
                                         │  └────────────┘  │
                                         └────────┬────────┘
                                                  │
                                         ┌────────▼────────┐
                                         │  SQLite + AES   │
                                         │  Encrypted Keys │
                                         └─────────────────┘
```

## Features

- **Key Selection Strategies** — Round Robin and Smooth Weighted Round Robin
- **Circuit Breaker** — Per-key state machine (Closed → Open → Half-Open) with configurable thresholds
- **Priority Queue** — 3-level priority (High/Normal/Low) with preemption support
- **Retry with Backoff** — Exponential backoff with jitter, configurable max attempts
- **AES-256-GCM Encryption** — API keys encrypted at rest in SQLite
- **Rate Limiting** — Per-key per-minute and per-day limits with concurrent request tracking
- **Load Shedding** — Automatic rejection of low-priority requests when healthy keys drop below thresholds
- **Hot Reload** — Runtime config changes via DB without restart
- **Idempotency** — Duplicate request detection via idempotency keys
- **Prometheus Metrics** — Counters, gauges, and latency tracking at `/metrics`
- **Graceful Shutdown** — SIGINT/SIGTERM handling with in-flight request draining
- **Worker Warmup** — Staggered start to prevent thundering herd on restart

## Project Structure

```
.
├── cmd/
│   └── main.go                    # Entrypoint — wires all components
├── internal/
│   ├── api/
│   │   ├── handlers.go            # HTTP endpoint handlers
│   │   ├── helpers.go             # JSON response/decode utilities
│   │   ├── middleware.go          # AdminAuth, RequestLogger
│   │   └── router.go             # Route registration
│   ├── config/
│   │   ├── config.go             # Config struct + Load() from env
│   │   ├── helpers.go            # getEnv*, type conversion helpers
│   │   ├── reload.go             # HotReloadConfig with RWMutex
│   │   └── validators.go         # Validation rules
│   ├── crypto/
│   │   └── crypto.go             # AES-256-GCM Encrypt/Decrypt
│   ├── db/
│   │   ├── adapter.go            # DBAdapter interface + domain types
│   │   ├── migrations.go         # SQL migration runner
│   │   ├── sqlite.go             # SQLite connection setup
│   │   ├── sqlite_config.go      # Key events + system config queries
│   │   ├── sqlite_keys.go        # API key CRUD
│   │   └── sqlite_requests.go    # Request CRUD
│   ├── httpclient/
│   │   └── client.go             # HTTP client wrapper
│   ├── keypool/
│   │   ├── circuitbreaker.go     # Per-key circuit breaker
│   │   ├── manager.go            # Key pool manager
│   │   ├── roundrobin.go         # Round Robin strategy
│   │   ├── strategy.go           # Strategy interface
│   │   ├── types.go              # PoolKey with rate limit + concurrency
│   │   └── weightedrr.go         # Smooth Weighted Round Robin
│   ├── metrics/
│   │   └── metrics.go            # Prometheus text-format metrics
│   ├── queue/
│   │   ├── priority_queue.go     # container/heap min-heap
│   │   ├── queue.go              # Thread-safe queue with preemption
│   │   └── types.go              # Queue item struct
│   ├── util/
│   │   ├── context.go            # DB context helpers
│   │   └── json.go               # JSON parsing utility
│   └── worker/
│       ├── pool.go               # Worker pool with staggered warmup
│       ├── retry.go              # Backoff calculation + retry logic
│       └── worker.go             # Single worker processing loop
├── migrations/
│   ├── 001_create_api_keys.sql
│   ├── 002_create_requests.sql
│   ├── 003_create_key_events.sql
│   ├── 004_create_system_config.sql
│   └── 005_seed_system_config.sql
├── .env.example                   # All config vars with documentation
├── .gitignore
├── Dockerfile                     # Multi-stage build
├── docker-compose.yml
├── go.mod
└── go.sum
```

## Prerequisites

- **Go 1.22+**
- **GCC** (CGO required for SQLite)
- **Docker & Docker Compose** (for containerized deployment)

## Quick Start

### 1. Clone the repository

```bash
git clone https://github.com/AnupamSingh2004/Go-Key-Pool-System.git
cd Go-Key-Pool-System
```

### 2. Configure environment

```bash
cp .env.example .env
```

Generate the required secrets:

```bash
# Generate encryption key (32-byte hex = 64 characters)
openssl rand -hex 32

# Generate admin token
openssl rand -hex 32
```

Edit `.env` and set:
```
ENCRYPTION_KEY=<your-64-char-hex-key>
ADMIN_TOKEN=<your-admin-token>
```

### 3. Run with Docker Compose

```bash
docker compose up --build
```

The server starts at `http://localhost:8080`.

### 4. Run locally (without Docker)

```bash
# Install dependencies
sudo apt-get install -y gcc libsqlite3-dev   # Debian/Ubuntu
# or: brew install sqlite3                    # macOS

# Create data directory
mkdir -p data

# Build and run
CGO_ENABLED=1 go build -o key-pool-system ./cmd/
./key-pool-system
```

## API Reference

### Public Endpoints

#### Health Check
```
GET /health
```
```json
{"status": "ok"}
```

#### Submit a Request
```
POST /api/requests
Content-Type: application/json

{
  "method": "GET",
  "url": "https://api.example.com/data",
  "priority": 1,
  "source": "manual",
  "idempotency_key": "unique-key-123",
  "headers": {"X-Custom": "value"},
  "payload": "{\"query\": \"test\"}"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `method` | string | Yes | HTTP method (GET, POST, PUT, DELETE, etc.) |
| `url` | string | Yes | Destination URL to call |
| `priority` | int | No | 1=High, 2=Normal (default), 3=Low |
| `source` | string | No | Request origin: `manual`, `cron`, `code_snippet` |
| `idempotency_key` | string | No | Prevents duplicate processing |
| `headers` | object | No | Custom headers to forward |
| `payload` | string | No | Request body to forward |

**Response** (202 Accepted):
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "pending",
  "priority": 1,
  "method": "GET",
  "url": "https://api.example.com/data",
  "attempts": 0,
  "created_at": "2026-03-05T10:30:00Z"
}
```

#### Get Request Status
```
GET /api/requests/{id}
```

**Response** (200 OK):
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "success",
  "priority": 1,
  "method": "GET",
  "url": "https://api.example.com/data",
  "attempts": 1,
  "assigned_key_id": "key-uuid",
  "response_status": 200,
  "response_body": "{\"result\": \"data\"}",
  "created_at": "2026-03-05T10:30:00Z",
  "completed_at": "2026-03-05T10:30:01Z"
}
```

### Admin Endpoints

All admin endpoints require the `Authorization` header:
```
Authorization: Bearer <ADMIN_TOKEN>
```

#### Add an API Key
```
POST /admin/keys
Content-Type: application/json

{
  "name": "openai-key-1",
  "key": "sk-abc123...",
  "weight": 3,
  "rate_limit_per_minute": 60,
  "rate_limit_per_day": 10000,
  "concurrent_limit": 5
}
```

#### List All Keys
```
GET /admin/keys
```
Returns an array of key objects (encrypted values are never exposed).

#### Delete a Key
```
DELETE /admin/keys/{id}
```

#### Update Key Weight
```
PUT /admin/keys/{id}/weight
Content-Type: application/json

{"weight": 5}
```

#### Reset Circuit Breaker
```
POST /admin/keys/{id}/reset
```
Manually resets a key's circuit breaker from Open back to Closed.

#### Pool Health
```
GET /admin/health
```
```json
{
  "pool_size": 3,
  "queue_size": 42,
  "keys": [
    {
      "id": "...",
      "name": "openai-key-1",
      "state": "closed",
      "failure_count": 0,
      "is_healthy": true,
      "concurrent": 2,
      "concurrent_limit": 5
    }
  ]
}
```

#### Get System Config
```
GET /admin/config
```

#### Update System Config
```
PUT /admin/config
Content-Type: application/json

{"worker_count": "20", "key_pool_strategy": "weighted_round_robin"}
```

### Metrics
```
GET /metrics
```
Prometheus text-format metrics including:
- `keypool_requests_submitted_total`
- `keypool_requests_completed_total`
- `keypool_requests_failed_total`
- `keypool_requests_retried_total`
- `keypool_queue_size`
- `keypool_workers_active` / `keypool_workers_total`
- `keypool_keys_total` / `keypool_keys_healthy`
- `keypool_http_response_status_total{code="200"}`
- `keypool_request_latency_avg_ms`
- `keypool_uptime_seconds`

## Testing

### Manual Testing with cURL

**1. Start the server:**
```bash
# With Docker
docker compose up --build

# Or locally
mkdir -p data
CGO_ENABLED=1 go build -o key-pool-system ./cmd/
./key-pool-system
```

**2. Verify health:**
```bash
curl http://localhost:8080/health
```

**3. Add API keys (replace `YOUR_ADMIN_TOKEN`):**
```bash
# Add first key
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "key-1", "key": "sk-test-key-111", "weight": 1, "rate_limit_per_minute": 60, "concurrent_limit": 5}'

# Add second key with higher weight
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "key-2", "key": "sk-test-key-222", "weight": 3, "rate_limit_per_minute": 120, "concurrent_limit": 10}'
```

**4. List keys:**
```bash
curl http://localhost:8080/admin/keys \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" | jq
```

**5. Submit a request:**
```bash
curl -X POST http://localhost:8080/api/requests \
  -H "Content-Type: application/json" \
  -d '{
    "method": "GET",
    "url": "https://httpbin.org/get",
    "priority": 2,
    "idempotency_key": "test-001"
  }'
```

**6. Check request status (use the ID from step 5):**
```bash
curl http://localhost:8080/api/requests/REQUEST_ID_HERE | jq
```

**7. Check pool health:**
```bash
curl http://localhost:8080/admin/health \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" | jq
```

**8. View metrics:**
```bash
curl http://localhost:8080/metrics
```

**9. Test circuit breaker (submit requests to a failing URL):**
```bash
for i in $(seq 1 5); do
  curl -X POST http://localhost:8080/api/requests \
    -H "Content-Type: application/json" \
    -d "{\"method\": \"GET\", \"url\": \"https://httpbin.org/status/500\", \"priority\": 2}"
  sleep 1
done

# Check health — key should show circuit_state: "open" after failures
curl http://localhost:8080/admin/health \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" | jq
```

**10. Reset a circuit breaker:**
```bash
curl -X POST http://localhost:8080/admin/keys/KEY_ID_HERE/reset \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN"
```

**11. Test priority preemption (fill queue then submit high-priority):**
```bash
# Submit several low-priority requests
for i in $(seq 1 10); do
  curl -X POST http://localhost:8080/api/requests \
    -H "Content-Type: application/json" \
    -d '{"method": "GET", "url": "https://httpbin.org/delay/5", "priority": 3}'
done

# Submit a high-priority request — it preempts low-priority items
curl -X POST http://localhost:8080/api/requests \
  -H "Content-Type: application/json" \
  -d '{"method": "GET", "url": "https://httpbin.org/get", "priority": 1}'
```

**12. Test idempotency:**
```bash
# Submit the same request twice with same idempotency key
curl -X POST http://localhost:8080/api/requests \
  -H "Content-Type: application/json" \
  -d '{"method": "GET", "url": "https://httpbin.org/get", "idempotency_key": "dedup-test"}'

# Second call returns the existing request, not a duplicate
curl -X POST http://localhost:8080/api/requests \
  -H "Content-Type: application/json" \
  -d '{"method": "GET", "url": "https://httpbin.org/get", "idempotency_key": "dedup-test"}'
```

**13. Update key weight and switch strategy:**
```bash
# Switch to weighted round robin
curl -X PUT http://localhost:8080/admin/config \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"key_pool_strategy": "weighted_round_robin"}'

# Update key weight
curl -X PUT http://localhost:8080/admin/keys/KEY_ID_HERE/weight \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"weight": 5}'
```

**14. Test admin auth rejection:**
```bash
# Without token — should return 401
curl http://localhost:8080/admin/keys

# With wrong token — should return 401
curl http://localhost:8080/admin/keys \
  -H "Authorization: Bearer wrong-token"
```

### Build Verification

```bash
# Compile everything
CGO_ENABLED=1 go build -o key-pool-system ./cmd/

# Run static analysis
CGO_ENABLED=1 go vet ./...
```

## Configuration

All configuration is done via environment variables (see [.env.example](.env.example) for the full list with documentation).

| Category | Key Variables |
|----------|--------------|
| **Server** | `SERVER_PORT`, `SERVER_READ_TIMEOUT_SECONDS`, `SERVER_SHUTDOWN_TIMEOUT_SECONDS` |
| **Database** | `DB_PATH`, `DB_MAX_OPEN_CONNS`, `DB_BUSY_TIMEOUT_MS` |
| **Security** | `ENCRYPTION_KEY` (required), `ADMIN_TOKEN` (required) |
| **Workers** | `WORKER_COUNT`, `WORKER_WARMUP_SECONDS` |
| **Queue** | `QUEUE_MAX_SIZE`, `QUEUE_HIGH_PRIORITY_PREEMPTS_LOW` |
| **Key Pool** | `KEY_POOL_STRATEGY`, `KEY_POOL_RELOAD_INTERVAL_SECONDS` |
| **Circuit Breaker** | `CIRCUIT_BREAKER_FAILURE_THRESHOLD`, `CIRCUIT_BREAKER_OPEN_DURATION_SECONDS` |
| **Retry** | `RETRY_MAX_ATTEMPTS`, `RETRY_BASE_DELAY_MS`, `RETRY_MAX_DELAY_MS` |
| **HTTP Client** | `HTTP_CLIENT_TIMEOUT_SECONDS`, `HTTP_CLIENT_MAX_IDLE_CONNS` |
| **Load Shedding** | `LOAD_SHED_LEVEL_1_THRESHOLD`, `LOAD_SHED_LEVEL_2_THRESHOLD` |
| **Logging** | `LOG_LEVEL`, `LOG_FORMAT`, `LOG_REQUESTS` |
| **Metrics** | `METRICS_ENABLED`, `METRICS_PATH` |

## How It Works

### Request Lifecycle

1. Client submits a request via `POST /api/requests`
2. Request is validated, saved to SQLite, and enqueued in the priority queue
3. A worker dequeues the request, selects an API key via the configured strategy
4. The worker decrypts the key, makes the outbound HTTP call
5. On **success**: response is saved, request marked `success`
6. On **failure**: circuit breaker records the failure, request is retried with exponential backoff
7. After max retries: request is marked `failed` with the last error

### Circuit Breaker States

```
         success
    ┌───────────────┐
    ▼               │
 CLOSED ──────▶ OPEN ──────▶ HALF-OPEN
 (normal)   (N failures)   (after timeout)
    ▲                           │
    └───────────────────────────┘
          probe succeeds
```

- **Closed**: Key is healthy, requests flow normally
- **Open**: Key has exceeded failure threshold, removed from rotation
- **Half-Open**: After cooldown, one probe request is sent. Success → Closed, Failure → Open

### Key Selection Strategies

- **Round Robin**: Simple counter-based rotation through all healthy keys
- **Weighted Round Robin**: Smooth weighted algorithm — keys with higher weight get proportionally more requests (e.g., weight 3 gets 3x the traffic of weight 1)

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22 |
| Database | SQLite (WAL mode) via `github.com/mattn/go-sqlite3` |
| Logging | `github.com/rs/zerolog` (structured JSON) |
| IDs | `github.com/google/uuid` |
| Encryption | AES-256-GCM (stdlib `crypto/aes`, `crypto/cipher`) |
| HTTP | stdlib `net/http` (no frameworks) |
| Containers | Docker multi-stage build + Docker Compose |

## License

MIT
