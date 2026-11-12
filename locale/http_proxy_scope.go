package locale

func init() {
	Register("Recording proxy destination binding is unavailable", "Recording proxy destination binding is unavailable")
	Register("HTTP proxy tunnel could not be established", "HTTP proxy tunnel could not be established")
	Register("HTTP proxy target resolution failed", "HTTP proxy target resolution failed")
	Register("HTTP proxy target resolved to no approved addresses", "HTTP proxy target resolved to no approved addresses")
	Register("Proxy-side DNS cannot enforce IP asset restrictions; use a direct request or a literal IP destination", "Proxy-side DNS cannot enforce IP asset restrictions; use a direct request or a literal IP destination")
}
