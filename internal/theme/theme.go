// Package theme holds the colours the interface draws with.
//
// crate renders raw ANSI rather than going through a styling library, so a
// theme is just a set of colours plus the escape sequences built from them.
// Truecolor is used throughout: every terminal worth running a music player in
// has had it for years, and the 256-colour cube cannot represent most of these
// palettes closely enough to be worth the fallback.
package theme

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Theme is a named palette.
type Theme struct {
	Name string
	// Accent carries the eye: selections, the vinyl label, the now-playing
	// title. Ok, Warn and Bad report state. Muted and Rule recede.
	Accent string
	Ok     string
	Warn   string
	Bad    string
	Muted  string
	Rule   string
	Fg     string
	// Spectrum is sampled across its length to colour the visualiser, low
	// frequencies at the start and high at the end. Two or more entries.
	Spectrum []string
}

// builtin themes. The names match what these palettes are known as elsewhere,
// so someone who likes a terminal theme can find it here.
var builtin = []Theme{
	{
		Name: "dracula", Accent: "#bd93f9", Ok: "#50fa7b", Warn: "#f1fa8c",
		Bad: "#ff5555", Muted: "#6272a4", Rule: "#44475a", Fg: "#f8f8f2",
		Spectrum: []string{"#8be9fd", "#bd93f9", "#ff79c6", "#ff5555"},
	},
	{
		Name: "catppuccin", Accent: "#cba6f7", Ok: "#a6e3a1", Warn: "#f9e2af",
		Bad: "#f38ba8", Muted: "#6c7086", Rule: "#45475a", Fg: "#cdd6f4",
		Spectrum: []string{"#89dceb", "#89b4fa", "#cba6f7", "#f38ba8"},
	},
	{
		Name: "gruvbox", Accent: "#d3869b", Ok: "#b8bb26", Warn: "#fabd2f",
		Bad: "#fb4934", Muted: "#928374", Rule: "#3c3836", Fg: "#ebdbb2",
		Spectrum: []string{"#83a598", "#b8bb26", "#fabd2f", "#fb4934"},
	},
	{
		Name: "nord", Accent: "#88c0d0", Ok: "#a3be8c", Warn: "#ebcb8b",
		Bad: "#bf616a", Muted: "#4c566a", Rule: "#3b4252", Fg: "#eceff4",
		Spectrum: []string{"#5e81ac", "#88c0d0", "#8fbcbb", "#a3be8c"},
	},
	{
		Name: "tokyonight", Accent: "#7aa2f7", Ok: "#9ece6a", Warn: "#e0af68",
		Bad: "#f7768e", Muted: "#565f89", Rule: "#292e42", Fg: "#c0caf5",
		Spectrum: []string{"#7dcfff", "#7aa2f7", "#bb9af7", "#f7768e"},
	},
	{
		Name: "rosepine", Accent: "#c4a7e7", Ok: "#9ccfd8", Warn: "#f6c177",
		Bad: "#eb6f92", Muted: "#6e6a86", Rule: "#26233a", Fg: "#e0def4",
		Spectrum: []string{"#9ccfd8", "#c4a7e7", "#eb6f92", "#f6c177"},
	},
	{
		Name: "everforest", Accent: "#a7c080", Ok: "#83c092", Warn: "#dbbc7f",
		Bad: "#e67e80", Muted: "#7a8478", Rule: "#3d484d", Fg: "#d3c6aa",
		Spectrum: []string{"#7fbbb3", "#a7c080", "#dbbc7f", "#e67e80"},
	},
}

var (
	mu      sync.RWMutex
	current = builtin[0]

	allOnce  sync.Once
	allCache []Theme
)

// all returns every theme: the hand tuned ones first, then the converted
// palettes, skipping any whose name is already taken.
//
// The curated entries win because they were picked by eye. The converted set
// maps a terminal palette onto these roles mechanically, and the slot a
// terminal calls "bright purple" is not always the colour a person would
// choose to draw attention with: dracula's is pink.
func all() []Theme {
	allOnce.Do(func() {
		seen := make(map[string]bool, len(builtin)+len(generated))
		allCache = make([]Theme, 0, len(builtin)+len(generated))
		for _, t := range builtin {
			seen[t.Name] = true
			allCache = append(allCache, t)
		}
		for _, t := range generated {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			allCache = append(allCache, t)
		}
	})
	return allCache
}

// Names lists the available themes, sorted.
func Names() []string {
	themes := all()
	out := make([]string, 0, len(themes))
	for _, t := range themes {
		out = append(out, t.Name)
	}
	slices.Sort(out)
	return out
}

// Set selects a theme by name. An unknown name is reported rather than
// silently ignored, so a typo in the config does not look like a theme that
// simply renders the same as the default.
func Set(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	for _, t := range all() {
		if t.Name == name {
			mu.Lock()
			current = t
			mu.Unlock()
			return nil
		}
	}
	return fmt.Errorf("unknown theme %q", name)
}

// Get looks up a theme by name, for listing swatches without selecting it.
func Get(name string) (Theme, bool) {
	for _, t := range all() {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// Current returns the active theme.
func Current() Theme {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Fg returns the escape sequence setting the foreground to a hex colour.
func Fg(hex string) string {
	r, g, b := RGB(hex)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// Bg returns the escape sequence setting the background to a hex colour,
// for a selected row that should read as a filled bar rather than as merely
// differently coloured text.
func Bg(hex string) string {
	r, g, b := RGB(hex)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// Ink returns a foreground that stays readable on the given background,
// chosen by the background's brightness rather than assumed.
func Ink(hex string) string {
	r, g, b := RGB(hex)
	if (r*299+g*587+b*114)/1000 > 140 {
		return "\x1b[38;2;16;16;20m"
	}
	return "\x1b[38;2;240;240;245m"
}

// SpectrumAt samples the spectrum gradient at p in [0,1], interpolating
// between the two nearest stops so a tall bar shades smoothly rather than
// stepping between a handful of colours.
func (t Theme) SpectrumAt(p float64) string {
	if len(t.Spectrum) == 0 {
		return Fg(t.Accent)
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	if len(t.Spectrum) == 1 {
		return Fg(t.Spectrum[0])
	}
	x := p * float64(len(t.Spectrum)-1)
	i := int(x)
	if i >= len(t.Spectrum)-1 {
		return Fg(t.Spectrum[len(t.Spectrum)-1])
	}
	f := x - float64(i)
	r1, g1, b1 := RGB(t.Spectrum[i])
	r2, g2, b2 := RGB(t.Spectrum[i+1])
	lerp := func(a, b int) int { return a + int(f*float64(b-a)) }
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", lerp(r1, r2), lerp(g1, g2), lerp(b1, b2))
}

// RGB parses "#rrggbb". An unparseable colour comes back mid grey, which is
// visible but obviously wrong, rather than black on black.
func RGB(hex string) (int, int, int) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return 128, 128, 128
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 128, 128, 128
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff
}
