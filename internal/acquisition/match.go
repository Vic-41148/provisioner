// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 GrooveSync Authors

package acquisition

// Candidate is one search result offered by a provider.
type Candidate struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Kind       string  `json:"kind"` // "audio", "video", "upload"
	Confidence float64 `json:"confidence"`
}

// MatchResult keeps the chosen candidate plus alternates.
type MatchResult struct {
	Best       Candidate   `json:"best"`
	Alternates []Candidate `json:"alternates"`
}

// PreferAudio picks the best candidate, preferring audio tracks over music
// videos and uploads, while keeping alternates with their confidence flag.
func PreferAudio(candidates []Candidate) (MatchResult, bool) {
	if len(candidates) == 0 {
		return MatchResult{}, false
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if kindRank(candidate.Kind) > kindRank(best.Kind) {
			best = candidate
			continue
		}
		if kindRank(candidate.Kind) == kindRank(best.Kind) && candidate.Confidence > best.Confidence {
			best = candidate
		}
	}
	alternates := make([]Candidate, 0, len(candidates)-1)
	for _, candidate := range candidates {
		if candidate.ID == best.ID {
			continue
		}
		alternates = append(alternates, candidate)
	}
	return MatchResult{Best: best, Alternates: alternates}, true
}

func kindRank(kind string) int {
	switch kind {
	case "audio":
		return 3
	case "video":
		return 2
	case "upload":
		return 1
	default:
		return 0
	}
}

// LowConfidence reports whether a match must go to review instead of
// downloading silently.
func LowConfidence(confidence, threshold float64) bool {
	return confidence < threshold
}
