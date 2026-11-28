// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package authorization

import (
	"io"
	"os"
)

// stateFile — открытый временный файл записи состояния.
type stateFile interface {
	Write(p []byte) (int, error)
	Sync() error
	Close() error
	Name() string
}

// stateFS — файловые операции File backend. Выделены, чтобы тесты могли имитировать короткую запись,
// «диск заполнен», сбои fsync/rename и fsync каталога.
type stateFS interface {
	MkdirAll(dir string) error
	ReadFile(path string, max int64) ([]byte, error)
	ReadDir(dir string) ([]string, error)
	CreateTemp(dir string) (stateFile, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
	SyncDir(dir string) error
}

type osFS struct{}

func (osFS) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o700) }

func (osFS) ReadFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, os.ErrInvalid // файл больше лимита: вызывающий превращает в ErrUnavailable
	}
	return data, nil
}

func (osFS) ReadDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

// CreateTemp создаёт файл с O_EXCL и правами 0600.
func (osFS) CreateTemp(dir string) (stateFile, error) {
	return os.CreateTemp(dir, tempPrefix+"*"+tempSuffix)
}

func (osFS) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
func (osFS) Remove(path string) error             { return os.Remove(path) }
func (osFS) SyncDir(dir string) error             { return syncDir(dir) }
