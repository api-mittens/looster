package objectstore

import (
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/dominikbb/looster/app"
)

type S3 struct {
	client *s3.Client
	bucket string
}

var _ app.Objects = (*S3)(nil)

type Config struct{ Endpoint, Region, Bucket, AccessKey, SecretKey string }

func New(ctx context.Context, c Config) (*S3, error) {
	if c.Bucket == "" {
		return nil, errors.New("s3.bucket is required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(c.Region)}
	if c.AccessKey != "" || c.SecretKey != "" {
		if c.AccessKey == "" || c.SecretKey == "" {
			return nil, errors.New("both s3.access_key and s3.secret_key are required")
		}
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, "")))
	}
	cfg, e := config.LoadDefaultConfig(ctx, opts...)
	if e != nil {
		return nil, e
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	return &S3{client, c.Bucket}, nil
}
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, e := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: r, ContentLength: &size})
	return e
}
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if e != nil {
		return nil, e
	}
	return r.Body, nil
}
