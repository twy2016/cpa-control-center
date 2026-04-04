//go:build windows

package main

import (
	"fmt"
	"os"
	goruntime "runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	trayWindowMessage = 0x8000 + 1
	trayCloseMessage  = 0x8000 + 2
	trayIconID        = 100
	trayMenuShowID    = 1001
	trayMenuStartID   = 1002
	trayMenuStopID    = 1003
	trayMenuOpenID    = 1004
	trayMenuQuitID    = 1005

	csVRedraw = 0x0001
	csHRedraw = 0x0002

	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmContextMenu   = 0x007B
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	ninSelect       = 0x0400
	ninKeySelect    = 0x0401
	idiApplication  = 32512
	idcArrow        = 32512
	nimAdd          = 0x00000000
	nimDelete       = 0x00000002
	nimSetVersion   = 0x00000004
	nifMessage      = 0x00000001
	nifIcon         = 0x00000002
	nifTip          = 0x00000004
	mfString        = 0x00000000
	mfDisabled      = 0x00000002
	mfSeparator     = 0x00000800
	tpmLeftAlign    = 0x0000
	tpmRightButton  = 0x0002
	notifyIconV4    = 4
)

var (
	trayUser32   = windows.NewLazySystemDLL("user32.dll")
	trayKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	trayShell32  = windows.NewLazySystemDLL("shell32.dll")

	procAppendMenuW         = trayUser32.NewProc("AppendMenuW")
	procCreatePopupMenu     = trayUser32.NewProc("CreatePopupMenu")
	procCreateWindowExW     = trayUser32.NewProc("CreateWindowExW")
	procDefWindowProcW      = trayUser32.NewProc("DefWindowProcW")
	procDestroyMenu         = trayUser32.NewProc("DestroyMenu")
	procDestroyWindow       = trayUser32.NewProc("DestroyWindow")
	procDispatchMessageW    = trayUser32.NewProc("DispatchMessageW")
	procGetCursorPos        = trayUser32.NewProc("GetCursorPos")
	procGetMessageW         = trayUser32.NewProc("GetMessageW")
	procLoadCursorW         = trayUser32.NewProc("LoadCursorW")
	procLoadIconW           = trayUser32.NewProc("LoadIconW")
	procPostMessageW        = trayUser32.NewProc("PostMessageW")
	procPostQuitMessage     = trayUser32.NewProc("PostQuitMessage")
	procRegisterClassExW    = trayUser32.NewProc("RegisterClassExW")
	procSetForegroundWindow = trayUser32.NewProc("SetForegroundWindow")
	procTrackPopupMenu      = trayUser32.NewProc("TrackPopupMenu")
	procTranslateMessage    = trayUser32.NewProc("TranslateMessage")
	procUnregisterClassW    = trayUser32.NewProc("UnregisterClassW")
	procGetModuleHandleW    = trayKernel32.NewProc("GetModuleHandleW")
	procShellNotifyIconW    = trayShell32.NewProc("Shell_NotifyIconW")

	trayWndProc = syscall.NewCallback(trayWindowProc)
	trayWindows sync.Map
)

