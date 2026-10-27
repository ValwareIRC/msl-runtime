//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// The installation is per user and needs no administrator rights: programs
// go to %LOCALAPPDATA%\Programs\mslrt, settings to %APPDATA%\mslrt, and the
// .mrc file type is registered under HKEY_CURRENT_USER.
const (
	progID      = "mslrt.script"
	guiExe      = "mslrtw.exe"
	consoleExe  = "mslrt.exe"
	classesRoot = `Software\Classes`
)

func installDir() string  { return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "mslrt") }
func userDataDir() string { return filepath.Join(os.Getenv("APPDATA"), "mslrt") }

func install() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	src := filepath.Dir(self)
	if _, err := os.Stat(filepath.Join(src, guiExe)); err != nil {
		return fmt.Errorf("%s is missing next to %s; run install.ps1, which builds both programs", guiExe, consoleExe)
	}
	dst := installDir()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		for _, name := range []string{consoleExe, guiExe} {
			if err := copyFile(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
				return fmt.Errorf("copying %s: %w (close any running mslrt first)", name, err)
			}
		}
	}
	icon, err := iconICO()
	if err != nil {
		return err
	}
	iconPath := filepath.Join(dst, "mslrt.ico")
	if err := os.WriteFile(iconPath, icon, 0o644); err != nil {
		return err
	}
	data := userDataDir()
	if err := os.MkdirAll(data, 0o755); err != nil {
		return err
	}

	run := fmt.Sprintf(`"%s" -status -data "%s" -log "%s" "%%1"`,
		filepath.Join(dst, guiExe), data, filepath.Join(data, "mslrt.log"))
	keys := []struct{ path, name, value string }{
		{classesRoot + `\.mrc`, "", progID},
		{classesRoot + `\.mrc`, "Content Type", "text/plain"},
		{classesRoot + `\.mrc`, "PerceivedType", "text"},
		{classesRoot + `\.mrc\OpenWithProgids`, progID, ""},
		{classesRoot + `\` + progID, "", "mIRC script"},
		{classesRoot + `\` + progID, "FriendlyTypeName", "mIRC script"},
		{classesRoot + `\` + progID + `\DefaultIcon`, "", iconPath},
		{classesRoot + `\` + progID + `\shell`, "", "open"},
		{classesRoot + `\` + progID + `\shell\open`, "", "Run with mslrt"},
		{classesRoot + `\` + progID + `\shell\open\command`, "", run},
		{classesRoot + `\` + progID + `\shell\edit`, "", "Edit"},
		{classesRoot + `\` + progID + `\shell\edit\command`, "", `notepad.exe "%1"`},
		{classesRoot + `\Applications\` + guiExe, "FriendlyAppName", "mslrt"},
		{classesRoot + `\Applications\` + guiExe + `\SupportedTypes`, ".mrc", ""},
		{classesRoot + `\Applications\` + guiExe + `\shell\open\command`, "", run},
	}
	for _, k := range keys {
		if err := setString(k.path, k.name, k.value); err != nil {
			return fmt.Errorf("registering .mrc: %w", err)
		}
	}
	added, err := addToPath(dst)
	if err != nil {
		return err
	}
	notifyShell(added)

	fmt.Println("Installed mslrt to", dst)
	fmt.Println("Scripts opened by double-click keep their settings in", data)
	if added {
		fmt.Println("Added it to your PATH; open a new terminal to run `mslrt` from anywhere.")
	}
	if choice := userChoice(); choice != "" && choice != progID {
		fmt.Printf("\nWindows is set to open .mrc files with another app (%s).\n", choice)
		fmt.Println("To switch: right-click a .mrc file > Open with > Choose another app >")
		fmt.Println("pick \"mslrt\" and tick \"Always use this app\".")
	} else {
		fmt.Println("Double-click any .mrc file to run it.")
	}
	return nil
}

func uninstall() error {
	if v, err := getString(classesRoot+`\.mrc`, ""); err == nil && v == progID {
		deleteTree(classesRoot + `\.mrc`)
	} else {
		if k, err := registry.OpenKey(registry.CURRENT_USER, classesRoot+`\.mrc\OpenWithProgids`, registry.SET_VALUE); err == nil {
			k.DeleteValue(progID)
			k.Close()
		}
	}
	deleteTree(classesRoot + `\` + progID)
	deleteTree(classesRoot + `\Applications\` + guiExe)
	removed, err := removeFromPath(installDir())
	if err != nil {
		return err
	}
	notifyShell(removed)

	self, _ := os.Executable()
	dst := installDir()
	inside := strings.EqualFold(filepath.Dir(self), filepath.Clean(dst))
	for _, name := range []string{guiExe, consoleExe, "mslrt.ico"} {
		p := filepath.Join(dst, name)
		if inside && strings.EqualFold(p, self) {
			continue
		}
		os.Remove(p)
	}
	fmt.Println("Removed the .mrc file type and the PATH entry.")
	if inside {
		fmt.Println("Delete", dst, "to remove the last file (this program is still running from it).")
	} else {
		os.Remove(dst)
	}
	fmt.Println("Your settings are kept in", userDataDir())
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func setString(path, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func getString(path, name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	return v, err
}

// deleteTree removes a key and its subkeys under HKEY_CURRENT_USER.
func deleteTree(path string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if err == nil {
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			deleteTree(path + `\` + n)
		}
	}
	registry.DeleteKey(registry.CURRENT_USER, path)
}

// userChoice returns the app Windows was told to always use for .mrc, if any.
func userChoice() string {
	v, _ := getString(`Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\.mrc\UserChoice`, "ProgId")
	return v
}

// pathEntries reads the user's PATH.
func pathEntries() (registry.Key, []string, uint32, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return 0, nil, 0, err
	}
	v, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return k, nil, registry.EXPAND_SZ, nil
	}
	if err != nil {
		k.Close()
		return 0, nil, 0, err
	}
	var parts []string
	for _, p := range strings.Split(v, ";") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return k, parts, typ, nil
}

func writePath(k registry.Key, parts []string, typ uint32) error {
	v := strings.Join(parts, ";")
	if typ == registry.SZ {
		return k.SetStringValue("Path", v)
	}
	return k.SetExpandStringValue("Path", v)
}

func addToPath(dir string) (bool, error) {
	k, parts, typ, err := pathEntries()
	if err != nil {
		return false, err
	}
	defer k.Close()
	for _, p := range parts {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			return false, nil
		}
	}
	return true, writePath(k, append(parts, dir), typ)
}

func removeFromPath(dir string) (bool, error) {
	k, parts, typ, err := pathEntries()
	if err != nil {
		return false, err
	}
	defer k.Close()
	kept := parts[:0]
	for _, p := range parts {
		if !strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(parts) {
		return false, nil
	}
	return true, writePath(k, kept, typ)
}

// notifyShell tells Explorer the file types changed (so icons refresh) and,
// when PATH changed, tells running programs to reload the environment.
func notifyShell(envChanged bool) {
	const shcneAssocChanged = 0x08000000
	syscall.NewLazyDLL("shell32.dll").NewProc("SHChangeNotify").Call(shcneAssocChanged, 0, 0, 0)
	if !envChanged {
		return
	}
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	env, _ := syscall.UTF16PtrFromString("Environment")
	var result uintptr
	syscall.NewLazyDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(
		hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
