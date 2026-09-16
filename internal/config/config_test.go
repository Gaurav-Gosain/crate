package config

import "testing"

// An absent list means every pane, an empty list means none, and a typo has
// to come back as an error: a pane silently missing from the play view looks
// exactly like a broken layout.
func TestPlayEnabled(t *testing.T) {
	on, err := Play{}.Enabled()
	if err != nil {
		t.Fatalf("nil panes: %v", err)
	}
	for _, n := range PaneNames {
		if !on[n] {
			t.Fatalf("nil panes: %s should default on", n)
		}
	}

	on, err = Play{Panes: []string{}}.Enabled()
	if err != nil {
		t.Fatalf("empty panes: %v", err)
	}
	if len(on) != 0 {
		t.Fatalf("empty panes: got %v", on)
	}

	on, err = Play{Panes: []string{"art", "lyrcs"}}.Enabled()
	if err == nil {
		t.Fatal("a misspelt pane was accepted silently")
	}
	if !on["art"] {
		t.Fatal("the valid pane beside the typo was lost")
	}
}
