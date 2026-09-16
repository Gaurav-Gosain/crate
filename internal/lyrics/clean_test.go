package lyrics

import "testing"

// Every case here is a real title from the library that found nothing before
// the cleaning was added.
func TestCleanTitle(t *testing.T) {
	cases := []struct{ title, artist, want string }{
		{"Diljit Dosanjh - RED CHILI (Visualiser) ｜ Drive Thru", "Diljit Dosanjh", "RED CHILI"},
		{"Diljit Dosanjh： Amiri (Official Audio) GHOST ｜ Thiarajxtt", "Diljit Dosanjh", "Amiri"},
		{"Thaa Karke (FULL VIDEO) B Mohit ft. Karan Aujla ｜ Swaalina", "B Mohit, Karan Aujla", "Thaa Karke"},
		{"Haule Haule ｜ Bad Newz ｜ Vicky Kaushal", "Ammy Virk", "Haule Haule"},
		{"Haan Haige aa (FULL VIDEO) KARAN AUJLA ft. Gurlez Akhtar I Rupan Bal", "Karan Aujla", "Haan Haige aa"},
		{"LEHNGA : DILJIT DOSANJH ｜ NEERU BAJWA", "Diljit Dosanjh", "LEHNGA"},
		{"Tauba Tauba", "Karan Aujla", "Tauba Tauba"},
	}
	for _, c := range cases {
		if got := cleanTitle(c.title, c.artist); got != c.want {
			t.Errorf("cleanTitle(%q)\n  got  %q\n  want %q", c.title, got, c.want)
		}
	}
}

// A remix is a different recording, so the word has to survive: dropping it
// would match the lyrics of the original and pin them to the wrong timings.
func TestCleanTitleKeepsRemix(t *testing.T) {
	got := cleanTitle("Chharhiyaan - Remix (Remix By Dj Sonu Dhillon)", "Dj Sonu Dhillon")
	if !contains(got, "Remix") {
		t.Fatalf("got %q, which has lost the remix", got)
	}
}

// A song whose name begins with a word from the artist field must survive.
func TestCleanTitleKeepsATitleThatLooksLikeAPrefix(t *testing.T) {
	if got := cleanTitle("Stranger", "Diljit Dosanjh"); got != "Stranger" {
		t.Fatalf("got %q", got)
	}
}

// Cleaning must never empty a title: an empty search finds everything.
func TestCleanTitleNeverReturnsEmpty(t *testing.T) {
	for _, in := range []string{"(Official Video)", "｜｜｜", "ft. Someone", "- Artist"} {
		if got := cleanTitle(in, "Someone"); got == "" {
			t.Fatalf("cleanTitle(%q) returned empty", in)
		}
	}
}

func TestFirstArtist(t *testing.T) {
	cases := map[string]string{
		"Avvy Sra, Karan Aujla, Jaani": "Avvy Sra",
		"Diljit Dosanjh":               "Diljit Dosanjh",
		"A & B":                        "A",
	}
	for in, want := range cases {
		if got := firstArtist(in); got != want {
			t.Errorf("firstArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
