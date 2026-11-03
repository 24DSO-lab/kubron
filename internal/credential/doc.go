// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package credential implements the cluster sign-in core described in ADR-0018.
//
// The package is split into a frozen contract (contract.go) and an implementation:
//
//   - Broker orchestrates providers, challenges and token refresh;
//   - CredentialStore keeps encrypted per-owner sign-in data (PostgreSQL and file backends);
//   - Describe turns a kubeconfig into a Descriptor without any network or file access.
//
// Providers live in internal/credential/providers and only depend on the contract.
// Secrets (passwords, tokens, refresh tokens, client secrets, plugin environment values) must never
// reach logs, errors, events, audit records, metrics or the API.
package credential
