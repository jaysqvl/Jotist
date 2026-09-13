package transcription

import (
	_ "embed"
	"encoding/json"
)

// These records supplement the common leaderboard; different datasets and
// evaluators must not be silently substituted into its accuracy ordering.
//
//go:embed additional_benchmarks.json
var additionalBenchmarksJSON []byte

type publishedEvaluation struct {
	Model       string  `json:"model"`
	Metric      string  `json:"metric"`
	Dataset     string  `json:"dataset"`
	Value       float64 `json:"value"`
	Source      string  `json:"source"`
	SourceLabel string  `json:"source_label"`
	Notes       string  `json:"notes"`
	Provenance  string  `json:"provenance"`
	Retrieved   string  `json:"retrieved"`
}

func additionalEvaluations(adapterID, model string) string {
	var all []publishedEvaluation
	if err := json.Unmarshal(additionalBenchmarksJSON, &all); err != nil {
		return "[]"
	}
	selected := make([]publishedEvaluation, 0)
	for _, row := range all {
		matches := row.Model == model
		if adapterID == ModelWhisperX {
			matches = row.SourceLabel == "OpenAI · Table 9" || row.SourceLabel == "Superwhisper · September 2026"
		} else if adapterID == ModelPyannote {
			matches = matches || row.Model == "pyannote/speaker-diarization-3.1"
		} else if adapterID == "suplime" {
			matches = row.Model == "rewayai/suplime" || row.Model == "rewayai/suplime-large"
		}
		if matches {
			selected = append(selected, row)
		}
	}
	encoded, _ := json.Marshal(selected)
	return string(encoded)
}
