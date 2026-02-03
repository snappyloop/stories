package kafka

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
)

// Producer wraps a Kafka producer
type Producer struct {
	writer *kafka.Writer
	topic  string
}

// NewProducer creates a new Kafka producer
func NewProducer(brokers []string, topic string) *Producer {
	writer := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  topic,
		Balancer:               &kafka.LeastBytes{},
		AllowAutoTopicCreation: true,
		RequiredAcks:           kafka.RequireOne,
		Async:                  false,
	}

	log.Info().
		Strs("brokers", brokers).
		Str("topic", topic).
		Msg("Kafka producer initialized")

	return &Producer{
		writer: writer,
		topic:  topic,
	}
}

// PublishJob publishes a job message to Kafka
func (p *Producer) PublishJob(ctx context.Context, jobID uuid.UUID, traceID string) error {
	msg := JobMessage{
		JobID:   jobID,
		TraceID: traceID,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal job message: %w", err)
	}

	kafkaMsg := kafka.Message{
		Key:   []byte(jobID.String()),
		Value: data,
	}

	if err := p.writer.WriteMessages(ctx, kafkaMsg); err != nil {
		return fmt.Errorf("failed to write message to kafka: %w", err)
	}

	log.Info().
		Str("job_id", jobID.String()).
		Str("topic", p.topic).
		Msg("Job message published to Kafka")

	return nil
}

// PublishWebhook publishes a webhook event message to Kafka (webhooks topic)
func (p *Producer) PublishWebhook(ctx context.Context, jobID uuid.UUID, event, traceID string) error {
	msg := WebhookMessage{
		JobID:   jobID,
		Event:   event,
		TraceID: traceID,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook message: %w", err)
	}

	kafkaMsg := kafka.Message{
		Key:   []byte(jobID.String()),
		Value: data,
	}

	if err := p.writer.WriteMessages(ctx, kafkaMsg); err != nil {
		return fmt.Errorf("failed to write webhook message to kafka: %w", err)
	}

	log.Info().
		Str("job_id", jobID.String()).
		Str("event", event).
		Str("topic", p.topic).
		Msg("Webhook event published to Kafka")

	return nil
}

// Close closes the producer
func (p *Producer) Close() error {
	log.Info().Msg("Closing Kafka producer")
	return p.writer.Close()
}