type winTrayController struct {
	actions trayActions

	done chan struct{}

	mu        sync.RWMutex
	labels    trayLabels
	hwnd      windows.Handle
	instance  windows.Handle
	className *uint16
	ready     bool
	iconAdded bool
	closed    bool
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type point struct {
	X int32
	Y int32
}

type winMsg struct {
	HWnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

func newTrayController(labels trayLabels, actions trayActions) (trayController, error) {
	controller := &winTrayController{
		actions: actions,
		done:    make(chan struct{}),
		labels:  labels,
	}
	if err := controller.start(); err != nil {
		return nil, err
	}
	return controller, nil
}

func (c *winTrayController) start() error {
	initCh := make(chan error, 1)
	go c.run(initCh)
	return <-initCh
}

func (c *winTrayController) run(initCh chan<- error) {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()
	defer close(c.done)

	instance, err := getModuleHandle()
	if err != nil {
		initCh <- err
		return
	}

	className, err := windows.UTF16PtrFromString(fmt.Sprintf("CPAControlCenterTray_%d", os.Getpid()))
	if err != nil {
		initCh <- err
		return
	}

	icon, err := loadIcon(0, idiApplication)
	if err != nil {
		initCh <- err
		return
	}
	cursor, err := loadCursor(0, idcArrow)
	if err != nil {
		initCh <- err
		return
	}

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		Style:         csHRedraw | csVRedraw,
		LpfnWndProc:   trayWndProc,
		HInstance:     instance,
		HIcon:         icon,
		HCursor:       cursor,
		LpszClassName: className,
		HIconSm:       icon,
	}
	if err := registerClassEx(&wc); err != nil {
		initCh <- err
		return
	}
	defer unregisterClass(className, instance)

	hwnd, err := createWindowEx(className, instance)
	if err != nil {
		initCh <- err
		return
	}
	trayWindows.Store(hwnd, c)
	defer trayWindows.Delete(hwnd)

	c.mu.Lock()
	c.hwnd = hwnd
	c.instance = instance
	c.className = className
	c.mu.Unlock()

	defer func() {
		c.removeNotifyIcon()
		c.mu.Lock()
		c.ready = false
		c.hwnd = 0
		c.mu.Unlock()
	}()

	if err := c.addNotifyIcon(); err != nil {
		_, _ = destroyWindow(hwnd)
		initCh <- err
		return
	}

	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	initCh <- nil

	var msg winMsg
	for {
		result, err := getMessage(&msg)
		switch {
		case result == -1:
			return
		case result == 0:
			return
		default:
			if err == nil {
				translateMessage(&msg)
				dispatchMessage(&msg)
			}
		}
	}
}

func (c *winTrayController) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

func (c *winTrayController) UpdateLabels(labels trayLabels) {
	c.mu.Lock()
	c.labels = labels
	c.mu.Unlock()
}

func (c *winTrayController) Close() error {
	c.mu.Lock()
	if c.closed {
		done := c.done
		c.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	c.closed = true
	hwnd := c.hwnd
	done := c.done
	c.mu.Unlock()

	if hwnd != 0 {
		_, _ = postMessage(hwnd, trayCloseMessage, 0, 0)
	}
	if done != nil {
		<-done
	}
	return nil
}

func (c *winTrayController) addNotifyIcon() error {
	c.mu.RLock()
	hwnd := c.hwnd
	labels := c.labels
	c.mu.RUnlock()

	icon, err := loadAppIconHandle()
	if err != nil || icon == 0 {
		icon, err = loadIcon(0, idiApplication)
		if err != nil {
			return err
		}
	}

	data := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             hwnd,
		UID:              trayIconID,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: trayWindowMessage,
		HIcon:            icon,
		UVersion:         notifyIconV4,
	}
	copyUTF16(data.SzTip[:], labels.Tooltip)

	if err := shellNotifyIcon(nimAdd, &data); err != nil {
		return err
	}
	_ = shellNotifyIcon(nimSetVersion, &data)

	c.mu.Lock()
	c.iconAdded = true
	c.mu.Unlock()
	return nil
}

func (c *winTrayController) removeNotifyIcon() {
	c.mu.Lock()
	if !c.iconAdded || c.hwnd == 0 {
		c.mu.Unlock()
		return
	}
	data := notifyIconData{
		CbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:   c.hwnd,
		UID:    trayIconID,
	}
	c.iconAdded = false
	c.mu.Unlock()

	_ = shellNotifyIcon(nimDelete, &data)
}

func (c *winTrayController) labelsSnapshot() trayLabels {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.labels
}

func (c *winTrayController) currentState() trayMenuState {
	if c.actions.CurrentState == nil {
		return trayMenuState{}
	}
	return c.actions.CurrentState()
}

func (c *winTrayController) invoke(action func()) {
	if action != nil {
		go action()
	}
}

func menuItemFlags(enabled bool) uintptr {
	flags := uintptr(mfString)
	if !enabled {
		flags |= mfDisabled
	}
	return flags
}

func (c *winTrayController) showContextMenu(hwnd windows.Handle) {
	menu, err := createPopupMenu()
	if err != nil {
		return
	}
	defer destroyMenu(menu)

	labels := c.labelsSnapshot()
	state := c.currentState()
	_, _ = appendMenu(menu, mfString, trayMenuShowID, labels.ShowLabel)
	_, _ = appendMenu(menu, mfSeparator, 0, "")
	_, _ = appendMenu(menu, menuItemFlags(state.CanStart), trayMenuStartID, labels.StartLabel)
	_, _ = appendMenu(menu, menuItemFlags(state.CanStop), trayMenuStopID, labels.StopLabel)
	_, _ = appendMenu(menu, menuItemFlags(state.CanOpenManagement), trayMenuOpenID, labels.OpenManagementLabel)
	_, _ = appendMenu(menu, mfSeparator, 0, "")
	_, _ = appendMenu(menu, mfString, trayMenuQuitID, labels.QuitLauncherLabel)

	x, y, err := getCursorPosition()
	if err != nil {
		return
	}
	_, _ = setForegroundWindow(hwnd)
	_, _ = trackPopupMenu(menu, tpmLeftAlign|tpmRightButton, x, y, hwnd)
	_, _ = postMessage(hwnd, wmNull, 0, 0)
}

func (c *winTrayController) handleMessage(hwnd windows.Handle, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case trayWindowMessage:
		switch notifyIconEvent(lParam) {
		case ninSelect, wmLButtonUp, wmLButtonDblClk:
			c.invoke(c.actions.Show)
		case ninKeySelect, wmRButtonUp, wmContextMenu:
			c.showContextMenu(hwnd)
		}
		return 0
	case wmCommand:
		switch uint16(wParam & 0xffff) {
		case trayMenuShowID:
			c.invoke(c.actions.Show)
		case trayMenuStartID:
			c.invoke(c.actions.Start)
		case trayMenuStopID:
			c.invoke(c.actions.Stop)
		case trayMenuOpenID:
			c.invoke(c.actions.OpenManagement)
		case trayMenuQuitID:
			c.invoke(c.actions.QuitLauncher)
		}
		return 0
	case trayCloseMessage:
		c.removeNotifyIcon()
		_, _ = destroyWindow(hwnd)
		return 0
	case wmDestroy:
		c.removeNotifyIcon()
		postQuitMessage(0)
		return 0
	default:
		result, _ := defWindowProc(hwnd, msg, wParam, lParam)
		return result
	}
}

