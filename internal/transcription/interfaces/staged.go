package interfaces

import "context"

// StageDescriptor describes an actual serializable backend boundary. The
// absence of this optional interface means Transcribe is one combined stage.
// Merely setting feature metadata never authorizes batch/window adaptation.
type StageDescriptor struct {
	Kind                  string              `json:"kind"`
	SchemaVersion         string              `json:"schema_version"`
	ImplementationVersion string              `json:"implementation_version"`
	Recoverable           bool                `json:"recoverable"`
	ModelArtifacts        map[string]string   `json:"model_artifacts"`
	DevicePrecisions      map[string][]string `json:"device_precisions"`
	QualifiedBatches      []int               `json:"qualified_batches,omitempty"`
	BatchParameter        string              `json:"batch_parameter,omitempty"`
	WindowParameter       string              `json:"window_parameter,omitempty"`
	PrecisionParameter    string              `json:"precision_parameter,omitempty"`
	DefaultPrecision      string              `json:"default_precision,omitempty"`
	WindowPolicy          *StageWindowPolicy  `json:"window_policy,omitempty"`
	CombinedStages        []string            `json:"combined_stages,omitempty"`
	QualificationNotes    []string            `json:"qualification_notes,omitempty"`
	Cancellable           bool                `json:"cancellable"`
	MeasurementSupport    []string            `json:"measurement_support,omitempty"`
}

// AdaptiveCapabilities describes runtime-supported candidates independently of
// whether an adapter can expose more than one durable subprocess boundary.
type AdaptiveCapabilities interface {
	RecoveryStage(kind string) (StageDescriptor, bool)
}

// StageParameterResolver resolves saved upstream-derived defaults without
// loading a model or changing the requested numerical/window policy.
type StageParameterResolver interface {
	ResolveStageParameters(StageDescriptor, map[string]interface{}, []byte) (map[string]interface{}, error)
}

type StageWindowPolicy struct {
	Unit             string `json:"unit"`
	Candidates       []int  `json:"candidates"`
	MinimumOverlap   int    `json:"minimum_overlap"`
	StitchingVersion string `json:"stitching_version"`
}

type StagedTranscriptionAdapter interface {
	TranscriptionAdapter
	Stages() []StageDescriptor
	RunStage(context.Context, StageDescriptor, AudioInput, map[string]interface{}, ProcessingContext, []byte) ([]byte, error)
}
