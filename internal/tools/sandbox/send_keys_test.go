package sandbox

import (
	"bytes"
	"testing"
)

func TestParseSendKeys(t *testing.T) {
	tokens, payload, signals, err := parseSendKeys("yes Enter")
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || tokens[0] != "yes" || tokens[1] != "Enter" {
		t.Fatalf("tokens=%v", tokens)
	}
	if string(payload) != "yes\n" {
		t.Fatalf("payload=%q", payload)
	}
	if len(signals) != 0 {
		t.Fatalf("signals=%v", signals)
	}

	_, payload, signals, err = parseSendKeys("C-c")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, []byte{0x03}) {
		t.Fatalf("ctrl-c payload=%v", payload)
	}
	if len(signals) != 1 || signals[0] != "INT" {
		t.Fatalf("signals=%v", signals)
	}

	if _, _, _, err := parseSendKeys(""); err == nil {
		t.Fatal("expected empty keys error")
	}
	if _, _, _, err := parseSendKeys("\x01"); err == nil {
		t.Fatal("expected unknown token error")
	}
}
