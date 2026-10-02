package repository

import (
	"context"
	"math"
	"time"

	"gorm.io/gorm"
	"github.com/jaysqvl/Jotist/internal/models"
)

func (r *RecoveryRepository) SetAttemptWaiting(ctx context.Context, id string, generation int64, waiting bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		a, stage, err := r.ownedAttempt(tx, id, generation)
		if err != nil {
			return err
		}
		if (a.State != models.RecoveryRunning && a.State != models.RecoveryWaiting) || (stage.State != models.RecoveryRunning && stage.State != models.RecoveryWaiting) {
			return ErrRecoveryConflict
		}
		state := models.RecoveryRunning
		if waiting {
			state = models.RecoveryWaiting
		}
		updates := map[string]interface{}{"state": state, "retry_at": nil}
		if err := tx.Model(a).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(stage).Update("state", state).Error
	})
}

// RetryAt distinguishes a scheduled backoff from waiting for GPU capacity.
// Both are active, cancellable work and use the same ownership fences.
func (r *RecoveryRepository) SetAttemptRetryAt(ctx context.Context, id string, generation int64, retryAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		a, stage, err := r.ownedAttempt(tx, id, generation)
		if err != nil {
			return err
		}
		if a.State != models.RecoveryWaiting || stage.State != models.RecoveryWaiting {
			return ErrRecoveryConflict
		}
		return tx.Model(a).Update("retry_at", retryAt).Error
	})
}

func (r *RecoveryRepository) RecordAttemptMeasurements(ctx context.Context, id string, generation int64, measurements models.StageMeasurements) error {
	if err := validateMeasurements(measurements); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Numeric telemetry may finish after a deadline; results remain fenced.
		a, _, err := r.ownedMeasurementAttempt(tx, id, generation)
		if err != nil {
			return err
		}
		if a.Measurements != nil {
			return ErrRecoveryConflict
		}
		a.Measurements = &measurements
		return tx.Model(a).Select("measurements").Updates(a).Error
	})
}

func validateMeasurements(measurements models.StageMeasurements) error {
	if measurements.Samples < 0 || math.IsNaN(measurements.ElapsedSeconds) || math.IsInf(measurements.ElapsedSeconds, 0) || measurements.ElapsedSeconds < 0 {
		return ErrRecoveryConflict
	}
	for _, value := range []*int64{measurements.HostTotalBytes, measurements.HostAvailableBeforeBytes, measurements.HostMinimumAvailableBytes, measurements.GPUTotalBytes, measurements.DeviceUsedBeforeBytes, measurements.DevicePeakUsedBytes, measurements.ProcessPeakBytes, measurements.AvailableAfterBytes, measurements.TorchPeakAllocatedBytes, measurements.TorchPeakReservedBytes, measurements.ProcessPeakRSSBytes, measurements.ProcessAverageRSSBytes, measurements.ProcessPeakVRAMBytes, measurements.ProcessAverageVRAMBytes, measurements.DeviceAverageUsedBytes} {
		if value != nil && *value < 0 {
			return ErrRecoveryConflict
		}
	}
	for _, value := range []*float64{measurements.ProcessCPUAveragePercent, measurements.CPUCapacityCores, &measurements.RSSSampledSeconds, &measurements.ProcessVRAMSampledSeconds, &measurements.DeviceSampledSeconds, &measurements.CPUSampledSeconds} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return ErrRecoveryConflict
		}
	}
	return nil
}
