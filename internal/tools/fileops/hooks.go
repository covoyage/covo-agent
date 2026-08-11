package fileops

import "sync"

// AfterWriteHook is called after a successful file write operation.
// Parameters: path (absolute), toolName, toolCallID.
type AfterWriteHook func(path, toolName, toolCallID string)

var (
	hooksMu      sync.Mutex
	activeHookID string
	hooks        = map[string]AfterWriteHook{}
)

// SetAfterWriteHook registers a hook with the given ID and makes it active.
// Only the active hook is called on file writes. Returns an unsubscribe
// function that removes the hook.
func SetAfterWriteHook(id string, hook AfterWriteHook) func() {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hooks[id] = hook
	activeHookID = id
	return func() {
		hooksMu.Lock()
		defer hooksMu.Unlock()
		delete(hooks, id)
		if activeHookID == id {
			activeHookID = ""
		}
	}
}

func notifyAfterWrite(path, toolName, toolCallID string) {
	hooksMu.Lock()
	h := hooks[activeHookID]
	hooksMu.Unlock()
	if h != nil {
		h(path, toolName, toolCallID)
	}
}
