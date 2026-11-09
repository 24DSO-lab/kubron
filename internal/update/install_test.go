// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import (
	"errors"
	"io/fs"
	"testing"
)

// fakeEnv собирает окружение из карт: переменные, существующие пути и содержимое файлов.
func fakeEnv(goos, goarch string, vars map[string]string, exists []string, files map[string]string, exe string) Env {
	have := map[string]bool{}
	for _, p := range exists {
		have[p] = true
	}
	return Env{
		GOOS: goos, GOARCH: goarch,
		Getenv: func(k string) string { return vars[k] },
		Executable: func() (string, error) {
			if exe == "" {
				return "", errors.New("no executable")
			}
			return exe, nil
		},
		Exists: func(p string) bool { return have[p] },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

func TestDetectWeb(t *testing.T) {
	cases := []struct {
		name      string
		vars      map[string]string
		exists    []string
		files     map[string]string
		want      Method
		release   string
		namespace string
	}{
		{name: "helm from the chart", vars: map[string]string{
			EnvInstallMethod: "helm", EnvHelmRelease: "prod-kubron", EnvHelmNamespace: "tools", "KUBERNETES_SERVICE_HOST": "10.0.0.1",
		}, want: MethodHelm, release: "prod-kubron", namespace: "tools"},
		{name: "helm without release and namespace", vars: map[string]string{EnvInstallMethod: "helm"}, want: MethodHelm},
		{name: "helm: unsafe values are dropped", vars: map[string]string{
			EnvInstallMethod: "helm", EnvHelmRelease: "x; rm -rf /", EnvHelmNamespace: "$(reboot)",
		}, want: MethodHelm},
		{name: "explicit value is case-insensitive", vars: map[string]string{EnvInstallMethod: " Helm "}, want: MethodHelm},
		{name: "explicit docker beats the Kubernetes variable", vars: map[string]string{
			EnvInstallMethod: "docker", "KUBERNETES_SERVICE_HOST": "10.0.0.1",
		}, want: MethodDocker},
		{name: "explicit binary inside a container", vars: map[string]string{EnvInstallMethod: "binary"},
			exists: []string{"/.dockerenv"}, want: MethodBinary},
		{name: "pod without Helm", vars: map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, want: MethodKubernetes},
		{name: "unknown explicit value falls back to detection", vars: map[string]string{
			EnvInstallMethod: "snap", "KUBERNETES_SERVICE_HOST": "10.0.0.1",
		}, want: MethodKubernetes},
		{name: "docker by /.dockerenv", exists: []string{"/.dockerenv"}, want: MethodDocker},
		{name: "podman", exists: []string{"/run/.containerenv"}, want: MethodDocker},
		{name: "docker by cgroup", files: map[string]string{"/proc/1/cgroup": "0::/system.slice/docker-abc.scope\n"}, want: MethodDocker},
		{name: "bare host", files: map[string]string{"/proc/1/cgroup": "0::/init.scope\n"}, want: MethodBinary},
		{name: "no /proc at all", want: MethodBinary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Detect(TargetWeb, fakeEnv("linux", "amd64", tc.vars, tc.exists, tc.files, ""))
			if in.Method != tc.want {
				t.Errorf("method = %q, want %q", in.Method, tc.want)
			}
			if in.HelmRelease != tc.release || in.HelmNamespace != tc.namespace {
				t.Errorf("helm release/namespace = %q/%q, want %q/%q", in.HelmRelease, in.HelmNamespace, tc.release, tc.namespace)
			}
			if in.Target != TargetWeb {
				t.Errorf("target = %q", in.Target)
			}
		})
	}
}

func TestDetectDesktop(t *testing.T) {
	cases := []struct {
		name   string
		goos   string
		exe    string
		exists []string
		want   Method
	}{
		{"deb package", "linux", "/usr/bin/kubron", []string{debInstalledFile}, MethodDeb},
		{"binary copied to /usr/bin without dpkg", "linux", "/usr/bin/kubron", nil, MethodArchive},
		{"tar.gz in the home directory", "linux", "/home/u/apps/Kubron", nil, MethodArchive},
		{"deb file exists but the exe is elsewhere", "linux", "/opt/Kubron", []string{debInstalledFile}, MethodArchive},
		{"macOS app bundle", "darwin", "/Applications/Kubron.app/Contents/MacOS/Kubron", nil, MethodMacApp},
		{"macOS bare binary", "darwin", "/Users/u/Kubron", nil, MethodArchive},
		{"windows zip", "windows", `C:\Tools\Kubron\Kubron.exe`, nil, MethodArchive},
		{"freebsd", "freebsd", "/usr/local/bin/kubron", nil, MethodUnknown},
		{"cannot resolve the executable", "linux", "", nil, MethodUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Detect(TargetDesktop, fakeEnv(tc.goos, "amd64", nil, tc.exists, nil, tc.exe))
			if in.Method != tc.want {
				t.Errorf("method = %q, want %q", in.Method, tc.want)
			}
			if in.Target != TargetDesktop || in.OS != tc.goos || in.Arch != "amd64" {
				t.Errorf("install = %+v", in)
			}
		})
	}
}

func TestDetectUnknownTarget(t *testing.T) {
	if in := Detect("tablet", fakeEnv("linux", "amd64", nil, nil, nil, "/x")); in.Method != MethodUnknown {
		t.Errorf("method = %q", in.Method)
	}
}
