//go:build !linux

package files

import "os/exec"

// startProcess starts cmd. Only Linux (production) has a parent-death signal; in dev on macOS
// a restic child outlives a killed service, the end entry (backupEndName) still keeps its
// snapshot from being restored.
func startProcess(cmd *exec.Cmd, _ <-chan struct{}) error { return cmd.Start() }
