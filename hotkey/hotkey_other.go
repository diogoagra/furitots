//go:build !darwin && !windows

package hotkey

// Available indica se há atalhos globais nesta plataforma.
func Available() bool { return false }

type nativeState struct{}

// Sem atalhos globais: a interface usa os atalhos com a janela em foco.
func (m *Manager) registerLocked(action, combo string) error { return nil }
