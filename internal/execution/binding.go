// Package execution carries an exact run identity across queue and processor
// boundaries without coupling either implementation to the other.
package execution

import (
	"context"
	"time"
)

type Binding struct {
	JobID         string
	QueueItemID   string
	ExecutionID   string
	Deadline      time.Time
	BindExecution func(context.Context, string) error
}

type bindingKey struct{}

func WithBinding(ctx context.Context, binding Binding) context.Context {
	return context.WithValue(ctx, bindingKey{}, binding)
}

// A multi-track child must never inherit the parent's queue-item binding.
func ForJob(ctx context.Context, jobID string) (Binding, bool) {
	binding, ok := ctx.Value(bindingKey{}).(Binding)
	return binding, ok && binding.JobID == jobID
}

func RegisterExecution(ctx context.Context, jobID, executionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if binding, ok := ForJob(ctx, jobID); ok && binding.BindExecution != nil {
		return binding.BindExecution(ctx, executionID)
	}
	return nil
}
