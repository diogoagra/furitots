//go:build darwin

package screencap

import (
	"bytes"
	"image/jpeg"
	"os"
	"testing"
	"time"
)

// Captura de verdade pelo ScreenCaptureKit. Só roda a pedido e só se o
// processo já tiver a permissão de gravação de tela (não pede nada ao sistema).
// Confere tamanho e ritmo dos quadros; a imagem não é guardada.
func TestRealCapture(t *testing.T) {
	if os.Getenv("FURITO_SCREEN_TEST") == "" {
		t.Skip("defina FURITO_SCREEN_TEST=1")
	}
	displays, err := Displays()
	t.Logf("captura nativa disponível=%v; telas=%v (%v); permissão de gravação de tela=%v", Available(), len(displays), err, HasAccess())
	if !HasAccess() {
		t.Skip("este processo não tem a permissão de gravação de tela; a captura real não pode ser testada aqui")
	}
	if err := Start(0, 1280, 720, 15); err != nil {
		t.Fatalf("iniciar a captura: %v", err)
	}
	defer Stop()
	var last uint64
	start := time.Now()
	n := 0
	for time.Since(start) < 3*time.Second {
		fr, ok := Next(last, time.Second)
		if !ok {
			continue // tela parada: o sistema não manda quadro repetido
		}
		last = fr.Seq
		n++
		if n == 1 {
			img, err := jpeg.Decode(bytes.NewReader(fr.JPEG))
			if err != nil {
				t.Fatalf("o quadro não é um JPEG válido: %v", err)
			}
			t.Logf("primeiro quadro: %dx%d, %d KB", img.Bounds().Dx(), img.Bounds().Dy(), len(fr.JPEG)/1024)
			if img.Bounds().Dx() > 1280 || img.Bounds().Dy() > 720 || img.Bounds().Dx() < 320 {
				t.Errorf("tamanho fora do pedido (até 1280x720): %v", img.Bounds())
			}
		}
	}
	t.Logf("%d quadros em 3 s", n)
	if n == 0 {
		t.Error("nenhum quadro chegou")
	}
}
