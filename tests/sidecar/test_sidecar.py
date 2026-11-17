# SPDX-License-Identifier: AGPL-3.0-or-later
# Copyright (C) 2026 GrooveSync Authors

import importlib.util
import json
import pathlib
import sys
import threading
import types
import unittest
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE = ROOT / "sidecar" / "ytmusic_sidecar.py"


class FakeYTMusic:
    def __init__(self, auth):
        self.auth = auth

    def get_search_results(self, query, filter=None, limit=20):
        assert filter == "songs"
        return [{"videoId": "v1", "title": f"Found {query}", "artists": [{"name": "Artist"}],
                 "album": {"name": "Album"}, "duration_seconds": 185, "resultType": "song"}]

    def get_liked_songs(self, limit=None):
        return {"tracks": [{"videoId": "v2", "title": "Liked", "artists": [{"name": "Artist"}],
                            "duration_seconds": 200}]}

    def get_library_songs(self, limit=None):
        return [{"videoId": "v3", "title": "Saved", "artists": ["Artist"], "duration": "3:05"}]

    def get_library_albums(self):
        return [{"browseId": "a1", "title": "Album", "artists": [{"name": "Artist"}], "year": "2020"}]

    def get_library_artists(self):
        return [{"browseId": "ar1", "artist": "Artist"}]

    def get_home(self, limit=8):
        return [{"title": "Listen again", "contents": [{"videoId": "v4", "title": "Again"}, {"title": "Shelf"}]}]

    def get_library_playlists(self):
        return [{"playlistId": "p1", "title": "Mix", "count": 2}]

    def get_playlist(self, playlist_id, limit=None):
        return {"tracks": [{"videoId": "v5", "title": f"From {playlist_id}"}]}

    def rate_song(self, video_id, rating):
        self.rated = (video_id, rating)


class SidecarTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        fake = types.ModuleType("ytmusicapi")
        fake.YTMusic = FakeYTMusic
        sys.modules["ytmusicapi"] = fake
        spec = importlib.util.spec_from_file_location("ytmusic_sidecar", MODULE)
        cls.sidecar = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.sidecar)

        auth = pathlib.Path(__file__).with_name("auth.json")
        auth.write_text("{}")
        cls.auth = auth
        cls.server = cls.sidecar.create_server(cls.sidecar.Account(str(auth)), "127.0.0.1", 0)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_address[1]}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.auth.unlink(missing_ok=True)

    def get(self, path):
        with urllib.request.urlopen(self.base + path, timeout=5) as response:
            return json.loads(response.read())

    def test_health(self):
        self.assertEqual({"ok": True}, self.get("/health"))

    def test_search_maps_tracks(self):
        payload = self.get("/search?q=night&limit=3")
        track = payload["tracks"][0]
        self.assertEqual("v1", track["video_id"])
        self.assertEqual("Found night", track["title"])
        self.assertEqual(["Artist"], track["artists"])
        self.assertEqual("Album", track["album"])
        self.assertEqual(185000, track["duration_ms"])

    def test_library_routes(self):
        self.assertEqual("v2", self.get("/likes")["tracks"][0]["video_id"])
        self.assertEqual("v3", self.get("/library/songs")["tracks"][0]["video_id"])
        self.assertEqual(185000, self.get("/library/songs")["tracks"][0]["duration_ms"])
        self.assertEqual("a1", self.get("/library/albums")["albums"][0]["browse_id"])
        self.assertEqual("Artist", self.get("/library/artists")["artists"][0]["name"])
        home = self.get("/home")["sections"]
        self.assertEqual("Listen again", home[0]["title"])
        self.assertEqual("v4", home[0]["tracks"][0]["video_id"])
        self.assertEqual("p1", self.get("/playlists")["playlists"][0]["id"])
        self.assertEqual("v5", self.get("/playlists/p1/tracks")["tracks"][0]["video_id"])

    def test_rating(self):
        request = urllib.request.Request(
            self.base + "/ratings",
            data=json.dumps({"video_id": "v1", "rating": "LIKE"}).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=5) as response:
            self.assertEqual(200, response.status)

    def test_unauthenticated_reports_reason(self):
        server = self.sidecar.create_server(self.sidecar.Account(""), "127.0.0.1", 0)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base = f"http://127.0.0.1:{server.server_address[1]}"
        try:
            with self.assertRaises(urllib.error.HTTPError) as raised:
                urllib.request.urlopen(base + "/health", timeout=5)
            self.assertEqual(503, raised.exception.code)
            body = json.loads(raised.exception.read())
            self.assertTrue(body["reason"])
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    unittest.main()
