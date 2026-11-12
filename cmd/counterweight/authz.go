package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/Philocyber/counterweight/authz"
	"github.com/Philocyber/counterweight/report"
)

const authzUsage = `Deterministic authorization check: replays a policy table (personas x requests)
against a target and compares every answer with the expected outcome. No model
is used and the server does not need to run.

Usage:
  counterweight authz validate <policy.yaml>           check the policy file; sends nothing
  counterweight authz run [-o report.json] <policy.yaml>

Credentials are read from the environment variables the policy names.

Exit status of run:
  0  every check passed
  1  the policy is violated (at least one check failed)
  2  the run proves nothing: invalid policy, a persona could not sign in or
     prove its identity, a check was inconclusive, or no check was sent

Guide: docs/authorization-checks.md
`

// authzCommand runs `counterweight authz`. It needs no database and no
// running server: it reads a policy file and talks to the target only.
func authzCommand(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, authzUsage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, authzUsage)
		return 0
	case "validate":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: counterweight authz validate <policy.yaml>")
			return 2
		}
		p, ok := loadPolicy(args[1], stderr)
		if !ok {
			return 2
		}
		personas := len(p.PersonaNames())
		fmt.Fprintf(stdout, "OK: %d personas, %d cases, %d checks.\n", personas, len(p.Cases), personas*len(p.Cases))
		if vars := p.EnvVars(); len(vars) > 0 {
			fmt.Fprintln(stdout, "Environment variables the run needs:")
			for _, v := range vars {
				if v.Secret {
					fmt.Fprintf(stdout, "  %s (credential)\n", v.Name)
				} else {
					fmt.Fprintf(stdout, "  %s\n", v.Name)
				}
			}
		}
		return 0
	case "run":
		fs := flag.NewFlagSet("authz run", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("o", "", "write the JSON report to this file")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: counterweight authz run [-o report.json] <policy.yaml>")
			return 2
		}
		p, ok := loadPolicy(fs.Arg(0), stderr)
		if !ok {
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		rep := authz.Run(ctx, p, authz.Options{Getenv: getenv, Redact: report.RedactSecrets})
		rep.WriteText(stdout)
		code := rep.ExitCode
		if *out != "" {
			b, err := json.MarshalIndent(rep, "", "  ")
			if err == nil {
				err = os.WriteFile(*out, append(b, '\n'), 0o600)
			}
			if err != nil {
				fmt.Fprintln(stderr, "[x] write report:", err)
				if code == 0 {
					code = 2 // the job asked for a report and has none
				}
			}
		}
		return code
	}
	fmt.Fprintf(stderr, "counterweight authz: unknown command %q\n\n", args[0])
	fmt.Fprint(stderr, authzUsage)
	return 2
}

func loadPolicy(path string, stderr io.Writer) (*authz.Policy, bool) {
	p, err := authz.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "[x] %s is not a valid policy:\n%v\n", path, err)
		return nil, false
	}
	return p, true
}
