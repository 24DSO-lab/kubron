// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"embed"

	"github.com/24DSO-labs/kubron/internal/app"
	wailsplatform "github.com/24DSO-labs/kubron/internal/platform/wails"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// App — оболочка над ядром internal/app. Нужна, чтобы Wails по-прежнему видел привязываемый
// тип в пакете main и генерировал биндинги в frontend/wailsjs/go/main/App (методы ядра
// продвигаются через встраивание). Когда фронт перейдёт на platform/api.ts (ADR-0010),
// оболочку можно убрать.
type App struct {
	*app.App
}

func main() {
	wailsplatform.HideChildConsole() // без мигающих окон cmd от kubectl oidc-login на Windows
	platform := &wailsplatform.Platform{}
	a := &App{app.NewApp(platform)}

	err := wails.Run(&options.App{
		Title:     "Kubron",
		Width:     1400,
		Height:    900,
		MinWidth:  1000,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 46, A: 1},
		OnStartup: func(ctx context.Context) {
			platform.SetContext(ctx)
			app.OnStartup(a.App)(ctx)
		},
		OnShutdown: app.OnShutdown(a.App),
		Bind: []interface{}{
			a,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
