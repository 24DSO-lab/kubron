// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/24DSO-lab/kubron/internal/audit"
)

// ExternalIdentity contains claims that have already been verified by the identity provider.
type ExternalIdentity struct {
	Issuer     string
	Subject    string
	Username   string
	Email      string
	Role       string
	RoleMapped bool
}

// ExternalPolicy controls account provisioning and role assignment for external sign-in.
type ExternalPolicy struct {
	AutoCreate   bool
	LinkExisting bool
	DefaultRole  Role
}

// LoginExternal links or provisions the verified identity and opens a session.
// Password lockouts are deliberately not applied: there is no password to guess.
func (s *Service) LoginExternal(ctx context.Context, id ExternalIdentity, pol ExternalPolicy) (*UserInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id.Issuer = strings.TrimSpace(id.Issuer)
	id.Subject = strings.TrimSpace(id.Subject)
	id.Username = strings.TrimSpace(id.Username)
	if id.Issuer == "" || id.Subject == "" || ValidateUsername(id.Username) != nil {
		return nil, ErrInvalidCredentials
	}

	role := pol.DefaultRole
	if id.RoleMapped {
		role = Role(strings.TrimSpace(id.Role))
	}
	if !role.Valid() {
		return nil, ErrUnknownRole
	}

	s.mu.Lock()
	if len(s.st.users) == 0 {
		s.mu.Unlock()
		if s.st.delta != nil {
			_ = s.refresh()
		}
		s.mu.Lock()
		if len(s.st.users) == 0 {
			s.mu.Unlock()
			return nil, ErrSetupRequired
		}
	}

	target := s.findExternalLocked(id.Issuer, id.Subject)
	if target == nil {
		s.mu.Unlock()
		s.refreshOnMiss()
		s.mu.Lock()
		target = s.findExternalLocked(id.Issuer, id.Subject)
	}

	if target == nil {
		local := s.st.findByName(normalizeUsername(id.Username))
		switch {
		case pol.LinkExisting && local != nil && local.ExternalIssuer == "" && local.ExternalSubject == "":
			prev := *local
			local.ExternalIssuer, local.ExternalSubject = id.Issuer, id.Subject
			local.UpdatedAt = s.now()
			if err := s.st.saveUser(local); err != nil {
				*local = prev
				s.record(local, "LinkExternal", audit.ResultFailed, "external identity link failed")
				s.mu.Unlock()
				return nil, fmt.Errorf("failed to save external identity: %w", err)
			}
			target = local
			s.record(target, "LinkExternal", audit.ResultOK, "issuer="+id.Issuer)
		case local != nil:
			s.record(local, "Login", audit.ResultDenied, "SSO username conflicts with an existing account")
			s.mu.Unlock()
			return nil, ErrUserExists
		case pol.AutoCreate:
			now := s.now()
			rec := UserRecord{
				ID: newID(), Username: id.Username, Role: role, ExternalIssuer: id.Issuer,
				ExternalSubject: id.Subject, MustChangePassword: false, CreatedAt: now, UpdatedAt: now,
			}
			s.st.users = append(s.st.users, rec)
			if err := s.st.saveUser(&rec); err != nil {
				s.st.users = s.st.users[:len(s.st.users)-1]
				s.mu.Unlock()
				if s.st.delta != nil {
					_ = s.refresh()
				}
				s.mu.Lock()
				target = s.findExternalLocked(id.Issuer, id.Subject)
				if target == nil {
					if s.st.findByName(normalizeUsername(id.Username)) != nil {
						s.record(&rec, "Login", audit.ResultDenied, "SSO username conflicts with an existing account")
						s.mu.Unlock()
						return nil, ErrUserExists
					}
					s.record(&rec, "Login", audit.ResultFailed, "external account provisioning failed")
					s.mu.Unlock()
					return nil, fmt.Errorf("failed to save external user: %w", err)
				}
			} else {
				target = s.st.findByID(rec.ID)
				s.record(target, "CreateExternalUser", audit.ResultOK, "issuer="+id.Issuer)
			}
		default:
			s.record(&UserRecord{Username: id.Username}, "Login", audit.ResultDenied, ErrExternalNotProvisioned.Error())
			s.mu.Unlock()
			return nil, ErrExternalNotProvisioned
		}
	}

	if target == nil {
		s.mu.Unlock()
		return nil, ErrExternalNotProvisioned
	}
	if target.Disabled {
		s.record(target, "Login", audit.ResultDenied, ErrAccountDisabled.Error())
		s.mu.Unlock()
		return nil, ErrAccountDisabled
	}

	var hooks []func()
	if id.RoleMapped && target.Role != role {
		if target.Role == RoleAdmin && !target.Disabled && s.st.enabledAdmins() <= 1 && role != RoleAdmin {
			s.record(target, "UpdateUserRole", audit.ResultFailed, "SSO role mapping would remove the last administrator; existing role retained")
		} else {
			prev := *target
			target.Role = role
			target.UpdatedAt = s.now()
			if err := s.st.saveUser(target); err != nil {
				*target = prev
				s.record(target, "UpdateUserRole", audit.ResultFailed, "SSO role mapping could not be saved")
				s.mu.Unlock()
				return nil, fmt.Errorf("failed to save mapped role: %w", err)
			}
			hooks = append(hooks, s.endUserSessionsLocked(target.ID, s, "UpdateUserRole")...)
			s.record(target, "UpdateUserRole", audit.ResultOK, "role mapped from SSO")
		}
	}

	replaced := s.sessionID != "" && s.sessionID != target.ID
	now := s.now()
	target.LastLoginAt = &now
	_ = s.st.saveLogin(target, "")
	s.sessionID = target.ID
	s.touchLocked()
	s.record(target, "Login", audit.ResultOK, "sso")
	info := toInfo(target)
	logoutHook := s.onLogout
	s.mu.Unlock()

	runAll(hooks)
	if replaced && logoutHook != nil {
		logoutHook()
	}
	return info, nil
}

func (s *Service) findExternalLocked(issuer, subject string) *UserRecord {
	for i := range s.st.users {
		u := &s.st.users[i]
		if u.ExternalIssuer == issuer && u.ExternalSubject == subject {
			return u
		}
	}
	return nil
}
