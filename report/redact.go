package report

import "regexp"

// Redacted replaces a credential value removed from a shareable export.
const Redacted = "[REDACTED]"

var (
	redactHeader = regexp.MustCompile(`(?im)^([ \t>]*(?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|api-key|x-auth-token|x-access-token|x-csrf-token|x-xsrf-token|x-amz-security-token)[ \t]*:[ \t]*)[^\r\n]+`)
	// Free-text bearer values; 16+ characters keeps prose such as "bearer authentication" intact.
	redactBearer = regexp.MustCompile(`(?i)\b(bearer)([ \t]+)[A-Za-z0-9._~+/=-]{16,}`)
	redactJWT    = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	redactParam  = regexp.MustCompile(`(?i)(^|[?&;\s"'])((?:access_token|refresh_token|id_token|auth_token|token|api_key|apikey|password|passwd|pwd|secret|client_secret|sessionid|session_id|phpsessid|jsessionid|signature|sig)=)[^&\s"'#;]+`)
	redactJSON   = regexp.MustCompile(`(?i)("(?:password|passwd|token|access_token|refresh_token|id_token|secret|client_secret|api_key|apikey|authorization|cookie)"[ \t]*:[ \t]*")[^"]*(")`)
	redactURLPw  = regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`)
)

// RedactSecrets masks common credential shapes (auth headers, bearer tokens,
// JWTs, secret query/form parameters, JSON secret fields, URL userinfo) so a
// report can leave the console. It is pattern-based and cannot catch every
// secret; exports that use it say so.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = redactHeader.ReplaceAllString(s, "${1}"+Redacted)
	s = redactJWT.ReplaceAllString(s, Redacted)
	s = redactBearer.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = redactParam.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = redactJSON.ReplaceAllString(s, "${1}"+Redacted+"${2}")
	s = redactURLPw.ReplaceAllString(s, "${1}"+Redacted+"@")
	return s
}
