// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package eks

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/24DSO-lab/kubron/internal/credential"
)

// Field names of the challenges. accessKeyId, secretAccessKey and sessionToken are shared labels of the sign-in
// dialog (credential.field.*); the others are specific to this provider (prefix eks).
const (
	fieldMethod    = "eksMethod"
	fieldRegion    = "eksRegion"
	fieldCluster   = "eksClusterName"
	fieldAccessKey = "accessKeyId"
	fieldSecret    = "secretAccessKey"
	fieldSession   = "sessionToken"
	fieldStartURL  = "eksSsoStartUrl"
	fieldSSORegion = "eksSsoRegion"
	fieldAccount   = "eksAccountId"
	fieldRole      = "eksRoleName"

	methodKeys    = "keys"    // long-lived access keys
	methodSession = "session" // temporary access keys with a session token
	methodSSO     = "sso"     // IAM Identity Center
)

// Provider implements credential.Provider for credential.KindEKS: `aws eks get-token` and
// `aws-iam-authenticator token`, without running either.
type Provider struct{}

// New returns the provider. It has no state.
func New() *Provider { return &Provider{} }

var _ credential.Provider = (*Provider)(nil)

// Kind implements credential.Provider.
func (*Provider) Kind() credential.Kind { return credential.KindEKS }

func newSession(env credential.Env) *session {
	now := env.Now
	if now == nil {
		now = time.Now
	}
	return &session{hc: env.HTTP, now: now, desktop: env.Desktop}
}

// Begin implements credential.Provider. It issues a token from what is saved (keys, or the Identity Center
// tokens) and, when nothing saved works, returns the first challenge. Saved data that does not work any more is
// erased (Result.Save is an empty non-nil slice) together with the new challenge. The token itself is signed
// locally; the network is used only to check the keys and to refresh Identity Center credentials.
func (*Provider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	cfg, err := newConfig(req.Descriptor)
	if err != nil {
		return credential.Result{}, err
	}
	sv, svErr := parseSaved(req.Saved)
	stale := len(req.Saved) > 0 && svErr != nil
	if sv != nil {
		res, err := newSession(env).fromSaved(ctx, cfg, sv)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, credential.ErrInvalidGrant) {
			return credential.Result{}, err
		}
		stale = true
	}
	res := firstChallenge(cfg)
	if stale {
		res.Save = []byte{}
	}
	return res, nil
}

// fromSaved issues a token from a saved record. credential.ErrInvalidGrant means that nothing saved works.
func (s *session) fromSaved(ctx context.Context, cfg config, sv *saved) (credential.Result, error) {
	cfg = cfg.fill(sv.Region, sv.Cluster)
	if cfg.region == "" || cfg.cluster == "" || cfg.check() != nil {
		return credential.Result{}, staleGrant(errState)
	}
	if sv.Type == savedSSO {
		return s.fromSSO(ctx, cfg, sv, false)
	}
	base := creds{accessKeyID: sv.AccessKeyID, secretAccessKey: sv.SecretAccessKey, sessionToken: sv.SessionToken}
	principal := sv.Principal
	if cfg.roleARN == "" {
		// The keys are checked on every issue (about every 14 minutes): revoked or expired keys must lead to a new
		// sign-in and not to a token that the cluster refuses. When AWS cannot be reached the token is still
		// signed, because signing needs no network.
		arn, err := s.identity(ctx, base, cfg.region)
		switch {
		case err == nil:
			principal = arn
		case errors.Is(err, errKeysRejected):
			return credential.Result{}, staleGrant(err)
		case errors.Is(err, credential.ErrServiceUnavailable):
		default:
			return credential.Result{}, err
		}
	}
	tok, err := s.issue(ctx, cfg, base, principal)
	if err != nil {
		if errors.Is(err, errKeysRejected) {
			return credential.Result{}, staleGrant(err)
		}
		return credential.Result{}, err
	}
	res := credential.Result{Token: tok}
	if principal != "" && principal != sv.Principal && cfg.roleARN == "" {
		c := *sv
		c.Principal = principal
		res.Save = c.encode()
	}
	return res, nil
}

// issue turns base credentials into a token. With a role the base credentials assume it first and the token is
// signed with the temporary credentials; the principal is then the role.
func (s *session) issue(ctx context.Context, cfg config, base creds, principal string) (*credential.Token, error) {
	c := base
	if cfg.roleARN != "" {
		var err error
		if c, err = s.assume(ctx, base, cfg.region, cfg.roleARN); err != nil {
			return nil, err
		}
		principal = cfg.roleARN
	}
	v, err := signToken(ctx, c, cfg.region, cfg.cluster, s.now())
	if err != nil {
		return nil, err
	}
	return &credential.Token{Value: v, Expiry: s.now().Add(tokenTTL), Principal: cleanPrincipal(principal)}, nil
}

// firstChallenge asks for the sign-in method and, when the kubeconfig does not say, the region and the cluster.
func firstChallenge(cfg config) credential.Result {
	fields := []credential.Field{{Name: fieldMethod}}
	if cfg.region == "" {
		fields = append(fields, credential.Field{Name: fieldRegion})
	}
	if cfg.cluster == "" {
		fields = append(fields, credential.Field{Name: fieldCluster})
	}
	return credential.Result{
		Challenge: &credential.Challenge{Kind: credential.ChallengeSecretFields, Fields: fields},
		Pending:   pending{Step: stepMethod}.encode(),
	}
}

func fieldsChallenge(p pending, names ...string) credential.Result {
	fields := make([]credential.Field, 0, len(names))
	for _, n := range names {
		fields = append(fields, credential.Field{Name: n, Secret: n == fieldSecret || n == fieldSession})
	}
	return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeSecretFields, Fields: fields}, Pending: p.encode()}
}

