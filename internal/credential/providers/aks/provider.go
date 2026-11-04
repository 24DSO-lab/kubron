// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// Package aks implements the Azure kubelogin credential provider. It uses only the
// request, encrypted broker state and the explicitly supplied HTTP client.
package aks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/24DSO-labs/kubron/internal/credential"
)

const (
	defaultServerID = "6dae42f8-4368-4678-94ff-3960e28e3630"
	defaultClientID = "80faf920-1908-4b52-b5ef-a8e7bedfc67a"
	maxResponse     = 1 << 20
)

var (
	errHTTP         = errors.New("Azure sign-in request failed")
	errResponse     = errors.New("Azure returned an invalid sign-in response")
	errMFA          = errors.New("Azure requires an authentication method unsupported by password sign-in")
	errSecret       = errors.New("the client secret is invalid")
)

type Provider struct{}
func New() *Provider { return &Provider{} }
func (*Provider) Kind() credential.Kind { return credential.KindAKS }

type saved struct {
	Tenant string `json:"tenant"`
	ClientID string `json:"clientId"`
	RefreshToken string `json:"refreshToken"`
	ClientSecret string `json:"clientSecret,omitempty"`
}
type pending struct {
	Tenant string `json:"tenant"`
	DeviceCode string `json:"deviceCode"`
	TokenURL string `json:"tokenUrl"`
	Interval int `json:"interval"`
	Expires time.Time `json:"expires"`
	Next time.Time `json:"next"`
}
type oauthError struct { Error string `json:"error"` }

type config struct {
	login, tenant, clientID, serverID, authority string
	secret, username, password string
	legacy bool
}

func parseConfig(d credential.Descriptor) (config, error) {
	p := d.Params
	c := config{
		login: strings.ToLower(strings.TrimSpace(p["login"])),
		tenant: strings.TrimSpace(p["tenant-id"]),
		clientID: strings.TrimSpace(p["client-id"]),
		serverID: strings.TrimSpace(p["server-id"]),
		secret: p["client-secret"], username: p["username"], password: p["password"],
		legacy: strings.EqualFold(strings.TrimSpace(p["is-legacy"]), "true"),
	}
	if c.login == "" { c.login = "devicecode" }
	for _, key := range []string{"identity-resource-id", "federated-token-file", "client-certificate", "client-certificate-path", "client-key", "client-key-path"} {
		if strings.TrimSpace(p[key]) != "" { return config{}, credential.ErrUnsupported }
	}
	if c.tenant != "" && !validTenant(c.tenant) { return config{}, credential.ErrUnsupported }
	if c.clientID == "" { c.clientID = defaultClientID }
	if c.serverID == "" { c.serverID = defaultServerID }
	
	switch strings.ToLower(strings.TrimSpace(p["environment"])) {
	case "", "azurepubliccloud", "azurepublic":
		c.authority = "https://login.microsoftonline.com"
	case "azureusgovernmentcloud", "azureusgovernment":
		c.authority = "https://login.microsoftonline.us"
	case "azurechinacloud", "azurechina":
		c.authority = "https://login.chinacloudapi.cn"
	default:
		return config{}, credential.ErrUnsupported
	}
	if raw := strings.TrimSpace(p["authority-host"]); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return config{}, credential.ErrUnsupported
		}
		c.authority = strings.TrimRight(raw, "/")
	}
	switch c.login {
	case "devicecode", "spn", "ropc":
	default:
		return config{}, credential.ErrUnsupported
	}
	
	if c.login == "ropc" && (c.username == "" || c.password == "") {
		// The credentials are requested through a Challenge, never read from the server environment.
	}
	return c, nil
}

