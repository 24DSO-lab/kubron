// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/24DSO-lab/kubron/internal/audit"
	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
	"github.com/24DSO-lab/kubron/internal/server"
	"github.com/24DSO-lab/kubron/internal/store"
)

// adminCommands — служебные команды: kubron-server <команда>. Настройки БД берутся из окружения
// (KUBRON_DATABASE_URL, KUBRON_SECRET_KEY, KUBRON_SECRET_KEY_PREVIOUS).
var adminCommands = map[string]bool{"import-files": true, "rekey": true, "create-admin": true}

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
	case "create-admin":
		return runCreateAdmin(args, os.Getenv, os.Stdin, out, store.FromPostgres(pg))
	case "rekey":
		rep, err := pg.Rekey(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "re-encrypted with key %q: %d kubeconfigs, %d cluster secrets, %d Argo CD tokens, %d Helm repository secrets, %d sign-in credentials (%d still in legacy format), %d baseline versions\n", cfg.SecretKey[:idLen(cfg.SecretKey)], rep.Clusters, rep.Secrets, rep.Tokens, rep.Helm, rep.Credentials, rep.LegacyCredentials, rep.Baselines)
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

// adminPasswordEnv — переменная окружения с паролем для create-admin. Пароль не принимается аргументом командной
// строки: аргументы видны в списке процессов и в истории оболочки.
const adminPasswordEnv = "KUBRON_ADMIN_PASSWORD"

// maxPasswordInput — предел чтения пароля из stdin (защита от случайной подачи файла).
const maxPasswordInput = 4096

// runCreateAdmin: kubron-server create-admin --username <имя>. Создаёт первого администратора напрямую в БД, минуя
// сеть и токен первичной настройки. Пароль — из KUBRON_ADMIN_PASSWORD или первой строки stdin.
func runCreateAdmin(args []string, getenv func(string) string, stdin io.Reader, out io.Writer, st store.Store) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	fs.SetOutput(out)
	username := fs.String("username", "", "administrator username (required); the password is read from "+adminPasswordEnv+" or stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("unexpected arguments: the password is never passed on the command line")
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}
	password, err := readAdminPassword(getenv, stdin)
	if err != nil {
		return err
	}
	u, err := createAdmin(st, *username, password)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "administrator %q created\n", u.Username)
	return err
}

// readAdminPassword берёт пароль из окружения, а если там пусто — из первой строки stdin (без перевода строки).
func readAdminPassword(getenv func(string) string, stdin io.Reader) (string, error) {
	if v := getenv(adminPasswordEnv); v != "" {
		return v, nil
	}
	if stdin == nil {
		return "", errors.New("the password is not provided: set " + adminPasswordEnv + " or pass it on stdin")
	}
	line, err := bufio.NewReaderSize(io.LimitReader(stdin, maxPasswordInput), maxPasswordInput).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("failed to read the password: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("the password is not provided: set " + adminPasswordEnv + " or pass it on stdin")
	}
	return line, nil
}

// createAdmin создаёт администратора через auth.Service: та же проверка логина и пароля, что у SetupAdmin, отказ при
// существующих пользователях, запись «Setup» в журнале аудита (источник — cli).
func createAdmin(st store.Store, username, password string) (*auth.UserInfo, error) {
	svc, err := auth.NewServiceWithBackend(st.Users())
	if err != nil {
		return nil, err
	}
	svc.SetAuditor(audit.New(st.Audit()))
	return svc.NewSession("cli").Setup(username, password)
}
