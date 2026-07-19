package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds everything needed for a cataloging run. S3 fields mirror the
// names already used in the project's .env (consumed by upload-to-s3.sh) so the
// same file works for both tools.
type Config struct {
	// S3-compatible object storage
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Region          string // e.g. "bhs"
	Endpoint        string // e.g. "s3.bhs.io.cloud.ovh.net" (scheme optional)
	Prefix          string // restrict listing to this key prefix (may be empty)
	UsePathStyle    bool   // force path-style addressing (S3_FORCE_PATH_STYLE=true)

	// Local output
	DataDir    string // root for DB + thumbnails
	DBPath     string // sqlite file path
	ThumbDir   string // directory for generated thumbnails
	ThumbSize  int    // longest-edge size in px for thumbnails
	FastThumbs bool   // use the cheaper ApproxBiLinear resampler (FRAGMENTS_FAST_THUMBS)
}

// LoadConfig reads the given .env file (ignored if absent) and the process
// environment, applies defaults, and validates the result.
func LoadConfig(envPath, dataDir string, thumbSize int) (*Config, error) {
	if envPath != "" {
		// Don't fail if the file is missing; real env vars may suffice.
		_ = godotenv.Load(envPath)
	}

	if dataDir == "" {
		dataDir = "./data"
	}
	if thumbSize <= 0 {
		thumbSize = 1024
	}

	endpoint := os.Getenv("S3_ENDPOINT")
	c := &Config{
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		Bucket:          os.Getenv("S3_BUCKET"),
		// The SigV4 signing region must match the endpoint's region (e.g.
		// "bhs"); a wrong default like us-east-1 fails against most providers.
		// Derive it from the endpoint host when S3_REGION is unset.
		Region:       firstNonEmpty(os.Getenv("S3_REGION"), regionFromEndpoint(endpoint)),
		Endpoint:     endpoint,
		Prefix:       os.Getenv("DEST_PREFIX"),
		UsePathStyle: strings.EqualFold(os.Getenv("S3_FORCE_PATH_STYLE"), "true"),
		DataDir:      dataDir,
		DBPath:       filepath.Join(dataDir, "catalog.db"),
		ThumbDir:     filepath.Join(dataDir, "thumbs"),
		ThumbSize:    thumbSize,
		FastThumbs:   strings.EqualFold(os.Getenv("FRAGMENTS_FAST_THUMBS"), "true"),
	}
	return c, nil
}

// Validate checks that the S3 credentials and target are present.
func (c *Config) Validate() error {
	var missing []string
	if c.AccessKeyID == "" {
		missing = append(missing, "S3_ACCESS_KEY_ID")
	}
	if c.SecretAccessKey == "" {
		missing = append(missing, "S3_SECRET_ACCESS_KEY")
	}
	if c.Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}
	if c.Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if c.Region == "" {
		missing = append(missing, "S3_REGION (or an endpoint of the form s3.<region>.io.cloud.ovh.net)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

// EndpointURL returns the endpoint with an https:// scheme if none was given.
func (c *Config) EndpointURL() string {
	if c.Endpoint == "" {
		return ""
	}
	if strings.Contains(c.Endpoint, "://") {
		return c.Endpoint
	}
	return "https://" + c.Endpoint
}

// regionFromEndpoint extracts the region from an S3 host of the form
// "s3.<region>.<provider-domain>" (e.g. "s3.bhs.io.cloud.ovh.net"),
// returning "" if it doesn't match.
func regionFromEndpoint(endpoint string) string {
	host := endpoint
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")
	parts := strings.Split(host, ".")
	if len(parts) >= 2 && parts[0] == "s3" {
		return parts[1]
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
