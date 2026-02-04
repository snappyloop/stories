package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/kafka"
	"github.com/snappy-loop/stories/internal/llm"
	"github.com/snappy-loop/stories/internal/processor"
	"github.com/snappy-loop/stories/internal/storage"
)

// JobHandler implements kafka.MessageHandler for job processing
type JobHandler struct {
	processor *processor.JobProcessor
}

func (h *JobHandler) HandleMessage(ctx context.Context, msg *kafka.JobMessage) error {
	log.Info().
		Str("job_id", msg.JobID.String()).
		Msg("Processing job message")

	// Process the job
	return h.processor.ProcessJob(ctx, msg.JobID)
}

func main() {
	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	level, err := zerolog.ParseLevel(logLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	log.Info().Msg("Starting Stories Worker")

	// Load configuration
	cfg := config.Load()

	// Initialize database connection
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer db.Close()

	// Initialize S3 storage client
	storageClient, err := storage.NewClient(
		cfg.S3Endpoint,
		cfg.S3Region,
		cfg.S3Bucket,
		cfg.S3AccessKey,
		cfg.S3SecretKey,
		cfg.S3UseSSL,
		cfg.S3PublicURL,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage client")
	}

	// Initialize Gemini LLM client
	llmClient := llm.NewClient(
		cfg.GeminiAPIKey,
		cfg.GeminiModelFlash,
		cfg.GeminiModelPro,
		cfg.GeminiModelImage,
		cfg.GeminiModelTTS,
		cfg.GeminiTTSVoice,
		cfg.GeminiAPIEndpoint,
	)

	// Initialize Kafka producer for webhook events
	webhookProducer := kafka.NewProducer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicWebhooks,
	)
	defer webhookProducer.Close()

	// Input processors for multi-modal support
	fileRepo := database.NewFileRepository(db)
	jobFileRepo := database.NewJobFileRepository(db)
	multiFileProcessor := processor.NewMultiFileProcessor(llmClient, storageClient, fileRepo, jobFileRepo)
	inputRegistry := processor.NewInputProcessorRegistry(
		processor.NewTextProcessor(),
		multiFileProcessor,
	)

	// Initialize job processor
	jobProcessor := processor.NewJobProcessor(
		db,
		llmClient,
		storageClient,
		webhookProducer,
		cfg,
		inputRegistry,
		jobFileRepo,
		fileRepo,
	)

	// Create job handler
	handler := &JobHandler{
		processor: jobProcessor,
	}

	// Initialize Kafka consumer for jobs
	consumer := kafka.NewJobConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicJobs,
		cfg.KafkaConsumerGroup,
		handler,
	)
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Kafka consumer in goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := consumer.Start(ctx); err != nil && err != context.Canceled {
			log.Error().Err(err).Msg("Kafka consumer error")
		}
	}()

	log.Info().Msg("Worker started, consuming job messages...")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down worker...")

	// Cancel context to stop consumer
	cancel()

	// Wait for consumer to finish with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Info().Msg("Consumer shutdown complete")
	case <-time.After(30 * time.Second):
		log.Warn().Msg("Consumer shutdown timeout")
	}

	log.Info().Msg("Worker exited")
}
