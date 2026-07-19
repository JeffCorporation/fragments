package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Cataloger runs the sequential catalog pipeline: list -> (download JPEG) ->
// extract EXIF -> render thumbnail -> upsert into SQLite.
type Cataloger struct {
	cfg   *Config
	store *Store

	// Logf, if set, receives one-line progress messages. Nil disables logging.
	Logf func(format string, args ...any)
}

// NewCataloger creates a Cataloger over an open Store.
func NewCataloger(cfg *Config, store *Store) *Cataloger {
	return &Cataloger{cfg: cfg, store: store}
}

// RunOptions tunes a single catalog run.
type RunOptions struct {
	Limit  int    // process at most N photos (0 = no limit)
	Force  bool   // reprocess even if the JPEG ETag is unchanged
	Prefix string // S3 key prefix; falls back to Config.Prefix when empty
}

// RunStats summarizes a run.
type RunStats struct {
	Total     int
	Processed int
	Skipped   int
	Failed    int
}

// FetchFunc returns the JPEG bytes for a photo (from S3 or local disk).
type FetchFunc = func(context.Context, *Photo) ([]byte, error)

// S3Source lists the bucket (under prefix, falling back to Config.Prefix) and
// returns the photos plus a fetcher that downloads each JPEG. Shared by the
// sequential CLI (RunS3) and the concurrent worker pool.
func (c *Cataloger) S3Source(ctx context.Context, prefix string) ([]Photo, FetchFunc, error) {
	bucket, err := NewBucket(ctx, c.cfg)
	if err != nil {
		return nil, nil, err
	}
	if prefix == "" {
		prefix = c.cfg.Prefix
	}
	c.logf("listing s3://%s/%s ...", c.cfg.Bucket, prefix)
	photos, err := bucket.ListPhotos(ctx, prefix)
	if err != nil {
		return nil, nil, err
	}
	fetch := func(ctx context.Context, p *Photo) ([]byte, error) {
		return bucket.GetObject(ctx, p.JPEG.Key)
	}
	return photos, fetch, nil
}

// LocalSource pairs JPEG+RAF files in dir and returns the photos plus a fetcher
// that reads each JPEG from disk.
func (c *Cataloger) LocalSource(dir string) ([]Photo, FetchFunc, error) {
	photos, err := listLocalPhotos(dir)
	if err != nil {
		return nil, nil, err
	}
	fetch := func(_ context.Context, p *Photo) ([]byte, error) {
		return os.ReadFile(p.JPEG.Key)
	}
	return photos, fetch, nil
}

// ShouldSkip reports whether p needs no work (its JPEG ETag is unchanged and its
// thumbnail already exists). force always returns false. Exported for the worker
// producer, which applies the same idempotency check as the CLI.
func (c *Cataloger) ShouldSkip(p *Photo, force bool) (bool, error) {
	return c.shouldSkip(p, force)
}

// RunS3 catalogs photos from the configured bucket.
func (c *Cataloger) RunS3(ctx context.Context, opts RunOptions) (*RunStats, error) {
	photos, fetch, err := c.S3Source(ctx, opts.Prefix)
	if err != nil {
		return nil, err
	}
	return c.run(ctx, photos, opts, fetch)
}

// RunLocal catalogs JPEG+RAF pairs from a local directory (e.g. ./sample).
func (c *Cataloger) RunLocal(ctx context.Context, dir string, opts RunOptions) (*RunStats, error) {
	photos, fetch, err := c.LocalSource(dir)
	if err != nil {
		return nil, err
	}
	return c.run(ctx, photos, opts, fetch)
}

// run is the shared sequential loop. fetch returns the JPEG bytes for a photo.
func (c *Cataloger) run(ctx context.Context, photos []Photo, opts RunOptions, fetch func(context.Context, *Photo) ([]byte, error)) (*RunStats, error) {
	if opts.Limit > 0 && len(photos) > opts.Limit {
		photos = photos[:opts.Limit]
	}
	stats := &RunStats{Total: len(photos)}
	c.logf("%d photo(s) to consider", stats.Total)

	for i := range photos {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		p := &photos[i]

		skip, err := c.shouldSkip(p, opts.Force)
		if err != nil {
			return stats, err
		}
		if skip {
			stats.Skipped++
			c.logf("[%d/%d] skip   %s (unchanged)", i+1, stats.Total, p.KeyBase)
			continue
		}

		if err := c.processOne(ctx, p, fetch); err != nil {
			stats.Failed++
			c.logf("[%d/%d] FAIL   %s: %v", i+1, stats.Total, p.KeyBase, err)
			continue
		}
		stats.Processed++
		c.logf("[%d/%d] catalog %s  %s  %s  ISO%d f/%g %s %gmm",
			i+1, stats.Total, p.KeyBase, shortFilm(p.Meta.FilmSimulation),
			p.Meta.CameraModel, p.Meta.ISO, p.Meta.FNumber, p.Meta.ExposureTime, p.Meta.FocalLength)
	}
	return stats, nil
}

