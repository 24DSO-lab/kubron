// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import "github.com/24DSO-labs/kubron/pkg/ext"

// FromExt адаптирует публичное хранилище пользователей (pkg/ext) к внутреннему Backend.
func FromExt(b ext.UserBackend) Backend { return extBackend{b} }

type extBackend struct{ b ext.UserBackend }

func (e extBackend) Load() (*Data, error) {
	d, err := e.b.Load()
	if err != nil {
		return nil, err
	}
	out := &Data{Settings: SecuritySettings{IdleTimeoutMinutes: d.Settings.IdleTimeoutMinutes}}
	for _, u := range d.Users {
		out.Users = append(out.Users, UserRecord{
			ID: u.ID, Username: u.Username, Role: Role(u.Role), PasswordHash: u.PasswordHash,
			Disabled: u.Disabled, MustChangePassword: u.MustChangePassword,
			CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, LastLoginAt: u.LastLoginAt,
		})
	}
	return out, nil
}

func (e extBackend) Save(d *Data) error {
	out := &ext.UserData{Settings: ext.SecuritySettings{IdleTimeoutMinutes: d.Settings.IdleTimeoutMinutes}}
	for _, u := range d.Users {
		out.Users = append(out.Users, ext.UserRecord{
			ID: u.ID, Username: u.Username, Role: string(u.Role), PasswordHash: u.PasswordHash,
			Disabled: u.Disabled, MustChangePassword: u.MustChangePassword,
			CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, LastLoginAt: u.LastLoginAt,
		})
	}
	return e.b.Save(out)
}
