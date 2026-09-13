package transcription

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	executionctx "scriberr/internal/execution"
	"scriberr/internal/models"
	"scriberr/internal/repository"
	"scriberr/internal/transcription/interfaces"
	"scriberr/internal/transcription/registry"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The real service handles metadata, routing, checkpoint files, merging and
// transactional publication. Only model inference is replaced by these fakes.
type recoveryServiceASR struct {
	*stageTestAdapter
	mu         sync.Mutex
	calls      map[string]int
	failures   map[string]int
	texts      map[string]string
	parameters map[string][]map[string]interface{}
}

func (a *recoveryServiceASR) GetSupportedModels() []string {
	return []string{a.capabilities.ModelID}
}

func (a *recoveryServiceASR) Transcribe(ctx context.Context, input interfaces.AudioInput, params map[string]interface{}, _ interfaces.ProcessingContext) (*interfaces.TranscriptResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[input.FilePath]++
	a.parameters[input.FilePath] = append(a.parameters[input.FilePath], copyStageParameters(params))
	if a.failures[input.FilePath] > 0 {
		a.failures[input.FilePath]--
		return nil, errors.New("fixture recognition failure")
	}
	result := stageTestTranscript(a.texts[input.FilePath])
	result.ModelUsed = a.capabilities.ModelID
	result.Metadata = map[string]string{"resolved_device": "cpu", "precision": "float32"}
	return result, nil
}

func (a *recoveryServiceASR) callCount(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[path]
}

type recoveryServiceDiarizer struct {
	*stageTestAdapter
	mu       sync.Mutex
	calls    int
	failures int
}

func (a *recoveryServiceDiarizer) GetMinSpeakers() int { return 1 }
func (a *recoveryServiceDiarizer) GetMaxSpeakers() int { return 10 }
func (a *recoveryServiceDiarizer) Diarize(ctx context.Context, _ interfaces.AudioInput, _ map[string]interface{}, _ interfaces.ProcessingContext) (*interfaces.DiarizationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.failures > 0 {
		a.failures--
		return nil, errors.New("fixture speaker failure")
	}
	return &interfaces.DiarizationResult{
		Segments:     []interfaces.DiarizationSegment{{Start: 0, End: 3, Speaker: "speaker_0"}},
		SpeakerCount: 1, Speakers: []string{"speaker_0"}, ModelUsed: a.capabilities.ModelID,
		Metadata: map[string]string{"resolved_device": "cpu"},
	}, nil
}

type recoveryServiceFixture struct {
	*stageTestFixture
	service     *UnifiedTranscriptionService
	asr         *recoveryServiceASR
	diarizer    *recoveryServiceDiarizer
	queueID     string
	publishedID string
	oldText     string
	bindings    []string
}

func recoveryServiceWAV(t *testing.T, name string) string {
	t.Helper()
	// Ten seconds of mono PCM16 at 16 kHz. Both real ffprobe and the service's
	// existing metadata fallback report ten seconds; no ffmpeg/ML install needed.
	const sampleBytes = 10 * 16000 * 2
	data := make([]byte, 44+sampleBytes)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], sampleBytes+36)
	copy(data[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 16000)
	binary.LittleEndian.PutUint32(data[28:32], 32000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], sampleBytes)
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

