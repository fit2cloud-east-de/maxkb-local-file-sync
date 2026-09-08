//go:build windows

package main

import (
	"context"
	_ "embed"
	"sync"

	"github.com/getlantern/systray"
)

// Keep the tray icon sourced from the same generated ICO as the Windows
// executable and installer. The ICO itself is regenerated from
// build/appicon.png by scripts/build-windows.ps1 before a release build.
//
//go:embed build/windows/icon.ico
var trayIcon []byte

type windowsTrayController struct {
	ctx      context.Context
	show     func()
	exit     func()
	stopOnce sync.Once
}

func newPlatformTrayController(ctx context.Context) trayController {
	return &windowsTrayController{ctx: ctx}
}

func (t *windowsTrayController) Start(show func(), exit func()) error {
	t.show, t.exit = show, exit
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("MaxKB 本地文件同步工具")
		showItem := systray.AddMenuItem("显示应用", "显示主窗口")
		exitItem := systray.AddMenuItem("退出应用", "退出并停止后台同步")
		go func() {
			for {
				select {
				case <-showItem.ClickedCh:
					t.show()
				case <-exitItem.ClickedCh:
					t.exit()
					return
				case <-t.ctx.Done():
					return
				}
			}
		}()
	}, func() {})
	return nil
}

func (t *windowsTrayController) Stop() {
	t.stopOnce.Do(func() { systray.Quit() })
}
