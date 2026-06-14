// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Config configures the S3/R2-backed Store. It works against Cloudflare R2 (an
// S3-compatible API) in production and against MinIO locally — the only
// differences are the Endpoint and UsePathStyle (MinIO needs path-style; R2 is
// virtual-hosted by default but accepts path-style too).
type S3Config struct {
	Bucket          string
	Region          string // R2 ignores this but SigV4 requires a value (e.g. "auto" or "us-east-1")
	Endpoint        string // R2/MinIO endpoint; empty → real AWS S3
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool // true for MinIO; safe for R2 too
}

// S3Store is the S3/R2 implementation of Store.
type S3Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3Store builds an S3Store from cfg. Static credentials are used (the deploy
// box / container supplies scoped keys via env); a real AWS deployment could swap
// in the default credential chain, but R2/MinIO use access-key pairs.
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("storage: S3 bucket is required")
	}
	region := cfg.Region
	if region == "" {
		region = "auto" // R2's conventional region token
	}

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})

	return &S3Store{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}, nil
}

// EnsureBucket creates the configured bucket if it does not already exist. This
// is a convenience for local/self-host bootstrap (MinIO) and tests; in production
// the R2 bucket is provisioned out of band. An "already owned/exists" error is
// treated as success.
func (s *S3Store) EnsureBucket(ctx context.Context) error {
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		return nil
	}
	var existsOwned *s3types.BucketAlreadyOwnedByYou
	var exists *s3types.BucketAlreadyExists
	if errors.As(err, &existsOwned) || errors.As(err, &exists) {
		return nil
	}
	return fmt.Errorf("storage: ensure bucket %s: %w", s.bucket, err)
}

// PresignPut returns a presigned PUT URL for the blob's content-addressed key.
func (s *S3Store) PresignPut(ctx context.Context, orgID, sha256 string, ttl time.Duration) (string, error) {
	key := BlobKey(orgID, sha256)
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign put %s: %w", key, err)
	}
	return req.URL, nil
}

// HeadBlob reports whether the blob already exists and its stored size.
func (s *S3Store) HeadBlob(ctx context.Context, orgID, sha256 string) (bool, int64, error) {
	key := BlobKey(orgID, sha256)
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("storage: head %s: %w", key, err)
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return true, size, nil
}

// GetBlob streams a blob's bytes.
func (s *S3Store) GetBlob(ctx context.Context, orgID, sha256 string) (io.ReadCloser, error) {
	key := BlobKey(orgID, sha256)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return out.Body, nil
}

// PutBlob writes a blob's bytes directly.
func (s *S3Store) PutBlob(ctx context.Context, orgID, sha256 string, r io.Reader, size int64, contentType string) error {
	key := BlobKey(orgID, sha256)
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if size > 0 {
		in.ContentLength = aws.Int64(size)
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

// PutManifest writes the immutable per-deploy manifest JSON.
func (s *S3Store) PutManifest(ctx context.Context, orgID, siteID, versionID string, manifest []byte) error {
	key := ManifestKey(orgID, siteID, versionID)
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(manifest),
		ContentType:   aws.String("application/json"),
		ContentLength: aws.Int64(int64(len(manifest))),
	}); err != nil {
		return fmt.Errorf("storage: put manifest %s: %w", key, err)
	}
	return nil
}

// ListBlobInfos enumerates every blob under the org's prefix (blobs/<org>/),
// returning the content-addressed sha256 (the last key segment) PLUS each object's
// LastModified time (the GC's age guard input). It paginates so a large org
// enumerates fully. The list is the GC's "what exists" input.
func (s *S3Store) ListBlobInfos(ctx context.Context, orgID string) ([]BlobInfo, error) {
	prefix := fmt.Sprintf("blobs/%s/", orgID)
	var out []BlobInfo
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("storage: list blobs %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			key := *obj.Key
			// Key is blobs/<org>/<sha>; take the segment after the prefix.
			sha := key[len(prefix):]
			if sha == "" {
				continue
			}
			var lastMod time.Time
			if obj.LastModified != nil {
				lastMod = *obj.LastModified
			}
			out = append(out, BlobInfo{SHA: sha, LastModified: lastMod})
		}
	}
	return out, nil
}

// DeleteBlob removes a blob by its content-addressed key. An absent key is not an
// error (S3/R2 DeleteObject is idempotent), so a GC re-run is safe.
func (s *S3Store) DeleteBlob(ctx context.Context, orgID, sha256 string) error {
	key := BlobKey(orgID, sha256)
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

// GetManifest reads a deploy manifest back.
func (s *S3Store) GetManifest(ctx context.Context, orgID, siteID, versionID string) ([]byte, error) {
	key := ManifestKey(orgID, siteID, versionID)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: get manifest %s: %w", key, err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// isNotFound recognizes the S3/R2 "no such key" responses. Both AWS and R2
// surface a 404 as a NoSuchKey/NotFound API error code; HeadObject in particular
// returns a generic smithy error whose code is "NotFound".
func isNotFound(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return true
		}
	}
	var nsk *notFoundErr
	return errors.As(err, &nsk)
}

// notFoundErr is unused at runtime but documents the second branch of isNotFound
// for future typed-error matching; kept minimal so the type assertion compiles.
type notFoundErr struct{}

func (*notFoundErr) Error() string { return "not found" }

// Ensure S3Store satisfies Store.
var _ Store = (*S3Store)(nil)
