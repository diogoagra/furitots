//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// showFatal mostra uma caixa de mensagem de erro do Windows. O app é um
// programa de janela, sem console: é a única forma de o usuário ver por que
// ele não abriu.
func showFatal(title, text string) {
	t, err1 := syscall.UTF16PtrFromString(title)
	m, err2 := syscall.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return
	}
	const mbIconError = 0x10
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError)
}
