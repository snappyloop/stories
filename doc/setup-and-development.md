# Setup and Development

## Project overview

The Great Stories is an API-first service that enriches text into segmented content with per-segment images and audio narration. The system consists of:

1. **API Service** — HTTP REST API for job creation and status queries
2. **Worker Service** — Processes jobs asynchronously using Gemini 3
3. **Webhook Dispatcher** — Delivers completion notifications with retries

## Technology stack

- **Language**: Go 1.24+
- **Web framework**: gorilla/mux
- **Database**: PostgreSQL 16
- **Message queue**: Kafka (Redpanda for local dev)
- **Object storage**: S3-compatible (MinIO for local dev)
- **Logging**: zerolog
- **AI/LLM**: Google Gemini 3

## Local development

### Prerequisites

- Docker & Docker Compose
- Go 1.24+ (optional, for local development without Docker)

### Quick start

1. Copy environment file:
   ```bash
   cp env.example .env
   ```

2. Add your Gemini API key to `.env`:
   ```bash
   GEMINI_API_KEY=your-actual-api-key
   ```

3. Start services:
   ```bash
   docker-compose up -d
   ```

4. Migrations run automatically when the API or dispatcher starts. No manual step needed.

5. Access services:
   - API: http://localhost:8080
   - MinIO Console: http://localhost:9001
   - Redpanda Console: http://localhost:19644
   - PostgreSQL: localhost:5432

### Service endpoints (local)

| Service   | Address           |
|----------|--------------------|
| API      | localhost:8080     |
| PostgreSQL | localhost:5432   |
| Kafka    | localhost:19092    |
| MinIO API | localhost:9000   |
| MinIO Console | localhost:9001 |

## Make commands

```bash
make help           # Show all commands
make build          # Build all binaries (api, worker, dispatcher)
make test           # Run tests
make up             # Start docker-compose
make down           # Stop docker-compose
make logs           # View logs
make migrate        # Run migrations manually (optional; API/dispatcher auto-run on startup)
make psql           # Connect to PostgreSQL
make dev-api        # Run API locally
make dev-worker     # Run worker locally
make dev-dispatcher # Run dispatcher locally
```

## API endpoints

All `/v1/*` endpoints require `Authorization: Bearer <api-key>`.

| Method | Path | Description |
|--------|------|-------------|
| GET | /health | Health check (with DB status) |
| POST | /v1/jobs | Create enrichment job |
| GET | /v1/jobs | List jobs (pagination) |
| GET | /v1/jobs/{id} | Get job details |
| GET | /v1/assets/{id} | Get asset metadata |
| GET | /v1/assets/{id}/content | Download asset content |

## Environment variables

Key variables (see `env.example` for full list):

| Variable | Description |
|----------|-------------|
| DATABASE_URL | PostgreSQL connection string |
| KAFKA_BROKERS | Kafka broker addresses |
| S3_ENDPOINT, S3_BUCKET | S3 storage |
| GEMINI_API_KEY | Google Gemini API key |
| HTTP_ADDR | Server listen address (default :8080) |
| LOG_LEVEL | debug, info, warn, error |

## Create API key and job

### Create API key

```bash
docker-compose exec postgres psql -U stories -d stories -c "
INSERT INTO users (id, email) VALUES (gen_random_uuid(), 'test@example.com') RETURNING id;
-- Use returned id as <USER_ID> below
INSERT INTO api_keys (id, user_id, key_hash, status, quota_period, quota_chars) 
VALUES (gen_random_uuid(), '<USER_ID>', crypt('test-key-123', gen_salt('bf')), 'active', 'monthly', 100000);
"
```

### Create job

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Authorization: Bearer test-key-123" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "The solar system consists of the Sun and eight planets...",
    "type": "educational",
    "pictures_count": 3,
    "audio_type": "free_speech",
    "webhook": {
      "url": "https://your-webhook.com/callback"
    }
  }'
```

### Get job status

```bash
curl http://localhost:8080/v1/jobs/<job-id> \
  -H "Authorization: Bearer test-key-123"
```

## Package structure

```
internal/
├── auth/           # API key validation & user context
├── config/         # Configuration management
├── database/       # DB connection, repositories (jobs, segments, assets, api_keys, webhook_deliveries)
├── handlers/       # HTTP handlers (jobs, assets)
├── kafka/          # Producer and consumer
├── models/         # Data models & DTOs
├── quota/          # Quota tracking & enforcement
├── services/       # Job creation, status, listing
├── storage/        # S3 client
└── webhook/        # Webhook delivery & retry worker
```

## Docker images

Images are built for all three binaries:

- `stories-api`
- `stories-worker`
- `stories-dispatcher`

Build locally: `make docker-build` or `docker build -t stories:latest .`

## References

- [Gemini API Documentation](https://ai.google.dev/docs)
- [Kafka Protocol](https://kafka.apache.org/protocol)
- [PostgreSQL Documentation](https://www.postgresql.org/docs/)
