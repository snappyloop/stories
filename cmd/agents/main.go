package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/agents"
	"github.com/snappy-loop/stories/internal/auth"
	"github.com/snappy-loop/stories/internal/config"
	"github.com/snappy-loop/stories/internal/database"
	"github.com/snappy-loop/stories/internal/grpcserver"
	"github.com/snappy-loop/stories/internal/llm"
	"github.com/snappy-loop/stories/internal/mcpserver"
	"github.com/snappy-loop/stories/internal/storage"
	"github.com/snappy-loop/stories/migrations"
	audiov1 "github.com/snappy-loop/stories/gen/audio/v1"
	imagev1 "github.com/snappy-loop/stories/gen/image/v1"
	segmentationv1 "github.com/snappy-loop/stories/gen/segmentation/v1"
	"google.golang.org/grpc"
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

	log.Info().Msg("Starting Stories Agents (gRPC + MCP)")

	cfg := config.Load()

	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}
	defer db.Close()

	if err := migrations.Run(db.SQLDB()); err != nil {
		log.Fatal().Err(err).Msg("Failed to run migrations")
	}

	authService := auth.NewService(db)

	llmClient := llm.NewClient(
		cfg.GeminiAPIKey,
		cfg.GeminiModelFlash,
		cfg.GeminiModelPro,
		cfg.GeminiModelImage,
		cfg.GeminiModelTTS,
		cfg.GeminiTTSVoice,
		cfg.GeminiAPIEndpoint,
		cfg.GeminiModelSegmentPrimary,
		cfg.GeminiModelSegmentFallback,
	)

	segmentAgent := agents.NewSegmentationAgent(llmClient)
	audioAgent := agents.NewAudioAgent(llmClient)
	imageAgent := agents.NewImageAgent(llmClient)

	var storageClient *storage.Client
	if cfg.S3Bucket != "" && (cfg.S3AccessKey != "" || cfg.S3Endpoint != "") {
		var err error
		storageClient, err = storage.NewClient(
			cfg.S3Endpoint, cfg.S3Region, cfg.S3Bucket,
			cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL, cfg.S3PublicURL,
		)
		if err != nil {
			log.Warn().Err(err).Msg("S3 not available; audio/image will be returned inline (may hit gRPC size limits)")
		}
	}

	// gRPC server with auth
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(grpcserver.AuthUnaryInterceptor(authService)))
	segmentationv1.RegisterSegmentationServiceServer(grpcSrv, grpcserver.NewSegmentationServer(segmentAgent))
	audiov1.RegisterAudioServiceServer(grpcSrv, grpcserver.NewAudioServer(audioAgent, storageClient))
	imagev1.RegisterImageServiceServer(grpcSrv, grpcserver.NewImageServer(imageAgent, storageClient))

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", cfg.GRPCAddr).Msg("Failed to listen for gRPC")
	}
	go func() {
		log.Info().Str("addr", cfg.GRPCAddr).Msg("gRPC server listening")
		if err := grpcSrv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			log.Error().Err(err).Msg("gRPC server error")
		}
	}()

	// MCP HTTP server with auth
	mcpSrv := mcpserver.NewServer(segmentAgent, audioAgent)
	mcpHandler := mcpserver.AuthMiddleware(authService)(mcpSrv.Handler())
	mcpHTTP := &http.Server{
		Addr:         cfg.MCPAddr,
		Handler:      mcpHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info().Str("addr", cfg.MCPAddr).Msg("MCP server listening")
		if err := mcpHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("MCP HTTP server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down agents...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	grpcSrv.GracefulStop()
	if err := mcpHTTP.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("MCP HTTP shutdown error")
	}

	log.Info().Msg("Agents exited")
}
