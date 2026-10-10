package queue

// Ready reports whether startup recovery finished and dispatch is available.
// Busy workers and intentionally waiting jobs are normal, ready states.
func (tq *TaskQueue) Ready() bool {
	tq.jobsMutex.RLock()
	defer tq.jobsMutex.RUnlock()
	return tq.started && tq.startupFailure() == nil && tq.ctx.Err() == nil
}

func (tq *TaskQueue) blockStartup(err error) {
	tq.startupMutex.Lock()
	defer tq.startupMutex.Unlock()
	tq.startupError = err
}

func (tq *TaskQueue) startupFailure() error {
	tq.startupMutex.RLock()
	defer tq.startupMutex.RUnlock()
	return tq.startupError
}
