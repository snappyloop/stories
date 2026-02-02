# Stories Project Setup Summary

This document describes the complete infrastructure setup for "The Great Stories" project, created for the Gemini 3 Hackathon.

## Project Overview

The Great Stories is an API-first service that enriches text into:
- Segmented content with logical breaks
- Per-segment AI-generated images
- Per-segment AI-generated audio narration
- Marked-up output for easy client embedding

## Architecture

The system consists of three main services:

1. **API Service** - HTTP REST API for job creation and status queries
2. **Worker Service** - Processes jobs asynchronously using Gemini 3
3. **Webhook Dispatcher** - Delivers completion notifications

## Created Files & Directories

### Main Project (`stories/`)

```
stories/
├── .github/workflows/
│   ├── docker-build.yml      # CI: Build & push Docker images on tag
│   └── test.yml               # CI: Run tests on push/PR
├── cmd/
│   ├── api/main.go            # API server entrypoint
│   ├── worker/main.go         # Worker service entrypoint
│   └── dispatcher/main.go     # Webhook dispatcher entrypoint
├── internal/
│   ├── config/config.go       # Configuration management
│   └── models/models.go       # Data models & DTOs
├── migrations/
│   └── 001_init.sql           # Initial database schema
├── .dockerignore              # Docker build exclusions
├── .env                       # Local development environment
├── .gitignore                 # Git exclusions
├── ARCHITECTURE.md            # Detailed architecture docs
├── compose.yaml               # Docker Compose for local dev
├── Dockerfile                 # Multi-stage build for all services
├── env.example                # Environment template
├── go.mod                     # Go module definition
├── go.sum                     # Go dependencies (placeholder)
├── Makefile                   # Development commands
├── README.md                  # Project documentation
└── REQUIREMENTS.md            # Functional requirements
```

### Helm Charts (`charts/stories/`)

```
charts/stories/
├── .github/workflows/
│   └── deploy-stories.yaml    # CD: Deploy to Kubernetes
├── secrets/values/
│   ├── decrypt.sh             # Decrypt production secrets
│   └── values.prod.yaml       # Production configuration
├── templates/
│   ├── _helpers.tpl           # Helm template helpers
│   ├── deployment-api.yaml    # API deployment
│   ├── deployment-worker.yaml # Worker deployment
│   ├── deployment-dispatcher.yaml # Dispatcher deployment
│   ├── hpa-api.yaml           # API autoscaling
│   ├── hpa-worker.yaml        # Worker autoscaling
│   ├── ingress.yaml           # API ingress
│   ├── secret-env.yaml        # Environment secrets
│   ├── service.yaml           # API service
│   └── serviceaccount.yaml    # Service account
├── .helmignore                # Helm package exclusions
├── Chart.yaml                 # Helm chart metadata
├── README.md                  # Helm chart docs
├── update.sh                  # Deployment script
└── values.yaml                # Default values
```

## Technology Stack

### Backend
- **Language**: Go 1.24
- **Web Framework**: gorilla/mux
- **Database**: PostgreSQL 16
- **Message Queue**: Kafka (Redpanda for local dev)
- **Object Storage**: S3-compatible (MinIO for local dev)
- **Logging**: zerolog
- **AI/LLM**: Google Gemini 3

### Infrastructure
- **Containerization**: Docker
- **Orchestration**: Kubernetes (Helm 3)
- **CI/CD**: GitHub Actions
- **Secrets Management**: age encryption

## Local Development Setup

### Quick Start

1. **Prerequisites**
   ```bash
   - Docker & Docker Compose
   - Go 1.24+ (optional, for local development)
   ```

2. **Clone and setup**
   ```bash
   cd stories
   cp env.example .env
   # Edit .env and add your GEMINI_API_KEY
   ```

3. **Start services**
   ```bash
   docker-compose up -d
   ```

4. **Check status**
   ```bash
   docker-compose ps
   docker-compose logs -f
   ```

5. **Access services**
   - API: http://localhost:8080
   - MinIO Console: http://localhost:9001
   - Redpanda Console: http://localhost:19644
   - PostgreSQL: localhost:5432

### Service Endpoints (Local)

- **API**: `localhost:8080`
- **PostgreSQL**: `localhost:5432`
- **Kafka**: `localhost:19092`
- **MinIO**: `localhost:9000` (API), `localhost:9001` (Console)

## Database

The database schema includes:
- `users` - User accounts
- `api_keys` - API authentication & quotas
- `jobs` - Enrichment jobs
- `segments` - Text segments within jobs
- `assets` - Generated images & audio
- `webhook_deliveries` - Webhook delivery tracking

Initialize with:
```bash
docker-compose exec postgres psql -U stories -d stories -f /docker-entrypoint-initdb.d/001_init.sql
```

## Docker Images

Images are built and pushed to Docker Hub on Git tags:

```bash
# Tag format
coyl/snappy-loop:stories-<version>

# Example
coyl/snappy-loop:stories-1.0.0
```

The Dockerfile builds all three binaries:
- `stories-api`
- `stories-worker`
- `stories-dispatcher`

## Kubernetes Deployment

### Production Deployment

