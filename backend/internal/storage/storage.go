// Package storage wraps the MinIO S3 client. The frontends never talk to
// MinIO directly — they use presigned URLs issued here (TECHSTACK).
package storage

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Buckets created by the compose `minio-init` job.
const (
	BucketBranding   = "branding"
	BucketQR         = "qrcodes"
	BucketKYC        = "kyc"
	BucketSignatures = "signatures"
)

// DefaultPresignTTL is the lifetime of issued presigned URLs.
const DefaultPresignTTL = 15 * time.Minute

// Client wraps a MinIO S3 client.
type Client struct {
	*minio.Client
}

// Open constructs a MinIO client. It performs no network I/O.
func Open(endpoint, accessKey, secretKey string, useSSL bool) (*Client, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: new client: %w", err)
	}
	return &Client{Client: c}, nil
}

// Ping checks reachability by listing buckets (implements httpserver.Pinger).
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("storage: no client")
	}
	if _, err := c.Client.ListBuckets(ctx); err != nil {
		return fmt.Errorf("storage: ping: %w", err)
	}
	return nil
}

// PutBytes uploads an in-memory object (QR PNGs are rendered, never written to
// local disk — TECHSTACK: no files on disk for persistence).
func (c *Client) PutBytes(ctx context.Context, bucket, object string, data []byte, contentType string) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("storage: no client")
	}
	_, err := c.Client.PutObject(ctx, bucket, object, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("storage: put %s/%s: %w", bucket, object, err)
	}
	return nil
}

// Exists reports whether an object is present in a bucket.
func (c *Client) Exists(ctx context.Context, bucket, object string) bool {
	if c == nil || c.Client == nil {
		return false
	}
	_, err := c.Client.StatObject(ctx, bucket, object, minio.StatObjectOptions{})
	return err == nil
}

// PresignGet returns a time-limited download URL for an object.
func (c *Client) PresignGet(ctx context.Context, bucket, object string, ttl time.Duration) (string, error) {
	if c == nil || c.Client == nil {
		return "", fmt.Errorf("storage: no client")
	}
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	u, err := c.Client.PresignedGetObject(ctx, bucket, object, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("storage: presign get %s/%s: %w", bucket, object, err)
	}
	return u.String(), nil
}

// PresignPut returns a time-limited upload URL for an object.
func (c *Client) PresignPut(ctx context.Context, bucket, object string, ttl time.Duration) (string, error) {
	if c == nil || c.Client == nil {
		return "", fmt.Errorf("storage: no client")
	}
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	u, err := c.Client.PresignedPutObject(ctx, bucket, object, ttl)
	if err != nil {
		return "", fmt.Errorf("storage: presign put %s/%s: %w", bucket, object, err)
	}
	return u.String(), nil
}
