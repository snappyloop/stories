package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
)

// Consumer wraps a Kafka consumer
type Consumer struct {
	reader  *kafka.Reader
	handler MessageHandler
}

// MessageHandler processes Kafka messages
type MessageHandler interface {
	HandleMessage(ctx context.Context, msg *WebhookMessage) error
}

// WebhookMessage represents a webhook event message
type WebhookMessage struct {
	JobID   uuid.UUID `json:"job_id"`
	Event   string    `json:"event"` // "job_completed", "job_failed"
	TraceID string    `json:"trace_id,omitempty"`
}

// NewConsumer creates a new Kafka consumer
func NewConsumer(brokers []string, topic, groupID string, handler MessageHandler) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,
		MaxBytes:       10e6, // 10MB
		CommitInterval: 0,    // Disable auto-commit, using manual commits
		// Start from earliest message when no committed offset exists (first deployment).
		// This ensures webhook events published before consumer startup are not lost.
		// After initial consumption, consumer continues from last committed offset.
		StartOffset: kafka.FirstOffset,
	})

	log.Info().
		Strs("brokers", brokers).
		Str("topic", topic).
		Str("group_id", groupID).
		Msg("Kafka consumer initialized")

	return &Consumer{
		reader:  reader,
		handler: handler,
	}
}

// Start starts consuming messages
func (c *Consumer) Start(ctx context.Context) error {
	log.Info().Msg("Starting Kafka consumer")

	const (
		maxRetries     = 10
		baseDelay      = 1 * time.Second
		maxDelay       = 5 * time.Minute
		maxRetriesSkip = 50 // After this many retries, skip the message to prevent blocking
	)

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Consumer context cancelled, stopping")
			return ctx.Err()
		default:
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Error().Err(err).Msg("Failed to fetch message")
				continue
			}

			// Process message with retries - block until success or max retries
			var lastErr error
			for attempt := 0; attempt < maxRetriesSkip; attempt++ {
				// Process message
				if err := c.processMessage(ctx, msg); err != nil {
					lastErr = err

					log.Error().
						Err(err).
						Str("topic", msg.Topic).
						Int("partition", msg.Partition).
						Int64("offset", msg.Offset).
						Int("attempt", attempt+1).
						Int("max_retries", maxRetriesSkip).
						Msg("Failed to process message - will retry")

					// Calculate exponential backoff delay
					delay := baseDelay * time.Duration(1<<uint(min(attempt, maxRetries)))
					if delay > maxDelay {
						delay = maxDelay
					}

					// Wait before retry
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
						continue
					}
				} else {
					// Success - clear lastErr so post-loop check does not treat as failure
					lastErr = nil
					// Commit and move to next message
					if err := c.reader.CommitMessages(ctx, msg); err != nil {
						log.Error().Err(err).Msg("Failed to commit message")
						// Even if commit fails, message was processed successfully
						// The message may be redelivered on restart, but handler should be idempotent
					}
					break
				}
			}

			// If we exhausted all retries, log as critical and skip to prevent blocking
			if lastErr != nil {
				log.Error().
					Err(lastErr).
					Str("topic", msg.Topic).
					Int("partition", msg.Partition).
					Int64("offset", msg.Offset).
					Msg("CRITICAL: Message processing failed after all retries - SKIPPING MESSAGE")

				// Commit the failed message to move past it
				// This prevents one bad message from blocking the entire queue
				if err := c.reader.CommitMessages(ctx, msg); err != nil {
					log.Error().Err(err).Msg("Failed to commit skipped message")
				}
			}
		}
	}
}

// processMessage processes a single Kafka message
func (c *Consumer) processMessage(ctx context.Context, msg kafka.Message) error {
	log.Debug().
		Str("topic", msg.Topic).
		Int("partition", msg.Partition).
		Int64("offset", msg.Offset).
		Msg("Processing message")

	// Parse message
	var webhookMsg WebhookMessage
	if err := json.Unmarshal(msg.Value, &webhookMsg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %w", err)
	}

	// Handle message
	if err := c.handler.HandleMessage(ctx, &webhookMsg); err != nil {
		return fmt.Errorf("handler error: %w", err)
	}

	log.Info().
		Str("job_id", webhookMsg.JobID.String()).
		Str("event", webhookMsg.Event).
		Msg("Message processed successfully")

	return nil
}

// Close closes the consumer
func (c *Consumer) Close() error {
	log.Info().Msg("Closing Kafka consumer")
	return c.reader.Close()
}
