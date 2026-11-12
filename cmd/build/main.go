// Command build compiles Counterweight for one target with the frontend embedded.
// It is a thin wrapper over the shared buildtool package; the same build is also
// available as the `counterweight build` subcommand:
//
//	go run ./cmd/build                         # current OS/arch → ./counterweight[.exe]
//	go run ./cmd/build -target linux/amd64 -o dist/counterweight
//
// Release packaging (UPX, zip archives, multiple targets) stays in build.sh.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/Philocyber/counterweight/cmd/internal/buildtool"
)

func main() {
	target := flag.String("target", runtime.GOOS+"/"+runtime.GOARCH, "target OS/ARCH")
	output := flag.String("o", "", "output binary (default ./counterweight, or ./counterweight.exe for windows)")
	version := flag.String("version", "", "version embedded in the binary (default: git describe)")
	skipFrontend := flag.Bool("skip-frontend", false, "reuse server/webui/dist instead of rebuilding web/")
	skipNpmCI := flag.Bool("skip-npm-ci", false, "skip npm ci before the frontend build")
	flag.Parse()

	if err := buildtool.Build(buildtool.Options{
		Target:       *target,
		Output:       *output,
		Version:      *version,
		SkipFrontend: *skipFrontend,
		SkipNpmCI:    *skipNpmCI,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "[x]", err)
		os.Exit(1)
	}
}
