package locale

func init() {
	Register("HTTP execution requires a concrete HTTP or HTTPS destination", "HTTP execution requires a concrete HTTP or HTTPS destination")
	Register("Execution destination policy could not be loaded: %w", "Execution destination policy could not be loaded: %w")
	Register("HTTP execution destination blocked by asset policy: %s", "HTTP execution destination blocked by asset policy: %s")
	Register("HTTP execution destination policy is unavailable", "HTTP execution destination policy is unavailable")
	Register("Redirect to %s was not followed: %s", "Redirect to %s was not followed: %s")
	Register("Stopped after %d redirects without reaching a final response", "Stopped after %d redirects without reaching a final response")
	Register("The request goes through the configured proxy (the recording proxy while traffic capture is on), which could not complete it. It was not retried without the proxy.", "The request goes through the configured proxy (the recording proxy while traffic capture is on), which could not complete it. It was not retried without the proxy.")
	Register("The server answered with status %s, which is not a final response", "The server answered with status %s, which is not a final response")
	Register("The response was received but could not be prepared for display", "The response was received but could not be prepared for display")
	Register("The request could not be prepared from this URL", "The request could not be prepared from this URL")
}
