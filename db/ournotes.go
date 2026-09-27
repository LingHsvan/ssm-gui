// Copyright (C) 2024, 2025, 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OurNotesDir is where the decoded BanG Dream! Our Notes assets live, relative
// to the working directory. Layout:
//
//	songs.json                     song DB (chart paths + jacket file names)
//	scores/<NNNN>/<NNNN>_<DD>.json  charts, DD = 00/01/02/03
//	jackets/<name>.png             full jackets
//	jackets/small/<name>.png       thumbnails
//	extra/                         special charts (archive only, unused here)
const OurNotesDir = "assets/ournotes"

// ourNotesDifficulties maps the chart keys of songs.json onto the difficulty
// indices the front-end uses (0..3). Our Notes has no SPECIAL/APPEND.
var ourNotesDifficulties = []string{"easy", "normal", "hard", "expert"}

type OurNotesChart struct {
	File      string `json:"file"`
	Level     int    `json:"level"`
	NoteCount int    `json:"noteCount"`
}

type OurNotesSong struct {
	ID       int      `json:"id"`
	Title    string   `json:"title"`
	Phonetic string   `json:"phonetic"`
	Bands    []string `json:"bands"`

	JacketAssetName string `json:"jacketAssetName"`
	JacketFile      string `json:"jacketFile"`
	JacketThumbFile string `json:"jacketThumbFile"`

	Charts map[string]OurNotesChart `json:"charts"`
}

// Artist returns the band names of a song; the sibling databases expose a
// combined artist string and the TUI renders it the same way.
func (s *OurNotesSong) Artist() string {
	names := make([]string, 0, len(s.Bands))
	for _, b := range s.Bands {
		if b != "" {
			names = append(names, b)
		}
	}
	return strings.Join(names, ", ")
}

type OurNotesDB struct {
	dir   string
	songs []*OurNotesSong
	byID  map[int]*OurNotesSong
}

var _ MusicDatabase = (*OurNotesDB)(nil)

// NewOurNotesDB loads the shipped Our Notes database from OurNotesDir.
func NewOurNotesDB() (*OurNotesDB, error) {
	return LoadOurNotesDB(OurNotesDir)
}

// LoadOurNotesDB loads the Our Notes database from an explicit directory.
func LoadOurNotesDB(dir string) (*OurNotesDB, error) {
	path := filepath.Join(dir, "songs.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc struct {
		Songs []*OurNotesSong `json:"songs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	d := &OurNotesDB{
		dir:   dir,
		songs: make([]*OurNotesSong, 0, len(doc.Songs)),
		byID:  make(map[int]*OurNotesSong, len(doc.Songs)),
	}
	for _, s := range doc.Songs {
		if s == nil || s.ID <= 0 {
			continue
		}
		d.songs = append(d.songs, s)
		d.byID[s.ID] = s
	}
	return d, nil
}

func (d *OurNotesDB) Songs() []*OurNotesSong { return d.songs }

func (d *OurNotesDB) Song(id int) (*OurNotesSong, bool) {
	s, ok := d.byID[id]
	return s, ok
}

// ChartPath resolves a song id + difficulty name to the chart file on disk.
func (d *OurNotesDB) ChartPath(id int, difficulty string) (string, bool) {
	s, ok := d.byID[id]
	if !ok {
		return "", false
	}
	c, ok := s.Charts[strings.ToLower(strings.TrimSpace(difficulty))]
	if !ok || c.File == "" {
		return "", false
	}
	return d.safeJoin(c.File)
}

// JacketPath resolves a song id to its jacket image (thumbnail when thumb is
// true).
func (d *OurNotesDB) JacketPath(id int, thumb bool) (string, bool) {
	s, ok := d.byID[id]
	if !ok {
		return "", false
	}
	name := s.JacketFile
	if thumb {
		name = s.JacketThumbFile
	}
	if name == "" {
		return "", false
	}
	return d.safeJoin(filepath.Join("jackets", name))
}

// safeJoin joins a database-relative slash path onto the data directory and
// refuses anything that escapes it.
func (d *OurNotesDB) safeJoin(rel string) (string, bool) {
	joined := filepath.Join(d.dir, filepath.FromSlash(rel))
	out, err := filepath.Rel(d.dir, joined)
	if err != nil || out == ".." || strings.HasPrefix(out, ".."+string(filepath.Separator)) {
		return "", false
	}
	return joined, true
}

// ── MusicDatabase ───────────────────────────────────────────────────────────

func (d *OurNotesDB) Title(id int, format string) string {
	s, ok := d.byID[id]
	if !ok {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(format, "${title}", s.Title), "${artist}", s.Artist())
}

func (d *OurNotesDB) Jacket(id int) (string, string) {
	thumb, _ := d.JacketPath(id, true)
	full, _ := d.JacketPath(id, false)
	return thumb, full
}

// ── /api/songdb payload ─────────────────────────────────────────────────────

type ourNotesPayloadSong struct {
	MusicTitle []string                        `json:"musicTitle"`
	Difficulty map[string]*ourNotesPayloadDiff `json:"difficulty"`
	BandID     int                             `json:"bandId"`
}

type ourNotesPayloadDiff struct {
	PlayLevel      int `json:"playLevel"`
	TotalNoteCount int `json:"totalNoteCount"`
}

type ourNotesPayloadBand struct {
	BandName []string `json:"bandName"`
}

// Payload renders the /api/songdb?mode=ournotes body in the same shape the
// BanG branch uses, so the front-end's normalizeSongDB needs no special case:
//   - songs is an OBJECT keyed by song id
//   - musicTitle[2] holds the display title (pickName prefers index 2 for
//     non-pjsk modes and index 0 for pjsk, so both carry the title)
//   - bands is a synthesized table so db.bands[song.bandId].bandName works
//   - difficulty keys are the front-end's 0..3 indices
func (d *OurNotesDB) Payload() ([]byte, error) {
	seen := map[string]bool{}
	for _, s := range d.songs {
		for _, b := range s.Bands {
			if b != "" {
				seen[b] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	bandID := make(map[string]int, len(names))
	bands := make(map[string]*ourNotesPayloadBand, len(names))
	for i, n := range names {
		id := i + 1
		bandID[n] = id
		bands[strconv.Itoa(id)] = &ourNotesPayloadBand{BandName: []string{n, n, n, n, ""}}
	}

	songs := make(map[string]*ourNotesPayloadSong, len(d.songs))
	for _, s := range d.songs {
		diffs := make(map[string]*ourNotesPayloadDiff, len(ourNotesDifficulties))
		for idx, name := range ourNotesDifficulties {
			c, ok := s.Charts[name]
			if !ok {
				continue
			}
			diffs[strconv.Itoa(idx)] = &ourNotesPayloadDiff{
				PlayLevel:      c.Level,
				TotalNoteCount: c.NoteCount,
			}
		}

		title := s.Title
		if title == "" {
			title = s.Phonetic
		}
		// A song without a band (tie-up tracks) gets bandId 0, which simply
		// resolves to no artist on the front-end.
		songs[strconv.Itoa(s.ID)] = &ourNotesPayloadSong{
			MusicTitle: []string{title, s.Phonetic, title},
			Difficulty: diffs,
			BandID:     bandID[firstBand(s)],
		}
	}

	return json.Marshal(struct {
		Songs map[string]*ourNotesPayloadSong `json:"songs"`
		Bands map[string]*ourNotesPayloadBand `json:"bands"`
	}{Songs: songs, Bands: bands})
}

func firstBand(s *OurNotesSong) string {
	if len(s.Bands) == 0 {
		return ""
	}
	return s.Bands[0]
}
