// Package useragent centralizes the User-Agent string sent to external
// services so the COVO_USER_AGENT override applies everywhere. It lives in a
// leaf package because tools and plugin platforms cannot import internal/cli.
package useragent

import "os"

// UserAgent returns the User-Agent string to send to external services.
// The COVO_USER_AGENT environment variable overrides; otherwise fallback is
// used (callers that know the build version pass "covo-agent/<version>").
func UserAgent(fallback string) string {
	if v := os.Getenv("COVO_USER_AGENT"); v != "" {
		return v
	}
	return fallback
}