func newRecoveryServiceFixture(t *testing.T, multitrack bool) *recoveryServiceFixture {
	t.Helper()
	t.Setenv("CUDA_VISIBLE_DEVICES", "")
	t.Setenv("CHECKPOINT_DIR", t.TempDir())
	t.Setenv("CHECKPOINT_MAX_BYTES", "0")
	f := &recoveryServiceFixture{stageTestFixture: newStageTestFixture(t), queueID: uuid.NewString(), publishedID: uuid.NewString(), oldText: `{"text":"Previously published meeting","segments":[]}`}
	require.NoError(t, f.db.AutoMigrate(&models.MultiTrackFile{}, &models.SpeakerMapping{}, &models.TranscriptionQueueItem{}))
	id := "recovery-fixture-asr-" + uuid.NewString()
	base := newStageTestAdapter(t, id)
	base.capabilities.ModelFamily = id
	base.capabilities.Features = map[string]bool{"context": true, "word_timestamps": true}
	f.asr = &recoveryServiceASR{stageTestAdapter: base, calls: map[string]int{}, failures: map[string]int{}, texts: map[string]string{}, parameters: map[string][]map[string]interface{}{}}
	diarizerID := "recovery-fixture-diarizer-" + uuid.NewString()
	f.diarizer = &recoveryServiceDiarizer{stageTestAdapter: newStageTestAdapter(t, diarizerID), failures: 1}
	// UUID-based identifiers avoid replacing any real or other-test adapter.
	registry.RegisterTranscriptionAdapter(id, f.asr)
	registry.RegisterDiarizationAdapter(diarizerID, f.diarizer)
	f.service = NewUnifiedTranscriptionService(repository.NewJobRepository(f.db), t.TempDir(), t.TempDir())
	require.NoError(t, f.service.recoveryInitError)
	f.store = f.service.recovery
	contextText, terms, language := "The C++ service calls foo.bar.", "C++\nfoo.bar", "en"
	params := models.WhisperXParams{
		ModelFamily: id, Model: id, Device: "cpu", ComputeType: "float32", RecoveryMode: RecoveryFixed,
		Language: &language, TranscriptionContext: &contextText, TranscriptionContextTerms: &terms,
		Diarize: !multitrack, DiarizeModel: diarizerID, DiarizationDevice: "cpu", IsMultiTrackEnabled: multitrack,
		BatchSize: 1, NoAlign: false, HFTokenSource: "none",
	}
	job := f.job(t)
	job.AudioPath = recoveryServiceWAV(t, "meeting.wav")
	job.Parameters, job.IsMultiTrack = params, multitrack
	job.Transcript, job.PinnedExecutionID = &f.oldText, &f.publishedID
	require.NoError(t, f.db.Save(&job).Error)
	f.asr.texts[job.AudioPath] = "C++ foo.bar, preserved!"
	ended := time.Now().Add(-time.Hour)
	require.NoError(t, f.db.Create(&models.TranscriptionJobExecution{
		ID: f.publishedID, TranscriptionJobID: f.recording, StartedAt: ended.Add(-time.Minute), CompletedAt: &ended,
		Status: models.StatusCompleted, Transcript: &f.oldText,
	}).Error)
	require.NoError(t, f.db.Create(&models.TranscriptionQueueItem{
		ID: f.queueID, TranscriptionJobID: f.recording, Status: models.QueueStatusProcessing, Parameters: params,
	}).Error)
	return f
}

func (f *recoveryServiceFixture) job(t *testing.T) models.TranscriptionJob {
	t.Helper()
	var job models.TranscriptionJob
	require.NoError(t, f.db.Preload("MultiTrackFiles").First(&job, "id = ?", f.recording).Error)
	return job
}

func (f *recoveryServiceFixture) queueItem(t *testing.T) models.TranscriptionQueueItem {
	t.Helper()
	var item models.TranscriptionQueueItem
	require.NoError(t, f.db.First(&item, "id = ?", f.queueID).Error)
	return item
}

func (f *recoveryServiceFixture) currentExecution(t *testing.T) models.TranscriptionJobExecution {
	t.Helper()
	item := f.queueItem(t)
	require.NotNil(t, item.ExecutionID)
	var execution models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&execution, "id = ?", *item.ExecutionID).Error)
	return execution
}

func (f *recoveryServiceFixture) binding(executionID string) context.Context {
	return executionctx.WithBinding(context.Background(), executionctx.Binding{
		JobID: f.recording, QueueItemID: f.queueID, ExecutionID: executionID,
		BindExecution: func(_ context.Context, id string) error { f.bindings = append(f.bindings, id); return nil },
	})
}

