package lyrics

import "testing"

// The second catalogue's search is loose: asked for one song it returns
// others that share a word, or a different track from the same film, with
// real timed lyrics attached. Accepting those puts the wrong words in time
// with the music, which looks right and is worse than showing nothing.
func TestTitlesAgree(t *testing.T) {
	same := [][2]string{
		{"Proper Patola", "Proper Patola"},
		{"Param Sundari", "param sundari"},
		{"Jealousy (From \"Something\")", "Jealousy"},
	}
	for _, c := range same {
		if !titlesAgree(c[0], c[1]) {
			t.Errorf("titlesAgree(%q, %q) = false, want true", c[0], c[1])
		}
	}

	// These are the real mismatches the second catalogue returned when asked
	// for the track on the right.
	different := [][2]string{
		{"Morni", "Mr. Singh"},
		{"Bheegi Saree", "Param Sundari"},
		{"Something Else Entirely", "Jealousy"},
	}
	for _, c := range different {
		if titlesAgree(c[0], c[1]) {
			t.Errorf("titlesAgree(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

// A short shared opening is not evidence. "Ok" against "Ok Report" would
// otherwise pass, and they are not the same song.
func TestTitlesAgreeRejectsAShortPrefix(t *testing.T) {
	if titlesAgree("Ok", "Ok Report") {
		t.Fatal("a two letter overlap must not count as a match")
	}
}

func TestArtistAgrees(t *testing.T) {
	type a = struct {
		Name string `json:"name"`
	}
	artists := []a{{Name: "Karan Aujla"}}
	if !artistAgrees(artists, []string{"Avvy Sra", "Karan Aujla"}) {
		t.Fatal("an artist named in the credits should vouch for a result")
	}
	if artistAgrees(artists, []string{"Somebody Else"}) {
		t.Fatal("an unrelated artist must not vouch for a result")
	}
}
