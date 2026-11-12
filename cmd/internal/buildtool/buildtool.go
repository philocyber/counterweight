// Package buildtool compiles Counterweight for one target with the frontend
// embedded. It is shared by `go run ./cmd/build` and the `counterweight build`
// subcommand so both behave identically on Windows, macOS and Linux (no bash,
// rsync or POSIX npm scripts involved). Release packaging (UPX, zip archives,
// multiple targets) stays in build.sh.
package buildtool

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Options controls a single-target build.
type Options struct {
	Target       string // OS/ARCH, e.g. "linux/amd64"; empty means the current host.
	Output       string // output binary path; empty means ./counterweight[.exe].
	Version      string // version embedded in the binary; empty means git describe.
	SkipFrontend bool   // reuse server/webui/dist instead of rebuilding web/.
	SkipNpmCI    bool   // skip npm ci before the frontend build.
}

// Build runs the frontend export and compiles the embedded single binary.
func Build(opts Options) error {
	target := opts.Target
	if target == "" {
		target = runtime.GOOS + "/" + runtime.GOARCH
	}
	goos, goarch, ok := strings.Cut(target, "/")
	if !ok || goos == "" || goarch == "" {
		return fmt.Errorf("invalid target %q (expected OS/ARCH)", target)
	}

	output := opts.Output
	if output == "" {
		output = "counterweight"
		if goos == "windows" {
			output = "counterweight.exe"
		}
	}
	version := opts.Version
	if version == "" {
		version = gitVersion()
	}

	if opts.SkipFrontend {
		if _, err := os.Stat(filepath.Join("server", "webui", "dist")); err != nil {
			return fmt.Errorf("skip-frontend set but server/webui/dist is missing")
		}
	} else {
		if !opts.SkipNpmCI {
			if err := runIn("web", nil, "npm", "ci"); err != nil {
				return err
			}
		}
		// Set NEXT_EXPORT here instead of using the POSIX-only build:static script.
		if err := runIn("web", []string{"NEXT_EXPORT=1"}, "npm", "run", "build"); err != nil {
			return err
		}
		fmt.Println("[*] Syncing web/out to server/webui/dist")
		dist := filepath.Join("server", "webui", "dist")
		if err := os.RemoveAll(dist); err != nil {
			return err
		}
		if err := copyTree(filepath.Join("web", "out"), dist); err != nil {
			return err
		}
	}

	fmt.Printf("[*] Building %s/%s, version %s → %s\n", goos, goarch, version, output)
	env := []string{"CGO_ENABLED=0", "GOOS=" + goos, "GOARCH=" + goarch}
	if err := runIn(".", env, "go", "build", "-tags", "embedui", "-trimpath",
		"-ldflags", "-s -w -buildid= -X main.version="+version,
		"-o", output, "./cmd/counterweight"); err != nil {
		return err
	}
	fmt.Println("[+] Built", output)
	return nil
}

func runIn(dir string, env []string, name string, args ...string) error {
	fmt.Printf("[*] %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// gitVersion matches build.sh: git describe without a leading "v", or "dev".
func gitVersion() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	})
}
