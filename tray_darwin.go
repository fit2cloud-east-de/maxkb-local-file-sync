//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

void startDarwinTray(const void *iconBytes, int iconLength);
void stopDarwinTray(void);
*/
import "C"

import (
	"context"
	_ "embed"
	"errors"
	"sync"
	"unsafe"
)

//go:embed build/appicon.png
var darwinTrayIcon []byte

var (
	darwinTrayMu     sync.RWMutex
	activeDarwinTray *darwinTrayController
)

type darwinTrayController struct {
	show     func()
	exit     func()
	stopOnce sync.Once
}

func newPlatformTrayController(context.Context) trayController {
	return &darwinTrayController{}
}

func (t *darwinTrayController) Start(show func(), exit func()) error {
	if len(darwinTrayIcon) == 0 {
		return errors.New("macOS menu bar icon is empty")
	}
	t.show, t.exit = show, exit
	darwinTrayMu.Lock()
	activeDarwinTray = t
	darwinTrayMu.Unlock()
	C.startDarwinTray(unsafe.Pointer(&darwinTrayIcon[0]), C.int(len(darwinTrayIcon)))
	return nil
}

func (t *darwinTrayController) Stop() {
	t.stopOnce.Do(func() {
		darwinTrayMu.Lock()
		if activeDarwinTray == t {
			activeDarwinTray = nil
		}
		darwinTrayMu.Unlock()
		C.stopDarwinTray()
	})
}

//export darwinTrayShowMainWindow
func darwinTrayShowMainWindow() {
	darwinTrayMu.RLock()
	t := activeDarwinTray
	darwinTrayMu.RUnlock()
	if t != nil && t.show != nil {
		go t.show()
	}
}

//export darwinTrayExitApplication
func darwinTrayExitApplication() {
	darwinTrayMu.RLock()
	t := activeDarwinTray
	darwinTrayMu.RUnlock()
	if t != nil && t.exit != nil {
		go t.exit()
	}
}