func trayWindowProc(hwnd uintptr, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	if value, ok := trayWindows.Load(windows.Handle(hwnd)); ok {
		return value.(*winTrayController).handleMessage(windows.Handle(hwnd), msg, wParam, lParam)
	}
	result, _ := defWindowProc(windows.Handle(hwnd), msg, wParam, lParam)
	return result
}

func getModuleHandle() (windows.Handle, error) {
	ret, _, err := procGetModuleHandleW.Call(0)
	if ret == 0 {
		return 0, callError("GetModuleHandleW", err)
	}
	return windows.Handle(ret), nil
}

func registerClassEx(class *wndClassEx) error {
	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(class)))
	if ret == 0 {
		return callError("RegisterClassExW", err)
	}
	return nil
}

func unregisterClass(className *uint16, instance windows.Handle) {
	_, _, _ = procUnregisterClassW.Call(
		uintptr(unsafe.Pointer(className)),
		uintptr(instance),
	)
}

func createWindowEx(className *uint16, instance windows.Handle) (windows.Handle, error) {
	ret, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		uintptr(instance),
		0,
	)
	if ret == 0 {
		return 0, callError("CreateWindowExW", err)
	}
	return windows.Handle(ret), nil
}

func destroyWindow(hwnd windows.Handle) (uintptr, error) {
	ret, _, err := procDestroyWindow.Call(uintptr(hwnd))
	if ret == 0 {
		return ret, callError("DestroyWindow", err)
	}
	return ret, nil
}

func defWindowProc(hwnd windows.Handle, msg uint32, wParam uintptr, lParam uintptr) (uintptr, error) {
	ret, _, err := procDefWindowProcW.Call(
		uintptr(hwnd),
		uintptr(msg),
		wParam,
		lParam,
	)
	if err != nil && err != syscall.Errno(0) {
		return ret, callError("DefWindowProcW", err)
	}
	return ret, nil
}

