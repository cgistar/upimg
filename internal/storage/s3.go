package storage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"upimg/internal/config"
	"upimg/internal/naming"
)

type S3 struct {
	cfg    config.S3Config
	client *s3.Client
}

func NewS3(ctx context.Context, cfg config.S3Config) (*S3, error) {
	if !cfg.Valid() {
		return nil, fmt.Errorf("s3 config is incomplete")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		if strings.TrimSpace(cfg.Endpoint) != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
			options.UsePathStyle = true
		}
	})

	return &S3{cfg: cfg, client: client}, nil
}

func (s *S3) Type() string {
	return "aws-s3"
}

func (s *S3) UploadPath() string {
	return s.cfg.UploadPath
}

func (s *S3) Probe(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.cfg.Bucket)})
	return err
}

func (s *S3) Put(ctx context.Context, key, fileName string, body io.Reader) (StoredObject, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return StoredObject{}, err
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType := imageContentType(fileName); contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	_, err = s.client.PutObject(ctx, input)
	if err != nil {
		return StoredObject{}, err
	}
	return StoredObject{FileName: fileName, URL: s.FileURL(key, ""), Type: s.Type()}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *S3) FileURL(key, _ string) string {
	key = strings.TrimLeft(strings.ReplaceAll(key, "\\", "/"), "/")
	if prefix := strings.TrimSpace(s.cfg.URLPrefix); prefix != "" {
		return strings.TrimRight(prefix, "/") + "/" + key
	}
	if endpoint := strings.TrimSpace(s.cfg.Endpoint); endpoint != "" {
		return strings.TrimRight(endpoint, "/") + "/" + s.cfg.Bucket + "/" + key
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.cfg.Bucket, s.cfg.Region, key)
}

func (s *S3) List(ctx context.Context, _ string) ([]Object, error) {
	var objects []Object
	var token *string
	for {
		output, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.cfg.Bucket),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range output.Contents {
			key := aws.ToString(item.Key)
			objects = append(objects, Object{
				Path:    key,
				URL:     s.FileURL(key, ""),
				Size:    aws.ToInt64(item.Size),
				ModTime: aws.ToTime(item.LastModified),
				Type:    s.Type(),
			})
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		token = output.NextContinuationToken
	}
	for i := range objects {
		if objects[i].ModTime.IsZero() {
			objects[i].ModTime = time.Unix(0, 0).UTC()
		}
	}
	return objects, nil
}

func imageContentType(fileName string) string {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".avif":
		return "image/avif"
	default:
		return ""
	}
}
