package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/privatefs"
)

func TestCloneForReviewUsesStoredTokenOnlyAfterAnonymousCloneFails(t *testing.T) {
	repo := codesrc.Repo{Owner: "acme", Name: "private-app"}
	stub := func(private bool, calls *[]codesrc.CloneOptions) func(context.Context, codesrc.CloneOptions) (string, error) {
		return func(_ context.Context, opts codesrc.CloneOptions) (string, error) {
			*calls = append(*calls, opts)
			if entries, _ := os.ReadDir(opts.Dest); len(entries) > 0 {
				t.Errorf("clone into a non-empty destination: %v", entries)
			}
			if private && opts.Token == "" {
				// Leave a partial tree behind, as an interrupted git clone does.
				if err := os.MkdirAll(filepath.Join(opts.Dest, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
				return "", errors.New("repository not found")
			}
			return opts.Dest, nil
		}
	}
	restore := cloneRepository
	t.Cleanup(func() { cloneRepository = restore })

	t.Run("public repository never sees the token", func(t *testing.T) {
		s := &Server{m: &Manager{dir: t.TempDir()}}
		saveTestGitHubToken(t, s)
		var calls []codesrc.CloneOptions
		cloneRepository = stub(false, &calls)
		if _, _, err := s.cloneForReview(context.Background(), repo, codesrc.ModeLight); err != nil {
			t.Fatal(err)
		}
		if len(calls) != 1 || calls[0].Token != "" {
			t.Fatalf("calls = %+v", calls)
		}
	})

	t.Run("private repository is retried with the token", func(t *testing.T) {
		s := &Server{m: &Manager{dir: t.TempDir()}}
		saveTestGitHubToken(t, s)
		var calls []codesrc.CloneOptions
		cloneRepository = stub(true, &calls)
		path, _, err := s.cloneForReview(context.Background(), repo, codesrc.ModeLight)
		if err != nil || path == "" {
			t.Fatalf("clone: %q, %v", path, err)
		}
		if len(calls) != 2 || calls[0].Token != "" || calls[1].Token != "test-token" || calls[0].Dest != calls[1].Dest {
			t.Fatalf("calls = %+v", calls)
		}
	})

	t.Run("without a token the error says how to review a private repository", func(t *testing.T) {
		s := &Server{m: &Manager{dir: t.TempDir()}}
		var calls []codesrc.CloneOptions
		cloneRepository = stub(true, &calls)
		_, _, err := s.cloneForReview(context.Background(), repo, codesrc.ModeLight)
		if err == nil || !strings.Contains(err.Error(), "GitHub token") || len(calls) != 1 {
			t.Fatalf("err = %v, calls = %d", err, len(calls))
		}
	})
}

func saveTestGitHubToken(t *testing.T, s *Server) {
	t.Helper()
	if err := privatefs.PublishFile(s.githubCredentialPath(), []byte(`{"token":"test-token","login":"octocat"}`)); err != nil {
		t.Fatal(err)
	}
}
