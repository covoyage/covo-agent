package nostr

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// parseRelayMessage parses a relay->client JSON message:
//
//	["EVENT", <subid>, <event>]  → payload is the event object JSON
//	["OK", <event-id>, <bool>, <reason>]
//	["NOTICE", <msg>] / ["CLOSED", <subid>, <reason>]
//
// It returns the message type, the id/subid, the boolean flag (OK), the
// reason string, and the raw payload (event object / notice text).
func parseRelayMessage(data []byte) (msgType, id string, ok bool, reason string, payload []byte, err error) {
	var frame []json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		return "", "", false, "", nil, fmt.Errorf("parse relay frame: %w", err)
	}
	if len(frame) == 0 {
		return "", "", false, "", nil, fmt.Errorf("empty relay frame")
	}
	if err := json.Unmarshal(frame[0], &msgType); err != nil {
		return "", "", false, "", nil, fmt.Errorf("parse relay frame type: %w", err)
	}

	switch msgType {
	case "EVENT":
		if len(frame) < 3 {
			return "", "", false, "", nil, fmt.Errorf("EVENT frame needs subid and event")
		}
		if err := json.Unmarshal(frame[1], &id); err != nil {
			return "", "", false, "", nil, fmt.Errorf("parse EVENT subid: %w", err)
		}
		return msgType, id, false, "", []byte(frame[2]), nil
	case "OK":
		if len(frame) < 4 {
			return "", "", false, "", nil, fmt.Errorf("OK frame needs id, ok and reason")
		}
		if err := json.Unmarshal(frame[1], &id); err != nil {
			return "", "", false, "", nil, fmt.Errorf("parse OK id: %w", err)
		}
		if err := json.Unmarshal(frame[2], &ok); err != nil {
			return "", "", false, "", nil, fmt.Errorf("parse OK flag: %w", err)
		}
		_ = json.Unmarshal(frame[3], &reason)
		return msgType, id, ok, reason, nil, nil
	case "NOTICE":
		if len(frame) >= 2 {
			_ = json.Unmarshal(frame[1], &reason)
			payload = []byte(reason)
		}
		return msgType, "", false, reason, payload, nil
	case "CLOSED":
		if len(frame) >= 2 {
			_ = json.Unmarshal(frame[1], &id)
		}
		if len(frame) >= 3 {
			_ = json.Unmarshal(frame[2], &reason)
		}
		return msgType, id, false, reason, nil, nil
	default:
		return msgType, "", false, "", nil, nil
	}
}

// jsonMarshal is a tiny indirection so adapter code reads cleanly.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// randRead wraps crypto/rand.Read for test seam symmetry.
func randRead(b []byte) (int, error) {
	return rand.Read(b)
}
