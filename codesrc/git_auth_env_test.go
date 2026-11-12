package codesrc

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
)

func assertTokenOnlyInEnv(t *testing.T, cmd *exec.Cmd, token, url string) {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	for _, a := range cmd.Args {
		if strings.Contains(a, token) || strings.Contains(a, b64) || strings.Contains(strings.ToLower(a), "extraheader") {
			t.Fatalf("credential material in argv: %q", a)
		}
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "GIT_CONFIG_COUNT=1") || !strings.Contains(env, "GIT_CONFIG_KEY_0=http."+url+".extraheader") {
		t.Fatalf("header config key missing from env")
	}
	if !strings.Contains(env, "AUTHORIZATION: basic "+b64) {
		t.Fatalf("auth header missing from env")
	}
}

func TestPushCmdKeepsTokenOutOfArgv(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	repo := Repo{Owner: "a", Name: "b"}
	cmd := newPushCmd(context.Background(), t.TempDir(), repo, "cw/fix-1", "tok-SECRET")
	assertTokenOnlyInEnv(t, cmd, "tok-SECRET", repo.CloneURL())
	if !strings.Contains(strings.Join(cmd.Args, " "), "push --no-verify -- "+repo.CloneURL()) {
		t.Errorf("push args changed: %v", cmd.Args)
	}
}

func TestFetchCmdKeepsTokenOutOfArgv(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	repo := Repo{Owner: "a", Name: "b"}
	sha := strings.Repeat("a", 40)
	cmd := newFetchCmd(context.Background(), t.TempDir(), repo, sha, "tok-SECRET")
	assertTokenOnlyInEnv(t, cmd, "tok-SECRET", repo.CloneURL())

	anon := newFetchCmd(context.Background(), t.TempDir(), repo, sha, "")
	if strings.Contains(strings.Join(anon.Env, "\n"), "extraheader") {
		t.Error("anonymous fetch must not carry an auth header")
	}
}

func TestGitConfigEnvAppendsToInheritedCount(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	got := strings.Join(gitConfigEnv("k", "v"), "|")
	if got != "GIT_CONFIG_COUNT=3|GIT_CONFIG_KEY_2=k|GIT_CONFIG_VALUE_2=v" {
		t.Errorf("gitConfigEnv = %q", got)
	}
}
