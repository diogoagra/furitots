//go:build windows

package hotkey

import (
	"fmt"
	"syscall"
	"time"
)

// Available indica se há atalhos globais nesta plataforma.
func Available() bool { return true }

// No Windows os atalhos são acompanhados por consulta ao estado das teclas
// (ver poll.go), e não com RegisterHotKey, que não avisa quando a tecla é
// solta — e o apertar-para-falar depende disso. Ao contrário do macOS, a
// tecla continua chegando ao programa em foco.
type nativeState struct {
	poll *poller
}

// pollInterval é o intervalo entre as consultas ao teclado: curto o bastante
// para o apertar-para-falar não cortar o começo nem esticar o fim da fala.
const pollInterval = 10 * time.Millisecond

// user32.dll é uma DLL conhecida do sistema: é sempre carregada de System32.
var procGetAsyncKeyState = syscall.NewLazyDLL("user32.dll").NewProc("GetAsyncKeyState")

// keyDown diz se a tecla virtual está apertada agora, com o app em foco ou não.
func keyDown(vk uint32) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return uint16(r)&0x8000 != 0 // devolve um SHORT; o bit mais alto é "apertada"
}

func (m *Manager) registerLocked(action, combo string) error {
	if m.native.poll == nil {
		if combo == "" {
			return nil
		}
		if err := procGetAsyncKeyState.Find(); err != nil {
			return fmt.Errorf("o sistema não permite acompanhar o teclado: %v", err)
		}
		m.native.poll = newPoller(keyDown, m.dispatch, pollInterval)
	}
	return m.native.poll.register(action, combo)
}
