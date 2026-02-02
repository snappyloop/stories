package kafka

import (
	"context"
	"encoding/json"
	"fmt"

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
		CommitInterval: 1,
		StartOffset:    kafka.LastOffset,
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

			// Process message
			if err := c.processMessage(ctx, msg); err != nil {
				log.Error().
					Err(err).
					Str("topic", msg.Topic).
					Int("partition", msg.Partition).
					Int64("offset", msg.Offset).
					Msg("Failed to process message")
				// Continue processing other messages
			}

			// Commit message
			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				log.Error().Err(err).Msg("Failed to commit message")
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