func (f *recoveryServiceFixture) admitResume(t *testing.T, saved models.TranscriptionJobExecution) context.Context {
	t.Helper()
	job := f.job(t)
	require.NoError(t, f.service.PrepareExecutionResume(WithResumeParameters(context.Background(), job.Parameters), f.recording, saved.ID))
	resumed := f.currentExecution(t)
	require.Equal(t, saved.ID, resumed.ID)
	require.Equal(t, saved.OwnerGeneration+1, resumed.OwnerGeneration)
	require.Equal(t, saved.PlanJSON, resumed.PlanJSON)
	require.Equal(t, saved.DeadlineAt, resumed.DeadlineAt)
	require.Equal(t, models.QueueStatusPending, f.queueItem(t).Status)
	require.Equal(t, models.StatusPending, f.job(t).Status)
	// The real queue performs this claim before invoking ProcessJob. The service
	// itself must still claim the exact pending execution through WithBinding.
	jobClaim := f.db.Model(&models.TranscriptionJob{}).Where("id = ? AND status = ?", f.recording, models.StatusPending).Update("status", models.StatusProcessing)
	require.NoError(t, jobClaim.Error)
	require.EqualValues(t, 1, jobClaim.RowsAffected)
	queueClaim := f.db.Model(&models.TranscriptionQueueItem{}).Where("id = ? AND status = ?", f.queueID, models.QueueStatusPending).Update("status", models.QueueStatusProcessing)
	require.NoError(t, queueClaim.Error)
	require.EqualValues(t, 1, queueClaim.RowsAffected)
	return f.binding(saved.ID)
}

func TestRecoverableExecutionRetainsPublicationAndResumesExactRun(t *testing.T) {
	f := newRecoveryServiceFixture(t, false)
	audio := f.job(t).AudioPath
	err := f.service.ProcessJob(f.binding(""), f.recording)
	require.ErrorContains(t, err, "diarization failed")
	failed := f.currentExecution(t)
	require.Equal(t, models.StatusFailed, failed.Status)
	require.Equal(t, "failed", failed.RecoveryState)
	require.Nil(t, failed.Transcript)
	require.Equal(t, models.QueueStatusFailed, f.queueItem(t).Status)
	job := f.job(t)
	require.Equal(t, &f.oldText, job.Transcript)
	require.Equal(t, &f.publishedID, job.PinnedExecutionID)
	require.Equal(t, 1, f.asr.callCount(audio))
	require.Equal(t, 1, f.diarizer.calls)
	stages, err := f.store.ListStages(context.Background(), failed.ID)
	require.NoError(t, err)
	require.Len(t, stages, 2)
	asrStage := f.stage(t, failed.ID, "asr")
	checkpoint, before, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	require.Contains(t, string(before), "C++ foo.bar, preserved!")
	require.NotContains(t, string(before), `"speaker"`, "ASR checkpoint must precede speaker assignment")
	// A new context cannot be smuggled into the saved execution on resume.
	changed := job.Parameters
	changedContext := "Changed project vocabulary"
	changed.TranscriptionContext = &changedContext
	require.ErrorContains(t, f.service.PrepareExecutionResume(WithResumeParameters(context.Background(), changed), f.recording, failed.ID), "original settings")
	require.Equal(t, failed.OwnerGeneration, f.currentExecution(t).OwnerGeneration)

	require.NoError(t, f.service.ProcessJob(f.admitResume(t, failed), f.recording))
	completed := f.currentExecution(t)
	require.Equal(t, failed.ID, completed.ID)
	require.Equal(t, models.StatusCompleted, completed.Status)
	require.Equal(t, "completed", completed.RecoveryState)
	require.Equal(t, models.QueueStatusCompleted, f.queueItem(t).Status)
	require.Equal(t, 1, f.asr.callCount(audio), "successful recognition is loaded from its selected artifact")
	require.Equal(t, 2, f.diarizer.calls)
	require.Equal(t, []string{failed.ID, failed.ID}, f.bindings)
	job = f.job(t)
	require.Equal(t, completed.Transcript, job.Transcript)
	require.NotEqual(t, &f.oldText, job.Transcript)
	require.Equal(t, &f.publishedID, job.PinnedExecutionID)
	var result interfaces.TranscriptResult
	require.NoError(t, json.Unmarshal([]byte(*completed.Transcript), &result))
	require.Equal(t, "C++ foo.bar, preserved!", result.Text)
	require.Equal(t, "speaker_0", *result.Segments[0].Speaker)
	require.Equal(t, "speaker_0", *result.WordSegments[0].Speaker)
	selected, after, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, selected.ID)
	require.Equal(t, before, after, "speaker merging cannot mutate the cached ASR result")
	require.Len(t, f.stage(t, failed.ID, "asr").Attempts, 1)
	require.Len(t, f.stage(t, failed.ID, "diarization").Attempts, 2)
	var prior models.TranscriptionJobExecution
	require.NoError(t, f.db.First(&prior, "id = ?", f.publishedID).Error)
	require.Equal(t, &f.oldText, prior.Transcript)
	var executionCount int64
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("transcription_job_id = ?", f.recording).Count(&executionCount).Error)
	require.EqualValues(t, 2, executionCount, "old published run plus one resumed execution")
	require.Equal(t, "The C++ service calls foo.bar.", f.asr.parameters[audio][0]["context"])
	require.Equal(t, "C++\nfoo.bar", f.asr.parameters[audio][0]["context_terms"])
}

