package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"fragments/catalog"
)

// restoreMain runs `fragments restore [-env FILE] [-data DIR] [-name NAME] [-force]`:
// downloads a backup from S3 (the most recent one when -name is not given),
// verifies it, and installs it as the local catalog.
func restoreMain(args []string) int {
	fs := flag.NewFlagSet("fragments restore", flag.ExitOnError)
	var (
		envPath = fs.String("env", ".env", "path to the .env file with S3 credentials")
		dataDir = fs.String("data", "./data", "directory holding the SQLite DB")
		name    = fs.String("name", "", "backup to restore (default: the most recent)")
		force   = fs.Bool("force", false, "replace an existing catalog.db")
	)
	_ = fs.Parse(args)

	logger := log.New(os.Stderr, "", 0)
	cfg, err := catalog.LoadConfig(*envPath, *dataDir, 0)
	if err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	if err := cfg.Validate(); err != nil {
		logger.Printf("config: %v", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bucket, err := catalog.NewBackupBucket(ctx, cfg)
	if err != nil {
		logger.Printf("s3: %v", err)
		return 1
	}

	var key string
	if *name != "" {
		if key, err = catalog.BackupKey(*name, time.Now()); err != nil {
			logger.Printf("%v", err)
			return 2
		}
	} else {
		backups, err := catalog.ListBackups(ctx, bucket)
		if err != nil {
			logger.Printf("list backups: %v", err)
			return 1
		}
		if len(backups) == 0 {
			logger.Printf("no backups found in s3://%s/backups/", cfg.BackupBucket)
			return 1
		}
		key = backups[len(backups)-1].Key // oldest-first: last one is the newest
	}

	_, statErr := os.Stat(cfg.DBPath)
	hadOld := statErr == nil

	if err := catalog.RestoreBackup(ctx, bucket, key, cfg.DBPath, *force); err != nil {
		logger.Printf("restore: %v", err)
		return 1
	}

	// Reopen immediately so an older backup runs its pending migrations now
	// rather than at the next serve, and to report what was restored.
	store, err := catalog.OpenStore(cfg.DBPath)
	if err != nil {
		logger.Printf("restored, but reopening the catalog failed: %v", err)
		return 1
	}
	photos, _ := store.Count()
	store.Close()

	fmt.Fprintf(os.Stderr, "restored %s: %d photos\n", catalog.BackupDisplayName(key), photos)
	if hadOld {
		fmt.Fprintf(os.Stderr, "previous catalog kept at %s.pre-restore\n", cfg.DBPath)
	}
	return 0
}

// backupsMain runs `fragments backups [-env FILE]`: lists the backups stored in
// S3, oldest first.
func backupsMain(args []string) int {
	fs := flag.NewFlagSet("fragments backups", flag.ExitOnError)
	envPath := fs.String("env", ".env", "path to the .env file with S3 credentials")
	_ = fs.Parse(args)

	logger := log.New(os.Stderr, "", 0)
	cfg, err := catalog.LoadConfig(*envPath, "", 0)
	if err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	if err := cfg.Validate(); err != nil {
		logger.Printf("config: %v", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bucket, err := catalog.NewBackupBucket(ctx, cfg)
	if err != nil {
		logger.Printf("s3: %v", err)
		return 1
	}
	backups, err := catalog.ListBackups(ctx, bucket)
	if err != nil {
		logger.Printf("list backups: %v", err)
		return 1
	}
	if len(backups) == 0 {
		fmt.Printf("no backups yet in s3://%s/backups/\n", cfg.BackupBucket)
		return 0
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tLAST MODIFIED")
	for _, b := range backups {
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			catalog.BackupDisplayName(b.Key), humanSize(b.Size),
			b.LastModified.Local().Format("2006-01-02 15:04:05"))
	}
	w.Flush()
	fmt.Printf("\n%d backup(s); \"fragments restore\" restores the last one\n", len(backups))
	return 0
}

// humanSize renders a byte count for CLI messages (binary units).
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
