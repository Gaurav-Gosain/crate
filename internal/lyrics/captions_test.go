package lyrics

import (
	"testing"
	"time"
)

// YouTube writes automatic captions as a rolling window: each cue repeats the
// tail of the previous one with new words appended. Kept as written, every
// phrase appears three or four times running.
func TestParseVTTRemovesTheRollingWindow(t *testing.T) {
	vtt := "WEBVTT\nKind: captions\nLanguage: pa\n\n" +
		"00:00:01.000 --> 00:00:03.000\nalpha one\n\n" +
		"00:00:03.000 --> 00:00:05.000\nalpha one\nbravo two\n\n" +
		"00:00:05.000 --> 00:00:07.000\nbravo two\ncharlie three\n"
	got := ParseVTT(vtt)
	if len(got) != 3 {
		t.Fatalf("want 3 distinct lines, got %d: %+v", len(got), got)
	}
	want := []string{"alpha one", "bravo two", "charlie three"}
	for i, w := range want {
		if got[i].Text != w {
			t.Fatalf("line %d is %q, want %q", i, got[i].Text, w)
		}
	}
}

func TestParseVTTReadsTimes(t *testing.T) {
	vtt := "WEBVTT\n\n00:01:02.500 --> 00:01:04.000\nplaceholder\n"
	got := ParseVTT(vtt)
	if len(got) != 1 {
		t.Fatalf("got %d lines", len(got))
	}
	want := 62*time.Second + 500*time.Millisecond
	if got[0].At != want {
		t.Fatalf("at %v, want %v", got[0].At, want)
	}
}

// Cues carry inline timing spans which are markup, not words.
func TestParseVTTStripsInlineTags(t *testing.T) {
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n<00:00:01.200><c>placeholder</c> text\n"
	got := ParseVTT(vtt)
	if len(got) != 1 || got[0].Text != "placeholder text" {
		t.Fatalf("got %+v", got)
	}
}

// Headers and notes are not cues. A blank line terminates a cue in this
// format, so one cannot appear inside one.
func TestParseVTTIgnoresHeadersAndNotes(t *testing.T) {
	vtt := "WEBVTT\nKind: captions\nLanguage: en\n\nNOTE something\n\n" +
		"00:00:01.000 --> 00:00:02.000\nplaceholder\n"
	got := ParseVTT(vtt)
	if len(got) != 1 || got[0].Text != "placeholder" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseVTTOnRubbish(t *testing.T) {
	if got := ParseVTT("not a vtt file at all"); len(got) != 0 {
		t.Fatalf("got %d lines from rubbish", len(got))
	}
}
