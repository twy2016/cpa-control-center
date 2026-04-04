//go:build windows

package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	launcherRunRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	launcherRunValueName    = "CPAControlCenter"
	launcherStillActiveCode = 259
)

func configureLauncherCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

func setLauncherWindowsStartup(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, launcherRunRegistryPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开开机启动注册表失败: %w", err)
	}
	defer key.Close()

	if !enabled {
		if err := key.DeleteValue(launcherRunValueName); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("删除开机启动项失败: %w", err)
		}
		return nil
	}

	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("解析当前程序路径失败: %w", err)
	}

	if err := key.SetStringValue(launcherRunValueName, fmt.Sprintf(`"%s"`, executablePath)); err != nil {
		return fmt.Errorf("写入开机启动项失败: %w", err)
	}
	return nil
}

func findLauncherProcessPID(executablePath string, configPath string) (int, error) {
	targetConfigPath := normalizeLauncherArgumentPath(configPath, "")
	if targetConfigPath == "" {
		return 0, nil
	}

	return findLauncherProcessPIDWithMatcher(executablePath, func(pid uint32) bool {
		commandLine, workingDirectory, err := queryLauncherProcessCommandLine(pid)
		if err != nil {
			return false
		}
		return launcherProcessMatchesConfig(commandLine, workingDirectory, targetConfigPath)
	})
}

func findLauncherProcessPIDByExecutablePath(executablePath string) (int, error) {
	return findLauncherProcessPIDWithMatcher(executablePath, func(pid uint32) bool {
		return true
	})
}

func findLauncherProcessPIDWithMatcher(executablePath string, matcher func(pid uint32) bool) (int, error) {
	targetPath, err := filepath.Abs(strings.TrimSpace(executablePath))
	if err != nil {
		return 0, err
	}
	targetPath = normalizeLauncherProcessPath(targetPath)
	if targetPath == "" {
		return 0, nil
	}

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("枚举进程失败: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		if err == windows.ERROR_NO_MORE_FILES {
			return 0, nil
		}
		return 0, fmt.Errorf("读取进程快照失败: %w", err)
	}

	for {
		processPath, queryErr := queryLauncherProcessPath(entry.ProcessID)
		if queryErr == nil && normalizeLauncherProcessPath(processPath) == targetPath && matcher(entry.ProcessID) {
			return int(entry.ProcessID), nil
		}

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return 0, fmt.Errorf("遍历进程快照失败: %w", err)
		}
	}

	return 0, nil
}

func killProcessTreeByPID(pid int) error {
	if pid <= 0 {
		return nil
	}

	killCmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	if err := killCmd.Run(); err != nil {
		return err
	}
	return nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == launcherStillActiveCode
}

func queryLauncherProcessPath(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, windows.MAX_PATH)
	for {
		size := uint32(len(buffer))
		err = windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size)
		if err == nil {
			return windows.UTF16ToString(buffer[:size]), nil
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER {
			return "", err
		}
		buffer = make([]uint16, len(buffer)*2)
	}
}

func queryLauncherProcessCommandLine(pid uint32) (string, string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return "", "", err
	}
	defer windows.CloseHandle(handle)

	var processInfo windows.PROCESS_BASIC_INFORMATION
	infoSize := uint32(unsafe.Sizeof(processInfo))
	if err := windows.NtQueryInformationProcess(
		handle,
		windows.ProcessBasicInformation,
		unsafe.Pointer(&processInfo),
		infoSize,
		&infoSize,
	); err != nil {
		return "", "", err
	}
	if processInfo.PebBaseAddress == nil {
		return "", "", fmt.Errorf("process %d has no PEB", pid)
	}

	var peb windows.PEB
	if err := readLauncherProcessStruct(handle, uintptr(unsafe.Pointer(processInfo.PebBaseAddress)), &peb); err != nil {
		return "", "", err
	}
	if peb.ProcessParameters == nil {
		return "", "", fmt.Errorf("process %d has no process parameters", pid)
	}

	var parameters windows.RTL_USER_PROCESS_PARAMETERS
	if err := readLauncherProcessStruct(handle, uintptr(unsafe.Pointer(peb.ProcessParameters)), &parameters); err != nil {
		return "", "", err
	}

	commandLine, err := readLauncherProcessUnicodeString(handle, parameters.CommandLine)
	if err != nil {
		return "", "", err
	}
	workingDirectory, err := readLauncherProcessUnicodeString(handle, parameters.CurrentDirectory.DosPath)
	if err != nil {
		return "", "", err
	}
	return commandLine, workingDirectory, nil
}

func readLauncherProcessStruct[T any](handle windows.Handle, address uintptr, out *T) error {
	var bytesRead uintptr
	size := unsafe.Sizeof(*out)
	if err := windows.ReadProcessMemory(handle, address, (*byte)(unsafe.Pointer(out)), size, &bytesRead); err != nil {
		return err
	}
	if bytesRead != size {
		return fmt.Errorf("short read from process memory: got %d, want %d", bytesRead, size)
	}
	return nil
}

func readLauncherProcessUnicodeString(handle windows.Handle, value windows.NTUnicodeString) (string, error) {
	if value.Length == 0 || value.Buffer == nil {
		return "", nil
	}

	buffer := make([]uint16, int(value.Length)/2)
	var bytesRead uintptr
	if err := windows.ReadProcessMemory(
		handle,
		uintptr(unsafe.Pointer(value.Buffer)),
		(*byte)(unsafe.Pointer(&buffer[0])),
		uintptr(value.Length),
		&bytesRead,
	); err != nil {
		return "", err
	}
	if bytesRead != uintptr(value.Length) {
		return "", fmt.Errorf("short read from process string buffer: got %d, want %d", bytesRead, value.Length)
	}
	return windows.UTF16ToString(buffer), nil
}

func launcherCommandLineArguments(commandLine string) ([]string, error) {
	return windows.DecomposeCommandLine(commandLine)
}

func normalizeLauncherProcessPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(path))
}