func TestRecoverableExecutionMultitrackResumePreservesIndividualOutputsAndSpeakers(t *testing.T) {
	f := newRecoveryServiceFixture(t, true)
	firstPath, secondPath := recoveryServiceWAV(t, "alice.wav"), recoveryServiceWAV(t, "bob.wav")
	tracks := []models.MultiTrackFile{
		{TranscriptionJobID: f.recording, FileName: "alice.wav", FilePath: firstPath, TrackIndex: 0, Offset: 0},
		{TranscriptionJobID: f.recording, FileName: "bob.wav", FilePath: secondPath, TrackIndex: 1, Offset: 4},
	}
	require.NoError(t, f.db.Create(&tracks).Error)
	f.asr.texts[firstPath], f.asr.texts[secondPath] = "C++", "foo.bar."
	f.asr.failures[secondPath] = 1
	oldIndividuals := `{"alice.wav":"old alice transcript","bob.wav":"old bob transcript"}`
	require.NoError(t, f.db.Model(&models.TranscriptionJob{}).Where("id = ?", f.recording).Update("individual_transcripts", &oldIndividuals).Error)
	customMappings := []models.SpeakerMapping{
		{TranscriptionJobID: f.recording, OriginalSpeaker: "Alice", CustomName: "Alice (platform)"},
		{TranscriptionJobID: f.recording, OriginalSpeaker: "Bob", CustomName: "Bob (API)"},
	}
	require.NoError(t, f.db.Create(&customMappings).Error)
	readMappings := func() []models.SpeakerMapping {
		var rows []models.SpeakerMapping
		require.NoError(t, f.db.Where("transcription_job_id = ?", f.recording).Order("id").Find(&rows).Error)
		return rows
	}
	customMappings = readMappings()
	err := f.service.ProcessJob(f.binding(""), f.recording)
	require.ErrorContains(t, err, "failed to transcribe track bob.wav")
	failed := f.currentExecution(t)
	require.Equal(t, models.StatusFailed, failed.Status)
	require.Nil(t, failed.Transcript)
	require.Nil(t, failed.IndividualTranscripts)
	job := f.job(t)
	require.Equal(t, &f.oldText, job.Transcript)
	require.Equal(t, &oldIndividuals, job.IndividualTranscripts)
	require.Equal(t, customMappings, readMappings())
	require.Equal(t, 1, f.asr.callCount(firstPath))
	require.Equal(t, 1, f.asr.callCount(secondPath))
	firstNode := "track-" + digestBytes([]byte(strconv.FormatUint(uint64(tracks[0].ID), 10))) + ".asr"
	secondNode := "track-" + digestBytes([]byte(strconv.FormatUint(uint64(tracks[1].ID), 10))) + ".asr"
	firstStage := f.stage(t, failed.ID, firstNode)
	checkpoint, before, err := f.store.SelectedCheckpoint(context.Background(), firstStage.ID)
	require.NoError(t, err)
	// Track offsets are assembly inputs. Changing one requires a fresh plan,
	// even though the audio and selected recognition checkpoint still match.
	require.NoError(t, f.db.Model(&tracks[1]).Update("offset", 5).Error)
	require.ErrorContains(t, f.service.PrepareExecutionResume(context.Background(), f.recording, failed.ID), "track layout changed")
	require.Equal(t, failed.OwnerGeneration, f.currentExecution(t).OwnerGeneration)
	require.NoError(t, f.db.Model(&tracks[1]).Update("offset", 4).Error)

	require.NoError(t, f.service.ProcessJob(f.admitResume(t, failed), f.recording))
	completed := f.currentExecution(t)
	require.Equal(t, failed.ID, completed.ID)
	require.Equal(t, models.StatusCompleted, completed.Status)
	require.Equal(t, models.QueueStatusCompleted, f.queueItem(t).Status)
	require.Equal(t, 1, f.asr.callCount(firstPath))
	require.Equal(t, 2, f.asr.callCount(secondPath))
	require.Zero(t, f.diarizer.calls, "multitrack children use track identity as speaker")
	require.Equal(t, f.asr.parameters[secondPath][0], f.asr.parameters[secondPath][1])
	job = f.job(t)
	require.Equal(t, completed.Transcript, job.Transcript)
	require.Equal(t, completed.IndividualTranscripts, job.IndividualTranscripts)
	require.NotEqual(t, &oldIndividuals, job.IndividualTranscripts)
	require.Equal(t, &f.publishedID, job.PinnedExecutionID)
	require.Equal(t, customMappings, readMappings(), "publishing a new run cannot reset custom speaker names")
	var individual map[string]string
	require.NotNil(t, completed.IndividualTranscripts)
	require.NoError(t, json.Unmarshal([]byte(*completed.IndividualTranscripts), &individual))
	require.Len(t, individual, 2)
	for name, expected := range map[string]string{"alice.wav": "C++", "bob.wav": "foo.bar."} {
		var result interfaces.TranscriptResult
		require.NoError(t, json.Unmarshal([]byte(individual[name]), &result))
		require.Equal(t, expected, result.Text)
	}
	var merged interfaces.TranscriptResult
	require.NoError(t, json.Unmarshal([]byte(*completed.Transcript), &merged))
	require.Equal(t, "C++ foo.bar.", merged.Text)
	require.Len(t, merged.WordSegments, 2)
	require.Equal(t, "Alice", *merged.WordSegments[0].Speaker)
	require.Equal(t, "Bob", *merged.WordSegments[1].Speaker)
	require.Equal(t, float64(4), merged.WordSegments[1].Start)
	require.Equal(t, float64(5), merged.WordSegments[1].End)
	var timings []models.MultiTrackTiming
	require.NotNil(t, completed.MultiTrackTimings)
	require.NoError(t, json.Unmarshal([]byte(*completed.MultiTrackTimings), &timings))
	require.Len(t, timings, 2)
	require.Equal(t, "alice.wav", timings[0].TrackName)
	require.Equal(t, "bob.wav", timings[1].TrackName)
	for _, timing := range timings {
		require.False(t, timing.StartTime.IsZero())
		require.False(t, timing.EndTime.Before(timing.StartTime))
		require.GreaterOrEqual(t, timing.Duration, int64(0))
	}
	require.NotNil(t, completed.MergeStartTime)
	require.NotNil(t, completed.MergeEndTime)
	require.NotNil(t, completed.MergeDuration)
	require.False(t, completed.MergeEndTime.Before(*completed.MergeStartTime))
	selected, after, err := f.store.SelectedCheckpoint(context.Background(), firstStage.ID)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, selected.ID)
	require.Equal(t, before, after)
	require.Len(t, f.stage(t, failed.ID, firstNode).Attempts, 1)
	require.Len(t, f.stage(t, failed.ID, secondNode).Attempts, 2)
	var jobs []models.TranscriptionJob
	require.NoError(t, f.db.Find(&jobs).Error)
	require.Len(t, jobs, 1, "track nodes must not leave disposable child jobs/executions")
	var executions []models.TranscriptionJobExecution
	require.NoError(t, f.db.Where("transcription_job_id = ?", f.recording).Find(&executions).Error)
	require.Len(t, executions, 2)
	for _, item := range executions {
		require.False(t, strings.HasPrefix(item.TranscriptionJobID, "track_"))
	}
}

