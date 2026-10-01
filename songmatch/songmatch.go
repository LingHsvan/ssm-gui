// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

// Package songmatch implements the fuzzy song-title matching used by the
// GUI's OCR "Detect Song" flow. It deliberately has no cgo dependencies so it
// can be unit tested without the decoders/av toolchain.
package songmatch

import (
	"regexp"
	"sort"
	"strings"
)

// Threshold is the default minimum score for a match to count as confident.
const Threshold = 74

// maxCandidates caps how many scored songs are returned for the UI picker.
const maxCandidates = 8

// SongCandidate is one entry of the song database used for matching.
type SongCandidate struct {
	SongID int
	Titles []string
}

// Match is a scored title hit, JSON-serialized as one of the "candidates" of
// the /api/detect-song response.
type Match struct {
	SongID int    `json:"songId"`
	Title  string `json:"title"`
	Score  int    `json:"score"`
}

var nonWordRE = regexp.MustCompile(`[\s\p{P}\p{S}]+`)

// confusableFold maps characters that OCR often confuses to a canonical
// glyph. Every pair differs by a single stroke or is a katakana vs. kanji
// lookalike (e.g. 「ハ/八」「オ/才」「ニ/二」), which shows up as misreads all
// the time — #208 「上海ハニー」 being read as 「上海八一二」 is exactly such a
// case. Folding both the OCR text and the song titles through the same table
// makes such pairs compare equal; it only ever produces more (weaker)
// candidates, never a wrong confident match.
//
// Keep in sync with the CONFUSABLE list of gui/frontend/scripts/gen-hanzi-fold.mjs,
// which feeds the same folds into the search box via src/hanzi-fold.js.
var confusableFold = map[rune]rune{
	'ハ': '八', // katakana ha vs. kanji eight
	'ニ': '二', // katakana ni vs. kanji two
	'ー': '一', // prolonged sound mark vs. kanji one
	'ロ': '口', // katakana ro vs. kanji mouth
	'カ': '力', // katakana ka vs. kanji strength
	'ク': '夕', // katakana ku vs. kanji evening (same target as タ)
	'タ': '夕', // katakana ta vs. kanji evening
	'ト': '卜', // katakana to vs. divination
	'エ': '工', // katakana e vs. kanji work
	'オ': '才', // katakana o vs. kanji talent
	'ミ': '三', // katakana mi vs. kanji three
	'チ': '千', // katakana chi vs. kanji thousand
	// Roman numeral I, lowercase L and digit 1 are the same glyph in most UI
	// fonts and the OCR mixes them freely: #100034「Symbol II : 🜁」comes back as
	// 「Symbol?Il?:△」. Unfolded that reading is one character *longer* than the
	// title, so「Symbol I : △」(#100033) won a "contains" hit (92) while the
	// correct near-equal「Symbol II」dropped to 74. Folding restores the equality.
	'l': 'i',
	'1': 'i',
}

// FoldConfusables replaces OCR-confusable characters with their canonical
// form. Extend confusableFold (not this function) when new misreads show up.
func FoldConfusables(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if c, ok := confusableFold[r]; ok {
			r = c
		}
		b.WriteRune(r)
	}
	return b.String()
}

