package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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

// NewBackupBucket builds a Bucket pointed at c.BackupBucket (same endpoint and
// credentials as the photo bucket). BackupBucket defaults to Bucket, so this
// only diverges when S3_BACKUP_BUCKET is set.
func NewBackupBucket(ctx context.Context, c *Config) (*Bucket, error) {
	bc := *c
	bc.Bucket = firstNonEmpty(c.BackupBucket, c.Bucket)
	return NewBucket(ctx, &bc)
}

// PutFile uploads the local file at fpath to key in a single streaming PUT.
// The *os.File body is an io.ReadSeeker, so the SDK streams it (and can rewind
// on retry) without loading it in memory. Returns the number of bytes sent.
func (b *Bucket) PutFile(ctx context.Context, key, fpath string) (int64, error) {
	f, err := os.Open(fpath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(b.name),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(st.Size()),
		ContentType:   aws.String("application/octet-stream"),
	})
	if err != nil {
		return 0, fmt.Errorf("put %s: %w", key, err)
	}
	return st.Size(), nil
}

// ObjectExists reports whether key exists in the bucket.
func (b *Bucket) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.name),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return false, nil
		}
		return false, fmt.Errorf("head %s: %w", key, err)
	}
	return true, nil
}

// BackupInfo describes one object found by ListBackupObjects.
type BackupInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// ListBackupObjects lists every object under prefix with size and modification
// time, sorted oldest-first (ties broken by key). Unlike ListPhotos it applies
// no extension filter, so it sees the .db backups ListPhotos would drop.
func (b *Bucket) ListBackupObjects(ctx context.Context, prefix string) ([]BackupInfo, error) {
	var infos []BackupInfo
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
			infos = append(infos, BackupInfo{
				Key:          key,
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}
	}
	sort.Slice(infos, func(i, j int) bool {
		if !infos[i].LastModified.Equal(infos[j].LastModified) {
			return infos[i].LastModified.Before(infos[j].LastModified)
		}
		return infos[i].Key < infos[j].Key
	})
	return infos, nil
}

// ListPhotos lists every object under prefix and pairs JPEG + RAW siblings into
// Photos, keyed by the path without extension. Objects with other extensions
// (and folder placeholders) are ignored. The result is sorted by key.
//
// The second return is every key base the listing saw — INCLUDING RAW-only
// bases, which the []Photo drops (no JPEG, nothing to catalog). Scan
// reconciliation must test presence against this set: a base whose JPEG
// vanished but whose RAF is still in the bucket still owns an original.
func (b *Bucket) ListPhotos(ctx context.Context, prefix string) ([]Photo, map[string]struct{}, error) {
	byBase := map[string]*Photo{}

	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.name),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("list objects: %w", err)
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

	present := make(map[string]struct{}, len(byBase))
	photos := make([]Photo, 0, len(byBase))
	for base, p := range byBase {
		present[base] = struct{}{}
		// Skip RAW-only captures: we need the JPEG for EXIF + thumbnail.
		if p.JPEG.Key == "" {
			continue
		}
		photos = append(photos, *p)
	}
	sort.Slice(photos, func(i, j int) bool { return photos[i].KeyBase < photos[j].KeyBase })
	return photos, present, nil
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

// DeleteError records one object the bucket refused to delete.
type DeleteError struct {
	Key     string
	Message string
}

// DeleteObjects permanently removes the given keys, batching the calls at 1000
// keys each (the S3 per-request cap). Per-key failures are collected and
// returned rather than aborting the remaining batches; the error is non-nil
// only when a whole call fails (network, auth). Deleting a key that is already
// absent succeeds — S3 deletes are idempotent — which is exactly what the
// purge wants for objects removed by hand.
func (b *Bucket) DeleteObjects(ctx context.Context, keys []string) ([]DeleteError, error) {
	const maxBatch = 1000
	var failed []DeleteError
	for start := 0; start < len(keys); start += maxBatch {
		batch := keys[start:min(start+maxBatch, len(keys))]
		objs := make([]types.ObjectIdentifier, 0, len(batch))
		for _, k := range batch {
			objs = append(objs, types.ObjectIdentifier{Key: aws.String(k)})
		}
		out, err := b.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(b.name),
			// Quiet: only failed deletions come back in the response.
			Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return failed, fmt.Errorf("delete objects: %w", err)
		}
		for _, e := range out.Errors {
			msg := aws.ToString(e.Message)
			if code := aws.ToString(e.Code); code != "" {
				msg = code + ": " + msg
			}
			failed = append(failed, DeleteError{Key: aws.ToString(e.Key), Message: msg})
		}
	}
	return failed, nil
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
