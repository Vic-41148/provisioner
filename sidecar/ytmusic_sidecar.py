#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 GrooveSync Authors
"""Read-only YouTube Music browse sidecar for GrooveSync.

The sidecar owns the signed-in account and answers JSON over loopback. It
never downloads audio: the provisioner worker does that after a client files an
acquisition request. Only ytmusicapi is required; the HTTP server is stdlib.

    python3 ytmusic_sidecar.py --auth ~/.config/groovesync/ytmusic-auth.json

Create the auth file once with `ytmusicapi browser`. The calls use the current
ytmusicapi surface (`search`, `get_library_*`, `get_album`, `get_artist`), with
`limit=None` on the library reads so every page is returned. The sidecar refuses to
start without it, reports why over /health, and keeps everything on loopback.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

try:
    from ytmusicapi import YTMusic
except ImportError:  # reported at startup, not import time in tests
    YTMusic = None


def _names(value) -> list[str]:
    """ytmusicapi artist fields are dicts in some calls and strings in others."""
    if not value:
        return []
    if isinstance(value, str):
        return [value]
    out = []
    for item in value:
        if isinstance(item, dict):
            name = item.get("name") or item.get("artist")
            if name:
                out.append(str(name))
        elif item:
            out.append(str(item))
    return out


def _album(value) -> str:
    if isinstance(value, dict):
        return str(value.get("name") or "")
    if isinstance(value, str):
        return value
    return ""


def track(item: dict) -> dict | None:
    video_id = item.get("videoId") or item.get("video_id")
    if not video_id:
        return None
    seconds = item.get("duration_seconds")
    if seconds is None and item.get("duration"):
        seconds = parse_duration(item["duration"])
    out = {
        "video_id": str(video_id),
        "title": str(item.get("title") or ""),
        "artists": _names(item.get("artists")),
        "duration_ms": int((seconds or 0) * 1000),
    }
    album = _album(item.get("album"))
    if album:
        out["album"] = album
    kind = item.get("videoType") or item.get("resultType") or ""
    if kind:
        out["kind"] = str(kind).lower()
    return out


def parse_duration(value: str) -> int:
    total = 0
    for part in str(value).split(":"):
        total = total * 60 + int(part)
    return total


def tracks(items) -> list[dict]:
    out = []
    for item in items or []:
        mapped = track(item)
        if mapped:
            out.append(mapped)
    return out


def playlist(item: dict) -> dict:
    return {
        "id": str(item.get("playlistId") or item.get("id") or ""),
        "name": str(item.get("title") or item.get("name") or ""),
        "track_count": int(item.get("count") or item.get("trackCount") or 0),
    }


def album(item: dict) -> dict:
    return {
        "browse_id": str(item.get("browseId") or ""),
        "title": str(item.get("title") or ""),
        "artists": _names(item.get("artists")),
        "year": str(item.get("year") or ""),
    }


def artist(item: dict) -> dict:
    return {
        "browse_id": str(item.get("browseId") or ""),
        "name": str(item.get("artist") or item.get("name") or ""),
    }


def section(item: dict) -> dict:
    contents = item.get("contents") or []
    flat = []
    for entry in contents:
        if isinstance(entry, dict) and entry.get("videoId"):
            flat.append(entry)
    return {
        "title": str(item.get("title") or ""),
        "tracks": tracks(flat),
    }


def home_sections(rows) -> list[dict]:
    out = []
    for item in rows or []:
        if not isinstance(item, dict):
            continue
        mapped = section(item)
        if mapped["tracks"]:
            out.append(mapped)
    return out


class Account:
    """One ytmusicapi client, or the reason it is unavailable."""

    def __init__(self, auth: str | None):
        self.error = ""
        self.client = None
        if not auth:
            self.error = "GROOVESYNC_YTMUSIC_AUTH is not set"
            return
        if not os.path.isfile(auth):
            self.error = "auth file not found"
            return
        if YTMusic is None:
            self.error = "ytmusicapi is not installed"
            return
        try:
            self.client = YTMusic(auth)
        except Exception as error:  # noqa: BLE001 - reported over /health
            self.error = f"ytmusicapi rejected the auth file: {error}"

    def ready(self) -> bool:
        return self.client is not None


class Handler(BaseHTTPRequestHandler):
    account: Account
    server_version = "groovesync-ytmusic/1"

    def log_message(self, fmt: str, *args) -> None:
        sys.stderr.write("%s %s\n" % (self.address_string(), fmt % args))

    def send_json(self, status: int, payload: dict) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def unavailable(self) -> None:
        self.send_json(503, {"error": "youtube music unavailable", "reason": self.account.error})

    def do_GET(self) -> None:  # noqa: N802 - http.server API
        parsed = urlparse(self.path)
        query = parse_qs(parsed.query)

        if parsed.path == "/health":
            if not self.account.ready():
                self.unavailable()
                return
            self.send_json(200, {"ok": True})
            return
        if not self.account.ready():
            self.unavailable()
            return

        try:
            self.route(parsed.path, query)
        except Exception as error:  # noqa: BLE001 - a sidecar failure is data
            self.send_json(502, {"error": "sidecar failure", "reason": str(error)})

    def route(self, path: str, query: dict) -> None:
        client = self.account.client
        if path == "/search":
            text = (query.get("q") or [""])[0].strip()
            if not text:
                self.send_json(400, {"error": "q is required"})
                return
            try:
                limit = int((query.get("limit") or ["20"])[0])
            except ValueError:
                self.send_json(400, {"error": "limit must be a number"})
                return
            rows = client.search(text, filter="songs", limit=max(1, min(limit, 50)))
            self.send_json(200, {"tracks": tracks(rows)})
            return
        if path == "/account":
            info = client.get_account_info() or {}
            self.send_json(200, {"account": {
                "name": str(info.get("accountName") or ""),
                "handle": str(info.get("channelHandle") or ""),
            }})
            return
        if path == "/likes":
            self.send_json(200, {"tracks": tracks(client.get_liked_songs(limit=None).get("tracks"))})
            return
        if path == "/library/songs":
            self.send_json(200, {"tracks": tracks(client.get_library_songs(limit=None))})
            return
        if path == "/library/albums":
            self.send_json(200, {"albums": [album(row) for row in client.get_library_albums(limit=None) or []]})
            return
        if path == "/library/artists":
            self.send_json(200, {"artists": [artist(row) for row in client.get_library_artists(limit=None) or []]})
            return
        if path == "/home":
            self.send_json(200, {"sections": home_sections(client.get_home(limit=8))})
            return
        if path == "/playlists":
            self.send_json(200, {"playlists": [playlist(row) for row in client.get_library_playlists(limit=None) or []]})
            return
        match = re.fullmatch(r"/playlists/([^/]+)/tracks", path)
        if match:
            playlist_id = match.group(1)
            payload = client.get_playlist(playlist_id, limit=None) or {}
            self.send_json(200, {"tracks": tracks(payload.get("tracks"))})
            return
        match = re.fullmatch(r"/albums/([^/]+)/tracks", path)
        if match:
            payload = client.get_album(match.group(1)) or {}
            self.send_json(200, {"tracks": tracks(payload.get("tracks"))})
            return
        match = re.fullmatch(r"/artists/([^/]+)/tracks", path)
        if match:
            payload = client.get_artist(match.group(1)) or {}
            songs = payload.get("songs") or {}
            rows = songs.get("results") if isinstance(songs, dict) else songs
            self.send_json(200, {"tracks": tracks(rows)})
            return
        self.send_json(404, {"error": "unknown endpoint"})

    def do_POST(self) -> None:  # noqa: N802 - http.server API
        parsed = urlparse(self.path)
        if not self.account.ready():
            self.unavailable()
            return
        length = int(self.headers.get("Content-Length") or 0)
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            self.send_json(400, {"error": "invalid json"})
            return
        if parsed.path == "/ratings":
            video_id = str(body.get("video_id") or "").strip()
            rating = str(body.get("rating") or "").strip().upper()
            if not video_id or rating not in {"LIKE", "INDIFFERENT", "DISLIKE"}:
                self.send_json(400, {"error": "video_id and rating are required"})
                return
            try:
                self.account.client.rate_song(video_id, rating)
            except Exception as error:  # noqa: BLE001
                self.send_json(502, {"error": "rating failed", "reason": str(error)})
                return
            self.send_json(200, {"ok": True})
            return
        self.send_json(404, {"error": "unknown endpoint"})


def create_server(account: Account, host: str, port: int) -> ThreadingHTTPServer:
    handler = type("BoundHandler", (Handler,), {"account": account})
    return ThreadingHTTPServer((host, port), handler)


def main() -> int:
    parser = argparse.ArgumentParser(description="GrooveSync YouTube Music browse sidecar")
    parser.add_argument("--auth", default=os.environ.get("GROOVESYNC_YTMUSIC_AUTH", ""),
                        help="ytmusicapi browser auth file")
    parser.add_argument("--host", default=os.environ.get("GROOVESYNC_YTMUSIC_HOST", "127.0.0.1"))
    parser.add_argument("--port", type=int, default=int(os.environ.get("GROOVESYNC_YTMUSIC_PORT", "8099")))
    parser.add_argument("--check", action="store_true", help="validate the auth file and exit")
    args = parser.parse_args()

    account = Account(args.auth)
    if args.check:
        if not account.ready():
            print(account.error, file=sys.stderr)
            return 2
        print("ok")
        return 0

    server = create_server(account, args.host, args.port)
    if account.ready():
        sys.stderr.write(f"listening on http://{args.host}:{args.port}\n")
    else:
        sys.stderr.write(f"starting unauthenticated: {account.error}\n")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
