// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package execplugin is the desktop provider for exec plugins (ADR-0018, ticket B.9). It runs the program
// named in the kubeconfig exec block and speaks the ExecCredential protocol (client.authentication.k8s.io
// v1 and v1beta1).
//
// A plugin is arbitrary code from a file the user may have received from somewhere. Therefore the command
// is never started before the user confirmed it: Begin returns a confirm-exec challenge that shows the
// command, the arguments and the names (never the values) of the environment variables. The answer
// "always" is stored in DataDir/exec-trust.json under a hash of the whole block and the cluster id, so any
// change of the block asks again. Continue starts the process only when the private state saved by Begin
// proves that the confirmation was shown for exactly this block.
//
// interactiveMode Always needs a pseudo terminal (pty, ConPTY) and is not supported yet: it is refused with
// credential.ErrUnsupported before anything is shown or run.
package execplugin
