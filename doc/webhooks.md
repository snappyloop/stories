# Webhooks

This document describes webhook delivery: payload format, security, retry behavior, and testing.

## Architecture

The webhook dispatcher consumes events from Kafka and delivers HTTP POSTs to user webhook URLs. Delivery is non-blocking: the Kafka consumer makes one immediate attempt per event; failed deliveries are retried by a background worker so one failing URL does not block others.

```
┌─────────────────────────────────────────────────────┐
│  Kafka Topic: greatstories.webhooks.v1              │
│  Message: {job_id, event, trace_id}                   │
└─────────────────┬───────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────┐
│  Dispatcher (Kafka Consumer)                         │
│  - Fetches webhook event                             │
│  - Makes ONE immediate delivery attempt              │
│  - Records result in database                        │
│  - Continues to next message (non-blocking)         │
└─────────────────┬───────────────────────────────────┘
                  │
                  ├─ Success → Mark as "sent"
                  │
                  └─ Failure (retryable) → Mark as "pending"
                                          │
                                          ▼
                  ┌─────────────────────────────────────┐
                  │  Background Retry Worker            │
                  │  - Runs every 10 seconds            │
                  │  - Queries pending deliveries       │
                  │  - Applies exponential backoff      │
                  │  - Attempts delivery, updates status │
                  └─────────────────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────────────┐
│  User's Webhook URL                                  │
│  POST with X-GS-Signature header                     │
└─────────────────────────────────────────────────────┘
```

## Payload format

```json
{
  "job_id": "uuid",
  "status": "succeeded|failed",
  "finished_at": "2024-02-02T22:00:00Z",
  "output_markup": "[[SEGMENT id=...]]...",
  "error": {
    "code": "error_code",
    "message": "error message"
  }
}
```

## Security

### Headers sent

- `Content-Type: application/json`
- `User-Agent: Stories-Webhook/1.0`
- `X-GS-Timestamp`: Unix timestamp (seconds)
- `X-GS-Signature`: HMAC-SHA256 hex (if secret configured)

### Verifying the signature (receiver)

```go
signature := hmac.New(sha256.New, []byte(secret))
signature.Write(requestBody)
expectedSig := hex.EncodeToString(signature.Sum(nil))

if hmac.Equal([]byte(expectedSig), []byte(receivedSig)) {
    // Valid signature
}
```

Receivers may optionally reject requests if `X-GS-Timestamp` skew is too large.

## Retry strategy

### Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| WEBHOOK_MAX_RETRIES | Maximum delivery attempts | 10 |
| WEBHOOK_RETRY_BASE_DELAY | Initial backoff | 30s |
| WEBHOOK_RETRY_MAX_DELAY | Cap on backoff | 24h |

### Backoff formula

- `delay = base_delay * 2^(attempt-1)` (attempt 1 is immediate)
- `delay = min(delay, max_delay)`

### Example schedule (defaults)

1. Immediate  
2. After 30s  
3. After 1m  
4. After 2m  
5. After 4m  
6. After 8m  
7. After 16m  
8. After 32m  
9. After 1h 4m  
10. After 2h 8m  

Total span: ~3–4 hours (or up to 24h depending on config).

### Retry worker

- Polls for pending deliveries every 10 seconds.
- Processes up to 100 pending deliveries per cycle.
- Next retry time is computed from `last_attempt_at` and backoff; only attempts when that time has passed.

## Error handling

### Transient (retried)

- Network timeouts
- HTTP 5xx
- HTTP 429 (Too Many Requests)
- Temporary DNS/connection failures

### Permanent (not retried)

- HTTP 4xx (except 429)
- Invalid URL
- Malformed payloads

Failed deliveries are stored in the database with status, attempts, and last error.

## Database: webhook_deliveries

| Column | Description |
|--------|-------------|
| id | UUID primary key |
| job_id | Reference to jobs |
| url | Webhook URL |
| status | pending / sent / failed |
| attempts | Retry count |
| last_attempt_at | Timestamp of last attempt |
| last_error | Error message if failed |
| created_at | Record creation time |

## Configuration (dispatcher)

Required:

- `DATABASE_URL` — PostgreSQL connection
- `KAFKA_BROKERS` — Kafka brokers
- `KAFKA_TOPIC_WEBHOOKS` — e.g. `greatstories.webhooks.v1`

Optional (with defaults above):

- `WEBHOOK_MAX_RETRIES`, `WEBHOOK_RETRY_BASE_DELAY`, `WEBHOOK_RETRY_MAX_DELAY`
- `LOG_LEVEL` (e.g. info)

## Graceful shutdown

On SIGINT/SIGTERM the dispatcher:

1. Cancels context and stops the consumer loop.
2. Waits for the consumer to finish the current message (with a timeout, e.g. 30s).
3. Stops the retry worker and closes Kafka/DB resources.

Pending deliveries remain in the database and are picked up after restart.

## Testing webhooks

### Non-blocking behavior

1. Start the dispatcher: `./bin/stories-dispatcher`
2. Create a job with a failing webhook URL (e.g. returns 500 or times out).
3. Create a job with a working webhook URL.
4. Confirm: the working webhook is delivered quickly; the failing one is scheduled for retry without blocking the consumer.

### Permanent vs transient errors

- **4xx (e.g. 404)**: One attempt, then status `failed`; no retries.
- **5xx or timeout**: First attempt fails, status `pending`; retries in background with backoff.

### Backoff and max retries

- Set e.g. `WEBHOOK_MAX_RETRIES=5` and `WEBHOOK_RETRY_BASE_DELAY=10s`.
- Expect retries at ~10s, ~20s, ~40s, ~80s; after 5 attempts status becomes `failed`.

### Monitoring queries

- Pending count: `SELECT COUNT(*) FROM webhook_deliveries WHERE status = 'pending'`
- Recent failures: `SELECT job_id, url, attempts, last_error, last_attempt_at FROM webhook_deliveries WHERE status = 'failed' AND last_attempt_at > NOW() - INTERVAL '1 hour'`

## Creating a job with webhook

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Test text...",
    "type": "educational",
    "pictures_count": 1,
    "audio_type": "free_speech",
    "webhook": {
      "url": "https://your-endpoint.com/webhook",
      "secret": "my-secret-key"
    }
  }'
```

Worker completes the job → publishes to `greatstories.webhooks.v1` → dispatcher consumes and delivers (immediate attempt + background retries if needed).