func (p *Provider) Begin(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	c, err := parseConfig(req.Descriptor)
	if err != nil { return credential.Result{}, err }
	sv := decodeSaved(req.Saved)
	staleSaved := false
	if sv != nil && c.secret == "" { c.secret = sv.ClientSecret }
	if c.tenant == "" { return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials, Fields: []credential.Field{{Name: "tenantId"}}}}, nil }
	if sv != nil && sv.ClientID == c.clientID && sv.Tenant == c.tenant && sv.RefreshToken != "" {
		if tok, rotated, e := refresh(ctx, env.HTTP, c, sv.RefreshToken, now(env)); e == nil {
			return credential.Result{Token: &tok, Save: encodeSaved(c, rotated)}, nil
		} else if !errors.Is(e, credential.ErrInvalidGrant) {
			return credential.Result{}, e
		} else {
			staleSaved = true
		}
	}
	switch c.login {
	case "devicecode":
		res, e := startDevice(ctx, env, c); if staleSaved && res.Save == nil { res.Save = []byte{} }; return res, e
	case "spn":
		if c.secret == "" { res := credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials, Fields: []credential.Field{{Name: "clientSecret", Secret: true}}}}, Pending: encodePending(pending{Tenant: c.tenant})}; if staleSaved { res.Save = []byte{} }; return res, nil }
		tok, e := clientCredentials(ctx, env.HTTP, c, now(env))
		if e != nil { return credential.Result{}, e }
		return credential.Result{Token: &tok, Save: encodeSaved(c, "")}, nil
	case "ropc":
		if c.username == "" || c.password == "" {
			return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials,
				Fields: []credential.Field{{Name:"username"}, {Name:"password", Secret:true}}}}, Pending: encodePending(pending{Tenant: c.tenant}), nil
		}
		tok, rt, e := passwordGrant(ctx, env.HTTP, c, c.username, c.password, now(env))
		if e != nil { return credential.Result{}, e }
		return credential.Result{Token:&tok, Save: encodeSaved(c, rt)}, nil
	default:
		return credential.Result{}, credential.ErrUnsupported
	}
}

func (p *Provider) beginConfigured(ctx context.Context, env credential.Env, req credential.Request, c config) (credential.Result, error) {
	switch c.login {
	case "devicecode":
		return startDevice(ctx, env, c)
	case "spn":
		if c.secret == "" { return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials, Fields: []credential.Field{{Name: "clientSecret", Secret: true}}}}, nil }
		tok, err := clientCredentials(ctx, env.HTTP, c, now(env)); if err != nil { return credential.Result{}, err }
		return credential.Result{Token: &tok, Save: encodeSaved(c, "")}, nil
	case "ropc":
		return credential.Result{Challenge: &credential.Challenge{Kind: credential.ChallengeCredentials, Fields: []credential.Field{{Name: "username"}, {Name: "password", Secret: true}}}}, Pending: encodePending(pending{Tenant: c.tenant}), nil
	default:
		return credential.Result{}, credential.ErrUnsupported
	}
}

func (p *Provider) Continue(ctx context.Context, env credential.Env, req credential.Request) (credential.Result, error) {
	c, err := parseConfig(req.Descriptor)
	if err != nil { return credential.Result{}, err }
	var prior pending
	_ = json.Unmarshal(req.Pending, &prior)
	if c.tenant == "" { c.tenant = prior.Tenant }
	switch req.Challenge.Kind {
	case credential.ChallengeCredentials:
		if c.tenant == "" {
			c.tenant = strings.TrimSpace(req.Answer["tenantId"])
			if c.tenant == "" { return credential.Result{}, credential.ErrInvalidAnswer }
			return p.beginConfigured(ctx, env, req, c)
		}
		if c.login == "spn" && c.secret == "" {
			c.secret = req.Answer["clientSecret"]
			if c.secret == "" { return credential.Result{}, credential.ErrInvalidAnswer }
			tok, e := clientCredentials(ctx, env.HTTP, c, now(env))
			if e != nil { return credential.Result{}, e }
			return credential.Result{Token: &tok, Save: encodeSaved(c, "")}, nil
		}
		user, pass := req.Answer["username"], req.Answer["password"]
		if user == "" || pass == "" { return credential.Result{}, credential.ErrInvalidAnswer }
		tok, rt, e := passwordGrant(ctx, env.HTTP, c, user, pass, now(env))
		if e != nil { return credential.Result{}, e }
		return credential.Result{Token:&tok, Save: encodeSaved(c, rt)}, nil
	case credential.ChallengeDeviceCode:
		return pollDevice(ctx, env, req, c)
	default:
		return credential.Result{}, credential.ErrUnsupported
	}
}