func loadCursor(instance windows.Handle, resource uintptr) (windows.Handle, error) {
	ret, _, err := procLoadCursorW.Call(uintptr(instance), resource)
	if ret == 0 {
		return 0, callError("LoadCursorW", err)
	}
	return windows.Handle(ret), nil
}

func loadIcon(instance windows.Handle, resource uintptr) (windows.Handle, error) {
	ret, _, err := procLoadIconW.Call(uintptr(instance), resource)
	if ret == 0 {
		return 0, callError("LoadIconW", err)
	}
	return windows.Handle(ret), nil
}

func shellNotifyIcon(command uintptr, data *notifyIconData) error {
	ret, _, err := procShellNotifyIconW.Call(command, uintptr(unsafe.Pointer(data)))
	if ret == 0 {
		return callError("Shell_NotifyIconW", err)
	}
	return nil
}

func createPopupMenu() (windows.Handle, error) {
	ret, _, err := procCreatePopupMenu.Call()
	if ret == 0 {
		return 0, callError("CreatePopupMenu", err)
	}
	return windows.Handle(ret), nil
}

func appendMenu(menu windows.Handle, flags uintptr, itemID uintptr, text string) (uintptr, error) {
	var textPtr uintptr
	if text != "" {
		ptr, err := windows.UTF16PtrFromString(text)
		if err != nil {
			return 0, err
		}
		textPtr = uintptr(unsafe.Pointer(ptr))
	}
	ret, _, err := procAppendMenuW.Call(
		uintptr(menu),
		flags,
		itemID,
		textPtr,
	)
	if ret == 0 {
		return ret, callError("AppendMenuW", err)
	}
	return ret, nil
}

func destroyMenu(menu windows.Handle) {
	_, _, _ = procDestroyMenu.Call(uintptr(menu))
}

func getCursorPosition() (int, int, error) {
	var pt point
	ret, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	if ret == 0 {
		return 0, 0, callError("GetCursorPos", err)
	}
	return int(pt.X), int(pt.Y), nil
}

func setForegroundWindow(hwnd windows.Handle) (uintptr, error) {
	ret, _, err := procSetForegroundWindow.Call(uintptr(hwnd))
	if ret == 0 {
		return ret, callError("SetForegroundWindow", err)
	}
	return ret, nil
}

func trackPopupMenu(menu windows.Handle, flags uint32, x int, y int, hwnd windows.Handle) (uintptr, error) {
	ret, _, err := procTrackPopupMenu.Call(
		uintptr(menu),
		uintptr(flags),
		uintptr(x),
		uintptr(y),
		0,
		uintptr(hwnd),
		0,
	)
	if ret == 0 {
		return ret, callError("TrackPopupMenu", err)
	}
	return ret, nil
}

func postMessage(hwnd windows.Handle, msg uint32, wParam uintptr, lParam uintptr) (uintptr, error) {
	ret, _, err := procPostMessageW.Call(
		uintptr(hwnd),
		uintptr(msg),
		wParam,
		lParam,
	)
	if ret == 0 {
		return ret, callError("PostMessageW", err)
	}
	return ret, nil
}

func postQuitMessage(exitCode int32) {
	procPostQuitMessage.Call(uintptr(exitCode))
}

func getMessage(msg *winMsg) (int32, error) {
	ret, _, err := procGetMessageW.Call(
		uintptr(unsafe.Pointer(msg)),
		0,
		0,
		0,
	)
	if int32(ret) == -1 {
		return -1, callError("GetMessageW", err)
	}
	return int32(ret), nil
}

func translateMessage(msg *winMsg) {
	procTranslateMessage.Call(uintptr(unsafe.Pointer(msg)))
}

func dispatchMessage(msg *winMsg) {
	procDispatchMessageW.Call(uintptr(unsafe.Pointer(msg)))
}

func copyUTF16(dst []uint16, value string) {
	if len(dst) == 0 {
		return
	}
	encoded, err := windows.UTF16FromString(value)
	if err != nil {
		return
	}
	copy(dst, encoded)
	dst[len(dst)-1] = 0
}

func notifyIconEvent(lParam uintptr) uint32 {
	return uint32(lParam & 0xffff)
}

func callError(name string, err error) error {
	if err != nil && err != syscall.Errno(0) {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Errorf("%s failed", name)
}
