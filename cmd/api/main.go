package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/auth"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/handlers"
	"github.com/snappy-loop/stories/internal/kafka"
	"github.com/snappy-loop/stories/internal/services"
	"github.com/snappy-loop/stories/internal/storage"
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

	log.Info().Msg("Starting Stories API server")

	// Load configuration
	cfg := config.Load()
	httpAddr := cfg.HTTPAddr

	// Initialize database
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer db.Close()

	// Initialize Kafka producer
	kafkaProducer := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopicJobs)
	defer kafkaProducer.Close()

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

	// Initialize services
	authService := auth.NewService(db)
	jobService := services.NewJobService(db, kafkaProducer, cfg)
	handler := handlers.NewHandler(jobService, storageClient)

	// Setup HTTP router
	router := mux.NewRouter()

	// Health check
	router.HandleFunc("/health", healthHandler(db)).Methods("GET")

	// API routes with authentication
	apiRouter := router.PathPrefix("/v1").Subrouter()
	apiRouter.Use(authService.Middleware)

	apiRouter.HandleFunc("/jobs", handler.CreateJob).Methods("POST")
	apiRouter.HandleFunc("/jobs", handler.ListJobs).Methods("GET")
	apiRouter.HandleFunc("/jobs/{id}", handler.GetJob).Methods("GET")
	apiRouter.HandleFunc("/assets/{id}", handler.GetAsset).Methods("GET")
	apiRouter.HandleFunc("/assets/{id}/content", handler.GetAssetContent).Methods("GET")

	// Setup server
	srv := &http.Server{
		Addr:         httpAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Info().Str("addr", httpAddr).Msg("API server listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal().Err(err).Msg("Server forced to shutdown")
	}

	log.Info().Msg("Server exited")
}

func healthHandler(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Check database health
		if err := db.Health(); err != nil {
			log.Error().Err(err).Msg("Database health check failed")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"status":"unhealthy","error":"database"}`)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}
}
