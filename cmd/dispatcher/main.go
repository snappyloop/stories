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

	log.Info().Msg("Starting Stories Webhook Dispatcher")

	// TODO: Initialize dependencies
	// - Database connection
	// - Kafka consumer for webhook events
	// - HTTP client for webhook delivery

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx // used when Kafka consumer is implemented

	// TODO: Start Kafka consumer
	// go func() {
	// 	if err := consumer.Start(ctx); err != nil {
	// 		log.Error().Err(err).Msg("Kafka consumer error")
	// 	}
	// }()

	log.Info().Msg("Dispatcher started, waiting for webhook events...")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down dispatcher...")
	cancel()

	// TODO: Wait for graceful shutdown
	// consumer.Close()

	log.Info().Msg("Dispatcher exited")
}
