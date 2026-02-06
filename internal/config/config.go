package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds application configuration
type Config struct {
	// Server
	HTTPAddr string
	LogLevel string
	Timezone string

	// Agents service (gRPC + MCP) — used by agents binary
	GRPCAddr string
	MCPAddr  string

	// Agents service URLs — used by API to call agents (e.g. localhost:9090 or agents:9090)
	AgentsGRPCURL string
	AgentsMCPURL  string

	// Database
	DatabaseURL string

	// Kafka
	KafkaBrokers       []string
	KafkaConsumerGroup string
	KafkaTopicJobs     string
	KafkaTopicEvents   string
	KafkaTopicWebhooks string

	// S3/Storage
	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool
	S3PublicURL string

	// Gemini API
	GeminiAPIKey      string
	GeminiAPIEndpoint string // if set, overrides default Gemini API base URL (e.g. http://host.docker.internal:31300/gemini)
	GeminiModelPro    string
	GeminiModelFlash  string
	GeminiModelImage  string // image generation, e.g. gemini-3-pro-image-preview
	GeminiModelTTS             string // TTS model, e.g. gemini-2.5-pro-preview-tts
	GeminiTTSVoice             string // TTS voice name, e.g. Zephyr, Puck, Aoede
	GeminiModelSegmentPrimary   string // primary model for segmentation, e.g. gemini-3.0-flash
	GeminiModelSegmentFallback  string // fallback model for segmentation, e.g. gemini-2.5-flash-lite

	// Processing
	MaxInputLength        int
	MaxSegmentsCount      int
	MaxConcurrentSegments int

	// File upload (multi-modal input)
	MaxFileSize       int64 // max size per file in bytes (default 10MB)
	MaxFilesPerJob    int   // max files per job (default 10)
	FileExpirationHrs int   // hours until unused file expires (default 24)
	CharsPerFile      int   // quota cost in chars per file (default 1000)

	// Quota
	DefaultQuotaChars  int64
	DefaultQuotaPeriod string

	// Webhook
	WebhookMaxRetries     int
	WebhookRetryBaseDelay time.Duration
	WebhookRetryMaxDelay  time.Duration

	// Observability
	SentryDSN             string
	SentryEnvironment     string
	SentryEnableTracing   bool
	SentryWithBreadcrumbs bool
}

// Load loads configuration from environment variables
func Load() *Config {
	return &Config{
		HTTPAddr: getEnv("HTTP_ADDR", ":8080"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
		Timezone: getEnv("TZ", "UTC"),

		GRPCAddr: getEnv("GRPC_ADDR", ":9090"),
		MCPAddr:  getEnv("MCP_ADDR", ":9091"),

		AgentsGRPCURL: getEnv("AGENTS_GRPC_URL", "localhost:9090"),
		AgentsMCPURL:  getEnv("AGENTS_MCP_URL", "http://localhost:9091"),

		DatabaseURL: getEnv("DATABASE_URL", ""),

		KafkaBrokers:       []string{getEnv("KAFKA_BROKERS", "localhost:9092")},
		KafkaConsumerGroup: getEnv("KAFKA_CONSUMER_GROUP", "stories-worker-main"),
		KafkaTopicJobs:     getEnv("KAFKA_TOPIC_JOBS", "greatstories.jobs.v1"),
		KafkaTopicEvents:   getEnv("KAFKA_TOPIC_EVENTS", "greatstories.events.v1"),
		KafkaTopicWebhooks: getEnv("KAFKA_TOPIC_WEBHOOKS", "greatstories.webhooks.v1"),

		S3Endpoint:  getEnv("S3_ENDPOINT", "http://localhost:9000"),
		S3Region:    getEnv("S3_REGION", "us-east-1"),
		S3Bucket:    getEnv("S3_BUCKET", "stories-assets"),
		S3AccessKey: getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey: getEnv("S3_SECRET_KEY", ""),
		S3UseSSL:    getEnvBool("S3_USE_SSL", false),
		S3PublicURL: getEnv("S3_PUBLIC_URL", ""),

		GeminiAPIKey:      getEnv("GEMINI_API_KEY", ""),
		GeminiAPIEndpoint: getEnv("GEMINI_API_ENDPOINT", ""),
		GeminiModelPro:    getEnv("GEMINI_MODEL_PRO", "gemini-3-pro-preview"),
		GeminiModelFlash:  getEnv("GEMINI_MODEL_FLASH", "gemini-2.5-flash-lite"),
		GeminiModelImage:  getEnv("GEMINI_MODEL_IMAGE", "gemini-3-pro-image-preview"),
		GeminiModelTTS:            getEnv("GEMINI_MODEL_TTS", "gemini-2.5-pro-preview-tts"),
		GeminiTTSVoice:            getEnv("GEMINI_TTS_VOICE", "Zephyr"),
		GeminiModelSegmentPrimary:  getEnv("GEMINI_MODEL_SEGMENT_PRIMARY", "gemini-3.0-flash"),
		GeminiModelSegmentFallback: getEnv("GEMINI_MODEL_SEGMENT_FALLBACK", "gemini-2.5-flash-lite"),

		MaxInputLength:        getEnvInt("MAX_INPUT_LENGTH", 50000),
		MaxSegmentsCount:      getEnvInt("MAX_SEGMENTS_COUNT", 20),
		MaxConcurrentSegments: clampMin(getEnvInt("MAX_CONCURRENT_SEGMENTS", 5), 1),

		MaxFileSize:       getEnvInt64("MAX_FILE_SIZE", 10*1024*1024), // 10MB
		MaxFilesPerJob:    getEnvInt("MAX_FILES_PER_JOB", 10),
		FileExpirationHrs: getEnvInt("FILE_EXPIRATION_HOURS", 24),
		CharsPerFile:      getEnvInt("CHARS_PER_FILE", 1000),

		DefaultQuotaChars:  int64(getEnvInt("DEFAULT_QUOTA_CHARS", 100000)),
		DefaultQuotaPeriod: getEnv("DEFAULT_QUOTA_PERIOD", "monthly"),

		WebhookMaxRetries:     getEnvInt("WEBHOOK_MAX_RETRIES", 10),
		WebhookRetryBaseDelay: getEnvDuration("WEBHOOK_RETRY_BASE_DELAY", 30*time.Second),
		WebhookRetryMaxDelay:  getEnvDuration("WEBHOOK_RETRY_MAX_DELAY", 24*time.Hour),

		SentryDSN:             getEnv("SENTRY_DSN", ""),
		SentryEnvironment:     getEnv("SENTRY_ENVIRONMENT", "development"),
		SentryEnableTracing:   getEnvBool("SENTRY_ENABLE_TRACING", false),
		SentryWithBreadcrumbs: getEnvBool("SENTRY_WITH_BREADCRUMBS", false),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// clampMin returns v if v >= min, otherwise min. Used to ensure config values are in valid range.
func clampMin(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
