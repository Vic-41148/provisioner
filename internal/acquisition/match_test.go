// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

import "testing"

func TestPreferAudioRanksKindsThenConfidence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		candidates []Candidate
		wantID     string
	}{
		{
			name: "audio beats a more confident video",
			candidates: []Candidate{
				{ID: "video", Kind: "video", Confidence: 0.99},
				{ID: "audio", Kind: "audio", Confidence: 0.6},
			},
			wantID: "audio",
		},
		{
			name: "confidence breaks a tie inside a kind",
			candidates: []Candidate{
				{ID: "low", Kind: "audio", Confidence: 0.4},
				{ID: "high", Kind: "audio", Confidence: 0.9},
			},
			wantID: "high",
		},
		{
			name: "video beats an upload",
			candidates: []Candidate{
				{ID: "upload", Kind: "upload", Confidence: 0.95},
				{ID: "video", Kind: "video", Confidence: 0.3},
			},
			wantID: "video",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			match, ok := PreferAudio(tc.candidates)
			if !ok {
				t.Fatal("PreferAudio() = false, want a match")
			}
			if match.Best.ID != tc.wantID {
				t.Fatalf("Best.ID = %q, want %q", match.Best.ID, tc.wantID)
			}
			if len(match.Alternates) != len(tc.candidates)-1 {
				t.Fatalf("Alternates = %d, want %d", len(match.Alternates), len(tc.candidates)-1)
			}
		})
	}
}

func TestPreferAudioEmpty(t *testing.T) {
	t.Parallel()

	if _, ok := PreferAudio(nil); ok {
		t.Fatal("PreferAudio(nil) = true, want false")
	}
}

func TestLowConfidence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		confidence float64
		threshold  float64
		want       bool
	}{
		{0.49, 0.5, true},
		{0.5, 0.5, false},
		{0.9, 0.5, false},
	}
	for _, tc := range cases {
		if got := LowConfidence(tc.confidence, tc.threshold); got != tc.want {
			t.Errorf("LowConfidence(%v, %v) = %v, want %v", tc.confidence, tc.threshold, got, tc.want)
		}
	}
}

func TestConfidenceUsesNormalisedTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		query  string
		title  string
		artist string
		want   float64
	}{
		{
			name:  "full match ignores punctuation and case",
			query: "Artist Song", title: "Artist - Song (Official Audio)", artist: "Artist",
			want: 1,
		},
		{
			name:  "half the tokens",
			query: "Artist Song", title: "Artist", artist: "",
			want: 0.5,
		},
		{
			name:  "no overlap",
			query: "Artist Song", title: "Unrelated", artist: "Nobody",
			want: 0,
		},
		{
			name:  "empty query",
			query: "", title: "Anything", artist: "Anyone",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Confidence(tc.query, tc.title, tc.artist); got != tc.want {
				t.Fatalf("Confidence() = %v, want %v", got, tc.want)
			}
		})
	}
}
