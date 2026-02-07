# The Great Stories — Architecture

## 1) Goal

Build an API-first service that **enriches a provided text** into:

* **Segmented text** (logical parts)
* **Per-segment image**
* **Per-segment audio** (free speech or podcast style)
* A **marked-up output** that references generated assets by IDs (so clients can embed them)

Processing is **asynchronous**:

* API publishes a task
* Workers consume tasks, run Gemini 3 pipelines, store artifacts to S3 + Postgres
* User gets completion via **webhook** or **polling**

This must be a "new project" for hackathon purposes. ([Gemini 3 Hackathon][1])

## 2) High-level components

### 2.1 Services

1. **API Service (Go)**

* Auth via API keys
* Validates request, checks quota
* Creates `job` + `segments` placeholders
* Publishes message to Kafka
* Exposes job status + artifact retrieval metadata
* Handles webhooks scheduling/retries (or publishes "webhook events" to Kafka for a small dispatcher worker)

2. **Worker Service (Go)**

* Kafka consumer group
* Executes pipeline:

  * segmentation
  * per-segment narration script + TTS/audio generation
  * per-segment image generation
  * assembly of final marked-up text
* Stores assets in S3 and metadata in Postgres
* Emits progress events (optional) and final event
* Triggers webhook delivery (either directly or via a separate dispatcher)

3. **Webhook Dispatcher (optional but recommended)**

* Dedicated worker that:

  * takes "job completed/failed" events
  * POSTs to user webhook
  * retries with backoff, signs payloads
  * isolates webhook failures from main processing

4. **Landing (TypeScript + React)**

* Marketing + docs + minimal "try it" demo
* Shows example output rendering with embedded audio/images

## 3) External dependencies

* **Gemini API (Gemini 3)** for multimodal generation. ([Google AI for Developers][2])
* Image generation may be via Gemini 3 image output and/or Imagen depending on what's available/optimal via the API. ([Firebase][3])
* Storage: S3-compatible object store (AWS S3, MinIO, etc.)
* Kafka (or Redpanda) for queueing
* Postgres for metadata + quotas

Notes:

* Keep providers swappable via interfaces (`LLMClient`, `Storage`, `Queue`).

## 4) Data model (Postgres)

### 4.1 Tables (minimum)

**users**

* id (uuid)
* email (text, nullable)
* created_at

**api_keys**

* id (uuid)
* user_id (fk users)
* key_hash (text) — store only hash
* status (active/disabled)
* quota_period (enum: daily/weekly/monthly/yearly)
* quota_chars (int64)
* used_chars_in_period (int64)
* period_started_at (timestamptz)
* created_at

**jobs**

* id (uuid)
* user_id (fk users)
* api_key_id (fk api_keys)
* status (enum: queued/running/succeeded/failed/canceled)
* input_type (enum: educational/financial/fictional)
* segments_count (int) — total desired
* audio_type (enum: free_speech/podcast)
* input_text (text) — consider storing raw text; optionally store in S3 and keep only pointer if you expect huge inputs
* output_markup (text) — final marked-up text (or pointer)
* webhook_url (text, nullable)
* webhook_secret (text, nullable) — optional per job or per key
* error_code (text, nullable)
* error_message (text, nullable)
* created_at, started_at, finished_at

**segments**

* id (uuid)
* job_id (fk jobs)
* idx (int)
* start_char (int)
* end_char (int)
* title (text, nullable)
* segment_text (text) — or derive by slicing input_text using start/end
* status (enum: queued/running/succeeded/failed)
* created_at, updated_at

**assets**

* id (uuid)
* job_id (fk jobs)
* segment_id (fk segments, nullable) — some assets may be job-level
* kind (enum: image/audio)
* mime_type (text)
* s3_bucket (text)
* s3_key (text)
* size_bytes (int64)
* checksum (text, nullable)
* meta (jsonb) — model name, prompt, voice, duration, resolution, safety flags
* created_at

**webhook_deliveries** (if you implement dispatcher)

