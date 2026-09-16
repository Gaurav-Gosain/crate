package library

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/crate/internal/config"
)

// Removal reports what taking a source out of the library cost.
type Removal struct {
	Files  int
	Kept   int
	Pruned int
}

// RemoveTracks deletes the music a source brought in, from this machine and
// from the remote, and forgets it in the download archive.
//
// Dropping a source from the list is not enough on its own: the tracks stay on
// disk, the mirror has no --delete so they stay on the server too, and the
// archive still lists them, so re-adding the source later downloads nothing
// and the gaps are silent.
//
// Tracks that another source also lists are kept. Sources overlap freely, and
// a single's album and an artist's channel can easily name the same recording.
func RemoveTracks(ctx context.Context, c *config.Config, s config.Source, log func(string, ...any)) (Removal, error) {
	var r Removal

	entries, err := resolve(ctx, s)
	if err != nil {
		return r, err
	}
	idx, err := index(ctx, c)
	if err != nil {
		return r, err
	}

	// The playlist this source owns, so it can be skipped when working out
	// which tracks other sources still want.
	self := ""
	for _, e := range entries {
		if e.playlist != "" {
			self = safeName(e.playlist)
			break
		}
	}
	if self == "" {
		self = safeName(s.Name)
	}

	// Anything still named by another source's playlist is spoken for.
	keep := claimedElsewhere(ctx, c, self)

	var (
		paths []string
		ids   []string
		seen  = map[string]bool{}
	)
	for _, e := range entries {
		rel, ok := idx.lookup(e.title)
		if !ok || seen[rel] {
			continue
		}
		seen[rel] = true
		if keep[rel] {
			r.Kept++
			continue
		}
		paths = append(paths, rel)
		if e.id != "" {
			ids = append(ids, e.id)
		}
	}
	if len(paths) == 0 {
		return r, nil
	}
	slices.Sort(paths)
	r.Files = len(paths)

	// Local copies, where they are kept.
	for _, rel := range paths {
		os.Remove(filepath.Join(c.Library, rel))
	}
	r.Pruned = pruneEmpty(c.Library)

	// The remote, which the mirror will never clean up by itself.
	if err := removeRemote(ctx, c, paths); err != nil {
		return r, err
	}

	// And the archive, so re-adding the source actually re-downloads.
	if err := forget(c, ids); err != nil {
		return r, err
	}

	// Finally the playlist file, local and remote. A leftover .m3u is not
	// cosmetic: it is what claimedElsewhere reads, so one that survives here
	// keeps claiming tracks for a source that no longer exists.
	if self != "" {
		os.Remove(filepath.Join(c.Library, self+".m3u"))
		if err := removeRemote(ctx, c, []string{self + ".m3u"}); err != nil {
			return r, err
		}
	}
	if log != nil {
		log("   removed %d file(s), kept %d shared, pruned %d folder(s)", r.Files, r.Kept, r.Pruned)
	}
	return r, nil
}

// claimedElsewhere collects every path named by a playlist other than this
// source's, so shared tracks survive the removal. The generated .m3u files are
// the record of what belongs to whom, which avoids re-resolving every other
// source over the network just to delete one.
//
// They are read from the remote rather than from the local library, because
// the local library may be staging that was emptied after the last mirror. On
// this path an empty answer is not harmless: it would mean deleting a track
// that another source still wants.
func claimedElsewhere(ctx context.Context, c *config.Config, self string) map[string]bool {
	keep := map[string]bool{}
	root := strings.TrimSuffix(c.Remote.Path, "/")
	script := fmt.Sprintf(`for f in %q/*.m3u; do [ -e "$f" ] || continue; echo "==$(basename "$f")"; cat "$f"; done`, root)
	out, err := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", c.Remote.Host, script).Output()
	if err != nil {
		return keep
	}
	skip := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if name, ok := strings.CutPrefix(line, "=="); ok {
			skip = name == self+".m3u"
			continue
		}
		line = strings.TrimSpace(line)
		if skip || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keep[line] = true
	}
	return keep
}

// pruneEmpty removes directories left behind with nothing in them.
func pruneEmpty(root string) int {
	var dirs []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	// Deepest first, so a folder emptied by pruning its child is itself seen.
	slices.Sort(dirs)
	slices.Reverse(dirs)
	n := 0
	for _, d := range dirs {
		if ents, err := os.ReadDir(d); err == nil && len(ents) == 0 {
			if os.Remove(d) == nil {
				n++
			}
		}
	}
	return n
}

// removeRemote deletes paths on the music server, relative to the music root.
func removeRemote(ctx context.Context, c *config.Config, rel []string) error {
	if len(rel) == 0 {
		return nil
	}
	root := strings.TrimSuffix(c.Remote.Path, "/")
	var in bytes.Buffer
	for _, p := range rel {
		in.WriteString(root + "/" + p)
		in.WriteByte(0)
	}
	// Paths go over stdin rather than the command line: music filenames are
	// long and there are no argument length limits to trip over this way.
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", c.Remote.Host,
		fmt.Sprintf("xargs -0 rm -f && find %q -mindepth 1 -type d -empty -delete", root))
	cmd.Stdin = &in
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remote remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// forget drops ids from the download archive.
func forget(c *config.Config, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	b, err := os.ReadFile(c.ArchivePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || drop[f[len(f)-1]] {
			continue
		}
		kept = append(kept, line)
	}
	return os.WriteFile(c.ArchivePath(), []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}
