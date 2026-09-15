package state

import (
	"testing"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

func urls(ss []config.Source) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.URL)
	}
	return out
}

func TestMergeUnionsAdditions(t *testing.T) {
	remote := shared{Sources: []config.Source{{URL: "https://a"}}}
	local := &config.Config{Sources: []config.Source{{URL: "https://b"}}}
	got := urls(merge(remote, local))
	if len(got) != 2 {
		t.Fatalf("want both sources, got %v", got)
	}
}

// A removal has to survive the round trip. This is the case that actually
// broke: the source was deleted here, another device still listed it, and the
// merge put it back along with all of its music.
func TestMergeRespectsLocalTombstone(t *testing.T) {
	remote := shared{Sources: []config.Source{{URL: "https://a"}, {URL: "https://b"}}}
	local := &config.Config{Removed: map[string]string{
		config.NormalizeURL("https://a"): "2026-01-01T00:00:00Z",
	}}
	got := urls(merge(remote, local))
	if len(got) != 1 || got[0] != "https://b" {
		t.Fatalf("tombstoned source came back: %v", got)
	}
}

func TestMergeRespectsRemoteTombstone(t *testing.T) {
	remote := shared{Removed: map[string]string{
		config.NormalizeURL("https://a"): "2026-01-01T00:00:00Z",
	}}
	local := &config.Config{Sources: []config.Source{{URL: "https://a"}}}
	if got := urls(merge(remote, local)); len(got) != 0 {
		t.Fatalf("remote removal ignored: %v", got)
	}
}

func TestMergeDeduplicates(t *testing.T) {
	remote := shared{Sources: []config.Source{{URL: "https://a"}}}
	local := &config.Config{Sources: []config.Source{{URL: "https://a"}}}
	if got := urls(merge(remote, local)); len(got) != 1 {
		t.Fatalf("want one, got %v", got)
	}
}

// Adding a source back is the only thing that beats a tombstone.
func TestClearRemovedLetsSourceReturn(t *testing.T) {
	local := &config.Config{}
	local.MarkRemoved("https://a")
	local.ClearRemoved("https://a")
	remote := shared{Sources: []config.Source{{URL: "https://a"}}}
	if got := urls(merge(remote, local)); len(got) != 1 {
		t.Fatalf("re-added source stayed dead: %v", got)
	}
}

func TestMergeKeepsTombstonesForNextPush(t *testing.T) {
	remote := shared{Removed: map[string]string{"x": "t1"}}
	local := &config.Config{Removed: map[string]string{"y": "t2"}}
	merge(remote, local)
	if len(local.Removed) != 2 {
		t.Fatalf("tombstones lost on merge: %v", local.Removed)
	}
}
