//go:build darwin

package hotkey

/*
#cgo LDFLAGS: -framework Carbon
#include "hotkey_darwin.h"
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// Available indica se há atalhos globais nesta plataforma.
func Available() bool { return true }

type nativeState struct {
	refs map[string]unsafe.Pointer // ação → atalho registrado
	ids  map[string]int
}

var (
	registryMu sync.Mutex
	registry   = map[int]struct {
		m      *Manager
		action string
	}{}
	nextID = 1
)

func (m *Manager) registerLocked(action, combo string) error {
	if m.native.refs == nil {
		m.native.refs = make(map[string]unsafe.Pointer)
		m.native.ids = make(map[string]int)
	}
	if ref := m.native.refs[action]; ref != nil {
		C.furito_hotkey_unregister(ref)
		delete(m.native.refs, action)
	}
	if combo == "" {
		return nil
	}
	c, err := Parse(combo)
	if err != nil {
		return err
	}
	id, ok := m.native.ids[action]
	if !ok {
		registryMu.Lock()
		id = nextID
		nextID++
		registry[id] = struct {
			m      *Manager
			action string
		}{m, action}
		registryMu.Unlock()
		m.native.ids[action] = id
	}
	flag := func(b bool) C.int {
		if b {
			return 1
		}
		return 0
	}
	var ref unsafe.Pointer
	if st := C.furito_hotkey_register(C.int(id), C.uint(keyCodes[c.Code]), flag(c.Ctrl), flag(c.Alt), flag(c.Shift), flag(c.Cmd), &ref); st != 0 {
		return fmt.Errorf("o sistema recusou esse atalho (em uso por outro programa?) — código %d", int(st))
	}
	m.native.refs[action] = ref
	return nil
}

//export furitoHotkeyEvent
func furitoHotkeyEvent(id C.int, down C.int) {
	registryMu.Lock()
	entry, ok := registry[int(id)]
	registryMu.Unlock()
	if ok {
		// Fora da thread principal: o tratamento pode chamar o servidor.
		go entry.m.dispatch(entry.action, down != 0)
	}
}
