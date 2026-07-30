package main

// The upload engine shells out to the rclone binary, configured entirely via
// RCLONE_CONFIG_S3_* environment variables (no rclone.conf, no secrets on the
// command line) — the exact mechanism upload-to-s3.sh used. Keeping rclone as
// the engine preserves its battle-tested incremental copy, retries and progress
// display; a native aws-sdk-go-v2 engine can slot in behind runUpload later.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"fragments/catalog"
)

// rcloneInclude matches the media files worth uploading. Always passed
// together with --ignore-case so any case mix (.Jpg) matches, exactly like the
// case-insensitive stats in sources.go — the menu must never count a file the
// copy would then skip. Must stay in sync with mediaExts in sources.go.
const rcloneInclude = "*.{JPG,JPEG,RAF,NEF,CR2,CR3,ARW,DNG,ORF,RW2,MOV,MP4}"

// destOf returns the rclone destination ("s3:bucket" or "s3:bucket/prefix").
func destOf(cfg *catalog.Config) string {
	dest := "s3:" + cfg.Bucket
	if p := strings.Trim(cfg.Prefix, "/"); p != "" {
		dest += "/" + p
	}
	return dest
}

// rcloneEnv builds the process environment that defines the on-the-fly "s3"
// remote for rclone.
func rcloneEnv(cfg *catalog.Config) []string {
	env := append(os.Environ(),
		"RCLONE_CONFIG_S3_TYPE=s3",
		"RCLONE_CONFIG_S3_PROVIDER=Other",
		"RCLONE_CONFIG_S3_ACCESS_KEY_ID="+cfg.AccessKeyID,
		"RCLONE_CONFIG_S3_SECRET_ACCESS_KEY="+cfg.SecretAccessKey,
		"RCLONE_CONFIG_S3_ENDPOINT="+cfg.Endpoint,
		"RCLONE_CONFIG_S3_REGION="+cfg.Region,
	)
	// S3_ACL in .env: unset means "private" (the script's default); explicitly
	// empty means "send no ACL" for providers that reject ACLs on upload.
	acl := "private"
	if v, ok := os.LookupEnv("S3_ACL"); ok {
		acl = v
	}
	env = append(env, "RCLONE_CONFIG_S3_ACL="+acl)
	if cfg.UsePathStyle {
		env = append(env, "RCLONE_CONFIG_S3_FORCE_PATH_STYLE=true")
	}
	return env
}

// runRclone runs one rclone command with the on-the-fly remote, wired to the
// terminal so rclone's own progress display just works. Ctrl-C is left to
// rclone: the parent catches SIGINT into a discarded channel (signal.Ignore
// would set SIG_IGN, which the child inherits across exec — rclone would then
// never see Ctrl-C at all), so rclone shuts down cleanly and the parent
// survives to report its exit code.
func runRclone(cfg *catalog.Config, args ...string) int {
	cmd := exec.Command("rclone", args...)
	cmd.Env = rcloneEnv(cfg)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	defer signal.Stop(ch)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "❌ rclone : %v\n", err)
		return 1
	}
	return 0
}

// runUpload performs the incremental copy of srcDir to the bucket. Copy never
// deletes anything remotely; already-uploaded files are skipped.
func runUpload(cfg *catalog.Config, srcDir string, dryRun bool, extra []string) int {
	// Create the bucket if it does not exist yet. Non-fatal (it usually already
	// exists, and some credentials lack CreateBucket), but bounded — a dead
	// endpoint must not stall silently for minutes — and a failure is worth a
	// warning since the copy that follows will explain it. Skipped in dry-run:
	// a simulation must not change anything remotely (the bash script got this
	// wrong).
	if !dryRun {
		mkdir := exec.Command("rclone", "mkdir", "s3:"+cfg.Bucket,
			"--contimeout", "15s", "--retries", "1", "--low-level-retries", "2")
		mkdir.Env = rcloneEnv(cfg)
		if err := mkdir.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "⚠️  Impossible de vérifier/créer le bucket — la copie le confirmera.")
		}
	}

	args := []string{
		"copy", srcDir, destOf(cfg),
		"--include", rcloneInclude,
		"--ignore-case",
		"--transfers", "4",
		"--checkers", "8",
		"--fast-list",
		"--progress",
		"--stats", "10s",
		"--stats-one-line",
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, extra...)
	return runRclone(cfg, args...)
}

// checkRclone verifies rclone is available before showing any menu.
func checkRclone() error {
	if _, err := exec.LookPath("rclone"); err != nil {
		return errors.New("rclone n'est pas installé (https://rclone.org/install/)")
	}
	return nil
}
