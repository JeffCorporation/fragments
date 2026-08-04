package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fragments/catalog"
)

// backupMain runs `fragments backup [-env FILE] [-data DIR] [-name NAME] [-keep N]`:
// a consistent snapshot of the SQLite catalog (VACUUM INTO) uploaded to S3
// under backups/. With -keep N, older timestamped backups beyond the N most
// recent are deleted after a successful upload.
func backupMain(args []string) int {
	fs := flag.NewFlagSet("fragments backup", flag.ExitOnError)
	var (
		envPath = fs.String("env", ".env", "path to the .env file with S3 credentials")
		dataDir = fs.String("data", "./data", "directory holding the SQLite DB")
		name    = fs.String("name", "", "backup name (default: catalog-<UTC timestamp>.db)")
		keep    = fs.Int("keep", 0, "keep only the N most recent timestamped backups (0 = keep all)")
	)
	_ = fs.Parse(args)

	logger := log.New(os.Stderr, "", 0)
	if fs.NArg() > 0 {
		logger.Printf(`local file backup was removed; "fragments backup" now uploads to S3 (run "fragments backup -h")`)
		return 2
	}

	cfg, err := catalog.LoadConfig(*envPath, *dataDir, 0)
	if err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	if err := cfg.Validate(); err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	key, err := catalog.BackupKey(*name, time.Now())
	if err != nil {
		logger.Printf("%v", err)
		return 2
	}

	store, err := catalog.OpenStore(cfg.DBPath)
	if err != nil {
		logger.Printf("open store: %v", err)
		return 1
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bucket, err := catalog.NewBackupBucket(ctx, cfg)
	if err != nil {
		logger.Printf("s3: %v", err)
		return 1
	}

	size, err := catalog.UploadBackup(ctx, store, bucket, cfg.DataDir, key)
	if err != nil {
		logger.Printf("backup: %v", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "backed up catalog (%s) to s3://%s/%s\n", humanSize(size), cfg.BackupBucket, key)

	if *keep > 0 {
		pruned, err := catalog.PruneBackups(ctx, bucket, *keep)
		switch {
		case err != nil:
			// The backup itself succeeded; a failed rotation is a warning.
			logger.Printf("warning: prune old backups: %v", err)
		case len(pruned) > 0:
			names := make([]string, len(pruned))
			for i, k := range pruned {
				names[i] = catalog.BackupDisplayName(k)
			}
			fmt.Fprintf(os.Stderr, "pruned %d old backup(s): %s\n", len(pruned), strings.Join(names, ", "))
		}
	}
	return 0
}
