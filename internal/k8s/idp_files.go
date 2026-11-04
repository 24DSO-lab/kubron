// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The identity provider of oidc-login and of the legacy oidc auth-provider may be given a CA certificate as a file
// (--certificate-authority, idp-certificate-authority). The broker's Describe is a pure function and understands only
// the -data forms, so on the desktop (policy AllowLocalFiles) those files are read here, before Describe, and put into
// the -data fields of the copy of the kubeconfig that goes to the broker. The stored kubeconfig is not changed. In the
// web the files of the server machine are never read: the references stay as they are and are not used.

const maxIdPCAFileSize = 1 << 20

// ErrIdPFile means that a certificate file of the identity provider named in the kubeconfig could not be read.
var ErrIdPFile = errors.New("the identity provider certificate file named in the kubeconfig could not be read")

// embedIdPFiles returns the kubeconfig with the identity provider CA files of the chosen context embedded as -data.
// baseDir resolves relative paths (the directory of the kubeconfig file). A kubeconfig that has nothing to embed is
// returned byte for byte.
func embedIdPFiles(kubeconfig []byte, contextName, baseDir string) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return kubeconfig, nil // Describe reports an unreadable kubeconfig itself
	}
	name := contextName
	if name == "" {
		name = cfg.CurrentContext
	}
	ctx := cfg.Contexts[name]
	if ctx == nil {
		return kubeconfig, nil
	}
	ai := cfg.AuthInfos[ctx.AuthInfo]
	if ai == nil {
		return kubeconfig, nil
	}
	changed := false
	if ai.Exec != nil {
		args, ch, err := embedExecCA(ai.Exec.Args, baseDir)
		if err != nil {
			return nil, err
		}
		if ch {
			ai.Exec.Args, changed = args, true
		}
	}
	if ap := ai.AuthProvider; ap != nil && ap.Name == "oidc" {
		path := ap.Config["idp-certificate-authority"]
		if path != "" && ap.Config["idp-certificate-authority-data"] == "" {
			data, err := readIdPFile(path, baseDir)
			if err != nil {
				return nil, err
			}
			ap.Config["idp-certificate-authority-data"] = data
			changed = true
		}
	}
	if !changed {
		return kubeconfig, nil
	}
	return writeKubeconfig(cfg)
}

func writeKubeconfig(cfg *clientcmdapi.Config) ([]byte, error) {
	return clientcmd.Write(*cfg)
}

// embedExecCA rewrites --certificate-authority=FILE and --certificate-authority FILE into
// --certificate-authority-data=BASE64. An explicit -data argument wins and the file argument is dropped untouched.
func embedExecCA(args []string, baseDir string) ([]string, bool, error) {
	hasData := false
	for _, a := range args {
		if a == "--certificate-authority-data" || strings.HasPrefix(a, "--certificate-authority-data=") {
			hasData = true
		}
	}
	out := make([]string, 0, len(args))
	changed := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		var path string
		switch {
		case strings.HasPrefix(a, "--certificate-authority="):
			path = strings.TrimPrefix(a, "--certificate-authority=")
		case a == "--certificate-authority" && i+1 < len(args):
			path = args[i+1]
			i++
		default:
			out = append(out, a)
			continue
		}
		changed = true
		if hasData || path == "" {
			continue
		}
		data, err := readIdPFile(path, baseDir)
		if err != nil {
			return nil, false, err
		}
		out = append(out, "--certificate-authority-data="+data)
		hasData = true
	}
	return out, changed, nil
}

// readIdPFile reads a certificate file (bounded) and returns it base64 encoded. The path and the content are not
// put into the error.
func readIdPFile(path, baseDir string) (string, error) {
	if !filepath.IsAbs(path) && baseDir != "" {
		path = filepath.Join(baseDir, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrIdPFile
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxIdPCAFileSize+1))
	if err != nil || len(raw) == 0 || len(raw) > maxIdPCAFileSize {
		return "", ErrIdPFile
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
