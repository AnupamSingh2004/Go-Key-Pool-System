# Key Pool System

A production-grade, **generic API Key Pooling System** built in Go. It acts as a proxy between your application and any external API — you submit requests, the system picks the best available key from its pool, attaches it, and forwards the call. It handles failures, retries, rate limits, and key health automatically.

## Why "Generic"?

This system is not tied to OpenAI, Anthropic, Stripe, or any specific API. It works with **any API that uses Bearer token authentication**. You tell it:
- **What URL to call** (`https://api.openai.com/v1/chat/completions`)
- **What method to use** (GET, POST, PUT, DELETE)
- **What payload to send**

The system handles everything else — selecting a key, attaching it as `Authorization: Bearer <key>`, making the call, retrying on failure, rotating to a different key if one is rate-limited. The same deployment can proxy requests to multiple different APIs simultaneously.

This makes it useful for any scenario where you have multiple API keys for the same service (different accounts, different tiers, different rate limits) and need to distribute load across them intelligently.

## Why Go?

This system is fundamentally about queue management, concurrent workers, network I/O, and long-running timing logic. Go was chosen specifically because:

| Requirement | Why Go Wins |
|-------------|-------------|
| **Multiple concurrent workers** | Goroutines cost ~2KB each. Spinning up 50 workers is trivial. Python's GIL blocks true parallelism. Node's single thread requires workarounds. |
| **Producer-Consumer queue** | Go's channels are a native queue primitive. No external library or message broker needed — the language itself provides the pattern. |
| **Long-running network I/O** | The system is always waiting on API responses, computing backoff timers, retrying. Go's scheduler multiplexes goroutines over OS threads without blocking — it was designed for exactly this. |
| **Timing and backoff** | Go's `time` package makes exponential backoff with jitter straightforward. No async/await complexity, no callback hell. |
| **Lightweight deployment** | Single static binary. No runtime, no interpreter, no dependency hell. Copy the binary and run it. |
| **SQLite integration** | Direct CGO driver, no ORM, simple and fast. |

**Why not the others:**

| Language | Problem |
|----------|---------|
| Node.js | Single-threaded event loop. Workers need workarounds. Not natural for CPU-bound queue management alongside I/O. |
| Python | GIL kills true parallelism. Slow for high-throughput concurrent I/O. asyncio adds complexity without real parallelism. |
| Java | Would work but massively over-engineered for this job. Heavy startup, verbose code, JVM overhead. |
| Rust | Would also work but the complexity cost isn't justified. Lifetimes and borrow checker add friction for a system that's more I/O than computation. |

Go hits the sweet spot — simple concurrency, fast compilation, lightweight runtime, and the language itself maps directly to this architecture.

## Architecture

```
┌──────────────┐
│ Request      │  Three sources with different characteristics:
│ Sources      │  • Cron jobs (predictable, periodic)
│              │  • Manual triggers (unpredictable, urgent)
│              │  • Code snippets (bursty, variable)
└──────┬───────┘
       │
       ▼
┌──────────────┐  Serializes chaos into an orderly line.
│ Priority     │  Without this, all 3 sources race for keys
│ Queue        │  simultaneously and you lose control entirely.
│ (3 levels)   │  Human waiting > background cron.
└──────┬───────┘
       │
       ▼
┌──────────────┐  Asynchronous processing (Producer-Consumer).
│ Worker Pool  │  Server is the reception desk that takes tickets.
│ (N goroutin) │  Workers are the back office doing actual work.
│              │  Server never slows down because a worker is busy.
└──────┬───────┘
       │
       ▼
┌──────────────┐  Heart of the system. Picks the right key.
│ Key Pool Mgr │
│ ┌──────────┐ │  Strategy: Round Robin (equal keys) or
│ │ Strategy │ │  Weighted Round Robin (unequal capacity).
│ │ (RR/WRR) │ │
│ └──────────┘ │  Think of WRR as a conveyor belt with marked slots.
│ ┌──────────┐ │  Key A (weight 3) gets 3 slots, Key B (weight 1)
│ │ Circuit  │ │  gets 1 slot. Requests fill slots in order.
│ │ Breakers │ │  No randomness, no clustering, perfectly proportional.
│ └──────────┘ │
└──────┬───────┘
       │
       ▼
┌──────────────┐  Keys encrypted with AES-256-GCM at rest.
│ SQLite + AES │  Decrypted only at the moment of the API call.
│ (WAL mode)   │  DB Adapter pattern — swap to PostgreSQL later
│              │  by changing the adapter, nothing else.
└──────────────┘
```

## Design Decisions — What Was Considered and Why

### 1. The Queue Comes First

Before a request touches the key pool, it enters a priority queue. This is the traffic controller — without it, all sources race to grab keys simultaneously and you lose control entirely.

