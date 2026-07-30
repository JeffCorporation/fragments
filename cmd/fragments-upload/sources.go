package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// mediaExts is the set of photo/video extensions we upload (upper-case, with
// dot; matching is case-insensitive, like rclone's --ignore-case filter). It
// must stay in sync with rcloneInclude in rclone.go.
var mediaExts = map[string]bool{
	".JPG": true, ".JPEG": true,
	".RAF": true, ".NEF": true, ".CR2": true, ".CR3": true,
	".ARW": true, ".DNG": true, ".ORF": true, ".RW2": true,
	".MOV": true, ".MP4": true,
}

// Source is a candidate DCIM folder found on a mounted volume.
type Source struct {
	Volume  string    // volume name shown in the menu (mount-point basename, prettified for gvfs)
	Path    string    // absolute path of the DCIM directory
	Files   int       // media files found under Path
	Size    int64     // total size of those files, in bytes
	LastMod time.Time // most recent modification time among them
}

// dcimSearchDepth bounds the DCIM lookup below a volume root. Depth 1 covers SD
// cards (VOLUME/DCIM); depth 2-3 covers MTP/PTP shares such as
// "Internal storage/DCIM" or "store_00010001/DCIM".
const dcimSearchDepth = 3

// volume is a mounted filesystem that may hold a DCIM directory.
type volume struct {
	name string // display name for the menu
	path string // mount point
}

// candidateVolumes enumerates mounted volumes worth scanning: udisks2 mounts
// (/run/media/$USER, the Fedora/systemd default), the Debian convention
// (/media, /media/$USER), plus the FUSE gateways desktops use for MTP/PTP
// devices — gvfs on GNOME, kio-fuse on KDE. Roots absent on this system are
// simply skipped.
func candidateVolumes() []volume {
	var vols []volume

	var roots []string
	if u, err := user.Current(); err == nil {
		roots = append(roots,
			filepath.Join("/run/media", u.Username),
			filepath.Join("/media", u.Username),
		)
	}
	roots = append(roots, "/media")
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				vols = append(vols, volume{e.Name(), filepath.Join(root, e.Name())})
			}
		}
	}

	// GNOME gvfs: /run/user/$UID/gvfs/{mtp,gphoto2}:host=<device>. Only take
	// those two schemes — walking sftp:/dav: network mounts would be slow.
	gvfs := filepath.Join(runtimeDir(), "gvfs")
	if entries, err := os.ReadDir(gvfs); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "mtp:host=") || strings.HasPrefix(e.Name(), "gphoto2:host=") {
				vols = append(vols, volume{prettyVolumeName(e.Name()), filepath.Join(gvfs, e.Name())})
			}
		}
	}

	// KDE kio-fuse: /run/user/$UID/kio-fuse-XXXXXX/{mtp,camera}/<device>.
	// Mounts appear on demand once the user opens the device in Dolphin.
	kioRoots, _ := filepath.Glob(filepath.Join(runtimeDir(), "kio-fuse-*"))
	for _, kio := range kioRoots {
		for proto, suffix := range map[string]string{"mtp": " (MTP)", "camera": " (PTP)"} {
			entries, err := os.ReadDir(filepath.Join(kio, proto))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					vols = append(vols, volume{e.Name() + suffix, filepath.Join(kio, proto, e.Name())})
				}
			}
		}
	}
	return vols
}

// runtimeDir returns the user runtime dir hosting the FUSE gateways.
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return filepath.Join("/run/user", fmt.Sprint(os.Getuid()))
}

// discoverTimeout bounds the whole discovery pass. FUSE mounts (gvfs,
// kio-fuse) can wedge indefinitely on a syscall; past the deadline we show
// whatever completed rather than hang the app.
const discoverTimeout = 10 * time.Second

// discoverSources scans every candidate volume for DCIM directories and
// computes their stats. Each volume is handled in its own goroutine (FUSE
// I/O is slow, and one wedged mount must not block the others); after
// discoverTimeout the partial result is returned — blocked FUSE syscalls
// cannot be cancelled, so the stragglers are simply abandoned (the process
// is short-lived). Results are sorted most-recently-modified first — the
// card the user just pulled out of the camera is almost always the one they
// want.
func discoverSources() []Source {
	var (
		mu      sync.Mutex
		seen    = map[string]bool{} // resolved DCIM paths, to dedupe overlapping roots
		sources []Source
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		// candidateVolumes touches the FUSE roots too, hence inside the
		// deadline-guarded goroutine.
		for _, vol := range candidateVolumes() {
			wg.Add(1)
			go func(vol volume) {
				defer wg.Done()
				for _, dcim := range findDCIM(vol.path, dcimSearchDepth) {
					resolved, err := filepath.EvalSymlinks(dcim)
					if err != nil {
						resolved = dcim
					}
					mu.Lock()
					dup := seen[resolved]
					seen[resolved] = true
					mu.Unlock()
					if dup {
						continue
					}
					src := Source{Volume: vol.name, Path: dcim}
					src.Files, src.Size, src.LastMod = statsOf(dcim)
					mu.Lock()
					sources = append(sources, src)
					mu.Unlock()
				}
			}(vol)
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(discoverTimeout):
		fmt.Fprintln(os.Stderr, "⚠️  Certains volumes ne répondent pas — liste partielle.")
	}

	mu.Lock()
	out := append([]Source(nil), sources...)
	mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastMod.Equal(out[j].LastMod) {
			return out[i].LastMod.After(out[j].LastMod)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// findDCIM returns the DCIM directories under root, searching at most depth
// levels down. Symlinks are not followed and hidden directories are skipped.
func findDCIM(root string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(root, e.Name())
		if strings.EqualFold(e.Name(), "DCIM") {
			found = append(found, path)
			continue // a DCIM inside a DCIM would be double-counted
		}
		found = append(found, findDCIM(path, depth-1)...)
	}
	return found
}

// statsOf walks dir and returns the media-file count, total size and most
// recent modification time. I/O errors skip the offending entry: a partial
// number beats no menu entry at all.
func statsOf(dir string) (files int, size int64, lastMod time.Time) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if !mediaExts[strings.ToUpper(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files++
		size += info.Size()
		if info.ModTime().After(lastMod) {
			lastMod = info.ModTime()
		}
		return nil
	})
	return files, size, lastMod
}

// prettyVolumeName turns a mount-point basename into a menu label. gvfs names
// URL-ish entries like "mtp:host=SAMSUNG_SAMSUNG_Android_RF8M33ZDXHV"; strip
// the scheme and de-underscore those. Plain volume labels pass through as-is.
func prettyVolumeName(name string) string {
	scheme, rest, ok := strings.Cut(name, ":host=")
	if !ok {
		return name
	}
	pretty := strings.TrimSpace(strings.ReplaceAll(rest, "_", " "))
	if pretty == "" {
		return name
	}
	switch scheme {
	case "mtp":
		return pretty + " (MTP)"
	case "gphoto2":
		return pretty + " (PTP)"
	}
	return pretty
}
