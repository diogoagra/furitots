//go:build darwin

package screencap

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics -framework CoreImage -framework CoreMedia -framework CoreVideo -framework ImageIO -framework ScreenCaptureKit
#include <stdlib.h>
#include "screencap_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Available indica se este sistema tem captura de tela nativa.
func Available() bool { return C.furito_screen_supported() != 0 }

// HasAccess indica se o sistema já liberou a gravação de tela para o app. Não
// mostra aviso nenhum.
func HasAccess() bool { return C.furito_screen_has_access() != 0 }

// Displays lista as telas que podem ser capturadas.
func Displays() ([]Display, error) {
	if !Available() {
		return nil, ErrUnavailable
	}
	var raw [16]C.furito_display
	n := int(C.furito_screen_displays(&raw[0], 16))
	out := make([]Display, 0, n)
	for i := 0; i < n; i++ {
		d := Display{ID: uint32(raw[i].id), Width: int(raw[i].width), Height: int(raw[i].height), Main: raw[i].main != 0}
		if d.Main {
			d.Name = fmt.Sprintf("Tela principal (%d×%d)", d.Width, d.Height)
		} else {
			d.Name = fmt.Sprintf("Tela %d (%d×%d)", i+1, d.Width, d.Height)
		}
		out = append(out, d)
	}
	return out, nil
}

// Start começa a capturar uma tela (0 = a principal), reduzida para caber em
// maxWidth×maxHeight. Na primeira vez o macOS pede a permissão de gravação de
// tela; enquanto ela não for dada, devolve ErrPermission.
func Start(displayID uint32, maxWidth, maxHeight, fps int) error {
	if !Available() {
		return ErrUnavailable
	}
	if !HasAccess() {
		// Mostra o aviso do sistema (só na primeira vez) e põe o app na lista
		// de Gravação de Tela dos Ajustes do Sistema.
		if C.furito_screen_request_access() == 0 {
			return ErrPermission
		}
	}
	displays, _ := Displays()
	srcW, srcH := 1920, 1080
	for _, d := range displays {
		if (displayID == 0 && d.Main) || d.ID == displayID {
			srcW, srcH = d.Width, d.Height
		}
	}
	w, h := fit(srcW, srcH, maxWidth, maxHeight)
	if fps < 1 || fps > 60 {
		fps = 15
	}

	Stop()
	gen := st.begin(func() { C.furito_screen_stop() })
	var msg [256]C.char
	rc := C.furito_screen_start(C.uint32_t(displayID), C.int(w), C.int(h), C.int(fps), C.uint64_t(gen), &msg[0], C.int(len(msg)))
	if rc != 0 {
		st.end(gen)
		if rc == 2 && !HasAccess() {
			return ErrPermission
		}
		return errors.New("não foi possível capturar a tela: " + C.GoString(&msg[0]))
	}
	return nil
}

//export furitoScreenFrame
func furitoScreenFrame(gen C.uint64_t, data unsafe.Pointer, length, width, height C.int) {
	st.push(uint64(gen), C.GoBytes(data, length), int(width), int(height))
}

//export furitoScreenEnded
func furitoScreenEnded(gen C.uint64_t, reason *C.char) {
	st.end(uint64(gen))
}