The queue carries priority information because sources have naturally different urgency levels. A manual trigger from a human waiting for a response should jump ahead of a background cron job.

**What we handle:**
- **Queue depth** — bounded to `QUEUE_MAX_SIZE`. When full, low-priority requests are rejected with 503 instead of growing unboundedly and eating memory.
- **Queue persistence** — requests are saved to SQLite *before* entering the queue. If the server crashes, nothing is silently lost.
- **Queue preemption** — when the queue is full and a high-priority request arrives, it drops the oldest low-priority item to make room.
- **Queue observability** — current depth is always visible via `/admin/health` and `/metrics`.

### 2. Key Selection — Why Round Robin, Not Random

Random selection sounds simple but creates **clustering** — by chance, some keys get hammered while others sit idle. Over time this causes uneven rate limit exhaustion. You can't predict or control it.

**Round Robin** works like a rotating door — request 1 gets key A, request 2 gets key B, request 3 gets key C, then back to A. It's perfectly fair, deterministic, and easy to debug.

**Weighted Round Robin** is the upgrade for when keys are not equal — different rate limits, different account tiers, different cost. Key A with weight 3 gets 3x the traffic of key B with weight 1. This maps directly to their actual capacity.

We use Smooth Weighted Round Robin specifically because plain weighted random can still cause short-term clustering — you might randomly send 5 requests in a row to the same key even if its weight is only 20%.

### 3. Circuit Breaker — Gradual Health Tracking

Keys don't fail in a binary way. A simple "working/broken" model is too coarse. The circuit breaker pattern provides three states:

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

- **Closed** (normal) — key is in rotation, requests flow through
- **Open** (broken) — key is removed from rotation, requests skip it
- **Half-Open** (recovery probe) — after a cooldown period, one test request goes through. If it succeeds, the circuit closes. If it fails, it reopens.

**Why not just retry forever?** Without a circuit breaker, one bad key keeps getting selected, keeps failing, and keeps consuming retry attempts — blocking the queue for everyone else. You want to **fail fast** on bad keys and route around them.

**Why not just blacklist permanently?** The half-open state is what makes this smarter. Rate limits are temporary — a key that was exhausted 60 seconds ago might be perfectly fine now.

### 4. Retry Logic — Exponential Backoff With Jitter

When a request fails, retrying immediately just hammers an already-struggling system.

**Exponential backoff**: wait 1s after first failure, 2s after second, 4s after third, 8s after fourth. Each retry doubles the wait time, giving the external API time to recover.

But pure exponential backoff has a problem called the **thundering herd**. If 100 requests all fail at the same moment, they all back off for exactly 1 second, then all retry at exactly the same moment, then all fail again. They move in lockstep.

The fix is **jitter** — adding a small random variation. Instead of exactly 1 second, it's 1 second plus a random amount between 0 and `RETRY_JITTER_MAX_MS`. Now your 100 requests are spread out.

The combination of exponential backoff + jitter is the industry standard. It's what AWS, Google, and every major system uses for retry logic.

### 5. Multi-Dimensional Rate Limiting

Rate limits come in multiple dimensions simultaneously. A key can be fine on requests-per-minute but still get rejected because you have too many concurrent connections open. We track all dimensions per key:

| Dimension | Config Variable | What It Means |
|-----------|----------------|---------------|
| Per-minute | `rate_limit_per_minute` | Sustained request rate |
| Per-day | `rate_limit_per_day` | Daily quota limit |
| Concurrent | `concurrent_limit` | Max simultaneous open connections |

If you only track one dimension, you get mysterious failures you can't explain.

### 6. Key Security

API keys in a database are a security surface. We handle this at multiple levels:

- **Encryption at rest** — all key values are encrypted with AES-256-GCM in SQLite. The actual key is decrypted only at the moment of the API call, never stored in plaintext.
- **Never logged** — key values never appear in log output. All internal references use the key's UUID, never the actual value.
- **Never exposed via API** — the `GET /admin/keys` endpoint returns key metadata (name, weight, health) but never the encrypted or decrypted value.
- **Admin access controlled** — all `/admin/*` endpoints require a Bearer token with constant-time comparison to prevent timing attacks.
- **Dynamic reload** — keys can be added, removed, or rotated without restarting the system. The pool reloads from the database on a configurable interval.

### 7. Worker Pool — Why Separate From the Server

If the server itself waited for each API call to complete before accepting the next request, you'd have a blocking architecture. One slow API call holds up everything behind it.

The Worker Pool implements the **Producer-Consumer pattern**. The server produces work items and puts them in the queue. Workers consume from that queue at their own pace. You can run multiple workers in parallel for higher throughput.

**Worker count is a tuning problem**, not a set-and-forget number. Too few and the queue backs up. Too many and you overwhelm the API you're calling — ironically causing *more* rate limit failures. The formula: if each API call takes 200ms and you have 10 workers, your theoretical max throughput is 50 req/s. If your keys allow 30 req/s total, you need at most 6 workers. This is why worker count is hot-reloadable at runtime.

