// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/24DSO-labs/kubron/internal/k8s"
	"github.com/24DSO-labs/kubron/internal/server"
	"github.com/24DSO-labs/kubron/internal/store"
)

// adminCommands — служебные команды: kubron-server <команда>. Настройки БД берутся из окружения
// (KUBRON_DATABASE_URL, KUBRON_SECRET_KEY, KUBRON_SECRET_KEY_PREVIOUS).
var adminCommands = map[string]bool{"import-files": true, "rekey": true}

func runAdmin(ctx context.Context, cmd string, args []string, out io.Writer) error {
	cfg, err := server.LoadConfig(nil, os.Getenv, out)
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("set KUBRON_DATABASE_URL: this command works with the database")
	}
	pg, err := openPostgres(ctx, cfg)
	if err != nil {
		return err
	}
	defer pg.Close()

	switch cmd {
	case "rekey":
		rep, err := pg.Rekey(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "re-encrypted with key %q: %d kubeconfigs, %d cluster secrets, %d Argo CD tokens, %d Helm repository secrets\n", cfg.SecretKey[:idLen(cfg.SecretKey)], rep.Clusters, rep.Secrets, rep.Tokens, rep.Helm)
		return err
	case "import-files":
		fs := flag.NewFlagSet("import-files", flag.ContinueOnError)
		fs.SetOutput(out)
		from := fs.String("from", "", "data directory with users.json, clusters/ and audit.log (default ~/.kubron)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		k8s.SetKubronDir(*from) // файловые хранилища читают каталог данных из k8s
		src := store.NewFile(k8s.GetKubronDir())
		rep, err := store.ImportFiles(ctx, src, pg)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(out, "imported from %s: %d users, %d clusters, %d Argo CD tokens, %d audit events\n",
			k8s.GetKubronDir(), rep.Users, rep.Clusters, rep.Tokens, rep.AuditEvents); err != nil {
			return err
		}
		for _, n := range rep.Notes {
			if _, err = fmt.Fprintln(out, "note:", n); err != nil {
				return err
			}
		}
		if _, err = fmt.Fprintln(out, "source files are untouched and can serve as a backup"); err != nil {
			return err
		}
	}
	return nil
}

// idLen — длина идентификатора ключа в строке «id:base64» (сам ключ в вывод не попадает).
func idLen(key string) int {
	for i, r := range key {
		if r == ':' {
			return i
		}
	}
	return 0
}