// foldHanzi maps each rune through the generated fold table in hanzi_fold.go,
// which folds simplified Chinese and Japanese shinjitai onto one canonical
// glyph per character class. Runes that are not in the table are kept as-is.
func foldHanzi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if c, ok := hanziFold[r]; ok {
			r = c
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Normalize lowercases, strips whitespace / punctuation / symbols, folds
// OCR-confusable katakana and folds hanzi variants. Applied to both sides of
// every comparison.
//
// Folding hanzi matters because the game draws titles with traditional and
// shinjitai glyphs while songs.json holds simplified ones: without it the OCR
// reading 「証命讚歌」 cannot reach the title 「证命赞歌」 (#100017).
func Normalize(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	return foldHanzi(FoldConfusables(nonWordRE.ReplaceAllString(s, "")))
}

// Score rates how well the OCR query matches a song title. Score tiers
// (after normalization and folding):
//
//	100: equal
//	 92: one contains the other, same length ballpark (ratio >= 0.55)
//	 86: one is a prefix of the other (kept for compatibility; prefix implies
//	     "contains" and therefore normally scores 92 first)
//	 84: edit similarity >= 0.90
//	 74: edit similarity >= 0.80
//	 62: two-character query fully contained in the title (weak on purpose)
//	 60: edit similarity >= 0.55 (candidate-class only, never confident)
//	  0: no usable signal
func Score(query, title string) int {
	q := Normalize(query)
	t := Normalize(title)
	if q == "" || t == "" {
		return 0
	}
	if q == t {
		return 100
	}
	qLen, tLen := len([]rune(q)), len([]rune(t))
	if qLen <= 1 || tLen <= 1 {
		// A single character matches far too much of the library to be a
		// usable signal.
		return 0
	}
	shortLen, longLen := qLen, tLen
	if qLen > tLen {
		shortLen, longLen = tLen, qLen
	}
	if strings.Contains(q, t) || strings.Contains(t, q) {
		if qLen == 2 {
			// A two-char query contained in the title (e.g.「上海」in
			// 「上海ハニー」) is a weak hint: keep it for the candidate
			// list, never as a confident match.
			return 62
		}
		if float64(shortLen)/float64(longLen) < 0.55 {
			return 0
		}
		return 92
	}
	if strings.HasPrefix(q, t) || strings.HasPrefix(t, q) {
		return 86
	}
	d := levenshteinDistance(q, t)
	maxLen := qLen
	if tLen > maxLen {
		maxLen = tLen
	}
	similarity := 1 - float64(d)/float64(maxLen)
	switch {
	case similarity >= 0.90:
		return 84
	case similarity >= 0.80:
		return 74
	case similarity >= 0.55:
		return 60
	}
	return 0
}

// Rank scores every candidate against every OCR text and returns the best
// match plus the top list (at most maxCandidates entries, sorted by score
// desc then song id asc for determinism).
//
// The texts concatenated in the given (reading) order are scored as one extra
// query: the detector happily splits a short title into several boxes, and then
// no single box covers enough of the title to score. #100039「Choir ‘S’ Choir」
// comes back as ["Choir","s","Choir"], where "Choir" alone is under the
// containment ratio (5/11) and scores nothing — the reading-order join is an
// exact match. Callers must therefore pass the texts in reading order; gui
// sorts the boxes before calling.
func Rank(texts []string, cands []SongCandidate) (best Match, top []Match) {
	queries := texts
	if joined := joinTexts(texts); joined != "" {
		queries = append(append(make([]string, 0, len(texts)+1), texts...), joined)
	}
	topBySong := map[int]Match{}
	for _, text := range queries {
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, song := range cands {
			maxScore, titleHit := 0, ""
			for _, title := range song.Titles {
				if sc := Score(text, title); sc > maxScore {
					maxScore, titleHit = sc, title
				}
			}
			if maxScore == 0 {
				continue
			}
			if prev, ok := topBySong[song.SongID]; !ok || maxScore > prev.Score {
				topBySong[song.SongID] = Match{SongID: song.SongID, Title: titleHit, Score: maxScore}
			}
		}
	}
	list := make([]Match, 0, len(topBySong))
	for _, c := range topBySong {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Score == list[j].Score {
			return list[i].SongID < list[j].SongID
		}
		return list[i].Score > list[j].Score
	})
	if len(list) > maxCandidates {
		list = list[:maxCandidates]
	}
	if len(list) > 0 {
		best = list[0]
	}
	return best, list
}

// joinTexts concatenates the OCR texts in reading order, or returns "" when
// there is nothing to join (a single text is already scored on its own).
func joinTexts(texts []string) string {
	if len(texts) < 2 {
		return ""
	}
	var b strings.Builder
	for _, t := range texts {
		b.WriteString(strings.TrimSpace(t))
	}
	return b.String()
}

// Detect ranks the OCR texts against the candidates and reports whether the
// top hit is confident: at or above the threshold, and either an exact match
// or leading the runner-up by a margin. Tied top scores are never confident —
// the caller is expected to offer the candidate picker instead.
func Detect(texts []string, cands []SongCandidate, threshold int) (best Match, top []Match, confident bool) {
	if threshold <= 0 {
		threshold = Threshold
	}
	best, top = Rank(texts, cands)
	confident = best.Score >= threshold && best.SongID > 0
	if confident && best.Score < 92 && len(top) > 1 {
		confident = best.Score-top[1].Score >= 8
	}
	if len(top) > 1 && top[0].Score == top[1].Score {
		confident = false
	}
	return best, top, confident
}

// levenshteinDistance is the classic edit distance over runes.
func levenshteinDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			ins := curr[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			m := ins
			if del < m {
				m = del
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
