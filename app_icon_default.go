//go:build !windows

package main

func applyNativeWindowIcon(windowTitle string) error {
	return nil
}

func releaseNativeAppIcon() {}
