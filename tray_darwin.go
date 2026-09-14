//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

void startDarwinTray(const void *iconBytes, int iconLength);
void stopDarwinTray(void);
void hideDarwinApplicationFromDock(void);
void showDarwinApplicationInDock(void);
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

// platformWindowHidden switches the app to accessory mode after its main
// window closes. The status item remains available, but macOS removes the
// running application from the Dock and therefore removes its indicator dot.
func platformWindowHidden() {
	C.hideDarwinApplicationFromDock()
}

// platformWindowShown restores normal Dock participation before Wails brings
// the main window to the foreground.
func platformWindowShown() {
	C.showDarwinApplicationInDock()
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
