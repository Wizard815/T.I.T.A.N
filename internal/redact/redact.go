// Package redact strips secrets out of log lines before anyone (or any
// model) sees them: API keys, tokens, passwords, emails, private keys.
package redact

import "regexp"

var rules = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), "[PRIVATE KEY]"},
	{regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_\-]{16,}|\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}|\bxox[abprs]-[A-Za-z0-9\-]{10,}|\bAKIA[0-9A-Z]{16}\b|\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`), "[SECRET]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/\-]{12,}=*`), "$1 [SECRET]"},
	{regexp.MustCompile(`(?i)\b([a-z_]*(?:password|passwd|secret|token|api[_-]?key|apikey|auth)[a-z_]*)(\s*[=:]\s*|"\s*:\s*")["']?[^\s"',;&]{3,}`), "$1$2[SECRET]"},
	{regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`), "[EMAIL]"},
	{regexp.MustCompile(`(?i)(://[^/\s:@]+:)[^@\s/]+@`), "$1[SECRET]@"},
}

// Line returns s with secrets replaced.
func Line(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}
