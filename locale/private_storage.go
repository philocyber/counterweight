package locale

func init() {
	Register("Legacy signing key conflicts with the active key; resolve the obsolete workspace key before starting", "Legacy signing key conflicts with the active key; resolve the obsolete workspace key before starting")
	Register("Cannot load private configuration file %s; check file permissions and JSON format", "Cannot load private configuration file %s; check file permissions and JSON format")
	Register("Local signing key is invalid; refusing to replace it", "Local signing key is invalid; refusing to replace it")
	Register("Private storage must not contain symbolic links: %s", "Private storage must not contain symbolic links: %s")
	Register("Private storage contains unsupported file type: %s", "Private storage contains unsupported file type: %s")
}
