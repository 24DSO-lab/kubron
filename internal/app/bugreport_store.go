// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"

	"github.com/24DSO-lab/kubron/internal/bugreport"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

// Errors of the settings of the mail server for bug reports. The texts are fixed and never hold a typed value.
var (
	errBugReportUnavailable = errors.New("the storage for the mail server settings is not available")
	errBugReportUnreadable  = errors.New("the saved mail server settings cannot be read")
)

// bugReportAAD binds the sealed password to its purpose.
var bugReportAAD = []byte("kubron/bugreport-smtp/v1")

// BugReportStore keeps the mail server of the desktop in one file of the data directory. The password is sealed
// with the envelope of the master key; the file is written with mode 0600 and replaced atomically. The password is
// never returned by any method.
type BugReportStore struct {
	mu   sync.Mutex
	path string
	env  *secrets.Envelope
}

// NewBugReportStore returns the store for the file at path.
func NewBugReportStore(path string, env *secrets.Envelope) *BugReportStore {
	return &BugReportStore{path: path, env: env}
}

type bugReportFile struct {
	V        int    `json:"v"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // base64 of the sealed envelope
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Security string `json:"security,omitempty"`
}

// Load returns the saved settings, or nil if there are none.
func (s *BugReportStore) Load() (*bugreport.SMTPConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *BugReportStore) load() (*bugreport.SMTPConfig, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errBugReportUnreadable
	}
	var f bugReportFile
	if json.Unmarshal(raw, &f) != nil || f.V != 1 || f.Host == "" {
		return nil, errBugReportUnreadable
	}
	c := &bugreport.SMTPConfig{
		Host: f.Host, Port: f.Port, Username: f.Username, From: f.From, To: f.To, Security: bugreport.Security(f.Security),
	}
	if f.Password != "" {
		blob, err := base64.StdEncoding.DecodeString(f.Password)
		if err != nil || s.env == nil {
			return nil, errBugReportUnreadable
		}
		plain, err := s.env.Open(blob, bugReportAAD)
		if err != nil {
			return nil, errBugReportUnreadable
		}
		c.Password = string(plain)
	}
	return c, nil
}

// Save stores the settings. An empty password keeps the saved one if the host and the user are the same, so that
// the user does not have to type it again.
func (s *BugReportStore) Save(c bugreport.SMTPConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env == nil {
		return errBugReportUnavailable
	}
	if c.Password == "" && c.Username != "" {
		if old, err := s.load(); err == nil && old != nil && old.Host == c.Host && old.Username == c.Username {
			c.Password = old.Password
		}
	}
	n, err := c.Normalize()
	if err != nil {
		return bugreport.ErrSettingsInvalid
	}
	f := bugReportFile{V: 1, Host: n.Host, Port: n.Port, Username: n.Username, From: n.From, To: n.To, Security: string(n.Security)}
	if n.Password != "" {
		blob, _, err := s.env.Seal([]byte(n.Password), bugReportAAD)
		if err != nil {
			return errBugReportUnavailable
		}
		f.Password = base64.StdEncoding.EncodeToString(blob)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return errBugReportUnavailable
	}
	if err := writeFileAtomic(s.path, raw); err != nil {
		return errBugReportUnavailable
	}
	return nil
}

// Clear removes the saved settings. A missing file is not an error.
func (s *BugReportStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errBugReportUnavailable
	}
	return nil
}

// bugReportConfigFromEnv reads the settings from KUBRON_BUGREPORT_SMTP_{HOST,PORT,USER,PASSWORD,FROM,TO,SECURITY}.
// nil means that no host is set there.
func bugReportConfigFromEnv(getenv func(string) string) *bugreport.SMTPConfig {
	host := getenv("KUBRON_BUGREPORT_SMTP_HOST")
	if host == "" {
		return nil
	}
	port, _ := strconv.Atoi(getenv("KUBRON_BUGREPORT_SMTP_PORT"))
	return &bugreport.SMTPConfig{
		Host: host, Port: port,
		Username: getenv("KUBRON_BUGREPORT_SMTP_USER"), Password: getenv("KUBRON_BUGREPORT_SMTP_PASSWORD"),
		From: getenv("KUBRON_BUGREPORT_SMTP_FROM"), To: getenv("KUBRON_BUGREPORT_SMTP_TO"),
		Security: bugreport.Security(getenv("KUBRON_BUGREPORT_SMTP_SECURITY")),
	}
}
