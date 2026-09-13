package transcription

import (
	"encoding/json"
	"strconv"
	"strings"
)

type meetingRecommendation struct {
	Model  string `json:"model"`
	Rank   int    `json:"rank"`
	Reason string `json:"reason"`
}

// Editorial starting order for English meetings, not an invented WER or a
// measured ranking on user audio. Consider both matched leaderboard results,
// contextual recognition and meeting features; retain sources separately.
var meetingRecommendations = []meetingRecommendation{
	{"CohereLabs/cohere-transcribe-03-2026", 1, "Start here for meeting accuracy: lowest AMI WER in the shared leaderboard snapshot."},
	{"ibm-granite/granite-speech-4.1-2b", 2, "Strong meeting results with vocabulary hints for project names and technical terms."},
	{"Qwen/Qwen3-ASR-1.7B-hf", 3, "Strong engineering-meeting candidate: best conversational WER here, with context and vocabulary support."},
	{"ibm-granite/granite-speech-5.0-470m-turboctc-nc", 4, "Low meeting WER in a compact model; check the noncommercial license before choosing."},
	{"Edge0/ARK-ASR-3B", 5, "Strong results on both meeting and conversational tests; needs more memory than compact models."},
	{"ibm-granite/granite-speech-4.1-2b-plus", 6, "Useful meeting features and native timing; promising publisher evaluation, with a provisional placement across test setups."},
	{"ibm-granite/granite-speech-5.0-470m-turboctc", 7, "Compact Apache-licensed option with strong meeting results and a smaller memory footprint."},
	{"OpenMOSS-Team/MOSS-Transcribe-preview-2B", 8, "Strong AMI result, but weaker conversational results than the leading context-aware choices."},
	{"nvidia/canary-qwen-2.5b", 9, "Strong published meeting results; compare its handling of your technical vocabulary before adopting it."},
	{"OpenMOSS-Team/MOSS-Transcribe-Diarize", 10, "Convenient native speaker labeling with competitive recognition; full-recording context increases memory."},
	{"Qwen/Qwen3-ASR-0.6B-hf", 11, "Smaller context-aware alternative to Qwen 1.7B, with somewhat weaker recognition scores."},
	{"nvidia/parakeet-tdt-0.6b-v3", 12, "Compact, capable transcription baseline; less contextual guidance than the leading choices."},
	{"nvidia/canary-1b-v2", 13, "Useful multilingual baseline; the newer leaders score better on the selected English meeting tests."},
	{"large-v3", 14, "Mature multilingual baseline with broad evaluations; newer candidates lead the selected meeting benchmark."},
	{"mistralai/Voxtral-Mini-3B-2507", 15, "Published English meeting results and memory requirements favor the higher-ranked candidates for this use."},
	{"mistralai/Voxtral-Mini-4B-Realtime-2602", 16, "Streaming-oriented design; lower priority when maximum offline meeting accuracy is the goal."},
	{"microsoft/VibeVoice-ASR-BitNet", 17, "Efficient CPU experiment; its separate publisher and small community evaluations do not establish top meeting accuracy."},
	{"large-v2", 18, "Older full-size Whisper baseline; prefer large-v3 as the first Whisper candidate."},
	{"large-v1", 19, "Original full-size Whisper checkpoint; retained for comparison with established profiles."},
	{"medium.en", 20, "English-only Whisper compromise when full-size Whisper is too expensive."},
	{"medium", 21, "Multilingual Whisper compromise; usually lower priority than full-size models for accuracy."},
	{"small.en", 22, "English-only lightweight Whisper; useful when memory or throughput matters more than maximum accuracy."},
	{"small", 23, "Lightweight multilingual Whisper; expect a recognition tradeoff against larger candidates."},
	{"base.en", 24, "Small English-only baseline for constrained systems; not a first choice for detailed meetings."},
	{"base", 25, "Small multilingual baseline for constrained systems; not a first choice for detailed meetings."},
	{"tiny.en", 26, "Minimum-size English-only baseline; prioritize it for speed and footprint."},
	{"tiny", 27, "Minimum-size multilingual baseline; prioritize it for speed and footprint."},
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
		return
	}
	for _, row := range meetingRecommendations {
		if row.Model == model {
			metadata["meeting_recommendation_rank"] = strconv.Itoa(row.Rank)
			metadata["meeting_recommendation_reason"] = row.Reason
			return
		}
	}
}
