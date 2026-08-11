package mcpserver

import (
	"sort"
	"strings"
	"sync"
)

// RegistryEntry describes an MCP server available for installation.
type RegistryEntry struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	EnvVars     []string          `json:"env_vars,omitempty"` // env var names the server needs
	Homepage    string            `json:"homepage,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
}

// builtinRegistry is the curated list of known MCP servers.
var builtinRegistry = []RegistryEntry{
	// --- File & Data ---
	{
		Name:        "filesystem",
		DisplayName: "Filesystem",
		Description: "Read/write access to local files and directories. The foundation for file-based workflows.",
		Category:    "Data",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", "${WORKSPACE}"},
		EnvVars:     []string{},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
		Tags:        []string{"files", "filesystem", "read", "write"},
	},
	{
		Name:        "sqlite",
		DisplayName: "SQLite",
		Description: "Query and manage SQLite databases. Run SQL queries, list tables, and inspect schema.",
		Category:    "Data",
		Command:     "uvx",
		Args:        []string{"mcp-server-sqlite", "--db-path", "${DB_PATH}"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sqlite",
		Tags:        []string{"database", "sql", "sqlite"},
	},
	{
		Name:        "postgres",
		DisplayName: "PostgreSQL",
		Description: "Connect to PostgreSQL databases. Run read-only queries and explore schema.",
		Category:    "Data",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-postgres", "${DATABASE_URL}"},
		EnvVars:     []string{"DATABASE_URL"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/postgres",
		Tags:        []string{"database", "sql", "postgres", "postgresql"},
	},

	// --- Developer Tools ---
	{
		Name:        "github",
		DisplayName: "GitHub",
		Description: "Interact with GitHub: create issues, manage PRs, search repos, and review code.",
		Category:    "Developer",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-github"},
		EnvVars:     []string{"GITHUB_PERSONAL_ACCESS_TOKEN"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/github",
		Tags:        []string{"github", "git", "issues", "prs", "code-review"},
	},
	{
		Name:        "gitlab",
		DisplayName: "GitLab",
		Description: "Interact with GitLab: manage issues, merge requests, and pipelines.",
		Category:    "Developer",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-gitlab"},
		EnvVars:     []string{"GITLAB_PERSONAL_ACCESS_TOKEN"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/gitlab",
		Tags:        []string{"gitlab", "git", "issues", "merge-requests"},
	},
	{
		Name:        "linear",
		DisplayName: "Linear",
		Description: "Manage Linear issues, projects, and cycles. Create and update tasks from conversations.",
		Category:    "Developer",
		Command:     "npx",
		Args:        []string{"-y", "mcp-linear"},
		EnvVars:     []string{"LINEAR_API_KEY"},
		Homepage:    "https://github.com/linear/linear-mcp-server",
		Tags:        []string{"linear", "issues", "project-management"},
	},

	// --- Search & Web ---
	{
		Name:        "brave-search",
		DisplayName: "Brave Search",
		Description: "Web and local search using the Brave Search API. Get real-time information from the web.",
		Category:    "Search",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-brave-search"},
		EnvVars:     []string{"BRAVE_API_KEY"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/brave-search",
		Tags:        []string{"search", "web", "brave"},
	},
	{
		Name:        "fetch",
		DisplayName: "Fetch",
		Description: "Fetch web pages and convert to markdown. Lightweight web content retrieval.",
		Category:    "Search",
		Command:     "uvx",
		Args:        []string{"mcp-server-fetch"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/fetch",
		Tags:        []string{"fetch", "web", "http", "markdown"},
	},
	{
		Name:        "puppeteer",
		DisplayName: "Puppeteer",
		Description: "Browser automation — navigate pages, take screenshots, interact with web UIs.",
		Category:    "Search",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-puppeteer"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/puppeteer",
		Tags:        []string{"browser", "puppeteer", "automation", "screenshots"},
	},

	// --- Productivity ---
	{
		Name:        "slack",
		DisplayName: "Slack",
		Description: "Interact with Slack: list channels, send messages, search history.",
		Category:    "Productivity",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-slack"},
		EnvVars:     []string{"SLACK_BOT_TOKEN"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/slack",
		Tags:        []string{"slack", "messaging", "chat"},
	},
	{
		Name:        "google-drive",
		DisplayName: "Google Drive",
		Description: "Search and read files from Google Drive. Access docs, sheets, and presentations.",
		Category:    "Productivity",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-google-drive"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/google-drive",
		Tags:        []string{"google", "drive", "docs", "files"},
	},
	{
		Name:        "memory",
		DisplayName: "Memory",
		Description: "Persistent knowledge graph. Store and retrieve entities, relationships, and observations.",
		Category:    "Productivity",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-memory"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/memory",
		Tags:        []string{"memory", "knowledge-graph", "persistent"},
	},
	{
		Name:        "notion",
		DisplayName: "Notion",
		Description: "Search and manage Notion pages, databases, and blocks.",
		Category:    "Productivity",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-notion"},
		EnvVars:     []string{"NOTION_API_KEY"},
		Homepage:    "https://github.com/modelcontextprotocol/servers",
		Tags:        []string{"notion", "notes", "docs", "wiki"},
	},

	// --- Cloud & DevOps ---
	{
		Name:        "aws",
		DisplayName: "AWS",
		Description: "Interact with AWS services: list resources, manage EC2/S3, and run AWS CLI commands.",
		Category:    "Cloud",
		Command:     "npx",
		Args:        []string{"-y", "mcp-server-aws"},
		EnvVars:     []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
		Homepage:    "https://github.com/geelen/aws-mcp-server",
		Tags:        []string{"aws", "cloud", "ec2", "s3", "devops"},
	},
	{
		Name:        "sentry",
		DisplayName: "Sentry",
		Description: "Retrieve and analyze error reports and performance metrics from Sentry.",
		Category:    "Cloud",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sentry"},
		EnvVars:     []string{"SENTRY_AUTH_TOKEN"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sentry",
		Tags:        []string{"sentry", "monitoring", "errors", "observability"},
	},
	{
		Name:        "docker",
		DisplayName: "Docker",
		Description: "Manage Docker containers, images, and volumes. List, start, stop, and inspect containers.",
		Category:    "Cloud",
		Command:     "npx",
		Args:        []string{"-y", "mcp-server-docker"},
		Homepage:    "https://github.com/ckreiling/mcp-server-docker",
		Tags:        []string{"docker", "containers", "devops"},
	},

	// --- Creative & Media ---
	{
		Name:        "everart",
		DisplayName: "EverArt",
		Description: "AI image generation. Create images from text descriptions using various models.",
		Category:    "Creative",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-everart"},
		EnvVars:     []string{"EVERART_API_KEY"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/everart",
		Tags:        []string{"image", "ai", "generation", "art"},
	},
	{
		Name:        "sequential-thinking",
		DisplayName: "Sequential Thinking",
		Description: "Dynamic problem-solving through structured, step-by-step reasoning. Helps with complex decisions.",
		Category:    "Utility",
		Command:     "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/sequentialthinking",
		Tags:        []string{"reasoning", "thinking", "problem-solving"},
	},
	{
		Name:        "time",
		DisplayName: "Time",
		Description: "Get current time, convert between timezones, and reason about dates and times.",
		Category:    "Utility",
		Command:     "uvx",
		Args:        []string{"mcp-server-time"},
		Homepage:    "https://github.com/modelcontextprotocol/servers/tree/main/src/time",
		Tags:        []string{"time", "timezone", "date"},
	},
}

// customEntries holds user-added or remote registry entries.
var (
	customMu       sync.RWMutex
	customEntries  []RegistryEntry
)

// BuiltinRegistry returns the curated list of known MCP servers.
func BuiltinRegistry() []RegistryEntry {
	result := make([]RegistryEntry, len(builtinRegistry))
	copy(result, builtinRegistry)
	return result
}

// AllEntries returns builtin plus custom entries, sorted by name.
func AllEntries() []RegistryEntry {
	customMu.RLock()
	custom := make([]RegistryEntry, len(customEntries))
	copy(custom, customEntries)
	customMu.RUnlock()

	result := make([]RegistryEntry, 0, len(builtinRegistry)+len(custom))
	result = append(result, builtinRegistry...)
	result = append(result, custom...)

	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

// Categories returns the distinct category names from all entries.
func Categories() []string {
	entries := AllEntries()
	seen := make(map[string]bool)
	var cats []string
	for _, e := range entries {
		if !seen[e.Category] {
			seen[e.Category] = true
			cats = append(cats, e.Category)
		}
	}
	sort.Strings(cats)
	return cats
}

// Search returns entries matching the given query string. Matching is
// case-insensitive against name, display name, description, category, and tags.
func Search(query string) []RegistryEntry {
	if strings.TrimSpace(query) == "" {
		return AllEntries()
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var result []RegistryEntry
	for _, e := range AllEntries() {
		if matchEntry(e, q) {
			result = append(result, e)
		}
	}
	return result
}

func matchEntry(e RegistryEntry, q string) bool {
	if strings.Contains(strings.ToLower(e.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.DisplayName), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Description), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Category), q) {
		return true
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// FindEntry returns the registry entry with the given name, or nil.
func FindEntry(name string) *RegistryEntry {
	for _, e := range AllEntries() {
		if strings.EqualFold(e.Name, name) {
			return &e
		}
	}
	return nil
}

// AddCustomEntry adds a user-defined entry to the custom list.
func AddCustomEntry(e RegistryEntry) {
	customMu.Lock()
	defer customMu.Unlock()
	customEntries = append(customEntries, e)
}

// IsInstalled checks whether a server with the given name is already
// configured in the provided mcp_servers map.
func IsInstalled(name string, configured map[string]bool) bool {
	return configured[strings.ToLower(name)]
}