* id (uuid)
* job_id (fk jobs)
* url (text)
* status (enum: pending/sent/failed)
* attempts (int)
* last_attempt_at
* last_error (text, nullable)
* created_at

### 4.2 Indexing

* `jobs(user_id, created_at desc)`
* `segments(job_id, idx)`
* `assets(job_id, segment_id, kind)`
* `api_keys(key_hash)` unique

## 5) Kafka topics

* `greatstories.jobs.v1`
  Payload: `{job_id}` (and optional trace fields)
* `greatstories.events.v1` (optional)
  Events: `job_started`, `segment_done`, `job_completed`, `job_failed`
* `greatstories.webhooks.v1` (optional)
  Payload: `{job_id, webhook_url}`

Consumer groups:

* `worker-main`
* `webhook-dispatcher`

## 6) Processing pipeline details

### 6.1 Segmentation

Input: full text, requested segments_count (N), type

Output:

* list of segments with `(start_char, end_char, title?)`
* guarantee:

  * segments cover the whole text
  * no overlap
  * stable ordering
  * target number of segments ≈ N (but may deviate with guardrails; record actual count)

Implementation:

* Single Gemini 3 call to produce a JSON schema:

  * `segments: [{start_char, end_char, title}]`
* Validate strictly in code:

  * bounds within `[0, len(text)]`
  * monotonic, `start < end`
  * fix minor issues (e.g., whitespace edges) deterministically

### 6.2 Per-segment generation

For each segment (possibly parallel with a concurrency limit):

1. **Narration script**

   * style depends on `audio_type` and `input_type`
   * For financial: include disclaimers and avoid creative claims; prefer conservative tone
2. **Audio generation**

   * Use Gemini/Google capabilities available in the hackathon stack (choose a consistent approach)
   * Save audio to S3; record duration in `assets.meta`
3. **Image prompt + image generation**

   * For educational: diagram-like, crisp, accurate
   * For financial: restrained, no misleading visual cues
   * For fictional: creative, cinematic
   * Save image to S3; record resolution in `assets.meta`

### 6.3 Assembly

* Produce `output_markup` with stable embedding syntax, e.g.:

Example markup convention (define in requirements):

* Segment boundaries expressed with tags:

  * `[[SEGMENT id=...]] ... [[/SEGMENT]]`
* Asset references:

  * `[[IMAGE asset_id=...]]`
  * `[[AUDIO asset_id=...]]`

Store `output_markup` in DB (or in S3 + pointer).

### 6.4 Idempotency & retries

Worker must be able to restart safely:

* On job start, set status `running`
* For each segment:

  * if assets already exist and segment status succeeded → skip
  * else regenerate and overwrite (or create new and mark previous as superseded)
* If failure:

  * mark segment failed and job failed with `error_code`
* Retries:

  * at Kafka/message level (at-least-once)
  * plus per-step retry with jitter for transient LLM/storage errors

## 7) Auth, quota, and abuse controls

* API key in `Authorization: Bearer <key>`
* Store only hash (bcrypt/argon2) and compare in constant time
* Quota:

  * count input characters + generated characters (or only input — pick one and be explicit)
  * period rollovers handled on request:

    * if `now - period_started_at` exceeds period length → reset used counter
* Hard limits:

  * max input length (e.g., 50k chars)
  * max segments_count (e.g., 20)
* Rate limiting per key (token bucket in memory + optional Redis later)

## 8) Observability

* Structured logs (zerolog)
* Tracing:

  * generate `trace_id` per job
* Metrics:

  * jobs created/succeeded/failed
  * processing time per stage
  * LLM calls count + latency
  * S3 upload latency
* Minimal dashboard-ready Prometheus endpoint `/metrics`

## 9) Deployment

* Docker for API, Worker, Dispatcher, Landing
* Local dev:

  * docker-compose with Postgres, Kafka/Redpanda, MinIO
* Prod:

  * k8s optional; hackathon can be one VM with compose
