package plugin

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// GlobalCLICommands is a registry of CLI commands provided by plugins.
// Plugins can register commands at import time via init() functions.
var (
	globalCLIMu       sync.RWMutex
	globalCLICommands []CLICommand
)

// RegisterGlobalCLICommand registers a CLI command globally.
// This is safe to call from plugin init() functions.
func RegisterGlobalCLICommand(cmd CLICommand) {
	globalCLIMu.Lock()
	globalCLICommands = append(globalCLICommands, cmd)
	globalCLIMu.Unlock()
}

// GlobalCLICommands returns all globally registered CLI commands.
func GlobalCLICommands() []CLICommand {
	globalCLIMu.RLock()
	defer globalCLIMu.RUnlock()
	result := make([]CLICommand, len(globalCLICommands))
	copy(result, globalCLICommands)
	return result
}

type Category string

const (
	CategoryPlatform  Category = "platform"
	CategoryTools     Category = "tools"
	CategoryAuth      Category = "auth"
	CategoryLifecycle Category = "lifecycle"
)

type IncomingMessage struct {
	Platform    string       `json:"platform"`
	ChannelID   string       `json:"channel_id"`
	UserID      string       `json:"user_id"`
	UserName    string       `json:"user_name"`
	Text        string       `json:"text"`
	Timestamp   time.Time    `json:"timestamp"`
	Raw         any          `json:"raw,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment types recognized by the gateway's inbound processing pipeline.
const (
	AttachmentTypeAudio = "audio"
	AttachmentTypeImage = "image"
	AttachmentTypeVideo = "video"
	AttachmentTypeFile  = "file"
)

// Attachment describes a piece of media attached to an incoming message.
// Platform adapters populate this when a message carries non-text content
// (e.g. a voice note). Either URL (remote, to be downloaded) or LocalPath
// (already saved to disk by the adapter) should be set.
type Attachment struct {
	Type      string `json:"type"`
	URL       string `json:"url,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	FileName  string `json:"file_name,omitempty"`
}

type OutgoingMessage struct {
	Text      string     `json:"text"`
	ParseMode string     `json:"parse_mode,omitempty"`
	ReplyTo   string     `json:"reply_to,omitempty"`
	Media     *MediaInfo `json:"media,omitempty"`
}

type MediaInfo struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data,omitempty"`
	Caption  string `json:"caption,omitempty"`
	FileName string `json:"file_name,omitempty"`
}

type PlatformProvider interface {
	Name() string
	Category() Category
	Start(ctx context.Context) error
	Stop() error
	Validate() error
	OnMessage(callback func(IncomingMessage))
	Send(ctx context.Context, channelID string, text string) error
	SendMessage(ctx context.Context, channelID string, msg OutgoingMessage) error
}

type SystemConfig struct {
	HomeDir string
	Logger  *slog.Logger
}

type Engine interface {
	Run(ctx context.Context, input string) (string, error)
	Close()
}

// LifecycleHook allows plugins to intercept agent execution at key points.
// Each method corresponds to a stage in the agent loop lifecycle.
// Return an error from a Before* method to abort execution.
type LifecycleHook interface {
	// Name returns a unique identifier for this hook.
	Name() string

	// BeforeToolCall runs before each tool execution. Return non-nil to block.
	BeforeToolCall(ctx context.Context, toolName string, args json.RawMessage) error

	// AfterToolCall runs after each tool execution.
	AfterToolCall(ctx context.Context, toolName string, args json.RawMessage, result string, err error)

	// BeforeModelCall runs before each LLM API call. Return non-nil to abort.
	BeforeModelCall(ctx context.Context, msgs []any) error

	// AfterModelCall runs after each LLM API call.
	AfterModelCall(ctx context.Context, msgs []any, response string, err error)

	// TransformModelOutput allows plugins to modify the LLM text output
	// before it is processed by the agent loop. Return the transformed text
	// and a nil error to apply the change. Return the original text to leave
	// it unchanged.
	TransformModelOutput(ctx context.Context, output string) (string, error)

	// OnTurnStart is called before each agent turn begins.
	OnTurnStart(ctx context.Context)

	// OnTurnEnd is called after each agent turn completes.
	OnTurnEnd(ctx context.Context)

	// OnSessionStart is called when a new session begins.
	OnSessionStart(ctx context.Context, sessionID string)

	// OnSessionEnd is called when a session ends.
	OnSessionEnd(ctx context.Context, sessionID string)
}

// BaseLifecycleHook provides default no-op implementations so plugins
// only need to override the methods they care about.
type BaseLifecycleHook struct{}

func (BaseLifecycleHook) Name() string { return "base" }

func (BaseLifecycleHook) BeforeToolCall(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

func (BaseLifecycleHook) AfterToolCall(_ context.Context, _ string, _ json.RawMessage, _ string, _ error) {
}

func (BaseLifecycleHook) BeforeModelCall(_ context.Context, _ []any) error { return nil }

func (BaseLifecycleHook) AfterModelCall(_ context.Context, _ []any, _ string, _ error) {}

func (BaseLifecycleHook) TransformModelOutput(_ context.Context, output string) (string, error) {
	return output, nil
}

func (BaseLifecycleHook) OnTurnStart(_ context.Context) {}

func (BaseLifecycleHook) OnTurnEnd(_ context.Context) {}

func (BaseLifecycleHook) OnSessionStart(_ context.Context, _ string) {}

func (BaseLifecycleHook) OnSessionEnd(_ context.Context, _ string) {}

// HookProvider is an optional interface a plugin entry can implement to
// contribute lifecycle hooks.
type HookProvider interface {
	LifecycleHooks() []LifecycleHook
}

// ContextEngineProvider is an optional interface a plugin entry can implement
// to register custom context engines.
type ContextEngineProvider interface {
	// ContextEngines returns a map of name -> factory function.
	// The factory receives the default engine and returns a wrapped engine.
	ContextEngines() map[string]func(inner any) any
}

// CLICommand describes a CLI subcommand provided by a plugin.
type CLICommand struct {
	// Name is the subcommand name (e.g. "myplugin").
	Name string
	// Description is the help text.
	Description string
	// Run is called when the subcommand is invoked with its args.
	Run func(ctx context.Context, args []string) error
}

// CLICommandProvider is an optional interface a plugin entry can implement
// to contribute CLI subcommands.
type CLICommandProvider interface {
	CLICommands() []CLICommand
}

// MemoryProviderFactory creates a memory provider instance given the home directory.
type MemoryProviderFactory func(homeDir string) (any, error)

// Note: plugins receive homeDir as a plain string rather than evolution.MemoryProviderConfig
// to avoid importing the internal evolution package. The bridge in commands.go wraps this
// into an evolution.MemoryProviderFactory.

// MemoryProviderRegistration describes a memory provider contributed by a plugin.
type MemoryProviderRegistration struct {
	// Name is the provider identifier (e.g. "redis").
	Name string
	// Factory creates the provider instance. The returned value must implement
	// the evolution.MemoryProvider interface.
	Factory func(homeDir string) (any, error)
}

// MemoryProviderPlugin is an optional interface a plugin entry can implement
// to contribute memory providers.
type MemoryProviderPlugin interface {
	MemoryProviders() []MemoryProviderRegistration
}
