// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package audit

import "os"

// syncCriticalDir сбрасывает записи каталога (создание файла) на диск.
func syncCriticalDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
