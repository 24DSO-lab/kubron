// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/24DSO-lab/kubron/internal/auth"
	"github.com/24DSO-lab/kubron/internal/k8s"
)

var errDebugDisabled = errors.New("debug containers are disabled by the administrator")

// DebugOptionsInfo — что интерфейс может предложить в диалоге отладочного контейнера.
type DebugOptionsInfo struct {
	// Images — образы на выбор: список администратора (KUBRON_DEBUG_IMAGES) или образы по умолчанию.
	Images []string `json:"images"`
	// Strict — образы ограничены списком администратора, любой другой образ будет отклонён.
	Strict         bool     `json:"strict"`
	Profiles       []string `json:"profiles"`
	DefaultProfile string   `json:"defaultProfile"`
}

func (a *App) checkDebugEnabled() error {
	if a.interactive.DisableExec { // подключение к контейнеру идёт терминалом (attach)
		return errExecDisabled
	}
	if a.interactive.DisableDebug {
		return errDebugDisabled
	}
	return nil
}

// checkDebugImage применяет список образов администратора. Без списка допустим любой образ.
func (a *App) checkDebugImage(image string) error {
	image = strings.TrimSpace(image)
	if image == "" {
		return errors.New("image is required")
	}
	if allowed := a.interactive.DebugImages; len(allowed) > 0 && !slices.Contains(allowed, image) {
		return fmt.Errorf("image %q is not allowed by the administrator (allowed: %s)", image, strings.Join(allowed, ", "))
	}
	return nil
}

// GetDebugOptions — образы и профили для диалога. Право то же, что и для самого действия.
func (a *App) GetDebugOptions() (DebugOptionsInfo, error) {
	if err := a.authz(auth.PermDebug); err != nil {
		return DebugOptionsInfo{}, err
	}
	if err := a.checkDebugEnabled(); err != nil {
		return DebugOptionsInfo{}, err
	}
	info := DebugOptionsInfo{
		Images:         k8s.DefaultDebugImages(),
		Profiles:       k8s.DebugProfiles(),
		DefaultProfile: k8s.DebugProfileGeneral,
	}
	if len(a.interactive.DebugImages) > 0 {
		info.Images, info.Strict = slices.Clone(a.interactive.DebugImages), true
	}
	return info, nil
}

// StartDebugContainer добавляет в под отладочный (ephemeral) контейнер и ждёт его запуска. Подключение к нему —
// обычный терминал в режиме attach (OpenTerminal / StartTerminalSession с пустой оболочкой), для которого нужно
// ещё и право exec. Запись в журнал делает authz: PermDebug входит в auditedPermissions, цель действия — namespace,
// pod, target, image и профиль.
func (a *App) StartDebugContainer(namespace, pod, target, image, profile string) (*k8s.DebugContainer, error) {
	if err := a.authz(auth.PermDebug, "namespace", namespace, "pod", pod, "target", target, "image", image, "profile", profile); err != nil {
		return nil, err
	}
	if err := a.checkDebugEnabled(); err != nil {
		return nil, err
	}
	// Под оболочки узла привилегирован: отладочный контейнер в нём равносилен доступу к узлу (право nodeshell).
	if err := a.checkTerminalTarget(namespace, pod); err != nil {
		return nil, err
	}
	if err := a.checkDebugImage(image); err != nil {
		return nil, err
	}
	return a.k8sMgr.AddDebugContainer(k8s.DebugOptions{
		Namespace: namespace, Pod: pod, Target: target, Image: strings.TrimSpace(image), Profile: profile,
	})
}
