// Package hotkey registra atalhos de teclado que funcionam com o app em
// segundo plano (apertar para falar, mutar). No macOS usa os atalhos globais
// do sistema, que não exigem permissão de Acessibilidade. No Windows consulta
// o estado das teclas, para saber também quando elas são soltas.
package hotkey

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Combo é uma combinação de teclas: modificadores + uma tecla, identificada
// pelo KeyboardEvent.code do navegador (ex.: "Cmd+Shift+KeyM", "F13").
type Combo struct {
	Ctrl, Alt, Shift, Cmd bool
	Code                  string
}

// keyCodes: KeyboardEvent.code → código de tecla virtual do macOS.
var keyCodes = map[string]uint32{
	"KeyA": 0, "KeyS": 1, "KeyD": 2, "KeyF": 3, "KeyH": 4, "KeyG": 5, "KeyZ": 6, "KeyX": 7, "KeyC": 8, "KeyV": 9,
	"IntlBackslash": 10, "KeyB": 11, "KeyQ": 12, "KeyW": 13, "KeyE": 14, "KeyR": 15, "KeyY": 16, "KeyT": 17,
	"Digit1": 18, "Digit2": 19, "Digit3": 20, "Digit4": 21, "Digit6": 22, "Digit5": 23, "Equal": 24, "Digit9": 25,
	"Digit7": 26, "Minus": 27, "Digit8": 28, "Digit0": 29, "BracketRight": 30, "KeyO": 31, "KeyU": 32,
	"BracketLeft": 33, "KeyI": 34, "KeyP": 35, "Enter": 36, "KeyL": 37, "KeyJ": 38, "Quote": 39, "KeyK": 40,
	"Semicolon": 41, "Backslash": 42, "Comma": 43, "Slash": 44, "KeyN": 45, "KeyM": 46, "Period": 47,
	"Tab": 48, "Space": 49, "Backquote": 50, "Backspace": 51, "Escape": 53, "CapsLock": 57,
	"F17": 64, "NumpadDecimal": 65, "NumpadMultiply": 67, "NumpadAdd": 69, "NumpadDivide": 75, "NumpadEnter": 76,
	"NumpadSubtract": 78, "F18": 79, "F19": 80, "Numpad0": 82, "Numpad1": 83, "Numpad2": 84, "Numpad3": 85,
	"Numpad4": 86, "Numpad5": 87, "Numpad6": 88, "Numpad7": 89, "F20": 90, "Numpad8": 91, "Numpad9": 92,
	"F5": 96, "F6": 97, "F7": 98, "F3": 99, "F8": 100, "F9": 101, "F11": 103, "F13": 105, "F16": 106, "F14": 107,
	"F10": 109, "F12": 111, "F15": 113, "Home": 115, "PageUp": 116, "Delete": 117, "F4": 118, "End": 119,
	"F2": 120, "PageDown": 121, "F1": 122, "ArrowLeft": 123, "ArrowRight": 124, "ArrowDown": 125, "ArrowUp": 126,
}

// Parse lê uma combinação no formato "Ctrl+Alt+Shift+Cmd+<code>".
func Parse(s string) (Combo, error) {
	var c Combo
	parts := strings.Split(strings.TrimSpace(s), "+")
	for i, p := range parts {
		last := i == len(parts)-1
		switch {
		case !last && p == "Ctrl":
			c.Ctrl = true
		case !last && p == "Alt":
			c.Alt = true
		case !last && p == "Shift":
			c.Shift = true
		case !last && p == "Cmd":
			c.Cmd = true
		case last:
			c.Code = p
		default:
			return Combo{}, fmt.Errorf("atalho inválido: %q", s)
		}
	}
	if _, ok := keyCodes[c.Code]; !ok {
		return Combo{}, fmt.Errorf("tecla não suportada em atalhos: %q", c.Code)
	}
	// Um atalho global "engole" a tecla em todos os programas. Uma letra ou
	// número sozinho deixaria de poder ser digitado.
	typing := strings.HasPrefix(c.Code, "Key") || strings.HasPrefix(c.Code, "Digit") || c.Code == "Space" ||
		c.Code == "Enter" || c.Code == "Backspace" || c.Code == "Tab" || c.Code == "Escape" ||
		strings.HasPrefix(c.Code, "Arrow") || len(c.Code) > 0 && strings.Contains("Minus Equal Comma Period Slash Semicolon Quote Backslash BracketLeft BracketRight Backquote", c.Code)
	if typing && !c.Ctrl && !c.Alt && !c.Cmd {
		return Combo{}, errors.New("combine essa tecla com Ctrl, Alt ou Cmd (sozinha, ela deixaria de funcionar nos outros programas), ou use uma tecla de função")
	}
	return c, nil
}

// Handler recebe os eventos: a ação e se a tecla foi apertada ou solta.
type Handler func(action string, down bool)

// Manager mantém os atalhos registrados. Antes de Start os atalhos só ficam
// guardados; o registro no sistema precisa da janela já em execução.
type Manager struct {
	mu      sync.Mutex
	handler Handler
	combos  map[string]string // ação → combinação desejada
	started bool
	native  nativeState
}

func New(h Handler) *Manager {
	return &Manager{handler: h, combos: make(map[string]string)}
}

// Set define (ou limpa, com "") o atalho de uma ação.
func (m *Manager) Set(action, combo string) error {
	if combo != "" {
		if _, err := Parse(combo); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for other, c := range m.combos {
		if combo != "" && other != action && c == combo {
			return errors.New("esse atalho já está em uso por outra ação")
		}
	}
	m.combos[action] = combo
	if !m.started {
		return nil
	}
	return m.registerLocked(action, combo)
}

// Start registra no sistema os atalhos guardados. Devolve os erros por ação.
func (m *Manager) Start() map[string]error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	errs := make(map[string]error)
	for action, combo := range m.combos {
		if err := m.registerLocked(action, combo); err != nil {
			errs[action] = err
		}
	}
	return errs
}

// Close remove todos os atalhos do sistema.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for action := range m.combos {
		m.registerLocked(action, "")
	}
	m.started = false
}

func (m *Manager) dispatch(action string, down bool) {
	if m.handler != nil {
		m.handler(action, down)
	}
}
