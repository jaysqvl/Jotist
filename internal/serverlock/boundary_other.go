//go:build !linux

package serverlock

func processBoundary() string { return "" }