### 8. Startup Thundering Herd

When the server restarts (after a crash, a deploy), all pending retries may resume simultaneously. Cron jobs that missed their windows may fire at once. The queue goes from empty to flooded in one second.

The system implements a **warmup period** — when starting, workers process at 25% capacity for `WORKER_WARMUP_SECONDS` (default 30s), then ramp up to full speed. This prevents the restart itself from triggering a cascade failure.

### 9. Cascading Failure Protection

The nightmare scenario: one key gets rate-limited → circuit breaker opens → more load shifts to remaining keys → they get rate-limited too → their circuit breakers open → no healthy keys → queue backs up → memory grows → server crashes → restart thundering herd.

**Load shedding** breaks this cascade. When healthy key count drops below a threshold, the system starts rejecting requests *before they enter the queue*:

| Healthy Keys | Action |
|-------------|--------|
| > 50% | Normal operation, all priorities accepted |
| < 50% (`LOAD_SHED_LEVEL_1_THRESHOLD`) | Low-priority requests rejected immediately |
| < 25% (`LOAD_SHED_LEVEL_2_THRESHOLD`) | Low and Normal priority rejected. Only High priority accepted. |

This protects remaining capacity for the most important work.

### 10. Idempotency

Retry logic re-sends requests. If the API call has side effects (writing data, sending an email, charging a card), you've now done it twice.

Every request supports an `idempotency_key`. When provided, the system checks if a request with that key already exists in SQLite. If it does, it returns the existing result instead of creating a duplicate. This works regardless of whether the external API supports idempotency natively.

### 11. Hot-Reloadable Configuration

All tuning parameters can be changed at runtime without restarting:

- Worker count, key pool strategy, queue size
- Circuit breaker thresholds and cooldown durations
- Retry timing and max attempts
- Key weights (via API)

The system re-reads `system_config` from SQLite every `CONFIG_RELOAD_INTERVAL_SECONDS`. Change a value via `PUT /admin/config`, the system picks it up within seconds.

### 12. Observability

When something goes wrong at 2am, you need to know immediately. The system exposes Prometheus-format metrics at `/metrics`:

**Per-system:** requests submitted/completed/failed/retried, queue depth, worker utilization, uptime

**Per-key:** health state, failure count, circuit state, concurrent connections (via `/admin/health`)

**Per-request:** source, assigned key, retry count, response status, duration (via `/api/requests/{id}`)

### 13. Why SQLite

SQLite is not a toy database. For this architecture it's the right choice:

- The data is **read-heavy** — key configs and weights are read on every request, written rarely. SQLite handles read-heavy workloads very well.
- **Zero operational overhead** — no separate server to manage, no connection pooling, no cluster configuration.
- **WAL mode** — concurrent readers don't block each other.
- **DB Adapter pattern** — business logic never talks to SQLite directly. It talks to the `DBAdapter` interface. If you later need PostgreSQL because you're scaling to multiple servers, you change the adapter implementation and nothing else.

### 14. Time Handling

All timestamps use **UTC internally, everywhere, without exception**. Some APIs reset rate limits at midnight UTC — if you compute windows in local time, you'll miscalculate resets. Convert to local time only for display, never for logic.

## Features Summary

| Feature | Implementation |
|---------|---------------|
| Key Selection Strategies | Round Robin + Smooth Weighted Round Robin |
| Circuit Breaker | Per-key 3-state machine (Closed → Open → Half-Open) |
| Priority Queue | 3-level (High/Normal/Low) with preemption |
| Retry | Exponential backoff + jitter |
| Key Encryption | AES-256-GCM at rest in SQLite |
| Rate Limiting | Per-minute, per-day, concurrent — all per key |
| Load Shedding | 2-threshold automatic rejection |
| Hot Reload | Runtime config via DB, no restart |
| Idempotency | Duplicate detection via idempotency keys |
| Metrics | Prometheus text format at `/metrics` |
| Graceful Shutdown | SIGINT/SIGTERM with in-flight draining |
| Worker Warmup | 25% capacity ramp-up to prevent thundering herd |
| Key Security | Never logged, never exposed, encrypted at rest |
| Observability | Per-key health, per-request status, system metrics |

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

## How It Works — The Complete Flow

