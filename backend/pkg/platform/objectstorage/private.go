package objectstorage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// PrivateConfig is a bucket for private files (support evidence): objects
// are never readable anonymously and are only served back through the
// owning service after its access check.
type PrivateConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Bucket    string
	// DownloadEndpoint is the browser-reachable origin used only for signed GETs.
	DownloadEndpoint string
}

// PrivateClient stores and reads objects in a private bucket.
type PrivateClient struct {
	mc     *minio.Client
	bucket string
	signer *minio.Client
}

// NewPrivateClient connects and makes sure the bucket exists. It refuses a
// bucket that carries any bucket policy, so it can never be pointed at the
// public-read media bucket by mistake; it never changes a policy itself.
func NewPrivateClient(ctx context.Context, cfg PrivateConfig) (*PrivateClient, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("objectstorage: new private client: %w", err)
	}
	exists, err := mc.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("objectstorage: private bucket exists check: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("objectstorage: make private bucket: %w", err)
		}
	}
	policy, err := mc.GetBucketPolicy(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("objectstorage: read private bucket policy: %w", err)
	}
	if strings.TrimSpace(policy) != "" {
		return nil, fmt.Errorf("objectstorage: bucket %q has a bucket policy; private files need a bucket without one", cfg.Bucket)
	}
	signer := mc
	if cfg.DownloadEndpoint != "" {
		u, err := url.Parse(cfg.DownloadEndpoint)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, fmt.Errorf("objectstorage: invalid private download origin")
		}
		signer, err = minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: u.Scheme == "https", Region: "us-east-1"})
		if err != nil {
			return nil, fmt.Errorf("objectstorage: download client: %w", err)
		}
	}
	return &PrivateClient{mc: mc, signer: signer, bucket: cfg.Bucket}, nil
}

// PutStream keeps large private exports out of process memory.
func (c *PrivateClient) PutStream(ctx context.Context, key string, data io.Reader, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, data, -1, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("objectstorage: stream private object: %w", err)
	}
	return nil
}

// DownloadURL is called only after the owning service reauthorizes the reader.
func (c *PrivateClient) DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return c.DownloadFileURL(ctx, key, "statement.csv", ttl)
}

// DownloadFileURL signs a GET that saves the object as filename. The name
// comes from the owning service and is reduced to safe characters.
func (c *PrivateClient) DownloadFileURL(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	safe := strings.Map(func(r rune) rune {
		if r < 128 && (r == '.' || r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return r
		}
		return '_'
	}, filename)
	params := url.Values{"response-content-disposition": {`attachment; filename="` + safe + `"`}, "response-cache-control": {"private, no-store"}}
	u, err := c.signer.PresignedGetObject(ctx, c.bucket, key, ttl, params)
	if err != nil {
		return "", fmt.Errorf("objectstorage: sign private download: %w", err)
	}
	return u.String(), nil
}

// Put writes an object.
func (c *PrivateClient) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("objectstorage: put private object: %w", err)
	}
	return nil
}

// Open returns the object's content; the caller closes it.
func (c *PrivateClient) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("objectstorage: get private object: %w", err)
	}
	// GetObject is lazy: Stat surfaces a missing object or a denied read now
	// instead of in the middle of the response.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("objectstorage: stat private object: %w", err)
	}
	return obj, nil
}

// Delete removes an object; removing a missing object succeeds.
func (c *PrivateClient) Delete(ctx context.Context, key string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("objectstorage: remove private object: %w", err)
	}
	return nil
}

func (c *PrivateClient) Ping(ctx context.Context) error {
	_, err := c.mc.BucketExists(ctx, c.bucket)
	return err
}
