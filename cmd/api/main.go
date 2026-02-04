package main

import (
	"context"
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
	"github.com/snappy-loop/stories/migrations"
)

func main() {
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

	log.Info().Msg("Starting Stories API")

	cfg := config.Load()

	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer db.Close()

	if err := migrations.Run(db.SQLDB()); err != nil {
		log.Fatal().Err(err).Msg("Failed to run migrations")
	}

	kafkaProducer := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopicJobs)
	defer kafkaProducer.Close()

	jobService := services.NewJobService(db, kafkaProducer, cfg)
	storageClient, err := storage.NewClient(
		cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
		cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL, cfg.S3PublicURL,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage client")
	}
	userRepo := database.NewUserRepository(db)
	apiKeyRepo := database.NewAPIKeyRepository(db)
	fileRepo := database.NewFileRepository(db)
	fileService := services.NewFileService(fileRepo, storageClient, cfg.S3Bucket, cfg)

	h := handlers.NewHandler(
		jobService,
		fileService,
		storageClient,
		userRepo,
		apiKeyRepo,
		cfg.DefaultQuotaChars,
		cfg.DefaultQuotaPeriod,
		cfg.MaxPicturesCount,
	)

	authService := auth.NewService(db)

	r := mux.NewRouter()
	r.HandleFunc("/", h.Index).Methods("GET")
	r.HandleFunc("/generation", h.Generation).Methods("GET")
	// POST /users (CreateUser) not registered; handler kept for later use
	r.HandleFunc("/view/asset/{id}", h.ViewAsset).Methods("GET")
	r.HandleFunc("/view/{id}", h.ViewJob).Methods("GET")

	api := r.PathPrefix("/v1").Subrouter()
	api.Use(authService.Middleware)
	api.HandleFunc("/jobs", h.CreateJob).Methods("POST")
	api.HandleFunc("/jobs/{id}", h.GetJob).Methods("GET")
	api.HandleFunc("/jobs", h.ListJobs).Methods("GET")
	api.HandleFunc("/files", h.UploadFile).Methods("POST")
	api.HandleFunc("/files", h.ListFiles).Methods("GET")
	api.HandleFunc("/files/{id}", h.DeleteFile).Methods("DELETE")
	api.HandleFunc("/assets/{id}", h.GetAsset).Methods("GET")
	api.HandleFunc("/assets/{id}/content", h.GetAssetContent).Methods("GET")

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("API listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down API...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("Server shutdown error")
	}
	log.Info().Msg("API exited")
}
