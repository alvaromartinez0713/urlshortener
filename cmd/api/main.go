// Command api is the minimal entry point of the process (S14): it
// constructs nothing and decides nothing — all bootstrap and wiring lives in
// internal/app. It just runs the app and exits non-zero on error.
package main

import (
	"log/slog"
	"os"

	"urlshortener/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		// app.Run's first step installs the JSON logger as slog default
		// (before config errors are even possible), so this fatal line
		// is already structured per ADR-003.
		slog.Error("api: fatal", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
