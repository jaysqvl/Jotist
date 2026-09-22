// Package presentation projects adapter results into readable speaker turns.
// Model output stays immutable: this is a versioned read model, not an ASR pass.
package presentation

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"scriberr/internal/transcription/interfaces"
)

const Version = 1

// Row owns its word indices exclusively. Consumers must not recover membership
// with timestamp tolerances: adjacent and zero-duration words can share a time.
type Row struct {
	interfaces.TranscriptSegment
	WordIndices []int `json:"word_indices"`
}

type Result struct {
	Version               int               `json:"version"`
	Grouping              string            `json:"grouping"`
	Segments              []Row             `json:"segments"`
	SpeakerLabels         map[string]string `json:"speaker_labels"`
	InferredPointSpeakers int               `json:"inferred_point_speakers"`
}

func speaker(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valid(start, end float64) bool {
	return !math.IsNaN(start) && !math.IsNaN(end) && !math.IsInf(start, 0) && !math.IsInf(end, 0) && start >= 0 && end >= start
}

// Build accepts the shared TranscriptionAdapter contract, including historical
// results without granularity metadata. Native phrase/turn boundaries survive.
func Build(t interfaces.TranscriptResult) *Result {
	for _, s := range t.Segments {
		if !valid(s.Start, s.End) {
			return nil
		}
	}
	for _, w := range t.WordSegments {
		if !valid(w.Start, w.End) {
			return nil
		}
	}
	r := &Result{Version: Version, Grouping: "native_segments", Segments: []Row{}, SpeakerLabels: map[string]string{}}
	wordShaped := len(t.WordSegments) > 0 && len(t.Segments) == len(t.WordSegments)
	for i := 0; wordShaped && i < len(t.Segments); i++ {
		s, w := t.Segments[i], t.WordSegments[i]
		wordShaped = s.Start == w.Start && s.End == w.End && strings.TrimSpace(s.Text) == strings.TrimSpace(w.Word)
	}
	if wordShaped || (len(t.Segments) == 0 && len(t.WordSegments) > 0) {
		r.Grouping = "speaker_turns"
		groupWords(t, r)
	} else {
		for _, s := range t.Segments {
			r.Segments = append(r.Segments, Row{TranscriptSegment: s, WordIndices: []int{}})
		}
		assignWordIndices(t.WordSegments, r.Segments)
	}
	ids := map[string]bool{}
	for _, s := range r.Segments {
		if id := speaker(s.Speaker); id != "" {
			ids[id] = true
		}
	}
	for _, w := range t.WordSegments {
		if id := speaker(w.Speaker); id != "" {
			ids[id] = true
		}
	}
	for id := range ids {
		label := id
		if numericID.MatchString(id) {
			label = "SPEAKER_" + id
			if ids[label] {
				label = "Speaker ID " + id
			}
		}
		r.SpeakerLabels[id] = label
	}
	return r
}

var numericID = regexp.MustCompile(`^[0-9]+$`)
var sentenceEnd = regexp.MustCompile(`[.!?。！？]["'”’)]*$`)

func groupWords(t interfaces.TranscriptResult, r *Result) {
	words := t.WordSegments
	labels := make([]string, len(words))
	nextKnown := make([]int, len(words))
	next := -1
	for i := len(words) - 1; i >= 0; i-- {
		nextKnown[i] = next
		labels[i] = speaker(words[i].Speaker)
		if labels[i] == "" && len(t.Segments) == len(words) {
			labels[i] = speaker(t.Segments[i].Speaker)
		}
		if labels[i] != "" {
			next = i
		} else if words[i].Start != words[i].End || (next >= 0 && words[i].End != words[next].Start) {
			next = -1
		}
	}
	previous := -1
	for i, w := range words {
		// Only fill a point-sized display attribution when immediately adjacent
		// timed words agree. Never cross a gap, speaker change or unknown span.
		if labels[i] == "" && w.Start == w.End && previous >= 0 && nextKnown[i] >= 0 {
			n := nextKnown[i]
			if labels[previous] == labels[n] && math.Abs(words[previous].End-w.Start) < 1e-6 && math.Abs(words[n].Start-w.Start) < 1e-6 {
				labels[i] = labels[previous]
				r.InferredPointSpeakers++
			}
		}
		if labels[i] != "" {
			previous = i
		} else {
			previous = -1
		}
		text := w.Word
		if len(t.Segments) == len(words) {
			text = t.Segments[i].Text
		}
		text = strings.TrimSpace(text)
		newRow := len(r.Segments) == 0
		if !newRow {
			last := r.Segments[len(r.Segments)-1]
			newRow = speaker(last.Speaker) != labels[i] || w.Start-words[i-1].End >= 1 || w.Start < words[i-1].Start || w.End-last.Start > 30 || len(last.WordIndices) >= 80 || sentenceEnd.MatchString(strings.TrimSpace(last.Text))
		}
		if newRow {
			var id *string
			if labels[i] != "" {
				value := labels[i]
				id = &value
			}
			r.Segments = append(r.Segments, Row{TranscriptSegment: interfaces.TranscriptSegment{Start: w.Start, End: w.End, Text: text, Speaker: id}, WordIndices: []int{i}})
		} else {
			row := &r.Segments[len(r.Segments)-1]
			row.Text += " " + text
			row.End = math.Max(row.End, w.End)
			row.WordIndices = append(row.WordIndices, i)
		}
	}
}

// Assign each word once by greatest overlap, respecting explicit speaker
// mismatches. A sorted index bounds the search without reordering output.
func assignWordIndices(words []interfaces.TranscriptWord, rows []Row) {
	order := make([]int, len(words))
	owners := make([]int, len(words))
	scores := make([]float64, len(words))
	maxDuration := 0.0
	for i, w := range words {
		order[i] = i
		owners[i] = -1
		maxDuration = math.Max(maxDuration, w.End-w.Start)
	}
	sort.SliceStable(order, func(i, j int) bool { return words[order[i]].Start < words[order[j]].Start })
	for rowIndex, row := range rows {
		first := sort.Search(len(order), func(i int) bool { return words[order[i]].Start >= row.Start-maxDuration })
		for _, wordIndex := range order[first:] {
			w := words[wordIndex]
			if w.Start > row.End {
				break
			}
			if speaker(w.Speaker) != "" && speaker(row.Speaker) != "" && speaker(w.Speaker) != speaker(row.Speaker) {
				continue
			}
			score := math.Min(row.End, w.End) - math.Max(row.Start, w.Start)
			if w.Start == w.End && w.Start >= row.Start && w.Start < row.End {
				score = 1e-9
			}
			if score > scores[wordIndex] {
				scores[wordIndex] = score
				owners[wordIndex] = rowIndex
			}
		}
	}
	for i, owner := range owners {
		if owner >= 0 {
			rows[owner].WordIndices = append(rows[owner].WordIndices, i)
		}
	}
}