func startDevice(ctx context.Context, env credential.Env, c config) (credential.Result, error) {
	if env.HTTP == nil { return credential.Result{}, credential.ErrUnsupported }
	form := url.Values{"client_id":{c.clientID}, "scope":{scope(c)+" offline_access"}}
	body, status, err := post(ctx, env.HTTP, c.authority+"/"+url.PathEscape(c.tenant)+"/oauth2/v2.0/devicecode", form)
	if err != nil { return credential.Result{}, errHTTP }
	if status < 200 || status >= 300 { return credential.Result{}, errResponse }
	var d struct { DeviceCode string `json:"device_code"`; UserCode string `json:"user_code"`; URI string `json:"verification_uri"`; Expires int `json:"expires_in"`; Interval int `json:"interval"` }
	if json.Unmarshal(body, &d) != nil || d.DeviceCode == "" || d.UserCode == "" || !safeHTTPS(d.URI) { return credential.Result{}, errResponse }
	interval := d.Interval; if interval < 1 { interval = 5 }; if interval > 60 { interval = 60 }
	ttl := d.Expires; if ttl <= 0 { ttl = 900 }; if ttl > 1800 { ttl = 1800 }; exp := now(env).Add(time.Duration(ttl)*time.Second)
	st := pending{Tenant:c.tenant, DeviceCode:d.DeviceCode, TokenURL:c.authority+"/"+url.PathEscape(c.tenant)+"/oauth2/v2.0/token", Interval:interval, Expires:exp, Next:now(env).Add(time.Duration(interval)*time.Second)}
	raw,_ := json.Marshal(st)
	return credential.Result{Challenge:&credential.Challenge{Kind:credential.ChallengeDeviceCode, URL:d.URI, UserCode:d.UserCode, Poll:true, IntervalSec:interval, ExpiresAt:exp}, Pending:raw}, nil
}

func pollDevice(ctx context.Context, env credential.Env, req credential.Request, c config) (credential.Result, error) {
	var st pending
	if json.Unmarshal(req.Pending, &st) != nil || st.DeviceCode == "" || !safeHTTPS(st.TokenURL) { return credential.Result{}, credential.ErrChallengeExpired }
	c.tenant = st.Tenant
	n := now(env)
	if !n.Before(st.Expires) { return credential.Result{}, credential.ErrChallengeExpired }
	if n.Before(st.Next) { return credential.Result{Challenge:&req.Challenge, Pending:req.Pending}, nil }
	form := url.Values{"grant_type":{"urn:ietf:params:oauth:grant-type:device_code"}, "client_id":{c.clientID}, "device_code":{st.DeviceCode}}
	body, status, err := post(ctx, env.HTTP, st.TokenURL, form)
	if err != nil { return credential.Result{}, errHTTP }
	if status >= 200 && status < 300 {
		tok, rt, e := parseToken(body, n); if e != nil { return credential.Result{}, e }
		return credential.Result{Token:&tok, Save:encodeSaved(c,rt)}, nil
	}
	var oe oauthError; _ = json.Unmarshal(body,&oe)
	switch oe.Error {
	case "authorization_pending":
	case "slow_down": st.Interval += 5; if st.Interval > 60 { st.Interval=60 }
	case "authorization_declined": return credential.Result{}, credential.ErrAnswerRejected
	case "expired_token": return credential.Result{}, credential.ErrChallengeExpired
	default: return credential.Result{}, errResponse
	}
	st.Next = n.Add(time.Duration(st.Interval)*time.Second)
	raw,_ := json.Marshal(st)
	ch := req.Challenge; ch.IntervalSec=st.Interval
	return credential.Result{Challenge:&ch, Pending:raw}, nil
}

