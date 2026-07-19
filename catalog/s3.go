package catalog

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Bucket is a thin wrapper over an S3-compatible bucket.
type Bucket struct {
	client *s3.Client
	name   string
}

// NewBucket builds an S3 client pointed at the configured endpoint.
func NewBucket(ctx context.Context, c *Config) (*Bucket, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(c.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if ep := c.EndpointURL(); ep != "" {
			o.BaseEndpoint = aws.String(ep)
		}
		o.UsePathStyle = c.UsePathStyle
		// Some S3-compatible providers (OVH among them) don't return the
		// CRC/SHA checksum headers the v2 SDK looks for, which otherwise logs
		// a WARN on every GetObject. Only validate when actually requested.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &Bucket{client: client, name: c.Bucket}, nil
}

// ListPhotos lists every object under prefix and pairs JPEG + RAW siblings into
// Photos, keyed by the path without extension. Objects with other extensions
// (and folder placeholders) are ignored. The result is sorted by key.
func (b *Bucket) ListPhotos(ctx context.Context, prefix string) ([]Photo, error) {
	byBase := map[string]*Photo{}

	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.name),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if key == "" || strings.HasSuffix(key, "/") {
				continue // folder placeholder
			}
			ext := strings.ToUpper(path.Ext(key))
			kind := classify(ext)
			if kind == kindOther {
				continue
			}
			// S3 keys are untrusted input to filepath.Join (thumbnails mirror the
			// bucket layout on disk): drop any key that could escape the data dir.
			if !safeKey(key) {
				continue
			}
			ref := ObjectRef{
				Key:  key,
				Size: aws.ToInt64(obj.Size),
				ETag: strings.Trim(aws.ToString(obj.ETag), `"`),
			}
			base := strings.TrimSuffix(key, path.Ext(key))
			p := byBase[base]
			if p == nil {
				p = &Photo{
					KeyBase: base,
					Folder:  dirOf(base),
					Name:    path.Base(base),
				}
				byBase[base] = p
			}
			switch kind {
			case kindJPEG:
				p.JPEG = ref
			case kindRAF:
				r := ref
				p.RAF = &r
			}
		}
	}

	photos := make([]Photo, 0, len(byBase))
	for _, p := range byBase {
		// Skip RAW-only captures: we need the JPEG for EXIF + thumbnail.
		if p.JPEG.Key == "" {
			continue
		}
		photos = append(photos, *p)
	}
	sort.Slice(photos, func(i, j int) bool { return photos[i].KeyBase < photos[j].KeyBase })
	return photos, nil
}

// OpenObject opens a streaming reader over the object at key; the caller must
// close it. Use this instead of GetObject when the payload may be large (e.g.
// RAW files in album exports) so it never sits fully in memory.
func (b *Bucket) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.name),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	return out.Body, nil
}

// GetObject downloads the full object at key into memory.
func (b *Bucket) GetObject(ctx context.Context, key string) ([]byte, error) {
	body, err := b.OpenObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return data, nil
}

// safeKey reports whether an S3 object key maps to a safe relative path: no
// absolute prefix, no backslashes, no "." or ".." path elements. Keys failing
// this are skipped during listing (they cannot come from a normal camera
// upload, but a writable bucket must not translate into disk writes elsewhere).
func safeKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, `\`) {
		return false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

type fileKind int

const (
	kindOther fileKind = iota
	kindJPEG
	kindRAF
)

func classify(upperExt string) fileKind {
	switch upperExt {
	case ".JPG", ".JPEG":
		return kindJPEG
	case ".RAF", ".NEF", ".CR2", ".CR3", ".ARW", ".DNG", ".ORF", ".RW2", ".PEF", ".SRW", ".X3F":
		return kindRAF
	default:
		return kindOther
	}
}

func dirOf(base string) string {
	d := path.Dir(base)
	if d == "." || d == "/" {
		return ""
	}
	return d
}
