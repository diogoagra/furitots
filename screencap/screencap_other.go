//go:build !darwin

package screencap

// Available indica se este sistema tem captura de tela nativa.
func Available() bool { return false }

// Displays lista as telas que podem ser capturadas.
func Displays() ([]Display, error) { return nil, ErrUnavailable }

// Start começa a capturar uma tela (0 = a principal).
func Start(displayID uint32, maxWidth, maxHeight, fps int) error { return ErrUnavailable }

// HasAccess indica se o sistema já liberou a gravação de tela para o app.
func HasAccess() bool { return false }
