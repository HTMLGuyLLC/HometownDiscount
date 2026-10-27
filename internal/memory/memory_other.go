//go:build !darwin && !linux

package memory

func read() Status { return Status{FreePct: -1} }
