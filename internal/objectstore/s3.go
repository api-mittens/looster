package objectstore

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/dominikbb/looster/internal/app"
)

type S3 struct {
	client *s3.Client
	bucket string
}

var _ app.Objects = (*S3)(nil)

type Config struct{ Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken string }

func New(ctx context.Context, c Config) (*S3, error) {
	if c.Bucket == "" {
		return nil, errors.New("s3.bucket is required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	// Never invoke the SDK's credential chain: it can read shared profiles,
	// invoke credential_process, or retrieve credentials from metadata services.
	if c.AccessKey == "" && c.SecretKey == "" && c.SessionToken == "" {
		c.AccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
		c.SecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
		c.SessionToken = os.Getenv("AWS_SESSION_TOKEN")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("S3 credentials require both LOOSTER_S3_ACCESS_KEY and LOOSTER_S3_SECRET_KEY, or both AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}
	cfg := aws.Config{Region: c.Region, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, c.SessionToken)}
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
