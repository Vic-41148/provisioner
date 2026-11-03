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
- Deno (default JS runtime) for full support.
- An optional PO-token provider for full support.
- Cookies are off by default. If used, a throwaway Google account cookie file
  is stored locally and is never logged or committed.

## Health

Startup checks verify the runtime, token provider and cookie file. On failure
the plugin reports "YouTube downloads unavailable" and GrooveSync core is
unaffected.

## Running

The runner drains queued acquisition requests over syncd's HTTP interface and
uploads each download as a cached rendition. It never opens syncd's database.

    provisioner -server http://127.0.0.1:8080 -once

The admin token comes from `~/.syncd-token` by default, or from `-token-file`,
`SYNCD_TOKEN`, or `-token`. A platform-supplied JS runtime can be selected with
`-js-runtime node:/path/to/node`; a bare path keeps the Deno default. Use
`-interval`, `-between`, `-timeout`, `-yt-dlp`, `-cookies` and `-po-provider` to
tune a long-running worker. Without `-once` the runner polls until interrupted.

For each request the runner claims it, calls yt-dlp, uploads the file to
`/sync/v1/library/uploads/{hash}?kind=cached`, and marks the request done. A
download or upload error is recorded with `last_error` and the request goes back
to the queue until its attempt budget is spent.
