package cli

import "os"

// Version is the covo-agent version.
var Version = "0.1.0-dev"

// UserAgent returns the User-Agent string identifying covo-agent to external
// services (LLM providers, etc.). Override with the COVO_USER_AGENT
// environment variable.
func UserAgent() string {
	if v := os.Getenv("COVO_USER_AGENT"); v != "" {
		return v
	}
	return "covo-agent/" + Version
}
