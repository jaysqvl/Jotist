package presentation

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"scriberr/internal/transcription/interfaces"
)

func id(value string) *string { return &value }

func wordTranscript(words []interfaces.TranscriptWord) interfaces.TranscriptResult {
	t := interfaces.TranscriptResult{WordSegments: words}
	for _, w := range words {
		t.Segments = append(t.Segments, interfaces.TranscriptSegment{Start: w.Start, End: w.End, Text: w.Word, Speaker: w.Speaker})
		t.Text += w.Word + " "
	}
	return t
}

func TestWordAdaptersShareGroupingWithoutChangingEvidence(t *testing.T) {
	for _, model := range []string{"CohereLabs/cohere-transcribe-03-2026", "Qwen/Qwen3-ASR-1.7B-hf", "Qwen/Qwen3-ASR-0.6B-hf", "ibm-granite/granite-speech-4.1-2b", "ibm-granite/granite-speech-4.1-2b-plus", "ibm-granite/granite-speech-5.0-470m-turboctc", "ibm-granite/granite-speech-5.0-470m-turboctc-nc", "Edge0/ARK-ASR-3B", "OpenMOSS-Team/MOSS-Transcribe-preview-2B", "OpenMOSS-Team/MOSS-Transcribe-Diarize", "mistralai/Voxtral-Mini-3B-2507", "mistralai/Voxtral-Mini-4B-Realtime-2602", "microsoft/VibeVoice-ASR-BitNet", "future-adapter"} {
		t.Run(model, func(t *testing.T) {
			input := wordTranscript([]interfaces.TranscriptWord{{Start: 1.92, End: 2.16, Word: "It", Speaker: id("2")}, {Start: 2.16, End: 2.16, Word: "would"}, {Start: 2.16, End: 2.4, Word: "be", Speaker: id("2")}, {Start: 2.4, End: 2.4, Word: "a"}, {Start: 2.4, End: 2.64, Word: "hair", Speaker: id("2")}, {Start: 2.64, End: 3.28, Word: "strictly", Speaker: id("2")}, {Start: 3.36, End: 3.6, Word: "sort", Speaker: id("2")}, {Start: 3.6, End: 3.68, Word: "of.", Speaker: id("2")}})
			input.ModelUsed = model
			before, _ := json.Marshal(input)
			got := Build(input)
			if got.Grouping != "speaker_turns" || len(got.Segments) != 1 || got.Segments[0].Text != "It would be a hair strictly sort of." {
				t.Fatalf("unexpected display: %+v", got)
			}
			if got.InferredPointSpeakers != 2 || got.SpeakerLabels["2"] != "SPEAKER_2" {
				t.Fatalf("point attribution: %+v", got)
			}
			if !reflect.DeepEqual(got.Segments[0].WordIndices, []int{0, 1, 2, 3, 4, 5, 6, 7}) {
				t.Fatal("word ownership is not exclusive and ordered")
			}
			after, _ := json.Marshal(input)
			if string(before) != string(after) {
				t.Fatal("raw words, timing or speakers changed")
			}
		})
	}
}

func TestSpeakerChangesPausesAndSentenceBoundaries(t *testing.T) {
	input := wordTranscript([]interfaces.TranscriptWord{{Start: 0, End: 1, Word: "Yes.", Speaker: id("A")}, {Start: 1, End: 2, Word: "I", Speaker: id("A")}, {Start: 2, End: 2, Word: "agree"}, {Start: 2, End: 3, Word: "but", Speaker: id("B")}, {Start: 5, End: 6, Word: "later", Speaker: id("B")}})
	got := Build(input)
	if len(got.Segments) != 5 || got.InferredPointSpeakers != 0 {
		t.Fatalf("merged a sentence, pause or ambiguous point: %+v", got)
	}
	if got.Segments[2].Speaker != nil {
		t.Fatal("guessed a speaker across a change")
	}
}

func TestPointAttributionDoesNotCrossUnknownSpeech(t *testing.T) {
	input := wordTranscript([]interfaces.TranscriptWord{
		{Start: 0, End: 2, Word: "known", Speaker: id("A")},
		{Start: 1, End: 2, Word: "uncertain"},
		{Start: 2, End: 2, Word: "point"},
		{Start: 2, End: 3, Word: "known", Speaker: id("A")},
	})
	got := Build(input)
	if got.InferredPointSpeakers != 0 {
		t.Fatal("guessed across an unknown spoken interval")
	}
}

func TestNativeSegmentsKeepTextAndEachWordHasOneOwner(t *testing.T) {
	input := interfaces.TranscriptResult{Segments: []interfaces.TranscriptSegment{{Start: 0, End: 2, Text: "Hello there.", Speaker: id("SPEAKER_0")}, {Start: 1, End: 3, Text: "Yes!", Speaker: id("SPEAKER_1")}, {Start: 3, End: 4, Text: "Next", Speaker: id("SPEAKER_0")}}, WordSegments: []interfaces.TranscriptWord{{Start: 0, End: 1, Word: "Hello", Speaker: id("SPEAKER_0")}, {Start: 1, End: 1, Word: "there.", Speaker: id("SPEAKER_0")}, {Start: 1.2, End: 1.5, Word: "Yes!", Speaker: id("SPEAKER_1")}, {Start: 3, End: 3, Word: "Next", Speaker: id("SPEAKER_0")}}}
	got := Build(input)
	for i, row := range got.Segments {
		if !reflect.DeepEqual(row.TranscriptSegment, input.Segments[i]) {
			t.Fatal("native row was rewritten")
		}
	}
	want := [][]int{{0, 1}, {2}, {3}}
	for i, indices := range want {
		if !reflect.DeepEqual(got.Segments[i].WordIndices, indices) {
			t.Fatalf("row %d: %+v", i, got.Segments[i])
		}
	}
}

func TestUnpunctuatedSpeechIsBoundedAndWordsAreConserved(t *testing.T) {
	words := make([]interfaces.TranscriptWord, 250)
	for i := range words {
		words[i] = interfaces.TranscriptWord{Start: float64(i) / 10, End: float64(i+1) / 10, Word: "repeat", Speaker: id("1")}
	}
	input := wordTranscript(words)
	got := Build(input)
	count := 0
	for _, row := range got.Segments {
		if len(row.WordIndices) > 80 {
			t.Fatal("unbounded row")
		}
		for _, index := range row.WordIndices {
			if index != count {
				t.Fatal("word reordered or duplicated")
			}
			count++
		}
	}
	if count != len(words) || strings.Join(strings.Fields(input.Text), " ") != strings.Join([]string{got.Segments[0].Text, got.Segments[1].Text, got.Segments[2].Text, got.Segments[3].Text}, " ") {
		t.Fatal("speech changed")
	}
}

func TestNativeWithoutWordsEmptyAndInvalidTiming(t *testing.T) {
	input := interfaces.TranscriptResult{Segments: []interfaces.TranscriptSegment{{Start: 1, End: 20, Text: "原文 stays unchanged.", Speaker: id("S01")}}}
	if got := Build(input); len(got.Segments) != 1 || got.Segments[0].Text != input.Segments[0].Text {
		t.Fatal("native text changed")
	}
	if got := Build(interfaces.TranscriptResult{}); len(got.Segments) != 0 {
		t.Fatal("invented speech")
	}
	input.Segments[0].End = math.Inf(1)
	if Build(input) != nil {
		t.Fatal("accepted invalid timing")
	}
}
