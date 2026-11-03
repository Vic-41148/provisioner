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