func clientCredentials(ctx context.Context, hc *http.Client, c config, n time.Time) (credential.Token,error) {
	form := url.Values{"grant_type":{"client_credentials"}, "client_id":{c.clientID}, "client_secret":{c.secret}, "scope":{scope(c)}}
	body, status, err := post(ctx,hc,c.authority+"/"+url.PathEscape(c.tenant)+"/oauth2/v2.0/token",form)
	if err != nil { return credential.Token{},errHTTP }
	if status < 200 || status >= 300 { var oe oauthError; _ = json.Unmarshal(body, &oe); if oe.Error == "invalid_client" { return credential.Token{}, errSecret }; return credential.Token{}, errResponse }
	t,_,err:=parseToken(body,n); return t,err
}
func passwordGrant(ctx context.Context, hc *http.Client, c config, user, pass string, n time.Time) (credential.Token,string,error) {
	form:=url.Values{"grant_type":{"password"},"client_id":{c.clientID},"username":{user},"password":{pass},"scope":{scope(c)+" offline_access"}}
	body,status,err:=post(ctx,hc,c.authority+"/"+url.PathEscape(c.tenant)+"/oauth2/v2.0/token",form)
	if err!=nil{return credential.Token{},"",errHTTP}
	if status<200||status>=300 {
		var oe oauthError; _=json.Unmarshal(body,&oe)
		if oe.Error=="interaction_required"||oe.Error=="mfa_required" { return credential.Token{},"",errMFA }
		if oe.Error=="invalid_grant"||oe.Error=="access_denied" { return credential.Token{},"",credential.ErrAnswerRejected }
		return credential.Token{},"",errResponse
	}
	t,rt,e:=parseToken(body,n);return t,rt,e
}
func refresh(ctx context.Context,hc *http.Client,c config,rt string,n time.Time)(credential.Token,string,error){
	form:=url.Values{"grant_type":{"refresh_token"},"client_id":{c.clientID},"refresh_token":{rt},"scope":{scope(c)+" offline_access"}}
	body,status,err:=post(ctx,hc,c.authority+"/"+url.PathEscape(c.tenant)+"/oauth2/v2.0/token",form)
	if err!=nil{return credential.Token{},"",errHTTP}
	if status<200||status>=300{var oe oauthError; _=json.Unmarshal(body,&oe); if oe.Error=="invalid_grant"||oe.Error=="invalid_grant"||oe.Error=="invalid_token" { return credential.Token{},"",credential.ErrInvalidGrant }; return credential.Token{},"",errResponse}
	return parseToken(body,n)
}
func scope(c config) string { s:=c.serverID+"/.default"; if c.legacy { s="sp:"+s }; return s }
func post(ctx context.Context,hc *http.Client, endpoint string, form url.Values)([]byte,int,error){
	if hc==nil||!safeHTTPS(endpoint){return nil,0,errors.New("blocked")}
	req,err:=http.NewRequestWithContext(ctx,http.MethodPost,endpoint,strings.NewReader(form.Encode()));if err!=nil{return nil,0,err}
	req.Header.Set("Content-Type","application/x-www-form-urlencoded")
	resp,err:=hc.Do(req);if err!=nil{return nil,0,err};defer resp.Body.Close()
	b,err:=io.ReadAll(io.LimitReader(resp.Body,maxResponse+1));if err!=nil||len(b)>maxResponse{return nil,0,errResponse}
	return b,resp.StatusCode,nil
}
func parseToken(b []byte,n time.Time)(credential.Token,string,error){
	var v struct{Access string `json:"access_token"`;Refresh string `json:"refresh_token"`;Expires int64 `json:"expires_in"`}
	if json.Unmarshal(b,&v)!=nil||v.Access==""||v.Expires<=0||v.Expires>86400{return credential.Token{},"",errResponse}
	principal:=""
	parts:=strings.Split(v.Access,".");if len(parts)==3{raw,e:=base64.RawURLEncoding.DecodeString(parts[1]);if e==nil{var claims struct{OID string `json:"oid"`;UPN string `json:"upn"`;Preferred string `json:"preferred_username"`};if json.Unmarshal(raw,&claims)==nil{principal=claims.OID;if principal==""{principal=claims.UPN};if principal==""{principal=claims.Preferred}}}}
	if len(principal)>256 {principal=""}
	return credential.Token{Value:v.Access,Expiry:n.Add(time.Duration(v.Expires)*time.Second),Principal:principal},v.Refresh,nil
}
func encodePending(p pending) []byte { b, _ := json.Marshal(p); return b }
func encodeSaved(c config,rt string) []byte { if rt=="" && c.secret=="" { return nil }; b,_:=json.Marshal(saved{Tenant:c.tenant,ClientID:c.clientID,RefreshToken:rt,ClientSecret:c.secret});return b }
func decodeSaved(b []byte)*saved{if len(b)==0{return nil};var s saved;if json.Unmarshal(b,&s)!=nil||(s.RefreshToken==""&&s.ClientSecret==""){return nil};return &s}
func now(e credential.Env)time.Time{if e.Now!=nil{return e.Now()};return time.Now()}
func safeHTTPS(raw string)bool{u,e:=url.Parse(raw);return e==nil&&u.Scheme=="https"&&u.Host!=""&&u.User==nil&&u.Fragment==""}
func validTenant(s string) bool {
	if len(s) == 0 || len(s) > 256 { return false }
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' { continue }
		return false
	}
	return true
}
