package transcription

import (
	"github.com/stretchr/testify/require"
	"scriberr/internal/models"
	"scriberr/internal/transcription/adapters"
	"scriberr/internal/transcription/registry"
	"testing"
)

func TestLocalModelRoutingAndContextCapabilities(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	prose, terms := "Software architecture review", "Kubernetes\nPostgreSQL"
	for _, spec := range adapters.LocalASRModels() {
		a, err := adapters.NewLocalASRAdapter(t.TempDir(), spec.ID)
		require.NoError(t, err)
		registry.RegisterTranscriptionAdapter(spec.ID, a)
		p := models.WhisperXParams{ModelFamily: spec.Family, Model: spec.ID, Device: "cpu", ComputeType: "float32", DiarizationDevice: "same", TranscriptionContext: &prose, TranscriptionContextTerms: &terms}
		id, _, err := s.selectModels(p)
		require.NoError(t, err)
		require.Equal(t, spec.ID, id)
		converted := s.convertParametersForModel(p, id)
		require.NoError(t, a.ValidateParameters(converted), spec.ID)
		_, hasProse := converted["context"]
		_, hasTerms := converted["context_terms"]
		require.Equal(t, spec.ContextMode == "prose_and_terms", hasProse, spec.ID)
		require.Equal(t, spec.ContextMode != "none", hasTerms, spec.ID)
		require.Equal(t, "cpu", converted["device"])
	}
	_, _, err := s.selectModels(models.WhisperXParams{ModelFamily: "unsupported", Model: "not-installed"})
	require.Error(t, err)
}

func TestIndependentAndLegacyDiarizationDevices(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	p := models.WhisperXParams{ModelFamily: FamilyNvidiaCanary, Device: "cpu", Diarize: true, DiarizeModel: DiarizeSortformer}
	require.Equal(t, "auto", s.convertToSortformerParams(p)["device"], "legacy CPU Canary did not constrain standalone diarization")
	p.DiarizationDevice = "same"
	require.Equal(t, "cpu", s.convertToSortformerParams(p)["device"])
	p.DiarizationDevice = "cuda"
	require.Equal(t, "cuda", s.convertToSortformerParams(p)["device"])
	require.Equal(t, "cpu", s.convertToCanaryParams(p)["device"])
	p.ModelFamily = FamilyWhisper
	p.DiarizeModel = ModelPyannote
	require.False(t, s.transcriptionIncludesDiarization(ModelWhisperX, p), "different device needs a separate diarizer")
	p.DiarizationDevice = ""
	require.True(t, s.transcriptionIncludesDiarization(ModelWhisperX, p), "legacy Whisper retains built-in same-device diarization")
	p.DiarizeModel = "diarizen"
	require.False(t, s.transcriptionIncludesDiarization(ModelWhisperX, p))
	for _, family := range []string{"qwen3_asr", "moss_asr", "ibm_granite_speech", "cohere_transcribe", "ark_asr", "vibevoice_bitnet"} {
		p.ModelFamily = family
		require.Equal(t, "cpu", resolvedDiarizationDevice(p), "new %s profiles inherit the explicit ASR device", family)
	}
}

func TestVoxtralDoesNotSubstituteUnknownCheckpoint(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	for _, model := range []string{"", "mistralai/Voxtral-mini", ModelVoxtral} {
		id, _, err := s.selectModels(models.WhisperXParams{ModelFamily: FamilyMistralVoxtral, Model: model})
		require.NoError(t, err)
		require.Equal(t, ModelVoxtral, id)
	}
	_, _, err := s.selectModels(models.WhisperXParams{ModelFamily: FamilyMistralVoxtral, Model: "mistralai/Voxtral-Mini-4B-Realtime-typo"})
	require.Error(t, err)
}

func TestWhisperExternalDiarizerDoesNotReachInlineValidation(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	for _, diarizer := range []string{"diarizen", "suplime", DiarizeSortformer} {
		params := models.WhisperXParams{ModelFamily: FamilyWhisper, Device: "cpu", Diarize: true, DiarizeModel: diarizer}
		converted := s.convertToWhisperXParams(params)
		require.Equal(t, false, converted["diarize"])
		require.Equal(t, "pyannote", converted["diarize_model"])
	}
}

func TestSeparatePyannotePreservesExplicitCheckpoint(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	for _, checkpoint := range []string{ModelDiarization31, "pyannote/speaker-diarization-community-1"} {
		params := models.WhisperXParams{ModelFamily: FamilyWhisper, Device: "cuda", DiarizationDevice: "cpu", Diarize: true, DiarizeModel: checkpoint}
		require.False(t, s.transcriptionIncludesDiarization(ModelWhisperX, params))
		converted := s.convertToPyannoteParams(params)
		require.Equal(t, "cpu", converted["device"])
		require.Equal(t, checkpoint, converted["model"])
		require.NoError(t, adapters.NewPyAnnoteAdapter(t.TempDir()).ValidateParameters(converted))
	}
}

func TestMOSSNativeSpeakerSelection(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	id := "OpenMOSS-Team/MOSS-Transcribe-Diarize"
	a, err := adapters.NewLocalASRAdapter(t.TempDir(), id)
	require.NoError(t, err)
	registry.RegisterTranscriptionAdapter(id, a)
	p := models.WhisperXParams{ModelFamily: "moss_asr", Model: id, Diarize: true, DiarizeModel: "native"}
	selected, diarizer, err := s.selectModels(p)
	require.NoError(t, err)
	require.Equal(t, id, selected)
	require.Empty(t, diarizer)
	converted := s.convertParametersForModel(p, id)
	require.Equal(t, true, converted["diarize"])
	require.Equal(t, "native", converted["diarize_model"])
	p.ModelFamily = FamilyWhisper
	_, _, err = s.selectModels(p)
	require.Error(t, err)
}

func TestExternalDiarizationIntentReachesLocalASRValidation(t *testing.T) {
	s := NewUnifiedTranscriptionService(nil, t.TempDir(), t.TempDir())
	for _, id := range []string{"Qwen/Qwen3-ASR-1.7B-hf", "mistralai/Voxtral-Mini-3B-2507"} {
		a, err := adapters.NewLocalASRAdapter(t.TempDir(), id)
		require.NoError(t, err)
		registry.RegisterTranscriptionAdapter(id, a)
		p := models.WhisperXParams{Model: id, Device: "cpu", ComputeType: "float32", Diarize: true, DiarizeModel: "pyannote", NoAlign: true}
		converted := s.convertParametersForModel(p, id)
		require.Equal(t, true, converted["external_diarization_requested"])
		require.ErrorContains(t, a.ValidateParameters(converted), "requires word alignment")
		p.NoAlign = false
		require.NoError(t, a.ValidateParameters(s.convertParametersForModel(p, id)))
		p.NoAlign, p.Diarize = true, false
		require.NoError(t, a.ValidateParameters(s.convertParametersForModel(p, id)))
	}
	legacy := s.convertToVoxtralParams(models.WhisperXParams{Diarize: true, NoAlign: true})
	require.Equal(t, true, legacy["external_diarization_requested"])
}
