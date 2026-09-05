// Package storage wraps the MinIO S3 client. The frontends never talk to
// MinIO directly — they use presigned URLs issued here (TECHSTACK).
package storage

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
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
//
// The embedded client talks to MinIO over the internal endpoint and is used for
// every real network call (uploads, stat, ping). The separate `public` client
// performs no I/O: it exists only so presigned URLs are signed for the public
// origin (proxy/ngrok), which is where the browser or phone actually sends the
// request. The proxy forwards bucket prefixes to MinIO without rewriting Host,
// so the signature still validates on arrival.
type Client struct {
	*minio.Client
	public *minio.Client
}

// Open constructs a MinIO client. It performs no network I/O.
//
// publicURL is the origin presigned URLs are signed against (host + scheme).
// Empty, unparseable, or host-less values fall back to signing against the
// internal endpoint.
func Open(endpoint, accessKey, secretKey string, useSSL bool, publicURL string) (*Client, error) {
	creds := credentials.NewStaticV4(accessKey, secretKey, "")
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: new client: %w", err)
	}
	cl := &Client{Client: c}

	host, secure, ok := parsePublicURL(publicURL)
	if !ok {
		return cl, nil
	}
	pub, err := minio.New(host, &minio.Options{
		Creds:  creds,
		Secure: secure,
		// The proxy routes by bucket-name path prefix, so URLs must be
		// path-style (host/bucket/object), never virtual-host style.
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: new public client (%s): %w", publicURL, err)
	}
	cl.public = pub
	return cl, nil
}

// parsePublicURL splits a public origin into a MinIO endpoint host[:port] and
// its TLS flag. ok is false when the value cannot yield a usable host.
func parsePublicURL(raw string) (host string, secure bool, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, false
	}
	if !strings.Contains(raw, "//") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false, false
	}
	return u.Host, u.Scheme == "https", true
}

// presigner returns the client that signs URLs: the public one when configured.
func (c *Client) presigner() *minio.Client {
	if c.public != nil {
		return c.public
	}
	return c.Client
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

// ObjectInfo is the subset of an object's metadata the upload-completion
// callbacks check (SPEC §7: content-type and size limits are enforced on
// completion, because a presigned PUT cannot enforce them itself).
type ObjectInfo struct {
	Size        int64
	ContentType string
}

// Stat reads an object's size and content type. A missing object yields an
// error, so a completion callback for something never uploaded fails cleanly.
func (c *Client) Stat(ctx context.Context, bucket, object string) (ObjectInfo, error) {
	if c == nil || c.Client == nil {
		return ObjectInfo{}, fmt.Errorf("storage: no client")
	}
	info, err := c.Client.StatObject(ctx, bucket, object, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("storage: stat %s/%s: %w", bucket, object, err)
	}
	return ObjectInfo{Size: info.Size, ContentType: info.ContentType}, nil
}

// Remove deletes an object, used to clean up an upload that failed its
// completion checks rather than leaving an unreferenced file in the bucket.
func (c *Client) Remove(ctx context.Context, bucket, object string) error {
	if c == nil || c.Client == nil {
		return fmt.Errorf("storage: no client")
	}
	if err := c.Client.RemoveObject(ctx, bucket, object, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: remove %s/%s: %w", bucket, object, err)
	}
	return nil
}

// PresignGet returns a time-limited download URL for an object.
func (c *Client) PresignGet(ctx context.Context, bucket, object string, ttl time.Duration) (string, error) {
	if c == nil || c.Client == nil {
		return "", fmt.Errorf("storage: no client")
	}
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	u, err := c.presigner().PresignedGetObject(ctx, bucket, object, ttl, url.Values{})
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
	u, err := c.presigner().PresignedPutObject(ctx, bucket, object, ttl)
	if err != nil {
		return "", fmt.Errorf("storage: presign put %s/%s: %w", bucket, object, err)
	}
	return u.String(), nil
}
