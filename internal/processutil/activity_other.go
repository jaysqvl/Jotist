//go:build !linux

package processutil

// Native macOS/Windows runs keep cancellation and structured observations, but
// do not terminate automatically without verified process activity telemetry.
func readWorkerActivity(root int) activityReading { return activityReading{} }
