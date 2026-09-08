package main

import "context"

// trayController hides the platform-specific system tray implementation from
// the Wails lifecycle. Start must return quickly; the controller owns its
// event loop until Stop is called.
type trayController interface {
	Start(show func(), exit func()) error
	Stop()
}

func newTrayController(ctx context.Context) trayController {
	return newPlatformTrayController(ctx)
}
