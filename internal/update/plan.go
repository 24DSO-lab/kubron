// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"strings"
)

const (
	checksumsAsset = "SHA256SUMS.txt"

	// Значения по умолчанию из docs/deploy.md: релиз и пространство имён в примерах установки.
	defaultHelmRelease   = "kubron"
	defaultHelmNamespace = "kubron"
)

// Идентификаторы команд (подписи — update.command.<id> в локалях интерфейса).
const (
	CmdHelmUpgrade = "helmUpgrade"
	CmdDockerPull  = "dockerPull"
	CmdComposeUp   = "composeUp"
	CmdImage       = "image"
	CmdDebInstall  = "debInstall"
)

// imageRepo и chartRepo выводятся из репозитория релизов так же, как в publish.yml:
// ghcr.io/<владелец>/kubron и oci://ghcr.io/<владелец>/charts/kubron (в нижнем регистре).
func imageRepo(repo string) string {
	return "ghcr.io/" + strings.ToLower(repo)
}

func chartRepo(repo string) string {
	owner, _, _ := strings.Cut(strings.ToLower(repo), "/")
	return "oci://ghcr.io/" + owner + "/charts/kubron"
}

// plan подбирает для способа установки то, что можно показать пользователю: файл для скачивания (десктоп)
// и/или команды (веб, .deb). Для MethodUnknown и неподдерживаемой архитектуры остаётся только ссылка на релиз.
func plan(in Install, rel *Release, repo string) (*Download, []Command) {
	v := imageTag(rel.Version)
	switch in.Method {
	case MethodHelm:
		release, ns := in.HelmRelease, in.HelmNamespace
		if release == "" {
			release = defaultHelmRelease
		}
		if ns == "" {
			ns = defaultHelmNamespace
		}
		// --reset-then-reuse-values: новые значения чарта берутся из него, а ваши переопределения сохраняются
		// (--reuse-values молча оставил бы значения прежней версии чарта).
		return nil, []Command{{ID: CmdHelmUpgrade, Text: "helm upgrade " + release + " " + chartRepo(repo) +
			" --version " + v + " --namespace " + ns + " --reset-then-reuse-values"}}
	case MethodKubernetes:
		return nil, []Command{{ID: CmdImage, Text: imageRepo(repo) + ":" + v}}
	case MethodDocker:
		return nil, []Command{
			{ID: CmdDockerPull, Text: "docker pull " + imageRepo(repo) + ":" + v},
			{ID: CmdComposeUp, Text: "docker compose pull && docker compose up -d"},
		}
	case MethodDeb:
		dl, ok := desktopDownload(in, rel)
		if !ok {
			return nil, nil
		}
		return dl, []Command{{ID: CmdDebInstall, Text: "sudo apt install ./" + dl.Name}}
	case MethodArchive, MethodMacApp:
		if dl, ok := desktopDownload(in, rel); ok {
			return dl, nil
		}
	}
	return nil, nil
}

// desktopDownload выбирает файл релиза под ОС, архитектуру и способ установки. Имена задаёт release.yml:
//
//	Kubron_<тег>_windows-amd64.zip, Kubron_<тег>_linux-amd64.tar.gz, Kubron_<тег>_macos-universal.zip,
//	kubron_<версия с ~>_amd64.deb
func desktopDownload(in Install, rel *Release) (*Download, bool) {
	var match func(string) bool
	switch {
	case in.Method == MethodDeb && in.Arch == "amd64":
		match = func(n string) bool { return strings.HasPrefix(n, "kubron_") && strings.HasSuffix(n, "_amd64.deb") }
	case in.OS == "linux" && in.Arch == "amd64":
		match = archive("_linux-amd64.tar.gz")
	case in.OS == "windows" && in.Arch == "amd64":
		match = archive("_windows-amd64.zip")
	case in.OS == "darwin": // universal-сборка: arm64 и amd64 в одном приложении
		match = archive("_macos-universal.zip")
	default:
		return nil, false
	}
	a, ok := rel.asset(match)
	if !ok {
		return nil, false
	}
	dl := &Download{Name: a.Name, URL: a.URL, Size: a.Size}
	if sums, ok := rel.asset(func(n string) bool { return n == checksumsAsset }); ok {
		dl.ChecksumsURL = sums.URL
	}
	return dl, true
}

func archive(suffix string) func(string) bool {
	return func(n string) bool { return strings.HasPrefix(n, "Kubron_") && strings.HasSuffix(n, suffix) }
}
