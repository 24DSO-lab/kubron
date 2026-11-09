// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Переменные окружения, которыми чарт и образ сообщают серверу, как он установлен.
// Эвристики ниже — запасной путь: явное указание надёжнее.
const (
	EnvInstallMethod  = "KUBRON_INSTALL_METHOD"   // helm | kubernetes | docker | binary
	EnvHelmRelease    = "KUBRON_HELM_RELEASE"     // имя релиза Helm (.Release.Name)
	EnvHelmNamespace  = "KUBRON_HELM_NAMESPACE"   // пространство имён релиза (.Release.Namespace)
	envKubernetesHost = "KUBERNETES_SERVICE_HOST" // задаётся kubelet каждому поду
)

// debInstalledFile — список файлов пакета в базе dpkg: есть, только если kubron поставлен через dpkg/apt,
// а не просто скопирован в /usr/bin.
const debInstalledFile = "/var/lib/dpkg/info/kubron.list"

// debBinary — куда пакет кладёт исполняемый файл (build/linux/nfpm.yaml).
const debBinary = "/usr/bin/kubron"

// Имя релиза и пространство имён попадают в команду, поэтому проходят по белому списку (DNS-1123).
var (
	dnsLabelRe    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	helmReleaseRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,51}[a-z0-9])?$`) // Helm: до 53 символов
)

// Env — всё, что нужно определению способа установки от окружения. В тестах подменяется целиком.
type Env struct {
	GOOS, GOARCH string
	Getenv       func(string) string
	// Executable — путь к запущенному файлу с раскрытыми ссылками.
	Executable func() (string, error)
	Exists     func(path string) bool
	ReadFile   func(path string) ([]byte, error)
}

// OSEnv — окружение текущего процесса.
func OSEnv() Env {
	return Env{
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		Getenv: os.Getenv,
		Executable: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			if r, err := filepath.EvalSymlinks(p); err == nil {
				p = r
			}
			return p, nil
		},
		Exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		ReadFile: os.ReadFile,
	}
}

// Detect определяет способ установки. Ошибки определения не возвращаются: при сомнении — MethodUnknown,
// и пользователь получает ссылку на релиз вместо неверной команды.
func Detect(target Target, env Env) Install {
	in := Install{Target: target, Method: MethodUnknown, OS: env.GOOS, Arch: env.GOARCH}
	switch target {
	case TargetWeb:
		detectWeb(&in, env)
	case TargetDesktop:
		in.Method = detectDesktop(env)
	}
	return in
}

func detectWeb(in *Install, env Env) {
	switch Method(strings.ToLower(strings.TrimSpace(env.Getenv(EnvInstallMethod)))) {
	case MethodHelm:
		in.Method = MethodHelm
		if r := strings.TrimSpace(env.Getenv(EnvHelmRelease)); helmReleaseRe.MatchString(r) {
			in.HelmRelease = r
		}
		if ns := strings.TrimSpace(env.Getenv(EnvHelmNamespace)); dnsLabelRe.MatchString(ns) {
			in.HelmNamespace = ns
		}
		return
	case MethodKubernetes:
		in.Method = MethodKubernetes
		return
	case MethodDocker:
		in.Method = MethodDocker
		return
	case MethodBinary:
		in.Method = MethodBinary
		return
	}
	// Явного указания нет. Под в Kubernetes узнаётся по переменной kubelet: без Helm — «манифесты или GitOps».
	if env.Getenv(envKubernetesHost) != "" {
		in.Method = MethodKubernetes
		return
	}
	if inContainer(env) {
		in.Method = MethodDocker
		return
	}
	in.Method = MethodBinary
}

// inContainer — процесс работает внутри контейнера (Docker, Podman, containerd).
func inContainer(env Env) bool {
	if env.Exists("/.dockerenv") || env.Exists("/run/.containerenv") {
		return true
	}
	if b, err := env.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		for _, marker := range []string{"docker", "containerd", "kubepods", "libpod"} {
			if strings.Contains(s, marker) {
				return true
			}
		}
	}
	return false
}

func detectDesktop(env Env) Method {
	exe, err := env.Executable()
	if err != nil {
		return MethodUnknown
	}
	exe = filepath.ToSlash(exe)
	switch env.GOOS {
	case "linux":
		if exe == debBinary && env.Exists(debInstalledFile) {
			return MethodDeb
		}
		return MethodArchive
	case "darwin":
		if strings.Contains(exe, ".app/Contents/MacOS/") {
			return MethodMacApp
		}
		return MethodArchive
	case "windows":
		return MethodArchive
	}
	return MethodUnknown
}
