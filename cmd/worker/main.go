package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

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

	// TODO: Initialize dependencies
	// - Database connection
	// - Kafka consumer
	// - S3 client
	// - Gemini LLM client
	// - Job processor

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx // used when Kafka consumer is implemented

	// TODO: Start Kafka consumer
	// go func() {
	// 	if err := consumer.Start(ctx); err != nil {
	// 		log.Error().Err(err).Msg("Kafka consumer error")
	// 	}
	// }()

	log.Info().Msg("Worker started, consuming messages...")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down worker...")
	cancel()

	// TODO: Wait for graceful shutdown
	// consumer.Close()

	log.Info().Msg("Worker exited")
}
