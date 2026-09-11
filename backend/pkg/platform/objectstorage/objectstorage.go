// Package objectstorage wraps the S3-compatible client (MinIO locally, any
// S3-compatible provider in production) used to store product images.
// Backend-proxied upload: the client validates and streams the file itself
// rather than handing out a presigned URL, so content type and size are
// enforced server-side before anything is written to the bucket.
package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint      string
	AccessKey     string
	SecretKey     string
	UseSSL        bool
	Bucket        string
	PublicBaseURL string
}

type Client struct {
	mc     *minio.Client
	bucket string
	// publicBaseURL is where a browser can fetch an uploaded object from
	// (e.g. the MinIO endpoint or a CDN in front of the bucket).
	publicBaseURL string
}

// NewClient connects to the object store and makes sure the configured
// bucket exists and is readable publicly, since product images are
// storefront assets, not private files.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("objectstorage: new client: %w", err)
	}

	exists, err := mc.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("objectstorage: bucket exists check: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("objectstorage: make bucket: %w", err)
		}
	}

	if err := mc.SetBucketPolicy(ctx, cfg.Bucket, publicReadPolicy(cfg.Bucket)); err != nil {
		return nil, fmt.Errorf("objectstorage: set bucket policy: %w", err)
	}

	return &Client{mc: mc, bucket: cfg.Bucket, publicBaseURL: strings.TrimRight(cfg.PublicBaseURL, "/")}, nil
}

// Upload streams an object into the bucket and returns its public URL.
func (c *Client) Upload(ctx context.Context, objectKey string, data []byte, contentType string) (string, error) {
	_, err := c.mc.PutObject(ctx, c.bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("objectstorage: put object: %w", err)
	}
	return fmt.Sprintf("%s/%s/%s", c.publicBaseURL, c.bucket, objectKey), nil
}

// Delete removes an object from the bucket. Used when a product's main
// image is replaced, to clean up the superseded file.
func (c *Client) Delete(ctx context.Context, objectKey string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("objectstorage: remove object: %w", err)
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.mc.BucketExists(ctx, c.bucket)
	return err
}

func publicReadPolicy(bucket string) string {
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]any{"AWS": []string{"*"}},
				"Action":    []string{"s3:GetObject"},
				"Resource":  []string{fmt.Sprintf("arn:aws:s3:::%s/*", bucket)},
			},
		},
	}
	b, _ := json.Marshal(policy)
	return string(b)
}
