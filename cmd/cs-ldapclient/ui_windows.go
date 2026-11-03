//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func platformTitle() string {
	return tr("Windows-Anmeldung mit cs-team-Konten (pGina)", "Windows sign-in with cs-team accounts (pGina)")
}

func uiLang() string {
	k := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")
	r, _, _ := k.Call()
	if r&0x3ff == 0x07 { // LANG_GERMAN
		return "de"
	}
	return "en"
}

// askSecret reads a line without echo.
func askSecret(label string) string {
	fmt.Printf("%s: ", label)
	h := syscall.Handle(os.Stdin.Fd())
	get := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode")
	set := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")
	var mode uint32
	r, _, _ := get.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r != 0 {
		set.Call(uintptr(h), uintptr(mode&^0x4)) // ENABLE_ECHO_INPUT off
		defer set.Call(uintptr(h), uintptr(mode))
	}
	s := readLine()
	fmt.Println()
	return s
}

func isAdmin() bool {
	_, err := runCmd("reg", "query", `HKU\S-1-5-19`)
	return err == nil
}

// pause keeps the console window open when the program was started by a double click.
func pause(o options) {
	if o.yes {
		return
	}
	fmt.Print(tr("\nEingabetaste zum Beenden ... ", "\nPress Enter to exit ... "))
	readLine()
}