// Continue implements credential.Provider. The step of the dialog is in Pending. For the device code (a Poll
// challenge) an empty Answer is a poll.
func (*Provider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	cfg, err := newConfig(req.Descriptor)
	if err != nil {
		return credential.Result{}, err
	}
	p, err := decodePending(req.Pending)
	if err != nil {
		return credential.Result{}, wrap(credential.ErrChallengeExpired, err)
	}
	s := newSession(env)
	switch p.Step {
	case stepMethod:
		return s.continueMethod(cfg, req.Answer)
	case stepKeys, stepSession:
		return s.continueKeys(ctx, cfg, p, req.Answer)
	case stepSSO:
		return s.continueSSO(ctx, cfg, p, req.Answer)
	default: // stepDevice
		return s.continueDevice(ctx, req, cfg.fill(p.Region, p.Cluster), p)
	}
}

// continueMethod reads the method (and the region and cluster the kubeconfig lacked) and asks for the details.
func (*session) continueMethod(cfg config, a credential.Answer) (credential.Result, error) {
	method := strings.ToLower(trimmed(a, fieldMethod))
	switch method {
	case methodKeys, methodSession, methodSSO:
	default:
		return credential.Result{}, invalid(errMethod)
	}
	if cfg.region == "" {
		cfg.region = trimmed(a, fieldRegion)
		switch {
		case cfg.region == "":
			return credential.Result{}, invalid(errRegionRequired)
		case checkRegion(cfg.region) != nil:
			if regionRE.MatchString(cfg.region) {
				return credential.Result{}, unsupported(errPartition) // iso partitions: the plugin of the user may do it
			}
			return credential.Result{}, invalid(errBadRegion)
		}
	}
	if cfg.cluster == "" {
		cfg.cluster = trimmed(a, fieldCluster)
		switch {
		case cfg.cluster == "":
			return credential.Result{}, invalid(errClusterRequired)
		case !clusterRE.MatchString(cfg.cluster):
			return credential.Result{}, invalid(errBadCluster)
		}
	}
	if err := cfg.check(); err != nil {
		return credential.Result{}, invalid(err)
	}
	p := pending{Region: cfg.region, Cluster: cfg.cluster}
	switch method {
	case methodKeys:
		p.Step = stepKeys
		return fieldsChallenge(p, fieldAccessKey, fieldSecret), nil
	case methodSession:
		p.Step = stepSession
		return fieldsChallenge(p, fieldAccessKey, fieldSecret, fieldSession), nil
	}
	p.Step = stepSSO
	return fieldsChallenge(p, fieldStartURL, fieldSSORegion, fieldAccount, fieldRole), nil
}

// continueKeys checks the typed keys with AWS (AssumeRole when the kubeconfig names a role, otherwise
// GetCallerIdentity) and issues the first token. Wrong keys are credential.ErrAnswerRejected.
func (s *session) continueKeys(ctx context.Context, cfg config, p pending, a credential.Answer) (credential.Result, error) {
	cfg = cfg.fill(p.Region, p.Cluster)
	if checkRegion(cfg.region) != nil || !clusterRE.MatchString(cfg.cluster) || cfg.check() != nil {
		return credential.Result{}, wrap(credential.ErrChallengeExpired, errState)
	}
	id, secret, session := trimmed(a, fieldAccessKey), strings.TrimSpace(a[fieldSecret]), strings.TrimSpace(a[fieldSession])
	if !akidRE.MatchString(id) || !secretRE.MatchString(secret) {
		return credential.Result{}, invalid(errKeyFormat)
	}
	// Temporary keys (ASIA...) need their session token; a session token with the wrong step is an input error too.
	if strings.HasPrefix(id, "ASIA") && p.Step != stepSession || p.Step == stepSession && !printableToken(session, maxSessionToken) {
		return credential.Result{}, invalid(errSessionToken)
	}
	base := creds{accessKeyID: id, secretAccessKey: secret}
	if p.Step == stepSession {
		base.sessionToken = session
	}
	principal := ""
	if cfg.roleARN == "" {
		arn, err := s.identity(ctx, base, cfg.region)
		if err != nil {
			return credential.Result{}, asAnswer(err)
		}
		principal = arn
	}
	tok, err := s.issue(ctx, cfg, base, principal)
	if err != nil {
		return credential.Result{}, asAnswer(err)
	}
	sv := saved{Type: savedKeys, Region: p.Region, Cluster: p.Cluster, Principal: tok.Principal,
		AccessKeyID: id, SecretAccessKey: secret, SessionToken: base.sessionToken}
	return credential.Result{Token: tok, Save: sv.encode()}, nil
}

// continueSSO checks the Identity Center details and starts the device authorization.
func (s *session) continueSSO(ctx context.Context, cfg config, p pending, a credential.Answer) (credential.Result, error) {
	cfg = cfg.fill(p.Region, p.Cluster)
	if checkRegion(cfg.region) != nil || !clusterRE.MatchString(cfg.cluster) || cfg.check() != nil {
		return credential.Result{}, wrap(credential.ErrChallengeExpired, errState)
	}
	start, region, account, role := trimmed(a, fieldStartURL), strings.ToLower(trimmed(a, fieldSSORegion)), trimmed(a, fieldAccount), trimmed(a, fieldRole)
	if !validSSO(start, region, account, role) {
		return credential.Result{}, invalid(errSSOFields)
	}
	return s.startDevice(ctx, cfg, start, region, account, role)
}
