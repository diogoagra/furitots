package screencap

import (
	"bytes"
	"image/jpeg"
	"testing"
	"time"
)

func TestFit(t *testing.T) {
	for _, c := range []struct{ w, h, maxW, maxH, wantW, wantH int }{
		{3456, 2234, 1920, 1080, 1670, 1080}, // tela de notebook, mais alta que 16:9
		{2560, 1440, 1920, 1080, 1920, 1080},
		{1280, 720, 1920, 1080, 1280, 720}, // não amplia
		{1920, 1080, 0, 0, 1920, 1080},
		{5120, 1440, 1920, 1080, 1920, 540}, // ultralarga
		{1001, 701, 1920, 1080, 1000, 700},  // lados pares
	} {
		w, h := fit(c.w, c.h, c.maxW, c.maxH)
		if w != c.wantW || h != c.wantH {
			t.Errorf("fit(%d×%d em %d×%d) = %d×%d, esperado %d×%d", c.w, c.h, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
		}
	}
}

// O padrão de teste entrega quadros JPEG válidos, numerados em ordem, no ritmo
// pedido; Next espera pelo próximo e desiste quando a captura para.
func TestPatternDeliversFrames(t *testing.T) {
	if _, ok := Next(0, 50*time.Millisecond); ok {
		t.Fatal("sem captura não deveria haver quadro")
	}
	if err := StartTestPattern(320, 180, 30); err != nil {
		t.Fatal(err)
	}
	defer Stop()
	if !Running() {
		t.Fatal("a captura deveria estar em andamento")
	}
	var last uint64
	start := time.Now()
	for i := 0; i < 10; i++ {
		fr, ok := Next(last, time.Second)
		if !ok {
			t.Fatalf("quadro %d não chegou", i)
		}
		if fr.Seq <= last {
			t.Fatalf("numeração fora de ordem: %d depois de %d", fr.Seq, last)
		}
		last = fr.Seq
		img, err := jpeg.Decode(bytes.NewReader(fr.JPEG))
		if err != nil || img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 || fr.Width != 320 || fr.Height != 180 {
			t.Fatalf("quadro inválido: %v %v", err, img.Bounds())
		}
	}
	if d := time.Since(start); d < 200*time.Millisecond || d > 2*time.Second {
		t.Errorf("10 quadros a 30 por segundo levaram %v", d)
	}

	// quem já viu o último espera; ao parar, a espera termina logo
	done := make(chan bool, 1)
	go func() { _, ok := Next(1<<60, 5*time.Second); done <- ok }()
	time.Sleep(100 * time.Millisecond)
	Stop()
	select {
	case ok := <-done:
		if ok {
			t.Error("não deveria vir quadro depois de parar")
		}
	case <-time.After(time.Second):
		t.Error("Next ficou preso depois de Stop")
	}
	if Running() {
		t.Error("depois de Stop a captura não deveria estar em andamento")
	}

	// uma captura nova não entrega quadro velho para quem pede "depois do último"
	if err := StartTestPattern(64, 64, 30); err != nil {
		t.Fatal(err)
	}
	fr, ok := Next(last, time.Second)
	if !ok || fr.Width != 64 || fr.Seq <= last {
		t.Errorf("primeiro quadro da captura nova: ok=%v %dx%d seq=%d (anterior %d)", ok, fr.Width, fr.Height, fr.Seq, last)
	}
	if err := StartTestPattern(4, 4, 30); err == nil {
		t.Error("tamanho absurdo deveria ser recusado")
	}
}
