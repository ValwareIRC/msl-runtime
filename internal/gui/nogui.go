//go:build nogui

// Package gui is empty in builds made with -tags nogui; the runtime then
// keeps dialogs and windows in memory only.
package gui

import (
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/ui"
)

// Available reports whether this build includes the desktop GUI.
const Available = false

// New returns the headless frontend.
func New(*interp.Runtime, *ui.UI) ui.Frontend { return ui.Headless{} }

// Main does nothing without a GUI.
func Main() { select {} }

// Status is the status window; without a GUI it does nothing.
type Status struct{}

// OpenStatus returns a status window that discards output.
func OpenStatus(string, func(string), func()) *Status { return &Status{} }

// Add discards a line.
func (*Status) Add(string, string, bool) {}
