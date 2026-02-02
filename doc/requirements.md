# The Great Stories — Requirements

## 1) Functional requirements

### 1.1 Create job

Endpoint: `POST /v1/jobs`

Request JSON:

* `text` (string, required)
* `type` (enum: `educational|financial|fictional`, required)
* `pictures_count` (int, required, min 1, max 20)
* `audio_type` (enum: `free_speech|podcast`, required)
* `webhook` (object, optional)

  * `url` (string)
  * `secret` (string, optional) — for signing webhook payloads

Response (202 Accepted):

* `job_id` (uuid)
* `status` = `queued`
* `created_at`

Behavior:

* Validate input size and enums
* Check API key validity and quota
* Create DB rows:

  * `jobs` status `queued`
* Publish Kafka message with `job_id`
* Return immediately

### 1.2 Get job status

Endpoint: `GET /v1/jobs/{job_id}`

Response:

* job fields: `status`, timestamps, requested options
* `segments`: array with status and asset IDs if available
* `output_markup` if succeeded (or `output_url` if stored in S3)

### 1.3 List jobs

Endpoint: `GET /v1/jobs?limit=20&cursor=...`

* Returns user's jobs sorted by newest
* Cursor pagination

### 1.4 Get asset metadata

Endpoint: `GET /v1/assets/{asset_id}`
Response:

* `asset_id`, `kind`, `mime_type`, `size_bytes`, `meta`
* one of:

  * `download_url` (pre-signed S3 URL, expires in e.g. 10 minutes)
  * or proxy download from API (`GET /v1/assets/{asset_id}/content`)

### 1.5 Webhook delivery

When job transitions to `succeeded` or `failed`, POST to webhook:
Payload:

* `job_id`
* `status`
* `finished_at`
* `output_markup` (optional; or provide a link to fetch it)
* `error` (if failed)

Security:

* `X-GS-Signature` = HMAC-SHA256(secret, raw_body)
* `X-GS-Timestamp` (unix seconds)
* Reject if timestamp skew too high (optional on receiver side)

Retries:

* At least 10 attempts over ~24h with exponential backoff
* Store delivery attempts

### 1.6 Quotas / plans

* API keys created in DB directly (admin action)
* Each key has:

  * `quota_chars`
  * `quota_period` (daily/weekly/monthly/yearly)
* On every `POST /v1/jobs`:

  * compute requested chars = `len(text)`
  * if `used + requested > quota_chars` → 402/429 style error (choose one and document)

## 2) Non-functional requirements

### 2.1 Reliability

* At-least-once processing guaranteed by Kafka
* Worker idempotency must prevent duplicate artifacts from breaking output

### 2.2 Performance

* API request should return in < 300ms excluding DB/Kafka overhead (best-effort)
* Worker concurrency:

  * configurable max parallel segments (default 3–5)

### 2.3 Security

* Never store raw API keys
* Use TLS in production
* Validate webhook URLs (optional allowlist)
* Avoid prompt injection risks by:

  * strict JSON schema output
  * never executing generated code
  * sanitizing markup

### 2.4 Compliance modes by `type`

**educational**

* prioritize correctness, clarity, explanatory tone
* images should be instructional (diagrams/illustrations), minimal hallucination

**financial**

* extremely strict:

  * avoid advice phrasing
  * add disclaimer in narration script
  * avoid fabricated numbers
  * images must not imply "guaranteed profits" or similar

**fictional**

* creativity encouraged, but keep consistent characters/setting across segments if possible

## 3) Definition of Done (MVP)

* End-to-end:

  * create job → async processing → succeeded → assets in S3 → output markup available
* Webhook OR polling works (both preferred, but polling alone is acceptable if time is tight)
* Landing page with:

  * project description
  * API docs (copy-paste curl)
  * one demo example rendering images + audio

## 4) Suggested repo structure

* `/cmd/api` (main)
* `/cmd/worker` (main)
* `/cmd/webhook-dispatcher` (optional)
* `/internal/auth`
* `/internal/quota`
* `/internal/jobs`
* `/internal/segments`
* `/internal/assets`
* `/internal/kafka`
* `/internal/storage`
* `/internal/llm`
* `/internal/markup`
* `/migrations`
* `/web` (react landing)

## 5) LLM integration requirements (LangChain in Go)

* Wrap LLM calls behind interfaces:

  * `Segmenter.Segment(text, picturesCount, type) -> []Segment`
  * `Narrator.MakeScript(segmentText, audioType, type) -> Script`
  * `Illustrator.MakePrompt(segmentText, type) -> Prompt`
  * `MediaGen.GenerateAudio(script, audioType) -> bytes/meta`
  * `MediaGen.GenerateImage(prompt) -> bytes/meta`
* Enforce:

  * strict JSON parsing for segmentation output
  * bounded output size (max tokens)
  * structured logging of request IDs, model name, and token usage
