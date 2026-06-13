package service

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2ObjectStoreConfig holds Cloudflare R2 S3-compatible client settings.
type R2ObjectStoreConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

// R2ObjectStore writes objects to Cloudflare R2.
type R2ObjectStore struct {
	client *s3.Client
	bucket string
}

// NewR2ObjectStore creates an S3-compatible R2 object store.
func NewR2ObjectStore(ctx context.Context, cfg R2ObjectStoreConfig) (*R2ObjectStore, error) {
	if cfg.Endpoint == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.Bucket == "" {
		return nil, fmt.Errorf("missing required R2 configuration")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load R2 AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.EndpointResolver = s3.EndpointResolverFromURL(cfg.Endpoint)
		options.UsePathStyle = true
	})

	return &R2ObjectStore{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// PutObject stores an object in R2.
func (s *R2ObjectStore) PutObject(ctx context.Context, object StoredObject) error {
	if object.Key == "" {
		return fmt.Errorf("object key is required")
	}

	input := &s3.PutObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(object.Key),
		Body:         bytes.NewReader(object.Body),
		ContentType:  aws.String(object.ContentType),
		CacheControl: aws.String(object.CacheControl),
	}

	if _, err := s.client.PutObject(ctx, input); err != nil {
		return fmt.Errorf("put R2 object %q: %w", object.Key, err)
	}

	return nil
}
