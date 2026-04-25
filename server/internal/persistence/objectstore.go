package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectStore is the small surface the rest of the server uses for asset
// uploads and signed-URL generation. The implementation is backed by any
// S3-compatible blob store (MinIO in dev, R2 / S3 in prod).
//
// Content-addressed: every object's key is its sha256, so writes are
// idempotent across re-uploads of identical bytes.
type ObjectStore struct {
	client     *s3.Client
	bucket     string
	publicBase string // CDN base URL for read-through
	presigner  *s3.PresignClient
}

// ObjectStoreConfig is the subset of config.Config used here. Kept narrow so
// the constructor is easy to test and reuse.
type ObjectStoreConfig struct {
	Endpoint        string // empty = use AWS default endpoints (i.e., real S3)
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
	PublicBaseURL   string
}

// NewObjectStore builds an ObjectStore. Connectivity is verified by a
// HeadBucket call.
func NewObjectStore(ctx context.Context, c ObjectStoreConfig) (*ObjectStore, error) {
	if c.Bucket == "" {
		return nil, errors.New("object store: bucket is required")
	}
	creds := credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, "")
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(c.Region),
		awsconfig.WithCredentialsProvider(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}

	cli := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = c.UsePathStyle
		if c.Endpoint != "" {
			// BaseEndpoint is the documented v2-SDK way to point at MinIO /
			// R2 / any S3-compatible store. EndpointResolverV2 is for advanced
			// per-operation rewriting, which we don't need.
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})

	headCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := cli.HeadBucket(headCtx, &s3.HeadBucketInput{Bucket: aws.String(c.Bucket)}); err != nil {
		return nil, fmt.Errorf("head bucket %q: %w", c.Bucket, err)
	}

	return &ObjectStore{
		client:     cli,
		bucket:     c.Bucket,
		publicBase: strings.TrimRight(c.PublicBaseURL, "/"),
		presigner:  s3.NewPresignClient(cli),
	}, nil
}

// ContentAddressedKey returns the sha256-based key for the given bytes.
// Layout: <prefix>/aa/bb/<full-sha256> where aa/bb are the first 4 hex
// characters; this caps any single directory's fan-out on real filesystems.
func ContentAddressedKey(prefix string, body []byte) string {
	sum := sha256.Sum256(body)
	hexSum := hex.EncodeToString(sum[:])
	return path.Join(prefix, hexSum[:2], hexSum[2:4], hexSum)
}

// Put uploads body at key with the given content-type. Idempotent for
// content-addressed keys: re-uploading identical bytes is a no-op semantically.
func (o *ObjectStore) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	_, err := o.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(o.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

// PublicURL returns the CDN-fronted read URL for a key.
func (o *ObjectStore) PublicURL(key string) string {
	if o.publicBase == "" {
		return key
	}
	return o.publicBase + "/" + strings.TrimLeft(key, "/")
}

// PresignGet returns a time-limited signed URL for a private object.
// Used for designer-only assets that should not be served straight off the
// CDN. Public game assets use PublicURL instead.
func (o *ObjectStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	out, err := o.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get %q: %w", key, err)
	}
	return out.URL, nil
}
