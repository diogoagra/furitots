package hotkey

import (
	"fmt"
	"testing"
)

// Tudo o que Parse aceita (a tabela do macOS) precisa ter tecla virtual no
// Windows; senão o atalho seria aceito e nunca dispararia.
func TestWinKeysCoverEveryParsedCode(t *testing.T) {
	for code := range keyCodes {
		if _, err := Parse("Ctrl+" + code); err != nil {
			t.Errorf("Parse(Ctrl+%s): %v", code, err)
		}
		if _, ok := winKeys[code]; !ok {
			t.Errorf("%q é aceito em atalhos mas não tem tecla virtual do Windows", code)
		}
	}
}

func TestWinKeysValues(t *testing.T) {
	check := func(code string, want uint32) {
		t.Helper()
		if got, ok := winKeys[code]; !ok || got != want {
			t.Errorf("winKeys[%q] = %#x (existe: %v), esperado %#x", code, got, ok, want)
		}
	}
	// Letras e dígitos usam o próprio código ASCII; teclas de função e o
	// teclado numérico são sequenciais.
	for c := 'A'; c <= 'Z'; c++ {
		check("Key"+string(c), uint32(c))
	}
	for i := 0; i <= 9; i++ {
		check(fmt.Sprint("Digit", i), uint32('0'+i))
		check(fmt.Sprint("Numpad", i), uint32(0x60+i))
	}
	for i := 1; i <= 24; i++ {
		check(fmt.Sprint("F", i), uint32(0x70+i-1))
	}
	// Valores de winuser.h (VK_*), conferidos um a um.
	for code, want := range map[string]uint32{
		"Backspace": 0x08, "Tab": 0x09, "Enter": 0x0D, "NumpadEnter": 0x0D, "CapsLock": 0x14, "Escape": 0x1B,
		"Space": 0x20, "PageUp": 0x21, "PageDown": 0x22, "End": 0x23, "Home": 0x24,
		"ArrowLeft": 0x25, "ArrowUp": 0x26, "ArrowRight": 0x27, "ArrowDown": 0x28, "Insert": 0x2D, "Delete": 0x2E,
		"NumpadMultiply": 0x6A, "NumpadAdd": 0x6B, "NumpadSubtract": 0x6D, "NumpadDecimal": 0x6E, "NumpadDivide": 0x6F,
		"Semicolon": 0xBA, "Equal": 0xBB, "Comma": 0xBC, "Minus": 0xBD, "Period": 0xBE, "Slash": 0xBF,
		"Backquote": 0xC0, "BracketLeft": 0xDB, "Backslash": 0xDC, "BracketRight": 0xDD, "Quote": 0xDE,
		"IntlBackslash": 0xE2,
	} {
		check(code, want)
	}
}

func TestWinKeysAreDistinctNonModifierKeys(t *testing.T) {
	mods := map[uint32]bool{vkShift: true, vkControl: true, vkAlt: true, vkLWin: true, vkRWin: true}
	seen := make(map[uint32]string)
	for code, vk := range winKeys {
		if vk == 0 || vk > 0xFE {
			t.Errorf("winKeys[%q] = %#x fora da faixa de teclas virtuais", code, vk)
		}
		if mods[vk] {
			t.Errorf("winKeys[%q] = %#x é um modificador", code, vk)
		}
		// A única repetição esperada: o Windows não distingue os dois Enter.
		if other, dup := seen[vk]; dup && vk != winKeys["Enter"] {
			t.Errorf("%q e %q têm a mesma tecla virtual %#x", code, other, vk)
		}
		seen[vk] = code
	}
}
