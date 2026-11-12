// Command counterweight runs the Counterweight backend: the PostgreSQL graph stores,
// the event-driven exploration engine, and the JSON HTTP API consumed by the
// shadcn/ui frontend.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/Philocyber/counterweight/locale"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/config"
	"github.com/Philocyber/counterweight/recontools"
	"github.com/Philocyber/counterweight/selfupdate"
	"github.com/Philocyber/counterweight/server"
	"github.com/Philocyber/counterweight/traffic"
)

// version is the build version, injected at release time via
// -ldflags "-X main.version=<tag>". Defaults to "dev" for local builds.
var version = "dev"

const banner = `
  Counterweight
`

// printBanner writes the startup banner + version/runtime info to stdout.
func printBanner(addr string) {
	fmt.Print(banner)
	fmt.Println(locale.Text(locale.ServerDefault(), "  Autonomous AI penetration-testing system"))
	fmt.Printf(locale.Text(locale.ServerDefault(), "  Version %s  ·  %s/%s  ·  %s  ·  Listening on %s\n\n"),
		version, runtime.GOOS, runtime.GOARCH, runtime.Version(), addr)
}

// main only maps run's result onto the process exit code. The exit code is part
// of the update protocol — the supervising start script reads it to decide
// whether to relaunch us (see selfupdate.ExitRestart) — so the body has to live
// in a function that can *return* rather than os.Exit past its own defers.
func main() {
	lang, _ := locale.FromEnv(os.Getenv)
	locale.SetServerDefault(lang)
	os.Exit(dispatch(os.Args[1:]))
}

func run() int {
	var (
		addr          = flag.String("addr", defaultManagementAddress, "management listen address")
		tlsCert       = flag.String("tls-cert", "", "management HTTPS certificate PEM file")
		tlsKey        = flag.String("tls-key", "", "management HTTPS private-key PEM file")
		dataDir       = flag.String("data", filepath.Join(config.BaseDir(), "data"), "data directory for SQLite stores (default: data/ next to the executable)")
		proxy         = flag.String("proxy", "127.0.0.1:8788", "traffic recording proxy address (empty to disable)")
		targetCA      = flag.String("target-tls-ca", "", "additional PEM CA for recording upstream HTTPS")
		targetTLS     = flag.String("target-tls-insecure-origins", "", "comma-separated exact HTTPS recording origins with explicitly authorized unverified certificates")
		setupToken    = flag.Bool("setup-token", false, "print the local first-owner setup token and exit")
		resetPassword = flag.Bool("reset-password", false, "reset the administrator password using COUNTERWEIGHT_RESET_NEWPASS and exit")
	)
	flag.Parse()
	if *resetPassword {
		if err := resetAdministratorPassword(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("Administrator password reset; earlier sessions are invalidated on their next request")
		return 0
	}
	if *setupToken {
		token, err := server.ReadSetupToken(config.KeyDir())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(token)
		return 0
	}
	tlsConfig, err := managementTLS(*tlsCert, *tlsKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	managementAddr, err := managementAddress(*addr, tlsConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	*addr = managementAddr

	// hand the build version to the server package so GET /api/health can report it
	// to the frontend top bar.
	server.BuildVersion = version

	printBanner(*addr)

	// capture backend logs into the in-memory sink (still to stderr) so the /logs
	// page can show a live log stream. Do this first, to catch startup logs too.
	server.StartLogCapture()

	// Self-update bootstrap: swap in a staged binary, or count a post-swap boot
	// attempt and roll back if the new build keeps dying. Must run before we open
	// the stores or bind a port — this may end with "exit and let the start script
	// relaunch me", and there is no point paying for either first.
	action, upState := selfupdate.Bootstrap()
	server.SetBootUpdateState(upState)
	if action == selfupdate.Restart {
		return selfupdate.ExitRestart
	}

	// surface which config file the binary reads (absolute, so `go run`'s relative
	// "config.json" — resolved against the CWD — is unambiguous).
	cfgPath := config.Path()
	if abs, e := filepath.Abs(cfgPath); e == nil {
		cfgPath = abs
	}
	if _, e := os.Stat(cfgPath); e == nil {
		log.Printf(locale.Text(locale.ServerDefault(), "[config] Configuration file: %s"), cfgPath)
	} else {
		log.Printf(locale.Text(locale.ServerDefault(), "[config] Configuration file: %s (not found; using COUNTERWEIGHT_PG_DSN only)"), cfgPath)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, shutdown := shutdownContext(sigCtx)
	defer shutdown(agent.AbortShutdown)

	mgr, err := server.NewManager(*dataDir, *proxy, traffic.TLSOptions{CAFile: *targetCA, InsecureOrigins: strings.Split(*targetTLS, ",")})
	if err != nil {
		log.Fatalf("open stores: %v", err)
	}
	defer mgr.Close()

	// Make any recon tool installed through the one-click installer (System →
	// Tools) immediately runnable by name from Bash, without per-tool PATH
	// wiring: prepend its managed bin directory once, here, regardless of
	// whether anything has been installed into it yet.
	if binDir := recontools.BinDir(*dataDir); binDir != "" {
		if err := os.MkdirAll(binDir, 0o755); err == nil {
			_ = os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	}

	// Surviving this long means a freshly swapped-in build actually works, so drop
	// the upgrade marker and stop counting attempts. Until it fires, every boot
	// increments the count and a build that keeps dying gets rolled back.
	settle := time.AfterFunc(selfupdate.SettleDelay, selfupdate.Settle)
	defer settle.Stop()

	skillDir := config.SkillDir()
	if abs, err := filepath.Abs(skillDir); err == nil {
		skillDir = abs
	}
	log.Printf(locale.Text(locale.ServerDefault(), "[config] Skill directory: %s"), skillDir)
	srv := server.New(ctx, mgr, skillDir, *dataDir, config.KeyDir())
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsConfig,
	}

	go func() {
		log.Printf("Counterweight %s backend listening on %s (data=%s, workers=%d)", version, *addr, *dataDir, mgr.Workers())
		serve := httpSrv.ListenAndServe
		if tlsConfig != nil {
			serve = func() error { return httpSrv.ListenAndServeTLS("", "") }
		}
		if err := serve(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	// Two ways out: a signal (normal stop → exit 0, the start script stops looping)
	// or a staged update / rollback (→ exit 75, the script relaunches us and the
	// bootstrap above installs the new build).
	code := 0
	select {
	case <-ctx.Done():
	case <-server.RestartRequested():
		code = selfupdate.ExitRestart
		shutdown(agent.AbortShutdown)
	}

	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	return code
}

// shutdownContext deliberately does not derive from signalCtx. If it did, the
// parent's plain context.Canceled could win the race before AbortShutdown was
// attached to the child, losing the diagnostic cause in every running Agent.
func shutdownContext(signalCtx context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, shutdown := context.WithCancelCause(context.Background())
	go func() {
		select {
		case <-signalCtx.Done():
			shutdown(agent.AbortShutdown)
		case <-ctx.Done():
		}
	}()
	return ctx, shutdown
}
