// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Constants of the token format. Source: awscli/customizations/eks/get_token.py (TOKEN_PREFIX, URL_TIMEOUT,
// TOKEN_EXPIRATION_MINS, K8S_AWS_ID_HEADER, AUTH_COMMAND, AUTH_API_VERSION) and aws-iam-authenticator
// pkg/token/token.go (v1Prefix, requestPresignParam, clusterIDHeader).
const (
	tokenPrefix    = "k8s-aws-v1."
	tokenTTL       = 14 * time.Minute // the signed STS URL lives 15 minutes; clients refresh after 14
	presignExpires = "60"             // X-Amz-Expires; STS ignores it, the authenticator checks 0..900
	clusterHeader  = "x-k8s-aws-id"
	stsAction      = "GetCallerIdentity"
	stsVersion     = "2011-06-15"

	// emptySHA256 is the hex SHA-256 of an empty body.
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// creds are AWS credentials in memory. They are never printed: no String method exists on purpose, and no
// error or log line of this package carries a field of it.
type creds struct {
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
}

func (c creds) aws() aws.Credentials {
	return aws.Credentials{AccessKeyID: c.accessKeyID, SecretAccessKey: c.secretAccessKey, SessionToken: c.sessionToken}
}

// presignGET returns the query-string signed (SigV4) form of a GET request. headers are signed as they are.
// No network is used: the signature is computed locally with the static credentials.
func presignGET(ctx context.Context, rawURL string, headers map[string]string, c creds, service, region, payloadHash string, at time.Time) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", errSign
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	u, _, err := v4.NewSigner().PresignHTTP(ctx, c.aws(), req, payloadHash, service, region, at.UTC())
	if err != nil {
		return "", errSign
	}
	return u, nil
}

// signToken builds the EKS token: "k8s-aws-v1." + base64url (no padding) of the presigned STS
// GetCallerIdentity URL. The cluster name is a signed header (x-k8s-aws-id), so the token works for that
// cluster only. region must have been validated (checkRegion).
func signToken(ctx context.Context, c creds, region, cluster string, at time.Time) (string, error) {
	raw := "https://" + stsHost(region) + "/?Action=" + stsAction + "&Version=" + stsVersion + "&X-Amz-Expires=" + presignExpires
	u, err := presignGET(ctx, raw, map[string]string{clusterHeader: cluster}, c, "sts", region, emptySHA256, at)
	if err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(u)), nil
}
