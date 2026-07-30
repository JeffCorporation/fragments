// Command fragments-upload is the interactive replacement for upload-to-s3.sh:
// it detects mounted DCIM sources (SD cards, MTP/PTP devices exposed through
// gvfs or kio-fuse), shows their stats, and incrementally copies the selected
// one to the S3 bucket configured in .env — never deleting anything remotely.
// rclone (installed separately) is the transfer engine.
//
// Usage:
//
//	fragments-upload [flags] [extra rclone args]     interactive upload
//	fragments-upload ls|lsl|lsd|size|tree [args]     inspect the bucket
//	fragments-upload check [dir] [args]              compare local <-> bucket
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fragments/catalog"
)

// version is stamped by GoReleaser at release time (-X main.version=...).
var version = "dev"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("fragments-upload", flag.ExitOnError)
	var (
		envPath = fs.String("env", defaultEnvPath(), "fichier .env de configuration")
		srcDir  = fs.String("src", "", "dossier source à sauvegarder (désactive le menu)")
		dryRun  = fs.Bool("dry-run", false, "simulation : ne transfère rien")
		yes     = fs.Bool("yes", false, "ne pas demander de confirmation")
		listSrc = fs.Bool("list", false, "lister les sources détectées puis quitter")
		showVer = fs.Bool("version", false, "afficher la version")
	)
	fs.Usage = func() { usage(os.Stderr) }

	// Like the bash script's "$@", anything that is not one of our own flags is
	// handed to rclone verbatim (e.g. --bwlimit 1M), so pre-split before Parse:
	// the stdlib flag package would otherwise reject unknown flags.
	own, rest := splitArgs(fs, args)
	_ = fs.Parse(own)

	if *showVer {
		fmt.Println("fragments-upload " + version)
		return 0
	}
	if *listSrc {
		return listSources()
	}

	// Bucket-inspection verbs mirror upload-to-s3.sh; everything after the verb
	// is handed to rclone verbatim. Flags like -env may precede the verb.
	if len(rest) > 0 {
		switch rest[0] {
		case "ls", "lsl", "lsd", "size", "tree":
			cfg, code := loadConfig(*envPath)
			if code != 0 {
				return code
			}
			return runRclone(cfg, append([]string{rest[0], destOf(cfg)}, rest[1:]...)...)
		case "check":
			return runCheck(*envPath, rest[1:])
		}
	}

	cfg, code := loadConfig(*envPath)
	if code != 0 {
		return code
	}
	if err := checkRclone(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		return 1
	}

	src, code := resolveSource(*srcDir, "-src /chemin/vers/DCIM")
	if code != 0 {
		return code
	}

	fmt.Printf("\n📤 Source      : %s\n", src.Path)
	fmt.Printf("   %s · %s · %s\n", frenchCount(src.Files), frenchSize(src.Size), frenchAgo(src.LastMod, time.Now()))
	fmt.Printf("🪣 Destination : %s\n", destOf(cfg))
	fmt.Printf("🌐 Endpoint    : %s (région %s)\n\n", cfg.Endpoint, orAuto(cfg.Region))

	if !*yes {
		// Fail closed without a terminal: a blank line on piped stdin must not
		// auto-approve an upload.
		if !isTerminal(os.Stdin) {
			fmt.Fprintln(os.Stderr, "❌ Pas de terminal pour confirmer : utilisez -yes")
			return 1
		}
		if !confirm() {
			fmt.Fprintln(os.Stderr, "Annulé.")
			return 1
		}
	}
	code = runUpload(cfg, src.Path, *dryRun, rest)
	if code == 0 {
		fmt.Println("\n✅ Terminé.")
	}
	return code
}

// splitArgs separates the tool's own flags (registered on fs) from everything
// rclone should receive. It stops claiming tokens at the first positional
// argument, the first unknown flag, or an explicit "--" separator; the rest
// passes through untouched.
func splitArgs(fs *flag.FlagSet, args []string) (own, rest []string) {
	isBool := func(f *flag.Flag) bool {
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		return ok && b.IsBoolFlag()
	}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			return own, args[i+1:]
		}
		if len(a) < 2 || a[0] != '-' {
			break // first positional: verb or passthrough
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name == "h" || name == "help" {
			own = append(own, a) // let the flag package print usage
			i++
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			break // unknown flag: rclone's business
		}
		own = append(own, a)
		i++
		if !hasValue && !isBool(f) && i < len(args) {
			own = append(own, args[i]) // the flag's value
			i++
		}
	}
	if i == len(args) {
		return own, nil
	}
	return own, args[i:]
}

// runCheck handles `fragments-upload check [dir] [rclone args]`: verify that
// the source's media files all exist on the bucket. Without an explicit dir it
// falls back to the interactive picker.
func runCheck(envPath string, args []string) int {
	cfg, code := loadConfig(envPath)
	if code != 0 {
		return code
	}
	var srcDir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		srcDir, args = args[0], args[1:]
	}
	src, code := resolveSource(srcDir, "fragments-upload check /chemin/vers/DCIM")
	if code != 0 {
		return code
	}
	rcArgs := []string{"check", src.Path, destOf(cfg), "--include", rcloneInclude, "--ignore-case"}
	return runRclone(cfg, append(rcArgs, args...)...)
}

