package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// Client wraps S3 storage operations
type Client struct {
	s3Client  *s3.Client
	bucket    string
	publicURL string // optional base URL for public bucket (e.g. http://localhost:9000/stories-assets)
}

// NewClient creates a new S3 storage client
func NewClient(endpoint, region, bucket, accessKey, secretKey string, useSSL bool, publicURL string) (*Client, error) {
	// Build config options
	configOpts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	}

	// Add custom endpoint if provided (for MinIO/LocalStack)
	if endpoint != "" {
		configOpts = append(configOpts, config.WithBaseEndpoint(endpoint))
	}

	// Load config with credentials
	cfg, err := config.LoadDefaultConfig(context.Background(), configOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client with path-style addressing for MinIO compatibility.
	// Disable automatic request checksums and response validation so S3-compatible
	// backends (e.g. Cloudflare R2) that don't fully support CRC32 headers work correctly.
	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})

	log.Info().
		Str("endpoint", endpoint).
		Str("bucket", bucket).
		Msg("S3 client initialized")

	return &Client{
		s3Client:  s3Client,
		bucket:    bucket,
		publicURL: publicURL,
	}, nil
}

// PublicURL returns the public URL for an object key. Empty if publicURL was not configured.
func (c *Client) PublicURL(key string) string {
	if c.publicURL == "" {
		return ""
	}
	if c.publicURL[len(c.publicURL)-1] == '/' {
		return c.publicURL + key
	}
	return c.publicURL + "/" + key
}

// Upload uploads data to S3. contentLength must be > 0; S3-compatible backends (e.g. R2) require the Content-Length header.
func (c *Client) Upload(ctx context.Context, key string, data io.Reader, contentType string, contentLength int64) error {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        data,
		ContentType: aws.String(contentType),
		ContentLength: aws.Int64(contentLength),
	}
	_, err := c.s3Client.PutObject(ctx, input)

	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	log.Info().
		Str("bucket", c.bucket).
		Str("key", key).
		Msg("File uploaded to S3")

	return nil
}

// GeneratePresignedURL generates a presigned URL for downloading an object
func (c *Client) GeneratePresignedURL(key string, expiration time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(c.s3Client)

	req, err := presignClient.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = expiration
	})

	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return req.URL, nil
}

// Delete deletes an object from S3
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	log.Info().
		Str("bucket", c.bucket).
		Str("key", key).
		Msg("File deleted from S3")

	return nil
}

// GetObject retrieves an object from S3
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	result, err := c.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}

	return result.Body, nil
}
