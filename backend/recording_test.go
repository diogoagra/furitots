package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Gravar a conversa: só conectado; o arquivo vai para a pasta de gravações
// com data e hora no nome, o estado mostra a gravação em andamento e, ao
// parar, o registro diz onde o arquivo ficou.
func TestRecordingLifecycle(t *testing.T) {
	app, _ := newTestApp(t)
	api := app.API()

	if _, err := api.ToggleRecording(); err == nil {
		t.Fatal("gravar sem estar conectado deveria ser recusado")
	}
	if _, err := os.Stat(app.opts.RecordingsDir); !os.IsNotExist(err) {
		t.Errorf("a pasta de gravações não deveria ser criada sem gravar (err=%v)", err)
	}

	at := time.Date(2026, 11, 12, 7, 45, 10, 0, time.Local)
	path, err := app.startRecording(at)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(app.opts.RecordingsDir, "2026-11-12 07.45.10.wav"); path != want {
		t.Errorf("arquivo %q, esperado %q", path, want)
	}
	if app.buildState().Self.RecordingSince == 0 {
		t.Error("o estado deveria mostrar a gravação em andamento")
	}
	if _, err := app.startRecording(at); err == nil {
		t.Error("uma segunda gravação ao mesmo tempo deveria ser recusada")
	}

	// O botão, com a gravação em andamento, para e salva.
	res, err := api.ToggleRecording()
	if err != nil || res.Recording || res.Path != path {
		t.Fatalf("parar a gravação: %+v, %v", res, err)
	}
	if app.buildState().Self.RecordingSince != 0 {
		t.Error("o estado ainda mostra gravação depois de parar")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Errorf("o arquivo salvo não é um WAV válido (%d bytes, err=%v)", len(data), err)
	}
	logged := false
	for _, m := range api.GetMessages() {
		if strings.HasPrefix(m.Text, "Gravação salva") && strings.Contains(m.Text, path) {
			logged = true
		}
	}
	if !logged {
		t.Error("o registro deveria dizer onde a gravação foi salva")
	}

	// Fechar o app com uma gravação em andamento fecha o arquivo.
	path2, err := app.startRecording(at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	app.Shutdown()
	if data, err := os.ReadFile(path2); err != nil || len(data) < 44 {
		t.Errorf("gravação não finalizada ao fechar o app (%d bytes, err=%v)", len(data), err)
	}
}
