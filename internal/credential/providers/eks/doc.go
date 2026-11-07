// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package eks is the sign-in provider for Amazon EKS clusters (ADR-0018, ticket B.7a, kind credential.KindEKS).
// It replaces `aws eks get-token` and `aws-iam-authenticator token`: the token is a presigned STS
// GetCallerIdentity URL (SigV4, query string) that carries the cluster name in the signed x-k8s-aws-id header.
// The format and constants follow awscli/customizations/eks/get_token.py and the token verifier of
// kubernetes-sigs/aws-iam-authenticator.
//
// Ways to sign in (the user answers a sequence of secret-fields challenges, because the sign-in dialog needs
// every listed field filled in and the contract has no optional fields):
//
//   - keys: an access key ID and a secret access key; for temporary keys (ASIA...) also a session token;
//   - IAM Identity Center (SSO): the OAuth device flow of sso-oidc, then sso:GetRoleCredentials;
//   - both can be combined with the role-arn of the kubeconfig (sts:AssumeRole with the base credentials).
//
// The provider is a function of its Request and Env. It keeps no state, has no storage and no globals. Saved
// holds the long-lived part of a sign-in (keys, or the SSO client registration and tokens) and is encrypted by
// the Broker; Pending holds the state of one sign-in dialog and is encrypted as well.
//
// Security rules (checked by tests):
//
//   - Every network request goes through Env.HTTP, which carries the SSRF policy. The provider has no HTTP
//     client or transport of its own. Endpoints are derived from a validated region (sts.<region>, oidc.<region>,
//     portal.sso.<region>); no address from the kubeconfig is ever requested. Addresses that come back in a
//     response (the verification link) must be https on the web server.
//   - The provider never uses credentials of the machine it runs on: no environment variables, no files
//     (~/.aws, shared config), no instance metadata, no SDK default credential chain. Only what the user typed
//     or what is in Saved. The kubeconfig parameter "profile" is ignored for this reason.
//   - Keys, session tokens, SSO tokens and client secrets are never put into an error text, a log line or a
//     challenge. Error texts are fixed.
package eks
