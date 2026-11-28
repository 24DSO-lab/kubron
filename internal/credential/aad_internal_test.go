// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/24DSO-lab/kubron/internal/identity"
	"github.com/24DSO-lab/kubron/internal/secrets"
)

// Legacy associated data is part of every stored record: it must stay byte-for-byte what secrets.AAD returns.
func TestLegacyAADIsUnchanged(t *testing.T) {
	for _, k := range []Key{
		{ClusterID: "c1", Scope: ScopeUser, OwnerID: "u1"},
		{ClusterID: "c1", Scope: ScopeTeam, OwnerID: "t1"},
	} {
		if !bytes.Equal(aad(k), secrets.AAD(k.ClusterID, string(k.Scope), k.OwnerID)) {
			t.Fatalf("the legacy AAD of %+v changed", k)
		}
	}
}

// The technical encoding is pinned: changing it would make every stored technical record unreadable.
func TestPrincipalAADIsPinned(t *testing.T) {
	k := Key{ClusterID: "c1", Scope: ScopePrincipal,
		Principal: identity.PrincipalRef{Kind: identity.PrincipalTeam, ID: "ti-1", OrgID: "o1", TeamID: "t1"}, BindingID: "b1"}
	const want = "6b7562726f6e2e637265642e7072696e636970616c2e763100" + // marker "kubron.cred.principal.v1\0"
		"00000002" + "6331" + // cluster
		"00000009" + "7072696e636970616c" + // scope
		"00000004" + "7465616d" + // kind
		"00000004" + "74692d31" + // principal id
		"00000002" + "6f31" + // org
		"00000002" + "7431" + // team
		"00000002" + "6231" // binding
	if got := hex.EncodeToString(aad(k)); got != want {
		t.Fatalf("the technical AAD changed:\n got %s\nwant %s", got, want)
	}
	if bytes.HasPrefix(aad(k), []byte("cred/")) {
		t.Fatal("a technical AAD must never look like a legacy one")
	}
	// A Service principal has an empty team: the empty field is still delimited.
	s := Key{ClusterID: "c1", Scope: ScopePrincipal, Principal: identity.PrincipalRef{Kind: identity.PrincipalService, ID: "x", OrgID: "o1"}, BindingID: "b1"}
	if bytes.Equal(aad(s), aad(Key{ClusterID: "c1", Scope: ScopePrincipal, Principal: identity.PrincipalRef{Kind: identity.PrincipalService, ID: "x", OrgID: "o1", TeamID: "b1"}, BindingID: ""})) {
		t.Fatal("fields must not be interchangeable")
	}
}