// ProcessNoStore performs the per-photo work that has NO database side effects:
// fetch the JPEG bytes, extract EXIF metadata, render the thumbnail — mutating p
// in place (p.Meta, p.ThumbPath). It is safe to call concurrently from a worker
// pool: it touches only p and writes a thumbnail to a path unique to p.KeyBase
// (atomically). The caller persists p via Store.Upsert. EXIF failures are logged
// and non-fatal (the thumbnail is still useful).
func (c *Cataloger) ProcessNoStore(ctx context.Context, p *Photo, fetch func(context.Context, *Photo) ([]byte, error)) error {
	data, err := fetch(ctx, p)
	if err != nil {
		return fmt.Errorf("fetch jpeg: %w", err)
	}

	meta, err := ExtractMetadata(data)
	if err != nil {
		// Non-fatal: keep cataloging (thumbnail still useful).
		c.logf("       warn   %s: exif: %v", p.KeyBase, err)
	}
	p.Meta = meta

	thumb := c.thumbPath(p.KeyBase)
	if _, _, err := GenerateThumbnail(data, meta.Orientation, c.cfg.ThumbSize, c.cfg.FastThumbs, thumb); err != nil {
		return fmt.Errorf("thumbnail: %w", err)
	}
	p.ThumbPath = thumb
	return nil
}

// processOne is the sequential CLI path: storeless work, then upsert. Splitting
// the store step out (ProcessNoStore) lets the web worker pool reuse the exact
// same per-photo work while funnelling every write through one goroutine.
func (c *Cataloger) processOne(ctx context.Context, p *Photo, fetch func(context.Context, *Photo) ([]byte, error)) error {
	if err := c.ProcessNoStore(ctx, p, fetch); err != nil {
		return err
	}
	return c.store.Upsert(p, time.Now())
}

func (c *Cataloger) shouldSkip(p *Photo, force bool) (bool, error) {
	if force {
		return false, nil
	}
	jpegETag, rafETag, found, err := c.store.ExistingETags(p.KeyBase)
	if err != nil {
		return false, err
	}
	if !found || jpegETag == "" || jpegETag != p.JPEG.ETag {
		return false, nil
	}
	// The JPEG ETag alone can't see a RAW sibling uploaded (or removed) since
	// the last run — reprocess when the listed sibling differs from the stored
	// one so raf_key/raf_etag get recorded without needing -force.
	listedRAF := ""
	if p.RAF != nil {
		listedRAF = p.RAF.ETag
	}
	if listedRAF != rafETag {
		return false, nil
	}
	return fileExists(c.thumbPath(p.KeyBase)), nil
}

// thumbPath mirrors the bucket layout under the thumbnails directory.
func (c *Cataloger) thumbPath(keyBase string) string {
	return filepath.Join(c.cfg.ThumbDir, filepath.FromSlash(keyBase)+".jpg")
}

func (c *Cataloger) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// listLocalPhotos pairs JPEG + RAF files in dir into Photos.
func listLocalPhotos(dir string) ([]Photo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	folder := filepath.Base(dir)
	byBase := map[string]*Photo{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToUpper(filepath.Ext(name))
		kind := classify(ext)
		if kind == kindOther {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		full := filepath.Join(dir, name)
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		base := folder + "/" + stem
		ref := ObjectRef{Key: full, Size: info.Size(), ETag: fmt.Sprintf("local-%d-%d", info.Size(), info.ModTime().UnixNano())}

		p := byBase[base]
		if p == nil {
			p = &Photo{KeyBase: base, Folder: folder, Name: stem}
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

	photos := make([]Photo, 0, len(byBase))
	for _, p := range byBase {
		if p.JPEG.Key == "" {
			continue
		}
		photos = append(photos, *p)
	}
	sort.Slice(photos, func(i, j int) bool { return photos[i].KeyBase < photos[j].KeyBase })
	return photos, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func shortFilm(s string) string {
	if s == "" {
		return "-"
	}
	if i := strings.IndexAny(s, "/+"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