// resolveSource turns an explicit source dir into a Source (with stats), or
// runs discovery + the interactive menu when none was given. hint is the
// command-specific way to pass a source explicitly, shown in error messages.
func resolveSource(srcDir, hint string) (*Source, int) {
	if srcDir != "" {
		// Absolute path: rclone would misread a relative path containing ':' as
		// a remote name, or one starting with '-' as a flag.
		if abs, err := filepath.Abs(srcDir); err == nil {
			srcDir = abs
		}
		info, err := os.Stat(srcDir)
		if err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "❌ Source introuvable ou pas un dossier : %s\n", srcDir)
			return nil, 1
		}
		src := Source{Volume: srcDir, Path: srcDir}
		src.Files, src.Size, src.LastMod = statsOf(srcDir)
		return &src, 0
	}

	if !isTerminal(os.Stdin) || !isTerminal(os.Stderr) {
		fmt.Fprintf(os.Stderr, "❌ Pas de terminal interactif : indiquez la source directement (%s)\n", hint)
		return nil, 1
	}

	fmt.Fprintln(os.Stderr, "🔍 Recherche de sources (DCIM) sur les volumes montés…")
	sources := discoverSources()
	if len(sources) == 0 {
		fmt.Fprint(os.Stderr, noSourceHint)
		return nil, 1
	}
	src, err := pickSource(sources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Menu : %v\n", err)
		return nil, 1
	}
	if src == nil {
		fmt.Fprintln(os.Stderr, "Annulé.")
		return nil, 1
	}
	return src, 0
}

const noSourceHint = `Aucune source DCIM détectée.
  · Insérez une carte SD (ou branchez un lecteur de carte).
  · Téléphone / appareil en MTP-PTP : ouvrez-le d'abord dans Fichiers ou
    Dolphin pour le monter, puis relancez.
  · Ou indiquez le dossier directement : fragments-upload -src /chemin/vers/DCIM
`

// listSources prints the detected sources without any menu (also handy for
// scripts and debugging).
func listSources() int {
	sources := discoverSources()
	if len(sources) == 0 {
		fmt.Fprint(os.Stderr, noSourceHint)
		return 1
	}
	now := time.Now()
	for _, s := range sources {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", s.Volume, s.Path,
			frenchCount(s.Files), frenchSize(s.Size), frenchAgo(s.LastMod, now))
	}
	return 0
}

// loadConfig loads .env + process env via the shared catalog loader and checks
// the S3 fields rclone needs. Note: real environment variables take precedence
// over .env values (the 12-factor convention, shared with the fragments CLI) —
// the bash script had it the other way around.
func loadConfig(envPath string) (*catalog.Config, int) {
	cfg, err := catalog.LoadConfig(envPath, "", 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Configuration : %v\n", err)
		return nil, 1
	}
	// Script parity: hand rclone exactly what the user configured. An empty
	// S3_REGION means "let the provider decide" — don't substitute the region
	// catalog.LoadConfig derives from the endpoint (the aws-sdk needs one, but
	// it can guess wrong, e.g. "wasabisys" from s3.wasabisys.com).
	cfg.Region = os.Getenv("S3_REGION")
	var missing []string
	for _, f := range []struct{ v, name string }{
		{cfg.AccessKeyID, "S3_ACCESS_KEY_ID"},
		{cfg.SecretAccessKey, "S3_SECRET_ACCESS_KEY"},
		{cfg.Bucket, "S3_BUCKET"},
		{cfg.Endpoint, "S3_ENDPOINT"},
	} {
		if f.v == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		if _, statErr := os.Stat(envPath); os.IsNotExist(statErr) {
			fmt.Fprintf(os.Stderr, "❌ Fichier de configuration introuvable : %s\n", envPath)
		} else {
			fmt.Fprintf(os.Stderr, "❌ Variables manquantes dans %s : %s\n", envPath, strings.Join(missing, ", "))
		}
		fmt.Fprintln(os.Stderr, "   Créez le fichier à partir du modèle :  cp .env.example .env")
		return nil, 1
	}
	return cfg, 0
}

// defaultEnvPath honours the ENV_FILE override the bash script supported.
func defaultEnvPath() string {
	if p := os.Getenv("ENV_FILE"); p != "" {
		return p
	}
	return ".env"
}

// confirm asks the user to proceed; Enter or "o"/"oui" means yes. The prompt
// goes to stderr like the rest of the interactive UI.
func confirm() bool {
	fmt.Fprint(os.Stderr, "Continuer ? [O/n] ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "o", "oui":
		return true
	}
	return false
}

// isTerminal reports whether f is attached to a terminal (character device).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func orAuto(s string) string {
	if s == "" {
		return "auto"
	}
	return s
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `fragments-upload %s — sauvegarde photos/vidéos vers un bucket S3 (via rclone).

Le mode "copy" ajoute les nouveaux fichiers sur le bucket et NE SUPPRIME
JAMAIS rien ; l'opération est incrémentale (les fichiers déjà envoyés sont
sautés). Sans -src, un menu interactif propose les dossiers DCIM détectés
sur les volumes montés (/run/media, /media, appareils MTP-PTP via GNOME/KDE).

Usage :
  fragments-upload [options] [args rclone supplémentaires]
  fragments-upload ls|lsl|lsd|size|tree [args rclone]   inspecter le bucket
  fragments-upload check [dossier] [args rclone]        vérifier local <-> bucket

Toute option inconnue est transmise telle quelle à rclone (ex. --bwlimit 1M) ;
"--" force la coupure : tout ce qui suit part à rclone.

Options :
  -env fichier   fichier .env (défaut : .env, ou $ENV_FILE)
  -src dossier   source à sauvegarder (désactive le menu)
  -dry-run       simulation : ne transfère rien
  -yes           ne pas demander de confirmation
  -list          lister les sources détectées puis quitter
  -version       afficher la version
`, version)
}
