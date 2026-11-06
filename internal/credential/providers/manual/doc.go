// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package manual is the web-mode provider for exec plugins (ADR-0018, ticket B.8). The server never runs a
// plugin: the provider shows the command from the kubeconfig and accepts the token the user obtained on
// their own computer, either as an ExecCredential JSON document or as a bare token.
//
// The provider registers under credential.KindExec in web mode only. It does not start processes, does not
// touch the network or the disk, and never writes the token into an error or a log.
package manual
