//go:build !windows

package main

import "context"

type noopTrayController struct{}

func newPlatformTrayController(context.Context) trayController { return &noopTrayController{} }
func (*noopTrayController) Start(func(), func()) error         { return nil }
func (*noopTrayController) Stop()                              {}
