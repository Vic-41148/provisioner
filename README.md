# provisioner

Optional acquisition plugin for GrooveSync. It is a separate repository and
is not included in any store build.

## Legal position

This plugin is an independent program that invokes the externally installed
`yt-dlp` executable. No extraction code is embedded in this repository or in
GrooveSync core. Users install `yt-dlp` themselves and are responsible for
complying with the terms of the services they access. GrooveSync core stores
only acquisition requests carrying MusicBrainz metadata and never Spotify
content.

## Runtime

- `yt-dlp` on the nightly channel, installed separately and available on PATH
  or configured by path.
- Deno, yt-dlp's default JS runtime, for full support.
- `ffmpeg` on PATH, required by yt-dlp's audio extraction.
- Cookies are off by default. If used, a throwaway Google account cookie file
  is stored locally and is never logged or committed.

## Health

Startup checks verify the runtime, `ffmpeg` and cookie file. On failure the
plugin reports "YouTube downloads unavailable" and GrooveSync core is
unaffected.

## Home connection only

Before the first request the runner asks `https://ipinfo.io/json` for the public
address and ASN organisation and refuses to start when it names a known cloud or
hosting provider. A failed lookup is also fatal, because an unknown egress
cannot be shown to be a home connection. `-egress-url` points the check at a
self-hosted endpoint with the same `ip` and `org` fields.

## Running

The runner drains queued acquisition requests over syncd's HTTP interface and
uploads each download as a cached rendition. It never opens syncd's database.

    provisioner -server http://127.0.0.1:8080 -once

The admin token comes from `~/.syncd-token` by default, or from `-token-file`,
`SYNCD_TOKEN`, or `-token`. A platform-supplied JS runtime can be selected with
`-js-runtime node:/path/to/node`; a bare path keeps the Deno default. Use
`-interval`, `-between`, `-timeout`, `-yt-dlp` and `-cookies` to tune a
long-running worker. Without `-once` the runner polls until interrupted.

For each request the runner claims it, searches for candidates, picks one, calls
yt-dlp, uploads the file to
`/sync/v1/library/uploads/{hash}?kind=cached`, and marks the request done. A
download or upload error is recorded with `last_error` and the request goes back
to the queue until its attempt budget is spent.

## Browse sidecar

The optional `sidecar/ytmusic_sidecar.py` is a read-only JSON service on
loopback that lets the GrooveSync client browse the signed-in YouTube Music
account. It owns the account and never downloads audio; acquisition requests
still go through this runner. It needs Python 3.10, `ytmusicapi`, and an auth
file created once with `ytmusicapi browser`.

    python3 sidecar/ytmusic_sidecar.py --auth ~/.config/groovesync/ytmusic-auth.json

It listens on `127.0.0.1:8099` by default and exposes `/health`, `/search`,
`/likes`, `/library/songs`, `/library/albums`, `/library/artists`, `/home`,
`/playlists` and `/playlists/{id}/tracks`, plus `POST /ratings`. Point syncd at
it with `SYNCD_YTMUSIC_URL=http://127.0.0.1:8099`; without that setting no
browse feature is advertised. Tests use a stub ytmusicapi and do not touch the
network:

    python3 -m unittest discover -s tests/sidecar

## Matching

Before downloading, the runner asks yt-dlp for up to `-search-limit` candidates
(five by default). `PreferAudio` ranks audio tracks above music videos and
uploads, then breaks ties on confidence. Confidence is the share of query tokens
that appear in the candidate title and artist, so punctuation and case do not
matter.

A match below `-match-threshold` (0.5 by default) is never downloaded silently.
The runner posts it to the core review queue and completes the request, leaving
the decision to a human. If the review call fails, the request is failed with the
error instead.

## Tests

```sh
go test ./...
go test -tags=integration ./tests/integration
```

The integration suite runs the real client and runner against a fake core over
`httptest`; it needs no YouTube, network, `yt-dlp` or `ffmpeg`.

