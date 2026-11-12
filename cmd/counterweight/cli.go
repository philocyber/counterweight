package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Philocyber/counterweight/cmd/internal/buildtool"
	"github.com/Philocyber/counterweight/selfupdate"
)

// dispatch is the single entry point for the counterweight CLI. One binary, a
// handful of subcommands:
//
//	counterweight serve [flags]     run the backend (default when no subcommand)
//	counterweight dev [flags]       backend + traffic proxy + frontend hot reload
//	counterweight build [flags]     compile the embedded single binary
//	counterweight setup-token       print the first-owner setup token and exit
//	counterweight reset-password    reset the admin password and exit
//	counterweight supervise [flags] restart loop (used by start.sh / start.bat)
//	counterweight authz run|validate deterministic authorization check from a policy file
//	counterweight release-keygen   maintainers: create a release signing key pair
//	counterweight release-sign F    maintainers: sign a SHA256SUMS file
//	counterweight release-verify F  verify a SHA256SUMS file against the embedded keys
//	counterweight help              show this overview
//
// No subcommand, or leading flags (e.g. `counterweight -addr :9000`), run the
// backend, so existing launchers and the self-update relaunch keep working.
func dispatch(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "-supervise", "--supervise", "supervise":
			return supervise(args[1:])
		case "serve":
			return runServe(args[1:])
		case "dev":
			return devCommand(args[1:])
		case "build":
			return buildCommand(args[1:])
		case "setup-token":
			return runServe([]string{"-setup-token"})
		case "reset-password":
			return runServe([]string{"-reset-password"})
		case "authz":
			return authzCommand(args[1:], os.Getenv, os.Stdout, os.Stderr)
		case "release-keygen":
			return releaseKeygen(os.Stdout, os.Stderr)
		case "release-sign":
			return releaseSign(args[1:], os.Getenv, os.Stdout, os.Stderr)
		case "release-verify":
			return releaseVerify(args[1:], selfupdate.TrustedKeys(), os.Stdout, os.Stderr)
		case "help", "-h", "--help":
			usage(os.Stdout)
			return 0
		}
		if !strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(os.Stderr, "counterweight: unknown command %q\n\n", args[0])
			usage(os.Stderr)
			return 2
		}
	}
	// No subcommand, or flags meant for the backend: run it (back-compat).
	return runServe(args)
}

// runServe hands the given arguments to the backend's flag parser. It rewrites
// os.Args so run()'s package-level flag.Parse sees exactly these flags, whether
// they arrived after `serve` or directly (counterweight -addr :9000).
func runServe(args []string) int {
	os.Args = append([]string{os.Args[0]}, args...)
	return run()
}

func usage(w *os.File) {
	fmt.Fprint(w, `Counterweight - autonomous AI penetration-testing system

Usage:
  counterweight <command> [flags]

Commands:
  serve            Run the backend (default if no command is given)
  dev              Backend + traffic proxy + frontend with hot reload
  build            Compile the single binary with the frontend embedded
  setup-token      Print the first-owner setup token and exit
  reset-password   Reset the admin password (COUNTERWEIGHT_RESET_NEWPASS) and exit
  supervise        Restart loop used by start.sh / start.bat
  authz            Authorization check from a policy file, for CI ('counterweight authz help')
  help             Show this help

Release maintenance:
  release-keygen   Create a release signing key pair (private key printed once)
  release-sign F   Sign the SHA256SUMS file F with $COUNTERWEIGHT_RELEASE_SIGNING_KEY, writing F.sig
  release-verify F Verify F against F.sig using the keys embedded in this build

Run a command with -h for its own flags, e.g. 'counterweight serve -h'.

From a source checkout, prefix with 'go run ./cmd/counterweight', e.g.
  go run ./cmd/counterweight dev
  go run ./cmd/counterweight build -target linux/amd64 -o dist/counterweight
`)
}

// buildCommand compiles the embedded single binary. Same flags and behavior as
// `go run ./cmd/build`, exposed here so one tool covers build and run.
func buildCommand(args []string) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	target := fs.String("target", "", "target OS/ARCH (default: this host)")
	output := fs.String("o", "", "output binary (default ./counterweight, or ./counterweight.exe on windows)")
	buildVersion := fs.String("version", "", "version embedded in the binary (default: git describe)")
	skipFrontend := fs.Bool("skip-frontend", false, "reuse server/webui/dist instead of rebuilding web/")
	skipNpmCI := fs.Bool("skip-npm-ci", false, "skip npm ci before the frontend build")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := buildtool.Build(buildtool.Options{
		Target:       *target,
		Output:       *output,
		Version:      *buildVersion,
		SkipFrontend: *skipFrontend,
		SkipNpmCI:    *skipNpmCI,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "[x]", err)
		return 1
	}
	return 0
}

// devCommand runs the development stack that dev.sh used to orchestrate: the Go
// backend (via `go run`, no embedded frontend) plus the Next.js dev server with
// hot reload. Ctrl-C stops both. It runs the same way on Windows, macOS and Linux.
func devCommand(args []string) int {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8787", "backend management listen address")
	proxy := fs.String("proxy", "127.0.0.1:8788", "traffic recording proxy address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	host := *addr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		host = "127.0.0.1"
	}

	backend := exec.Command("go", "run", "./cmd/counterweight", "serve", "-addr", *addr, "-proxy", *proxy)
	frontend := exec.Command("npm", "run", "dev", "--", "--hostname", host, "--port", "3000")
	frontend.Dir = "web"
	// The Next.js /api proxy follows -addr unless COUNTERWEIGHT_API is set explicitly.
	if os.Getenv("COUNTERWEIGHT_API") == "" {
		apiHost, apiPort := host, *addr
		if i := strings.LastIndex(apiPort, ":"); i >= 0 {
			apiPort = apiPort[i+1:]
		}
		if apiHost == "0.0.0.0" || apiHost == "[::]" {
			apiHost = "127.0.0.1"
		}
		frontend.Env = append(os.Environ(), "COUNTERWEIGHT_API=http://"+apiHost+":"+apiPort)
	}
	for _, c := range []*exec.Cmd{backend, frontend} {
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		setProcessGroup(c)
	}

	if err := backend.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "[dev] start backend:", err)
		return 1
	}
	if err := frontend.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "[dev] start frontend:", err)
		terminate(backend)
		_ = backend.Wait()
		return 1
	}
	fmt.Printf("[dev] backend %s / proxy %s / frontend http://%s:3000 (Ctrl-C to stop)\n", *addr, *proxy, host)

	// Stop when either child exits or a signal arrives, then tear down both groups.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{}, 2)
	go func() { _ = backend.Wait(); done <- struct{}{} }()
	go func() { _ = frontend.Wait(); done <- struct{}{} }()
	select {
	case <-sig:
	case <-done:
	}
	terminate(backend)
	terminate(frontend)
	return 0
}
