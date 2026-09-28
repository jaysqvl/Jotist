package transcription

import (
	_ "embed"
	"encoding/json"
)

// Publisher coverage and the pinned multilingual evaluation use one shared
// source. Coverage describes recognition, not qualification of later stages.
//
//go:embed language_comparison.json
var languageComparisonJSON []byte

type languageWER struct {
	MeanWER  float64            `json:"mean_wer"`
	Datasets map[string]float64 `json:"datasets"`
}

type modelLanguageComparison struct {
	Model              string                 `json:"model"`
	PublisherCoverage  string                 `json:"publisher_coverage"`
	PublisherLanguages []string               `json:"publisher_languages,omitempty"`
	PublisherSource    string                 `json:"publisher_source"`
	Notes              string                 `json:"notes,omitempty"`
	WER                map[string]languageWER `json:"wer,omitempty"`
	Retrieved          string                 `json:"retrieved"`
	BenchmarkSource    string                 `json:"benchmark_source,omitempty"`
	BenchmarkRevision  string                 `json:"benchmark_revision,omitempty"`
	BenchmarkNotes     string                 `json:"benchmark_notes,omitempty"`
}

type languageComparisonSnapshot struct {
	Retrieved         string                    `json:"retrieved"`
	BenchmarkSource   string                    `json:"benchmark_source"`
	BenchmarkRevision string                    `json:"benchmark_revision"`
	BenchmarkNotes    string                    `json:"benchmark_notes"`
	Models            []modelLanguageComparison `json:"models"`
}

func addLanguageComparisonMetadata(metadata map[string]string, adapterID, model string) {
	var snapshot languageComparisonSnapshot
	if err := json.Unmarshal(languageComparisonJSON, &snapshot); err != nil {
		return
	}
	selected := make([]modelLanguageComparison, 0)
	for _, row := range snapshot.Models {
		matches := row.Model == model
		if adapterID == ModelWhisperX {
			matches = isWhisperLanguageCheckpoint(row.Model)
		}
		if !matches {
			continue
		}
		row.Retrieved = snapshot.Retrieved
		if len(row.WER) > 0 {
			row.BenchmarkSource = snapshot.BenchmarkSource
			row.BenchmarkRevision = snapshot.BenchmarkRevision
			row.BenchmarkNotes = snapshot.BenchmarkNotes
		}
		selected = append(selected, row)
	}
	encoded, _ := json.Marshal(selected)
	metadata["language_comparisons"] = string(encoded)
	if len(selected) > 0 {
		metadata["language_comparison_checkpoint"] = model
	}
}

func isWhisperLanguageCheckpoint(model string) bool {
	for _, checkpoint := range whisperCheckpointParameters {
		if checkpoint.model == model {
			return true
		}
	}
	return false
}
