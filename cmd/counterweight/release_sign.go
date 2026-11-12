package main

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"os"

	"github.com/Philocyber/counterweight/selfupdate"
)

// signingKeyEnv is the only way to hand the private key to release-sign; an argument
// would leak into process listings and shell history.
const signingKeyEnv = "COUNTERWEIGHT_RELEASE_SIGNING_KEY"

// releaseKeygen prints a new release signing key pair. The private key goes to stdout
// only here; no other command ever prints it.
func releaseKeygen(stdout, stderr io.Writer) int {
	priv, pub, err := selfupdate.GenerateKey()
	if err != nil {
		fmt.Fprintln(stderr, "[x] generate key:", err)
		return 1
	}
	raw, _ := selfupdate.ParseKeys(pub)
	fmt.Fprintln(stderr, "WARNING: the private key below can sign releases that every installation will trust.")
	fmt.Fprintln(stderr, "Store it as a secret (for example the RELEASE_SIGNING_KEY repository secret) and never commit it.")
	fmt.Fprintf(stdout, "Private key (secret):\n%s\n\nPublic key line to add to selfupdate/release-keys.pub:\n%s\n\nKey id: %s\n",
		priv, pub, selfupdate.KeyID(raw[0]))
	return 0
}

// releaseSign signs a SHA256SUMS file with the key in COUNTERWEIGHT_RELEASE_SIGNING_KEY
// and writes the detached signature next to it as <path>.sig.
func releaseSign(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: counterweight release-sign <SHA256SUMS path>  (private key in "+signingKeyEnv+")")
		return 2
	}
	keyText := getenv(signingKeyEnv)
	if keyText == "" {
		fmt.Fprintln(stderr, "[x]", signingKeyEnv, "is not set")
		return 1
	}
	priv, err := selfupdate.ParsePrivateKey(keyText)
	if err != nil {
		fmt.Fprintln(stderr, "[x]", signingKeyEnv, "does not hold a valid private key (expected the base64 key printed by release-keygen)")
		return 1
	}
	sums, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "[x]", err)
		return 1
	}
	if len(sums) == 0 {
		fmt.Fprintln(stderr, "[x]", args[0], "is empty")
		return 1
	}
	sigFile, id := selfupdate.SignSums(priv, sums)
	if err := os.WriteFile(args[0]+".sig", sigFile, 0o644); err != nil {
		fmt.Fprintln(stderr, "[x]", err)
		return 1
	}
	fmt.Fprintf(stdout, "Signed %s with key %s, wrote %s\n", args[0], id, args[0]+".sig")
	return 0
}

// releaseVerify checks <path>.sig against the keys embedded in this build.
func releaseVerify(args []string, keys []ed25519.PublicKey, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: counterweight release-verify <SHA256SUMS path>  (signature in <path>.sig)")
		return 2
	}
	if len(keys) == 0 {
		fmt.Fprintln(stderr, "[x] this build embeds no release keys (selfupdate/release-keys.pub is empty), so a signature cannot be verified")
		return 1
	}
	sums, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "[x]", err)
		return 1
	}
	sigFile, err := os.ReadFile(args[0] + ".sig")
	if err != nil {
		fmt.Fprintln(stderr, "[x]", err)
		return 1
	}
	id, err := selfupdate.VerifySums(keys, sums, sigFile)
	if err != nil {
		fmt.Fprintln(stderr, "[x] signature NOT verified:", err)
		return 1
	}
	fmt.Fprintf(stdout, "OK: %s is signed by trusted key %s\n", args[0], id)
	return 0
}
