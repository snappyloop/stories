package grpcserver

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/snappy-loop/stories/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const metadataKeyAuthorization = "authorization"

// AuthUnaryInterceptor returns a gRPC unary interceptor that validates the API key
// from the "authorization" metadata (Bearer <key>) using auth.Service.
func AuthUnaryInterceptor(authService *auth.Service) func(context.Context, interface{}, *grpc.UnaryServerInfo, grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}
		vals := md.Get(metadataKeyAuthorization)
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization")
		}
		authHeader := vals[0]
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization format")
		}
		apiKey := strings.TrimSpace(parts[1])
		if apiKey == "" {
			return nil, status.Error(codes.Unauthenticated, "empty api key")
		}
		storedKey, err := authService.ValidateAPIKey(ctx, apiKey)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid api key")
		}
		ctx = context.WithValue(ctx, auth.UserIDKey, storedKey.UserID)
		return handler(ctx, req)
	}
}

// userIDFromContext returns the authenticated user ID from context for artifact paths, or "anonymous" if missing.
func userIDFromContext(ctx context.Context) string {
	if v := ctx.Value(auth.UserIDKey); v != nil {
		if u, ok := v.(uuid.UUID); ok {
			return u.String()
		}
	}
	return "anonymous"
}