func TestRecoverableExecutionExpiredPendingResumeDoesNotBlockFreshRun(t *testing.T) {
	f := newRecoveryServiceFixture(t, false)
	audio := f.job(t).AudioPath
	require.Error(t, f.service.ProcessJob(f.binding(""), f.recording))
	failed := f.currentExecution(t)
	asrStage := f.stage(t, failed.ID, "asr")
	checkpoint, before, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	bound := f.admitResume(t, failed)
	pending := f.currentExecution(t)
	require.Equal(t, models.RecoveryPending, pending.RecoveryState)
	// Advance the saved deadline without a timing-sensitive sleep. The worker's
	// context is already expired, so its very first database read fails before
	// ClaimResume or model preparation can run.
	expiredAt := time.Now().Add(-time.Minute).UTC()
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("id = ?", pending.ID).Update("deadline_at", expiredAt).Error)
	expiredContext, cancel := context.WithDeadline(bound, expiredAt)
	defer cancel()
	require.ErrorIs(t, expiredContext.Err(), context.DeadlineExceeded)
	require.ErrorIs(t, f.service.ProcessJob(expiredContext, f.recording), context.DeadlineExceeded)
	expired := f.currentExecution(t)
	require.Equal(t, failed.ID, expired.ID)
	require.Equal(t, pending.OwnerGeneration, expired.OwnerGeneration)
	require.Equal(t, models.StatusFailed, expired.Status)
	require.Equal(t, models.RecoveryFailed, expired.RecoveryState)
	require.NotNil(t, expired.ErrorMessage)
	require.Equal(t, repository.ErrExecutionDeadline.Error(), *expired.ErrorMessage)
	require.NotNil(t, expired.CompletedAt)
	require.NotNil(t, expired.DeadlineAt)
	require.True(t, expired.DeadlineAt.Equal(expiredAt), "expiry must not extend the immutable deadline")
	require.Nil(t, expired.CancelledAt, "deadline expiry is distinct from user cancellation")
	require.Equal(t, models.QueueStatusFailed, f.queueItem(t).Status)
	job := f.job(t)
	require.Equal(t, models.StatusFailed, job.Status)
	require.Equal(t, &f.oldText, job.Transcript)
	require.Equal(t, &f.publishedID, job.PinnedExecutionID)
	require.Equal(t, 1, f.asr.callCount(audio))
	require.Equal(t, 1, f.diarizer.calls)
	require.Equal(t, []string{failed.ID}, f.bindings, "an expired resume never registers a running attempt")
	require.Len(t, f.stage(t, failed.ID, "asr").Attempts, 1)
	require.Len(t, f.stage(t, failed.ID, "diarization").Attempts, 1)
	selected, after, err := f.store.SelectedCheckpoint(context.Background(), asrStage.ID)
	require.NoError(t, err)
	require.Equal(t, checkpoint.ID, selected.ID)
	require.Equal(t, before, after)
	var active int64
	require.NoError(t, f.db.Model(&models.TranscriptionJobExecution{}).Where("transcription_job_id = ? AND status IN ?", f.recording, []models.JobStatus{models.StatusPending, models.StatusProcessing}).Count(&active).Error)
	require.Zero(t, active, "expired pending ownership must not strand the recording")

	// Prove a later ordinary new execution can acquire this recording. No model
	// call is needed to establish that the old pending owner no longer blocks it.
	require.NoError(t, f.db.Model(&models.TranscriptionJob{}).Where("id = ?", f.recording).Update("status", models.StatusProcessing).Error)
	deadline := time.Now().Add(time.Hour)
	fresh := &models.TranscriptionJobExecution{
		ID: uuid.NewString(), TranscriptionJobID: f.recording, RecoveryVersion: 1,
		ActualParameters: job.Parameters.WithoutSecrets(), PlanJSON: failed.PlanJSON,
		StartedAt: time.Now(), DeadlineAt: &deadline,
	}
	require.NoError(t, f.service.lifecycle.Begin(context.Background(), fresh))
	require.NotEqual(t, failed.ID, fresh.ID)
	require.Equal(t, models.RecoveryRunning, fresh.RecoveryState)
	require.Equal(t, &f.oldText, f.job(t).Transcript)
}
