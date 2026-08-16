package externalagent

// Product-specific provider adapters. Each drives the product's CLI and
// returns its final text output.

func ClaudeProvider() Provider {
	return &claudeProvider{}
}

func CodexProvider() Provider {
	return &codexProvider{}
}

func OpenCodeProvider() Provider {
	return &cliProvider{
		name: "opencode",
		args: func(task string) []string { return []string{"run", task} },
	}
}
