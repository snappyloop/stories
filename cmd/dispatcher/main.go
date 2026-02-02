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
	"github.com/snappy-loop/stories/internal/webhook"
)

// WebhookHandler implements kafka.MessageHandler
type WebhookHandler struct {
	deliveryService *webhook.DeliveryService
}

func (h *WebhookHandler) HandleMessage(ctx context.Context, msg *kafka.WebhookMessage) error {
	log.Info().
		Str("job_id", msg.JobID.String()).
		Str("event", msg.Event).
		Msg("Processing webhook event")

	// Deliver webhook for the job
	return h.deliveryService.DeliverWebhook(ctx, msg.JobID)
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

	log.Info().Msg("Starting Stories Webhook Dispatcher")

	// Load configuration
	cfg := config.Load()

	// Initialize database connection
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer db.Close()

	// Initialize webhook delivery service
	deliveryService := webhook.NewDeliveryService(db, cfg)

	// Create webhook handler
	handler := &WebhookHandler{
		deliveryService: deliveryService,
	}

	// Initialize Kafka consumer for webhook events
	consumer := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaTopicWebhooks,
		"webhook-dispatcher",
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

	log.Info().Msg("Dispatcher started, waiting for webhook events...")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down dispatcher...")

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

	log.Info().Msg("Dispatcher exited")
}
