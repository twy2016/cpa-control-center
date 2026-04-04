//go:build windows

package main

import (
	_ "embed"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	appIconResourceID = 3
	iconTypeSmall     = 0
	iconTypeBig       = 1
	imageTypeIcon     = 1
	wmSetIcon         = 0x0080
	lrLoadFromFile    = 0x00000010
	lrDefaultSize     = 0x00000040
)

var (
	//go:embed build/windows/icon.ico
	embeddedAppIconICO []byte

	appIconOnce     sync.Once
	appIconMu       sync.Mutex
	appIconHandle   windows.Handle
	appIconErr      error
	appIconDestroy  bool
	appIconTempPath string

	procDestroyIcon  = trayUser32.NewProc("DestroyIcon")
	procFindWindowW  = trayUser32.NewProc("FindWindowW")
	procLoadImageW   = trayUser32.NewProc("LoadImageW")
	procSendMessageW = trayUser32.NewProc("SendMessageW")
)

func loadAppIconHandle() (windows.Handle, error) {
	appIconOnce.Do(func() {
		appIconHandle, appIconDestroy, appIconErr = initAppIconHandle()
	})
	if appIconHandle == 0 && appIconErr == nil {
		appIconErr = errors.New("应用图标句柄为空")
	}
	return appIconHandle, appIconErr
}

func initAppIconHandle() (windows.Handle, bool, error) {
	resourceIcon, err := loadIconFromAppResource()
	if err == nil && resourceIcon != 0 {
		return resourceIcon, false, nil
	}
	if len(embeddedAppIconICO) == 0 {
		if err != nil {
			return 0, false, err
		}
		return 0, false, errors.New("嵌入的应用图标为空")
	}

	icon, tempPath, loadErr := loadIconFromEmbeddedICO()
	if loadErr == nil && icon != 0 {
		appIconTempPath = tempPath
		return icon, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	return 0, false, loadErr
}

func loadIconFromAppResource() (windows.Handle, error) {
	instance, err := getModuleHandle()
	if err != nil {
		return 0, err
	}
	return loadIcon(instance, appIconResourceID)
}

func loadIconFromEmbeddedICO() (windows.Handle, string, error) {
	tempFile, err := os.CreateTemp("", "cpa-control-center-*.ico")
	if err != nil {
		return 0, "", err
	}

	tempPath := tempFile.Name()
	if _, err := tempFile.Write(embeddedAppIconICO); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return 0, "", err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return 0, "", err
	}

	icon, err := loadIconFromFile(tempPath)
	if err != nil {
		_ = os.Remove(tempPath)
		return 0, "", err
	}
	return icon, tempPath, nil
}

func loadIconFromFile(path string) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	ret, _, err := procLoadImageW.Call(
		0,
		uintptr(unsafe.Pointer(pathPtr)),
		imageTypeIcon,
		0,
		0,
		lrLoadFromFile|lrDefaultSize,
	)
	if ret == 0 {
		return 0, callError("LoadImageW", err)
	}
	return windows.Handle(ret), nil
}

func applyNativeWindowIcon(windowTitle string) error {
	icon, err := loadAppIconHandle()
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		hwnd, findErr := findWindowByTitle(windowTitle)
		if findErr == nil && hwnd != 0 {
			sendWindowIcon(hwnd, icon)
			return nil
		}
		lastErr = findErr
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("未找到主窗口句柄")
}

func findWindowByTitle(windowTitle string) (windows.Handle, error) {
	title, err := windows.UTF16PtrFromString(windowTitle)
	if err != nil {
		return 0, err
	}
	ret, _, callErr := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	if ret == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return 0, callError("FindWindowW", callErr)
		}
		return 0, errors.New("未找到主窗口")
	}
	return windows.Handle(ret), nil
}

func sendWindowIcon(hwnd windows.Handle, icon windows.Handle) {
	procSendMessageW.Call(uintptr(hwnd), wmSetIcon, iconTypeSmall, uintptr(icon))
	procSendMessageW.Call(uintptr(hwnd), wmSetIcon, iconTypeBig, uintptr(icon))
}

func releaseNativeAppIcon() {
	appIconMu.Lock()
	defer appIconMu.Unlock()

	if appIconDestroy && appIconHandle != 0 {
		procDestroyIcon.Call(uintptr(appIconHandle))
		appIconHandle = 0
		appIconDestroy = false
	}
	if appIconTempPath != "" {
		_ = os.Remove(appIconTempPath)
		appIconTempPath = ""
	}
}
