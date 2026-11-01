// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

// oidc-spike — проверка нативного oidc-password против настоящего IdP и кластера.
//
// Пароль берётся из переменной KUBRON_SPIKE_PASSWORD (не из флагов, чтобы не попасть в историю shell).
//
//	export KUBRON_SPIKE_PASSWORD='...'
//	go run ./cmd/oidc-spike --kubeconfig ~/.kube/config --user ivanov
//
// Параметры IdP берутся из exec-блока kubeconfig (kubectl oidc-login ... --grant-type=password);
// их можно переопределить флагами --issuer и --client-id.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/24DSO-labs/kubron/internal/oidcpassword"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "path to kubeconfig")
	kctx := flag.String("context", "", "kubeconfig context (defaults to the current one)")
	user := flag.String("user", "", "IdP username")
	issuer := flag.String("issuer", "", "override issuer")
	clientID := flag.String("client-id", "", "override client-id")
	flag.Parse()

	pass := os.Getenv("KUBRON_SPIKE_PASSWORD")
	if *kubeconfig == "" || *user == "" || pass == "" {
		fmt.Fprintln(os.Stderr, "--kubeconfig, --user and KUBRON_SPIKE_PASSWORD are required")
		os.Exit(2)
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: *kctx},
	).ClientConfig()
	fatal("kubeconfig", err)

	oc := oidcpassword.Config{IssuerURL: *issuer, ClientID: *clientID, Username: *user, Password: pass}
	if cfg.ExecProvider != nil {
		p, ok, err := oidcpassword.ParseOIDCLoginExec(cfg.ExecProvider.Command, cfg.ExecProvider.Args)
		fatal("exec block", err)
		if ok {
			if oc.IssuerURL == "" {
				oc.IssuerURL = p.IssuerURL
			}
			if oc.ClientID == "" {
				oc.ClientID = p.ClientID
			}
			oc.ClientSecret = p.ClientSecret
			oc.Scopes = append([]string{"openid"}, p.Scopes...)
		}
	}
	if oc.IssuerURL == "" || oc.ClientID == "" {
		fatal("parameters", fmt.Errorf("issuer/client-id found neither in kubeconfig nor in flags"))
	}
	cfg.ExecProvider = nil // exec на сервере запрещён
	src := oidcpassword.New(oc)
	cfg.Wrap(src.RoundTripper)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tok, exp, err := src.Token(ctx)
	fatal("token request", err)
	fmt.Printf("1) token received (%d bytes), valid until %s, grant=%s\n", len(tok), exp.Format(time.RFC3339), src.LastGrant())

	cs, err := kubernetes.NewForConfig(cfg)
	fatal("client", err)
	ns, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 5})
	fatal("list namespaces", err)
	fmt.Printf("2) kube-apiserver accepted the token, namespaces on the first page: %d\n", len(ns.Items))
	fmt.Println("3) to verify renewal, wait until the token expires (minus 30 s) and run again in a loop; the grant should become refresh.")
}

func fatal(what string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
