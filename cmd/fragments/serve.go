package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"fragments/catalog"
	"fragments/server"
	"fragments/worker"
)

// serveMain runs `fragments serve`: open the existing catalog and start the web
// server. It does not require S3 credentials (the gallery reads the local DB and
// thumbnails); those are only needed once the worker pool lands.
func serveMain(args []string) int {
	fs := flag.NewFlagSet("fragments serve", flag.ExitOnError)
	var (
		envPath   = fs.String("env", ".env", "path to the .env file")
		dataDir   = fs.String("data", "./data", "directory holding the SQLite DB and thumbnails")
		thumbSize = fs.Int("thumb", 1024, "thumbnail longest-edge size in pixels (must match the catalog)")
		addr      = fs.String("addr", "", "listen address (overrides FRAGMENTS_ADDR; default 127.0.0.1:8080)")
		network   = fs.Bool("network", false, "expose on the local network (bind all interfaces); off = localhost only")
	)
	_ = fs.Parse(args)

	logger := log.New(os.Stderr, "", log.LstdFlags)

	// -network (without an explicit -addr) binds all interfaces so the UI is
	// reachable from the Steam Deck / phone on the LAN. An explicit -addr always
	// wins; otherwise the default stays localhost-only (see server.LoadConfig).
	listenAddr := *addr
	if listenAddr == "" && *network {
		listenAddr = "0.0.0.0:8080"
	}

	cfg, err := catalog.LoadConfig(*envPath, *dataDir, *thumbSize)
	if err != nil {
		logger.Printf("config: %v", err)
		return 1
	}
	if err := os.MkdirAll(cfg.ThumbDir, 0o755); err != nil {
		logger.Printf("create thumbs dir: %v", err)
		return 1
	}

	store, err := catalog.OpenStore(cfg.DBPath)
	if err != nil {
		logger.Printf("open store: %v", err)
		return 1
	}
	defer store.Close()

	scfg, err := server.LoadConfig(cfg, listenAddr)
	if err != nil {
		logger.Printf("server config: %v", err)
		return 1
	}
	if scfg.SecretGenerated {
		logger.Printf("warning: FRAGMENTS_SECRET not set — generated a random one; sessions will not survive a restart. Set FRAGMENTS_SECRET to a long random string to persist logins.")
	}
	for _, u := range serveURLs(scfg.Addr) {
		logger.Printf("open %s", u)
	}
	if !exposedOnLAN(scfg.Addr) {
		logger.Printf("(localhost only — pass -network to reach it from other devices on your LAN)")
	}

	// Worker pool: shares the catalog package with the CLI; all DB writes funnel
	// through the coordinator's single writer goroutine.
	cataloger := catalog.NewCataloger(cfg, store)
	hub := worker.NewHub()
	coord := worker.NewCoordinator(store, cataloger, hub, logger.Printf, scfg.Workers)

	srv := server.New(scfg, cfg, store, coord, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Run(ctx); err != nil {
		logger.Printf("serve: %v", err)
		return 1
	}
	return 0
}

// exposedOnLAN reports whether addr binds all interfaces (reachable from the
// LAN) rather than just loopback.
func exposedOnLAN(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "" || host == "0.0.0.0" || host == "::"
}

// serveURLs builds the human-friendly URLs the server is reachable at: always
// localhost, plus every non-loopback IPv4 when bound to all interfaces, or the
// specific host otherwise.
func serveURLs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{"http://" + addr}
	}
	switch host {
	case "", "0.0.0.0", "::":
		urls := []string{"http://localhost:" + port}
		for _, ip := range lanIPv4s() {
			urls = append(urls, "http://"+net.JoinHostPort(ip, port))
		}
		return urls
	case "127.0.0.1", "localhost", "::1":
		return []string{"http://localhost:" + port}
	default:
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
}

// lanIPv4s returns the non-loopback IPv4 addresses of the host's up interfaces.
func lanIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				ips = append(ips, ip4.String())
			}
		}
	}
	return ips
}
