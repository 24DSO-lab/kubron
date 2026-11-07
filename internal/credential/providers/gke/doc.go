// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package gke is the sign-in provider for Google GKE clusters (ADR-0018, ticket B.7b, kind
// credential.KindGKE). It is the native equivalent of gke-gcloud-auth-plugin for one way of signing in:
// a service account key that the user pastes.
//
// The provider is a function of its Request and Env. It has no storage, no global state and no
// background work. The only thing it sends over the network is the JWT-bearer token request
// (RFC 7523) to the fixed address TokenURL, and it sends it through Env.HTTP and nothing else.
//
// What the provider never does (each point is checked by a test):
//
//   - It never uses the credentials of the machine it runs on. Application Default Credentials, the
//     GOOGLE_APPLICATION_CREDENTIALS variable, the gcloud configuration, the metadata server and
//     workload identity are not consulted. The package reads no files, calls no os functions and
//     does not import golang.org/x/oauth2/google, so the search chain of the Google client libraries
//     is not even linked in. Descriptor.Params (including "use-adc") is not read at all.
//   - It never takes an address from the key file. The token_uri field of the key is ignored: a key
//     file is data that the user pasted, and a file with a foreign token_uri would otherwise send the
//     signed assertion, and with it proof of possession of the key, to a host of someone else's choice.
//   - It never writes the key, the assertion or a token into an error text. Error texts are fixed.
//
// Unsupported kinds of Google credentials (external_account, which is workload identity federation,
// authorized_user, impersonated_service_account and so on) are reported as credential.ErrUnsupported
// and the Broker falls back to the manual-token or exec provider.
package gke
