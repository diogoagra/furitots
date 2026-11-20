package hotkey

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]Combo{
		"F13":             {Code: "F13"},
		"CapsLock":        {Code: "CapsLock"},
		"Ctrl+Alt+Space":  {Ctrl: true, Alt: true, Code: "Space"},
		"Cmd+Shift+KeyM":  {Cmd: true, Shift: true, Code: "KeyM"},
		"Alt+Backquote":   {Alt: true, Code: "Backquote"},
		"Numpad0":         {Code: "Numpad0"},
		"Ctrl+ArrowRight": {Ctrl: true, Code: "ArrowRight"},
	}
	for s, want := range ok {
		got, err := Parse(s)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v", s, got, err)
		}
	}
	// teclas de digitação sozinhas (ou só com Shift) e combinações inválidas
	for _, s := range []string{"KeyV", "Shift+KeyV", "Space", "Digit1", "Comma", "", "Cmd+", "Hyper+F1", "Cmd+Shift", "F99"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) deveria falhar", s)
		}
	}
}

func TestManagerStoresUntilStart(t *testing.T) {
	m := New(nil)
	if err := m.Set("ptt", "F13"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("toggleMute", "F13"); err == nil {
		t.Error("o mesmo atalho em duas ações deveria ser recusado")
	}
	if err := m.Set("toggleMute", "KeyM"); err == nil {
		t.Error("letra sozinha deveria ser recusada")
	}
	if err := m.Set("ptt", ""); err != nil {
		t.Errorf("limpar atalho: %v", err)
	}
	if err := m.Set("toggleMute", "F13"); err != nil {
		t.Errorf("depois de liberado, o atalho pode ser usado: %v", err)
	}
}
