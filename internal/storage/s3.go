package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
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

func (s *S3) Rename(ctx context.Context, sourceKey, destinationKey string) error {
	sourceKey, err := naming.SafeRelative(sourceKey)
	if err != nil {
		return err
	}
	destinationKey, err = naming.SafeRelative(destinationKey)
	if err != nil {
		return err
	}
	// S3 CopyObject 不能对目标对象做原子 If-None-Match，这里只能先检查再复制。
	if _, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(destinationKey),
	}); err == nil {
		return fmt.Errorf("destination already exists")
	} else if !isS3NotFound(err) {
		return err
	}
	if _, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.cfg.Bucket),
		Key:        aws.String(destinationKey),
		CopySource: aws.String(s.copySource(sourceKey)),
	}); err != nil {
		return err
	}
	if err := s.Delete(ctx, sourceKey); err != nil {
		return &PartialRenameError{SourceKey: sourceKey, DestinationKey: destinationKey, Cause: err}
	}
	return nil
}

func (s *S3) DeleteDir(ctx context.Context, key string) error {
	key, err := normalizeDirectoryKey(key)
	if err != nil {
		return err
	}
	prefix := strings.TrimRight(key, "/") + "/"
	var batch []types.ObjectIdentifier
	var token *string
	for {
		output, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.cfg.Bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return err
		}
		for _, item := range output.Contents {
			objectKey := aws.ToString(item.Key)
			if objectKey == "" {
				continue
			}
			batch = append(batch, types.ObjectIdentifier{Key: aws.String(objectKey)})
			if len(batch) == 1000 {
				if err := s.deleteObjects(ctx, batch); err != nil {
					return err
				}
				batch = batch[:0]
			}
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		token = output.NextContinuationToken
	}
	if len(batch) > 0 {
		return s.deleteObjects(ctx, batch)
	}
	return nil
}

func (s *S3) deleteObjects(ctx context.Context, objects []types.ObjectIdentifier) error {
	if len(objects) == 0 {
		return nil
	}
	_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(s.cfg.Bucket),
		Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
	})
	return err
}

func (s *S3) copySource(key string) string {
	escapedBucket := url.PathEscape(s.cfg.Bucket)
	escapedKey := strings.ReplaceAll(url.PathEscape(key), "%2F", "/")
	return escapedBucket + "/" + escapedKey
}

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NotFound", "NoSuchKey", "404":
		return true
	default:
		return false
	}
}

func (s *S3) OpenReader(ctx context.Context, key string) (io.ReadCloser, error) {
	key, err := naming.SafeRelative(key)
	if err != nil {
		return nil, err
	}
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	size := int64(-1)
	if output.ContentLength != nil {
		size = *output.ContentLength
	}
	return &sizedReadCloser{ReadCloser: output.Body, size: size}, nil
}

func (s *S3) FileURL(key, _ string) string {
	key = strings.TrimLeft(strings.ReplaceAll(key, "\\", "/"), "/")
	if prefix := strings.TrimSpace(s.cfg.URLPrefix); prefix != "" {
		return joinObjectURL(prefix, key)
	}
	if endpoint := strings.TrimSpace(s.cfg.Endpoint); endpoint != "" {
		return joinObjectURL(strings.TrimRight(endpoint, "/")+"/"+s.cfg.Bucket, key)
	}
	return joinObjectURL(fmt.Sprintf("https://%s.s3.%s.amazonaws.com", s.cfg.Bucket, s.cfg.Region), key)
}

func (s *S3) List(ctx context.Context, _ string, dir string) ([]Object, error) {
	dir, err := normalizeListDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if dir != "" {
		prefix = strings.TrimRight(dir, "/") + "/"
	}

	var objects []Object
	var token *string
	for {
		output, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.cfg.Bucket),
			Delimiter:         aws.String("/"),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range output.CommonPrefixes {
			key := aws.ToString(item.Prefix)
			if key == "" {
				continue
			}
			objects = append(objects, Object{
				Path:    key,
				Size:    0,
				ModTime: time.Unix(0, 0).UTC(),
				Type:    s.Type(),
				IsDir:   true,
			})
		}
		for _, item := range output.Contents {
			key := aws.ToString(item.Key)
			if key == "" || key == prefix {
				continue
			}
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
	sortObjects(objects)
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
