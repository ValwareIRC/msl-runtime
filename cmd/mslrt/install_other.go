//go:build !windows

package main

import "errors"

func install() error {
	return errors.New("install registers the .mrc file type on Windows; on this system, copy mslrt onto your PATH and associate .mrc files with it in your desktop's file settings")
}

func uninstall() error { return errors.New("uninstall is only needed on Windows") }
