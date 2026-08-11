package panels

import (
	"testing"

	"github.com/covoyage/covonaut/tui/core"
)

func TestApprovalPickerKeyboardChoices(t *testing.T) {
	tests := []struct {
		key  string
		want ApprovalChoice
	}{
		{key: "y", want: ChoiceOnce},
		{key: "s", want: ChoiceSession},
		{key: "a", want: ChoiceAlways},
		{key: "n", want: ChoiceDeny},
	}

	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			var got ApprovalChoice
			picker := NewApprovalPicker("approve?", func(choice ApprovalChoice) { got = choice }, nil)
			picker.Update(core.KeyMsg{Data: test.key})
			if got != test.want {
				t.Fatalf("choice = %q, want %q", got, test.want)
			}
		})
	}
}

func TestApprovalPickerNavigationWraps(t *testing.T) {
	var got ApprovalChoice
	picker := NewApprovalPicker("approve?", func(choice ApprovalChoice) { got = choice }, nil)
	picker.Update(core.KeyMsg{Data: "up"})
	picker.Update(core.KeyMsg{Data: "enter"})
	if got != ChoiceDeny {
		t.Fatalf("wrapped choice = %q, want deny", got)
	}
}

func TestApprovalPickerCancel(t *testing.T) {
	cancelled := false
	picker := NewApprovalPicker("approve?", nil, func() { cancelled = true })
	picker.Update(core.KeyMsg{Data: "\x1b"})
	if !cancelled {
		t.Fatal("escape did not cancel approval picker")
	}
}
