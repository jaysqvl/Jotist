package transcription

import (
	"encoding/json"
	"strconv"
	"strings"
)

type meetingRecommendation struct {
	Model    string `json:"model"`
	Rank     int    `json:"rank"`
	Reason   string `json:"reason"`
	Category string `json:"category"`
}

// Editorial starting order for English meetings, not an invented WER or a
// measured ranking on user audio. Consider both matched leaderboard results,
// contextual recognition, resource requirements and meeting features; retain
// sources separately. Core choices form the English shortlist. Specialist and
// legacy checkpoints remain supported and selectable in the full catalog.
var meetingRecommendations = []meetingRecommendation{
	{"CohereLabs/cohere-transcribe-03-2026", 1, "English meeting accuracy: lowest AMI WER in the shared leaderboard snapshot.", "core"},
	{"Qwen/Qwen3-ASR-1.7B-hf", 2, "English conversational accuracy: strongest conversational result here, with context and vocabulary support.", "core"},
	{"ibm-granite/granite-speech-4.1-2b", 3, "English technical meetings: strong meeting results with vocabulary hints for project names and technical terms.", "core"},
	{"nvidia/canary-qwen-2.5b", 4, "English accuracy alternative: stronger published English recognition than Parakeet, with greater resource demand.", "core"},
	{"nvidia/parakeet-tdt-0.6b-v3", 5, "Efficient GPU candidate for recorded English speech: compact recognizer with high published throughput and word timestamps.", "core"},
	{"Qwen/Qwen3-ASR-0.6B-hf", 6, "Smaller CPU candidate: retains context and vocabulary support with strong English conversational results.", "core"},
	{"ibm-granite/granite-speech-5.0-470m-turboctc", 7, "Compact CPU candidate: Apache-licensed English recognition with a smaller model footprint.", "core"},
	{"large-v3", 8, "Established Whisper baseline for English: broad evaluations and compatibility with existing workflows.", "core"},
	{"ibm-granite/granite-speech-5.0-470m-turboctc-nc", 9, "Compact alternative with lower published meeting WER; weights carry a noncommercial license.", "specialist"},
	{"Edge0/ARK-ASR-3B", 10, "Strong English results; its larger memory requirements favor it as an additional accuracy experiment.", "specialist"},
	{"ibm-granite/granite-speech-4.1-2b-plus", 11, "Specialist vocabulary and native word timing; its publisher evaluation uses a separate test setup.", "specialist"},
	{"OpenMOSS-Team/MOSS-Transcribe-Diarize", 12, "Native speaker labeling with competitive recognition; full-recording context increases memory demand.", "specialist"},
	{"OpenMOSS-Team/MOSS-Transcribe-preview-2B", 13, "Additional English ASR experiment: strong AMI result, with weaker conversational results than the leading choices.", "specialist"},
	{"microsoft/VibeVoice-ASR-BitNet", 14, "Quantized CPU alternative with promising publisher meeting results; requires separate runtime qualification.", "specialist"},
	{"nvidia/canary-1b-v2", 15, "Multilingual recognition and speech translation between English and 24 languages; lower priority for English-only meetings.", "specialist"},
	{"mistralai/Voxtral-Mini-3B-2507", 16, "Multilingual alternative; published English meeting results and memory requirements favor the core choices.", "specialist"},
	{"large-v2", 18, "Older full-size Whisper baseline; prefer large-v3 as the first Whisper candidate.", "legacy"},
	{"large-v1", 19, "Original full-size Whisper checkpoint; retained for established profiles.", "legacy"},
	{"medium.en", 20, "English-only Whisper compromise when full-size Whisper is too expensive.", "specialist"},
	{"medium", 21, "Multilingual Whisper compromise; usually lower priority than full-size models for accuracy.", "specialist"},
	{"small.en", 22, "English-only lightweight Whisper for constrained systems.", "specialist"},
	{"small", 23, "Lightweight multilingual Whisper with a recognition tradeoff against larger candidates.", "specialist"},
	{"base.en", 24, "Small English-only baseline for constrained systems.", "specialist"},
	{"base", 25, "Small multilingual baseline for constrained systems.", "specialist"},
	{"tiny.en", 26, "Minimum-size English-only baseline for speed and footprint.", "specialist"},
	{"tiny", 27, "Minimum-size multilingual baseline for speed and footprint.", "specialist"},
}

func addMeetingRecommendation(metadata map[string]string, adapterID, model string) {
	if adapterID == ModelWhisperX {
		rows := make([]meetingRecommendation, 0)
		for _, row := range meetingRecommendations {
			if !strings.Contains(row.Model, "/") {
				rows = append(rows, row)
			}
		}
		encoded, _ := json.Marshal(rows)
		metadata["meeting_recommendations"] = string(encoded)
		metadata["meeting_recommendation_language"] = "en"
		return
	}
	for _, row := range meetingRecommendations {
		if row.Model == model {
			metadata["meeting_recommendation_rank"] = strconv.Itoa(row.Rank)
			metadata["meeting_recommendation_reason"] = row.Reason
			metadata["meeting_recommendation_category"] = row.Category
			metadata["meeting_recommendation_language"] = "en"
			return
		}
	}
}
