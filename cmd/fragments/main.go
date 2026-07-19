// Command fragments catalogs photos (from an S3 bucket or a local folder) and
// serves the web UI used to grade them.
//
// Usage:
//
//	fragments scan [flags]     catalog photos (sequential CLI)
//	fragments serve [flags]    run the web server over an existing catalog
//	fragments backup [flags]   back up the catalog database
//
// With no command (or -h/--help) it prints this usage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fragments/catalog"
)

// version is stamped by GoReleaser at release time (-X main.version=...).
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		os.Exit(catalogMain(os.Args[2:]))
	case "serve":
		os.Exit(serveMain(os.Args[2:]))
	case "backup":
		os.Exit(backupMain(os.Args[2:]))
	case "-h", "--help", "help":
		usage(os.Stdout)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "fragments: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

// usage prints the top-level command list. Each subcommand has its own -h for
// the full flag set (e.g. `fragments scan -h`).
func usage(w io.Writer) {
	fmt.Fprintf(w, `fragments %s — catalogue, browse and grade your photos.

Usage:
  fragments <command> [flags]

Commands:
  scan     catalog photos from S3 (or a local folder with -local) into ./data
  serve    run the web UI over an existing catalog (localhost; -network for LAN)
  backup   back up the catalog database

Run "fragments <command> -h" for the flags of a command.
`, version)
}

// catalogMain runs the sequential cataloging CLI and returns a process exit
// code. Using a helper (rather than log.Fatalf) ensures deferred cleanup runs
// and partial results are reported even on interruption.
func catalogMain(args []string) int {
	fs := flag.NewFlagSet("fragments scan", flag.ExitOnError)
	var (
		envPath   = fs.String("env", ".env", "path to the .env file with S3 credentials")
		dataDir   = fs.String("data", "./data", "output directory for the SQLite DB and thumbnails")
		thumbSize = fs.Int("thumb", 1024, "thumbnail longest-edge size in pixels")
		limit     = fs.Int("limit", 0, "process at most N photos (0 = all)")
		prefix    = fs.String("prefix", "", "S3 key prefix to restrict the listing (e.g. 100_FUJI/); S3 mode only")
		force     = fs.Bool("force", false, "reprocess photos even if unchanged")
		local     = fs.String("local", "", "process this local directory instead of S3 (e.g. ./sample)")
	)
	_ = fs.Parse(args)

	logger := log.New(os.Stderr, "", log.Ltime)

	cfg, err := catalog.LoadConfig(*envPath, *dataDir, *thumbSize)
	if err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		logger.Printf("create data dir: %v", err)
		return 1
	}

	store, err := catalog.OpenStore(cfg.DBPath)
	if err != nil {
		logger.Printf("open store: %v", err)
		return 1
	}
	defer store.Close()

	cat := catalog.NewCataloger(cfg, store)
	cat.Logf = logger.Printf

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *local != "" && *prefix != "" {
		logger.Printf("note: -prefix is ignored in -local mode")
	}
	opts := catalog.RunOptions{Limit: *limit, Force: *force, Prefix: *prefix}

	start := time.Now()
	var stats *catalog.RunStats
	if *local != "" {
		logger.Printf("mode: local %s", *local)
		stats, err = cat.RunLocal(ctx, *local, opts)
	} else {
		if verr := cfg.Validate(); verr != nil {
			logger.Printf("config: %v (use -local ./sample to test offline)", verr)
			return 1
		}
		logger.Printf("mode: s3 bucket=%s endpoint=%s region=%s", cfg.Bucket, cfg.Endpoint, cfg.Region)
		stats, err = cat.RunS3(ctx, opts)
	}

	// Always report whatever progress was made.
	if stats != nil {
		logger.Printf("done in %s: %d processed, %d skipped, %d failed (of %d considered)",
			time.Since(start).Round(time.Millisecond), stats.Processed, stats.Skipped, stats.Failed, stats.Total)
	}
	if total, cerr := store.Count(); cerr == nil {
		logger.Printf("catalog now holds %d photo(s) at %s; thumbnails in %s", total, cfg.DBPath, cfg.ThumbDir)
	}

	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled):
		logger.Printf("interrupted; partial results saved")
		return 0
	default:
		logger.Printf("run: %v", err)
		return 1
	}
}