1. **Prepare secrets**
   ```bash
   cd charts/stories
   # Edit secrets/values/values.prod.yaml with production config
   # Encrypt: age --encrypt -r $(cat ../age-public-key.txt) -o secrets/values/values.prod.yaml.age secrets/values/values.prod.yaml
   ```

2. **Deploy**
   ```bash
   ./update.sh 1.0.0
   ```

3. **Or use GitHub Actions**
   - Go to Actions → Deploy Stories
   - Enter tag (e.g., `1.0.0`)
   - Run workflow

### Scaling Configuration

The Helm chart includes HPA (Horizontal Pod Autoscaler):

- **API**: 2-10 replicas (CPU/Memory based)
- **Worker**: 3-20 replicas (CPU based)
- **Dispatcher**: Fixed 1 replica

## Development Workflow

### Make Commands

```bash
make help           # Show all commands
make build          # Build all binaries
make test           # Run tests
make up             # Start docker-compose
make down           # Stop docker-compose
make logs           # View logs
make migrate        # Run migrations
make psql           # Connect to PostgreSQL
make dev-api        # Run API locally
make dev-worker     # Run worker locally
make dev-dispatcher # Run dispatcher locally
```

### Testing Locally

```bash
# Run all tests
make test

# With coverage
make test-coverage
```

### Building Docker Image

```bash
# Local build
make docker-build

# Or manually
docker build -t stories:latest .
```

## API Usage

### Create API Key

```bash
docker-compose exec postgres psql -U stories -d stories -c "
INSERT INTO users (id, email) VALUES (gen_random_uuid(), 'test@example.com') RETURNING id;
-- Use the returned ID in the next command
INSERT INTO api_keys (id, user_id, key_hash, status, quota_period, quota_chars) 
VALUES (gen_random_uuid(), '<USER_ID>', crypt('test-key-123', gen_salt('bf')), 'active', 'monthly', 100000);
"
```

### Create Job

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Authorization: Bearer test-key-123" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "The solar system consists of the Sun and everything that orbits it, including eight planets, dwarf planets, and countless asteroids and comets.",
    "type": "educational",
    "pictures_count": 3,
    "audio_type": "free_speech",
    "webhook": {
      "url": "https://your-webhook.com/callback"
    }
  }'
```

### Get Job Status

```bash
curl http://localhost:8080/v1/jobs/<job-id> \
  -H "Authorization: Bearer test-key-123"
```

## Environment Variables

Key environment variables (see `env.example` for full list):

- `DATABASE_URL` - PostgreSQL connection string
- `KAFKA_BROKERS` - Kafka broker addresses
- `S3_ENDPOINT` - S3 endpoint URL
- `S3_BUCKET` - S3 bucket name
- `GEMINI_API_KEY` - Google Gemini API key
- `HTTP_ADDR` - HTTP server address (default :8080)
- `LOG_LEVEL` - Logging level (debug, info, warn, error)

## CI/CD Pipeline

### GitHub Actions Workflows

1. **Test** (`.github/workflows/test.yml`)
   - Triggers: Push to main/develop, PRs
   - Runs: Go tests with PostgreSQL service
   - Coverage: Uploads to Codecov

2. **Docker Build** (`.github/workflows/docker-build.yml`)
   - Triggers: Git tags
   - Builds: Multi-arch Docker image
   - Pushes: To Docker Hub as `coyl/snappy-loop:stories-<tag>`

3. **Deploy** (`charts/.github/workflows/deploy-stories.yaml`)
   - Triggers: Manual workflow dispatch
   - Decrypts: Production secrets
   - Deploys: To Kubernetes via Helm

## Next Steps

To complete the implementation:

1. **Implement API handlers** in `cmd/api/main.go`
2. **Add Kafka producer/consumer** in `internal/kafka/`
3. **Implement S3 storage client** in `internal/storage/`
4. **Add Gemini LLM integration** in `internal/llm/`
5. **Implement job processing pipeline** in `internal/jobs/`
6. **Add authentication middleware** in `internal/auth/`
7. **Implement quota checking** in `internal/quota/`
8. **Add webhook delivery logic** in `cmd/dispatcher/`
9. **Write tests** for all packages
10. **Update AGENTS.md** to include stories project

## Package Structure (To Implement)

```
internal/
├── auth/           # API key validation & user management
├── quota/          # Quota tracking & enforcement
├── jobs/           # Job creation & management
├── segments/       # Segmentation logic
├── assets/         # Asset storage & retrieval
├── kafka/          # Kafka producer/consumer
├── storage/        # S3 client wrapper
├── llm/            # Gemini API integration
├── markup/         # Output markup generation
└── webhook/        # Webhook delivery
```

## References

- [Gemini 3 API Documentation](https://ai.google.dev/docs)
- [Kafka Protocol](https://kafka.apache.org/protocol)
- [PostgreSQL Documentation](https://www.postgresql.org/docs/)
- [Helm Chart Best Practices](https://helm.sh/docs/chart_best_practices/)

## Support

For issues or questions:
1. Check the ARCHITECTURE.md and REQUIREMENTS.md
2. Review compose.yaml for local development setup
3. Check GitHub Actions logs for deployment issues
