// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package update

import "testing"

// testRelease — релиз со всеми файлами, которые выпускает release.yml.
func testRelease(version string) *Release {
	const dl = "https://github.com/24DSO-lab/kubron/releases/download/v"
	mk := func(name string) Asset { return Asset{Name: name, URL: dl + version + "/" + name, Size: 10} }
	return &Release{
		Tag: "v" + version, Version: version, URL: "https://github.com/24DSO-lab/kubron/releases/tag/v" + version,
		Assets: []Asset{
			mk("Kubron_v" + version + "_windows-amd64.zip"),
			mk("Kubron_v" + version + "_linux-amd64.tar.gz"),
			mk("Kubron_v" + version + "_macos-universal.zip"),
			mk("kubron_" + version + "_amd64.deb"),
			mk("SHA256SUMS.txt"),
		},
	}
}

func commandsByID(cmds []Command) map[string]string {
	out := map[string]string{}
	for _, c := range cmds {
		out[c.ID] = c.Text
	}
	return out
}

func TestPlanHelm(t *testing.T) {
	dl, cmds := plan(Install{Target: TargetWeb, Method: MethodHelm, HelmRelease: "prod", HelmNamespace: "tools"}, testRelease("1.3.0"), DefaultRepo)
	if dl != nil {
		t.Errorf("a server has nothing to download: %+v", dl)
	}
	want := "helm upgrade prod oci://ghcr.io/24dso-lab/charts/kubron --version 1.3.0 --namespace tools --reset-then-reuse-values"
	if got := commandsByID(cmds)[CmdHelmUpgrade]; got != want {
		t.Errorf("command = %q\nwant      %q", got, want)
	}
}

func TestPlanHelmDefaultsWhenReleaseUnknown(t *testing.T) {
	_, cmds := plan(Install{Target: TargetWeb, Method: MethodHelm}, testRelease("1.3.0"), DefaultRepo)
	want := "helm upgrade kubron oci://ghcr.io/24dso-lab/charts/kubron --version 1.3.0 --namespace kubron --reset-then-reuse-values"
	if got := commandsByID(cmds)[CmdHelmUpgrade]; got != want {
		t.Errorf("command = %q", got)
	}
}

func TestPlanDockerAndKubernetes(t *testing.T) {
	_, cmds := plan(Install{Target: TargetWeb, Method: MethodDocker}, testRelease("1.3.0"), DefaultRepo)
	got := commandsByID(cmds)
	if got[CmdDockerPull] != "docker pull ghcr.io/24dso-lab/kubron:1.3.0" {
		t.Errorf("docker pull = %q", got[CmdDockerPull])
	}
	if got[CmdComposeUp] == "" {
		t.Error("a Compose command is expected next to docker pull")
	}

	_, cmds = plan(Install{Target: TargetWeb, Method: MethodKubernetes}, testRelease("1.3.0"), DefaultRepo)
	if got := commandsByID(cmds)[CmdImage]; got != "ghcr.io/24dso-lab/kubron:1.3.0" {
		t.Errorf("image = %q", got)
	}
}

func TestPlanImageTagDropsBuildMetadata(t *testing.T) {
	_, cmds := plan(Install{Target: TargetWeb, Method: MethodKubernetes}, testRelease("1.3.0+build.7"), DefaultRepo)
	if got := commandsByID(cmds)[CmdImage]; got != "ghcr.io/24dso-lab/kubron:1.3.0" {
		t.Errorf("the '+' is not allowed in an image tag: %q", got)
	}
}

func TestPlanBinaryAndUnknownOnlyLinkToTheRelease(t *testing.T) {
	for _, m := range []Method{MethodBinary, MethodUnknown} {
		dl, cmds := plan(Install{Target: TargetWeb, Method: m}, testRelease("1.3.0"), DefaultRepo)
		if dl != nil || len(cmds) != 0 {
			t.Errorf("%s: dl=%+v cmds=%+v, want nothing", m, dl, cmds)
		}
	}
}

func TestPlanDesktopDownloads(t *testing.T) {
	cases := []struct {
		name string
		in   Install
		file string
	}{
		{"deb", Install{Target: TargetDesktop, Method: MethodDeb, OS: "linux", Arch: "amd64"}, "kubron_1.3.0_amd64.deb"},
		{"linux archive", Install{Target: TargetDesktop, Method: MethodArchive, OS: "linux", Arch: "amd64"}, "Kubron_v1.3.0_linux-amd64.tar.gz"},
		{"windows zip", Install{Target: TargetDesktop, Method: MethodArchive, OS: "windows", Arch: "amd64"}, "Kubron_v1.3.0_windows-amd64.zip"},
		{"macOS app on Apple silicon", Install{Target: TargetDesktop, Method: MethodMacApp, OS: "darwin", Arch: "arm64"}, "Kubron_v1.3.0_macos-universal.zip"},
		{"macOS app on Intel", Install{Target: TargetDesktop, Method: MethodMacApp, OS: "darwin", Arch: "amd64"}, "Kubron_v1.3.0_macos-universal.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dl, _ := plan(tc.in, testRelease("1.3.0"), DefaultRepo)
			if dl == nil || dl.Name != tc.file {
				t.Fatalf("download = %+v, want %s", dl, tc.file)
			}
			if dl.ChecksumsURL == "" {
				t.Error("the checksums file of the same release should be offered next to the download")
			}
		})
	}
}

func TestPlanDebAddsInstallCommand(t *testing.T) {
	_, cmds := plan(Install{Target: TargetDesktop, Method: MethodDeb, OS: "linux", Arch: "amd64"}, testRelease("1.3.0"), DefaultRepo)
	if got := commandsByID(cmds)[CmdDebInstall]; got != "sudo apt install ./kubron_1.3.0_amd64.deb" {
		t.Errorf("command = %q", got)
	}
}

// Файла под эту платформу нет (arm64 на Linux/Windows пока не выпускается): остаётся ссылка на релиз.
func TestPlanNoAssetForPlatform(t *testing.T) {
	for _, in := range []Install{
		{Target: TargetDesktop, Method: MethodArchive, OS: "linux", Arch: "arm64"},
		{Target: TargetDesktop, Method: MethodArchive, OS: "windows", Arch: "arm64"},
		{Target: TargetDesktop, Method: MethodDeb, OS: "linux", Arch: "arm64"},
	} {
		if dl, cmds := plan(in, testRelease("1.3.0"), DefaultRepo); dl != nil || len(cmds) != 0 {
			t.Errorf("%+v: dl=%+v cmds=%+v, want nothing", in, dl, cmds)
		}
	}
	// файл не выпущен в этом релизе
	rel := testRelease("1.3.0")
	rel.Assets = nil
	if dl, cmds := plan(Install{Target: TargetDesktop, Method: MethodDeb, OS: "linux", Arch: "amd64"}, rel, DefaultRepo); dl != nil || len(cmds) != 0 {
		t.Errorf("dl=%+v cmds=%+v", dl, cmds)
	}
}

func TestRepoDerivedNames(t *testing.T) {
	if got := imageRepo("24DSO-lab/kubron"); got != "ghcr.io/24dso-lab/kubron" {
		t.Errorf("imageRepo = %q", got)
	}
	if got := chartRepo("24DSO-lab/kubron"); got != "oci://ghcr.io/24dso-lab/charts/kubron" {
		t.Errorf("chartRepo = %q", got)
	}
}
