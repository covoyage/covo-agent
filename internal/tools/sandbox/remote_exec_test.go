package sandbox

import "testing"

func TestShellQuoteWorkdir(t *testing.T) {
	got := shellQuote("a b'c")
	want := `'a b'\''c'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
