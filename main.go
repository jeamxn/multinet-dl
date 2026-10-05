// MultiNet Downloader desktop app. It is a thin UI over the mndl CLI: every
// action runs the CLI (shipped next to this executable) and reads its JSON.
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

var version = "dev"

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:            "MultiNet Downloader",
		Width:            1120,
		Height:           760,
		MinWidth:         880,
		MinHeight:        580,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 16, G: 17, B: 20, A: 255},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "dev.jeamxn.multinet-dl",
			OnSecondInstanceLaunch: app.secondInstance,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: false,
			About:                &mac.AboutInfo{Title: "MultiNet Downloader", Message: "네트워크 여러 개로 파일 하나를 나눠 받기"},
		},
		Windows: &windows.Options{Theme: windows.SystemDefault},
		Linux:   &linux.Options{ProgramName: "multinet-dl"},
	})
	if err != nil {
		log.Fatal(err)
	}
}
