package locale

// Credential encryption at rest (packages sealed and db).
func init() {
	for _, s := range []string{
		"%s.%s row %v: %w",
		"%w (key %s, expected in %s)",
		"%w: %s",
		"[secrets] Discarded %d stored credential value(s) that could not be decrypted; re-enter them in the console",
		"[secrets] Encrypted %d stored credential value(s) with key %s",
		"[secrets] Left %s unchanged: %v",
		"Credential encryption is not initialized; the database was not opened through Open",
		"Credential encryption key file %s is invalid; refusing to replace it: %w",
		"Credential encryption key not found",
		"Credential encryption key source %s contains a key that is not %d bytes",
		"Credential encryption key source %s contains no key",
		"each key must be %d bytes encoded as standard base64",
		"encrypt stored credentials: %w",
		"environment variable %s",
		"Environment variable %s is invalid: %w",
		"no key present",
		"scan stored credentials: %w",
		"Stored credential failed to decrypt",
		"Stored credential has an unsupported encrypted format",
		"Stored credential is encrypted with a key that is not available",
		"The database contains credentials encrypted with key %s, which is not in %s. Restore the matching key file from your backup (or set %s) and restart. To discard the stored credentials instead and re-enter them, start once with %s=1",
		"The database contains encrypted credentials but their key is missing (%w). Restore the key file from your backup (or set %s) and restart. To discard the stored credentials instead and re-enter them, start once with %s=1",
		"write credential encryption key %s: %w",
	} {
		Register(s, s)
	}
}
