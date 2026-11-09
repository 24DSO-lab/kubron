// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package wailsplatform

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// RevealFile shows a file in the file manager of the system. Wails opens only web addresses (it refuses file://),
// so the system command is run here. path comes from the core, which has checked that it is its own file; it is
// passed as an argument, never through a shell.
func (p *Platform) RevealFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", "/select,"+path)
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // explorer.exe exits with 1 even when it worked; only the start matters
	return nil
}
