package repository

import "context"

// Retry only rolled-back database operations on SQLite's transient writer
// contention. Validation, ownership and constraint failures remain failures.
func retryDatabaseContention(ctx context.Context, write func() error) error {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = write()
		if !isTransientDatabaseLock(err) || attempt == 7 {
			return err
		}
		if err := waitForDatabaseRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return err
}
