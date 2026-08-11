package slashcmd

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/covoyage/covo-agent/internal/i18n"
	"github.com/covoyage/covo-agent/internal/safego"
)

func handleVoice(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}

	wakeWord := "hey covo"
	recordSec := 10
	maxDuration := 300

	if len(parts) > 1 {
		wakeWord = parts[1]
	}
	if len(parts) > 2 {
		if v, err := strconv.Atoi(parts[2]); err == nil && v > 0 {
			recordSec = v
		}
	}
	if len(parts) > 3 {
		if v, err := strconv.Atoi(parts[3]); err == nil && v > 0 {
			maxDuration = v
		}
	}

	sctx.UI.App.PrintSystem(fmt.Sprintf("Starting voice listening (wake word: %q, record: %ds, max: %ds)...",
		wakeWord, recordSec, maxDuration))

	args, _ := json.Marshal(map[string]any{
		"wake_word":      wakeWord,
		"record_seconds": recordSec,
		"max_duration":   maxDuration,
	})

	safego.SafeGo(func() {
		result, err := covoAgent.Core().InvokeTool(sctx.Runtime.Context, "voice_listen", args)
		if err != nil {
			sctx.UI.App.PrintSystem(fmt.Sprintf("Voice listening error: %v", err))
			return
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("Voice result: %s", result))
	}, nil)

	return true
}

func handlePTT(sctx *SlashContext, parts []string) bool {
	covoAgent := sctx.Runtime.Agents.Current()
	if covoAgent == nil {
		sctx.UI.App.PrintSystem(i18n.T("system.no_active_agent"))
		return true
	}

	duration := 15
	language := ""

	if len(parts) > 1 {
		if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
			duration = v
		}
	}
	if len(parts) > 2 {
		language = parts[2]
	}

	sctx.UI.App.PrintSystem(fmt.Sprintf("Recording for %d seconds... Speak now!", duration))

	args, _ := json.Marshal(map[string]any{
		"duration": duration,
		"language": language,
	})

	safego.SafeGo(func() {
		result, err := covoAgent.Core().InvokeTool(sctx.Runtime.Context, "voice_record", args)
		if err != nil {
			sctx.UI.App.PrintSystem(fmt.Sprintf("Voice record error: %v", err))
			return
		}
		sctx.UI.App.PrintSystem(fmt.Sprintf("Voice result: %s", result))
	}, nil)

	return true
}
