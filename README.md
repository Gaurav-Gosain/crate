<div align="center">
  <h1>crate</h1>
  <p>Keep a music library in sync, from the terminal.</p>
</div>

---

crate downloads new tracks from the sources you list, files them by artist and
album, mirrors the library to your music server over rsync, and asks that server
to reindex. Repeated syncs are cheap: anything already fetched is skipped
without touching the network.

It is a terminal interface written directly against the terminal, with no TUI
framework, in the same style as [youterm](https://github.com/Gaurav-Gosain/youterm).

## Install

    go install github.com/Gaurav-Gosain/crate/cmd/crate@latest

Requires `yt-dlp`, `ffmpeg` and `rsync` on PATH.

## Use

    crate          open the interface
    crate sync     run one sync without the interface, for cron
    crate config   print the config file path

| key | action |
|---|---|
| `/` | search YouTube Music |
| `a` | add a source by URL and fetch it |
| `d` | remove the selected source |
| `s` | sync everything |
| `enter` | sync the selected source |
| `r` | ask the server to reindex |
| `j` `k` | move, `g` `G` for top and bottom |
| `?` | key help |
| `q` | quit |

## Searching

Press `/` and type. crate queries YouTube Music and shows both the individual
tracks and the albums they belong to, so you can take one song or the whole
record. Press enter to add the highlighted result, escape to go back.

YouTube Music is used rather than plain YouTube because its entries carry real
artist, album and track tags. A plain YouTube search returns video titles like
`M I L E S D A V I S - Kind Of Blue - Full Album`, which file badly.

That quality has a cost: yt-dlp has to resolve each entry, roughly two seconds
apiece, and album results expand into their tracks. A search takes tens of
seconds. The header shows `searching` while it runs.

Spotify is not supported and cannot be: its catalogue is DRM protected, so
there is nothing to download. Paste a Spotify link and nothing will happen.

## Adding music you already own

Anything you drop into the library directory is mirrored on the next sync,
tagged or not. crate does not need to have downloaded a file to look after it,
so an existing collection can be copied in and will sync alongside the rest.

## Speed

Downloads run in parallel at two levels: sources fetch concurrently, and each
source is split across several yt-dlp processes using strided playlist
selection, so worker k of n takes items k+1, k+1+n and so on. A single
semaphore bounds the total process count, because n sources each fanning out
to n workers would otherwise start n squared of them.

Striding keeps every worker inside the playlist rather than fetching items as
standalone URLs, which matters: album and track-number metadata come from that
context and would be lost otherwise.

Measured on an 11 track album:

| `parallel` | time |
|---|---|
| 1 | 25.8s |
| 4 | 11.2s |
| 8 | 9.3s |

The default is 8. Lower it in the config on a machine where transcoding
competes for cores.

## How a sync works

1. Each source is passed to `yt-dlp`, which skips anything already listed in
   the download archive. This is what makes repeat runs cheap.
2. Audio is extracted, converted, and tagged with artist, album, title, track
   number and date, then filed as `Artist/Album/N - Title.opus`.
3. The whole library is mirrored with `rsync`, so only new or changed files
   cross the wire.
4. If a scan URL is configured, the server is asked to reindex immediately
   rather than waiting for its own schedule.

Downloading happens for every source first, then one mirror and one reindex at
the end. That matters when the link to the server is slow: one rsync pass
instead of one per source.

## Unattended

`crate sync` performs one sync and exits, printing what it did. It takes the
same path as the interface, so a cron entry keeps a library current without
anyone watching:

    0 4 * * *  /usr/local/bin/crate sync >> ~/.local/state/crate.log 2>&1

## More than one machine

The sources list and the download archive are shared, not per laptop. Without
that, a second machine has no idea what the first one already fetched and
downloads the entire library again.

Both live next to the music on the same remote, under `.crate-state`, which is
outside the music root so the music server does not try to index it. crate
pulls that state when it starts and publishes it after a sync. If the remote is
itself backed up, the state inherits that backup rather than needing a second
set of credentials.

First machine to run simply finds nothing to pull, and its own state becomes
the shared one.

## Keeping no local copy

Set `keep_local` to `false` and the library becomes a staging area: files are
downloaded, mirrored, then removed locally. The remote is the only copy, so the
two cannot drift apart, and nothing has to reconcile them.

The archive lives beside the config rather than inside the library, so clearing
staged files never destroys the record of what was already fetched.

Staging is only cleared after a mirror actually succeeds. yt-dlp cannot write
to the remote directly, since transcoding needs a real filesystem, so files are
always staged first regardless of this setting.

## Whole artists

An artist handle works as a source:

    https://music.youtube.com/@someartist

crate rewrites a bare handle to that artist's releases tab, because a handle on
its own resolves to Videos: music videos, where the audio has to be pulled out
of a video and the titles are promotional rather than track names. The releases
tab is the same artist's albums and singles, which carry real album and track
tags. Name a tab yourself and crate leaves it alone.

Expect hundreds of tracks and several gigabytes. crate says so when you add one.

## Endless mixes

YouTube URLs containing `list=RD` are generated radio: they have no end and
keep proposing tracks. Adding one as a source pulls in hundreds of files rather
than an album's worth. crate says so when you add one, but it will still fetch
what you asked for.

## Configuration

`crate config` prints the path. It is created on first run.

```json
{
  "library": "/Users/you/Music/crate",
  "format": "opus",
  "quality": "0",
  "parallel": 8,
  "keep_local": true,
  "remote": {
    "host": "music-server",
    "path": "/var/lib/music",
    "scan_url": "https://music.example.com",
    "scan_user": "you",
    "scan_pass": "secret",
    "state": ""
  },
  "sources": []
}
```

`library` is the local staging directory, which doubles as an offline copy.

`remote.host` is anything `ssh` and `rsync` understand. An entry in
`~/.ssh/config` is the tidiest way to express it.

`remote.scan_*` is optional and speaks the Subsonic API, so it works with
Navidrome and anything else implementing it. The password is sent as a salted
token, never in the clear. The file is written `0600` because it holds that
password.

The mirror deliberately does not use `--delete`. The server may hold music that
did not come from crate, and quietly removing it would be rude.

## Notes

Album playlists often carry no track numbers, which leaves a music server
sorting a record alphabetically. crate falls back to the position in the
playlist, which is the track order, so albums play in sequence.

Filing depends on the metadata the source provides. When a source has no artist
tag, crate falls back through several fields and finally to `Unknown Artist`.
Values shaped like an email address are rejected, because some hosts put an
uploader's address where the artist belongs.

crate only fetches what you point it at. What you are allowed to download is
between you and the source.

## Licence

MIT.
