package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"fragments/catalog"
)

// backupMain runs `fragments backup [-data DIR] <dest.db>`: a consistent copy of
// the SQLite catalog via VACUUM INTO. The destination must not already exist.
func backupMain(args []string) int {
	fs := flag.NewFlagSet("fragments backup", flag.ExitOnError)
	dataDir := fs.String("data", "./data", "directory holding the SQLite DB")
	_ = fs.Parse(args)

	dest := fs.Arg(0)
	logger := log.New(os.Stderr, "", 0)
	if dest == "" {
		logger.Printf("usage: fragments backup [-data DIR] <dest.db>")
		return 2
	}
	if _, err := os.Stat(dest); err == nil {
		logger.Printf("refusing to overwrite existing file: %s", dest)
		return 1
	}

	cfg, err := catalog.LoadConfig("", *dataDir, 0)
	if err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	store, err := catalog.OpenStore(cfg.DBPath)
	if err != nil {
		logger.Printf("open store: %v", err)
		return 1
	}
	defer store.Close()

	if err := store.Backup(dest); err != nil {
		logger.Printf("backup: %v", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "backed up %s -> %s\n", cfg.DBPath, dest)
	return 0
}
