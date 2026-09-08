// Command generate-windows-icon creates the Windows ICO used by the Wails
// executable, installer, and system tray from the single source PNG.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/leaanthony/winicon"
)

func main() {
	input := flag.String("input", "build/appicon.png", "source PNG")
	output := flag.String("output", "build/windows/icon.ico", "destination ICO")
	flag.Parse()

	in, err := os.Open(*input)
	if err != nil {
		fatal("open source icon", err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal("create icon directory", err)
	}

	// Write atomically so a failed conversion never leaves a truncated ICO in
	// the repository or in a concurrent build.
	tmp, err := os.CreateTemp(filepath.Dir(*output), ".icon-*.ico")
	if err != nil {
		fatal("create temporary icon", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := winicon.GenerateIcon(in, tmp, []int{256, 128, 64, 48, 32, 16}); err != nil {
		tmp.Close()
		fatal("generate Windows icon", err)
	}
	if err := tmp.Close(); err != nil {
		fatal("close temporary icon", err)
	}
	if err := os.Rename(tmpName, *output); err != nil {
		fatal("replace Windows icon", err)
	}

	if info, err := os.Stat(*output); err == nil {
		fmt.Printf("Generated %s (%d bytes)\n", *output, info.Size())
	}
}

func fatal(action string, err error) {
	_, _ = io.WriteString(os.Stderr, fmt.Sprintf("%s: %v\n", action, err))
	os.Exit(1)
}
