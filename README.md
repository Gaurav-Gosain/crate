<div align="center">
  <h1>crate</h1>
  <p>My music library, in the terminal.</p>
</div>

---

This is personalware. It does what I need and nothing else, and it assumes my
setup: a music server I run, a file server I run, and a particular way of
filing tracks. It is public because there is no reason for it not to be, not
because it is meant to fit anyone else's arrangement. There is no support and
no plan to generalise it. Fork it if it is close to something you want.

crate downloads tracks from the sources I list, files them by artist and album,
mirrors them to my music server, and plays them. Repeated syncs are cheap:
anything already fetched is skipped without touching the network.

It is written directly against the terminal, with no TUI framework, in the same
style as [youterm](https://github.com/Gaurav-Gosain/youterm).

## Install

    go install github.com/Gaurav-Gosain/crate/cmd/crate@latest

Needs `yt-dlp`, `ffmpeg` and `rsync` on PATH, plus `mpv` for play mode.

## Use

    crate            open the interface
    crate sync       one sync, no interface, for cron
    crate playlists  rebuild one playlist per source
    crate config     print the config file path

Press `:` for the command palette, which lists everything the interface can do.

| key | action |
|---|---|
| `:` | command palette |
| `/` | search YouTube Music |
| `s` | sync every source |
| `enter` | sync the selected source |
| `a` | add a source by URL |
| `d` | remove the selected source, and its tracks |
| `p` | play mode |
| `t` | choose a theme |
| `r` | ask the music server to reindex |
| `q` | quit |

In play mode: `space` pauses, `enter` plays the selected track, `n` and `b`
step, `h` and `l` seek. Arrow keys work everywhere. So does the mouse: the
wheel scrolls, a click plays, and the progress bar can be dragged.

## How it is arranged

The remote is the only copy. Downloads are staged in a cache directory and
cleared once the mirror has succeeded, so there is one source of truth and
nothing local to drift out of step with it. Set `keep_local` if you want an
offline copy as well, and accept that the two can then disagree.

Sources and albums are different groupings, and tags can only express one. A
mix or an artist's channel spans many albums, so a music server that files by
album tag scatters one source across dozens of entries. crate writes an `.m3u`
per source alongside the music, which adds the missing grouping without
touching the tags, so both views stay right.

Play mode streams from the file server when there is no local copy, draws the
cover with the kitty graphics protocol where the terminal supports it, and
spins a record where it does not.

## Things that were not obvious

Collected because each one cost an evening.

**`--playlist-items` applies to nested playlists too.** Parallelising by giving
worker k the stride `k::n` looks right and is not: on an artist's releases tab
it takes every nth album and then every nth track inside each of those. About a
quarter of a catalogue arrives, and since the arithmetic is identical every run,
syncing again picks the same quarter. Sources whose entries are themselves
playlists are split by album instead.

**yt-dlp's metadata pass will not write tags to an opus file that already has
cover art.** It runs `ffmpeg -map 0 -c copy`, which tries to copy the cover as a
video stream, and the ogg muxer refuses it. It only bites on a second pass, and
it sustains itself: the failure keeps the download out of the archive, so the
next run retries the same file forever. `--postprocessor-args Metadata:-vn`
fixes it. The symptom is `.temp.opus` files piling up.

**Subsonic answers every request with HTTP 200**, including the ones it refused.
Checking the status code alone means a scan that never ran reports itself as a
success.

**Deleting a source has to leave a tombstone.** Shared state merged without one
turns every deletion into a temporary one: whichever device still lists the
source puts it back, and the music comes with it.

**Terminal cells are not runes.** These filenames are full of characters like
`｜`, the fullwidth bar yt-dlp substitutes for a pipe, and each takes two
columns. Measured as one, a line trimmed to fit a panel still runs over the
border.

**Time a transfer by the bytes that landed, not by the clock.** rsync.net's
restricted shell rejects shell redirection and oversized sftp blocks, and both
fail in milliseconds, which is indistinguishable from a very fast transfer if
the test only measures elapsed time.