```
You send:  POST /api/requests  {"method": "GET", "url": "https://api.openai.com/v1/models"}
                │
                ▼
        ┌───────────────┐
        │ 1. Validate   │  Check required fields, priority, idempotency
        │    & Save     │  Persist to SQLite BEFORE queuing (crash-safe)
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ 2. Enqueue    │  Into priority queue (High=1, Normal=2, Low=3)
        │               │  If full: preempt low-priority or reject with 503
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ 3. Worker     │  A goroutine picks it up from the queue
        │    Dequeues   │  (highest priority first, FIFO within same level)
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ 4. Get Key    │  Pool Manager runs strategy on healthy keys only
        │    from Pool  │  RR: next in rotation / WRR: weighted slot
        │               │  Check: rate limit OK? concurrent slot available?
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ 5. Decrypt    │  AES-256-GCM decrypt the key from DB
        │    API Key    │  Key exists in plaintext only in memory, briefly
        └───────┬───────┘
                │
                ▼
        ┌───────────────┐
        │ 6. HTTP Call  │  Forward YOUR request to YOUR destination URL
        │               │  Attach the decrypted key as:
        │               │  Authorization: Bearer sk-abc123...
        │               │  Plus your custom headers and payload
        └───────┬───────┘
                │
          ┌─────┴─────┐
          ▼           ▼
     ┌────────┐  ┌─────────┐
     │ 2xx    │  │ Failure │
     │ Success│  │ 429/5xx │
     └───┬────┘  └────┬────┘
         │            │
         ▼            ▼
    Save result   Record failure on key's circuit breaker
    Mark success  ├─ Under max retries? → backoff + jitter → re-enqueue
    Key healthy   └─ Max retries hit? → mark request "failed"
                  └─ 3 consecutive failures? → circuit breaker OPENS
                     Key removed from rotation for cooldown period
```

**In short: your system is a proxy.** You don't send an API key *to* this system. The system picks one of its pooled keys and forwards your request to the destination with that key attached.

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
Returns key metadata (name, weight, health). Encrypted values are never exposed.

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

#### Get / Update System Config
```
GET /admin/config
PUT /admin/config  {"worker_count": "20", "key_pool_strategy": "weighted_round_robin"}
```

### Metrics
```
GET /metrics
```
Prometheus text-format metrics:
- `keypool_requests_submitted_total` / `completed` / `failed` / `retried`
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

**5. Submit a request (httpbin.org echoes back everything including headers):**
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

In the `response_body` you'll see httpbin's echo, which includes the API key the system attached:
```json
{"headers": {"Authorization": "Bearer sk-test-key-111"}}
```

This confirms the system picked a key from the pool and forwarded it to the destination.

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
# Submit several low-priority requests to slow endpoints
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
# Submit the same request twice with the same idempotency key
curl -X POST http://localhost:8080/api/requests \
  -H "Content-Type: application/json" \
  -d '{"method": "GET", "url": "https://httpbin.org/get", "idempotency_key": "dedup-test"}'

# Second call returns the existing request, not a duplicate
curl -X POST http://localhost:8080/api/requests \
  -H "Content-Type: application/json" \
  -d '{"method": "GET", "url": "https://httpbin.org/get", "idempotency_key": "dedup-test"}'
```

**13. Switch strategy and update weights:**
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
# Without token — 401
curl http://localhost:8080/admin/keys

# Wrong token — 401
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

All configuration is via environment variables. See [.env.example](.env.example) for the full list with inline documentation.

| Category | Key Variables |
|----------|--------------|
| **Server** | `SERVER_PORT`, `SERVER_READ_TIMEOUT_SECONDS`, `SERVER_SHUTDOWN_TIMEOUT_SECONDS` |
| **Database** | `DB_PATH`, `DB_MAX_OPEN_CONNS`, `DB_BUSY_TIMEOUT_MS` |
| **Security** | `ENCRYPTION_KEY` (required), `ADMIN_TOKEN` (required) |
| **Workers** | `WORKER_COUNT`, `WORKER_WARMUP_SECONDS` |
| **Queue** | `QUEUE_MAX_SIZE`, `QUEUE_HIGH_PRIORITY_PREEMPTS_LOW` |
| **Key Pool** | `KEY_POOL_STRATEGY`, `KEY_POOL_RELOAD_INTERVAL_SECONDS` |
| **Circuit Breaker** | `CIRCUIT_BREAKER_FAILURE_THRESHOLD`, `CIRCUIT_BREAKER_OPEN_DURATION_SECONDS` |
| **Retry** | `RETRY_MAX_ATTEMPTS`, `RETRY_BASE_DELAY_MS`, `RETRY_MAX_DELAY_MS`, `RETRY_JITTER_MAX_MS` |
| **HTTP Client** | `HTTP_CLIENT_TIMEOUT_SECONDS`, `HTTP_CLIENT_MAX_IDLE_CONNS` |
| **Load Shedding** | `LOAD_SHED_LEVEL_1_THRESHOLD`, `LOAD_SHED_LEVEL_2_THRESHOLD` |
| **Logging** | `LOG_LEVEL`, `LOG_FORMAT`, `LOG_REQUESTS` |
| **Metrics** | `METRICS_ENABLED`, `METRICS_PATH` |

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
