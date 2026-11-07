package engine

// Re-exports for archive scanning options, so the UI packages reach
// internal/scan only through engine.

import "github.com/TTox-Tech/chitragit/internal/scan"

type (
	ScanOptions = scan.Options
)
