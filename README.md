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
    crate config   print the config file path

| key | action |
|---|---|
| `/` | search YouTube Music |
| `a` | add a source by URL |
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

## Configuration

`crate config` prints the path. It is created on first run.

```json
{
  "library": "/Users/you/Music/crate",
  "format": "opus",
  "quality": "0",
  "remote": {
    "host": "music-server",
    "path": "/var/lib/music",
    "scan_url": "https://music.example.com",
    "scan_user": "you",
    "scan_pass": "secret"
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

Filing depends on the metadata the source provides. When a source has no artist
tag, crate falls back through several fields and finally to `Unknown Artist`.
Values shaped like an email address are rejected, because some hosts put an
uploader's address where the artist belongs.

crate only fetches what you point it at. What you are allowed to download is
between you and the source.

## Licence

MIT.
