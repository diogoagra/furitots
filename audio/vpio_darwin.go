//go:build darwin

package audio

/*
#cgo LDFLAGS: -framework AudioToolbox -framework CoreAudio -framework AVFoundation -framework Foundation
#include "vpio_darwin.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"unsafe"
)

const vpAvailable = true

// vpUnit é a unidade de processamento de voz do macOS em execução.
type vpUnit struct {
	ptr    *C.FuritoVP
	handle cgo.Handle
}

func startVP(e *Engine) (*vpUnit, error) {
	h := cgo.NewHandle(e)
	var status C.int
	ptr := C.furito_vp_start(C.uintptr_t(h), &status)
	if ptr == nil {
		h.Delete()
		return nil, fmt.Errorf("o macOS recusou iniciar o processamento de voz (código %d)", int(status))
	}
	return &vpUnit{ptr: ptr, handle: h}, nil
}

func (v *vpUnit) stop() {
	C.furito_vp_stop(v.ptr) // só retorna depois do último callback
	v.handle.Delete()
}

//export furitoVPCapture
func furitoVPCapture(h C.uintptr_t, samples *C.int16_t, frames C.int) {
	e := cgo.Handle(h).Value().(*Engine)
	e.feedCapture(unsafe.Slice((*int16)(unsafe.Pointer(samples)), int(frames)))
}

//export furitoVPRender
func furitoVPRender(h C.uintptr_t, samples *C.int16_t, frames C.int) {
	e := cgo.Handle(h).Value().(*Engine)
	e.fillPlayback(unsafe.Slice((*int16)(unsafe.Pointer(samples)), int(frames)))
}

// MicrophoneMode devolve o "Modo do Microfone" ativo no macOS: "standard",
// "wide" (espectro amplo) ou "isolation" (isolamento de voz). Só tem efeito
// enquanto o processamento de voz do sistema está em uso.
func MicrophoneMode() string {
	switch int(C.furito_mic_mode()) {
	case 0:
		return "standard"
	case 1:
		return "wide"
	case 2:
		return "isolation"
	}
	return ""
}

// ShowMicrophoneModes abre o seletor de modo do microfone do sistema.
func ShowMicrophoneModes() { C.furito_show_mic_modes() }
