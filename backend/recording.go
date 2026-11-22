package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"furito-ts/audio"
)

// Gravação da conversa: as vozes recebidas e o que transmitimos, num arquivo
// WAV na pasta de gravações. Enquanto grava, o servidor é avisado e os outros
// veem o indicador de gravação ao lado do nosso nome (como no cliente
// oficial). A gravação termina sozinha ao desconectar ou fechar o app.

// RecordingDTO é o resultado de ligar/desligar a gravação.
type RecordingDTO struct {
	Recording bool   `json:"recording"`
	Path      string `json:"path"`    // arquivo sendo gravado ou que acabou de ser salvo
	Seconds   int    `json:"seconds"` // duração salva (ao parar)
}

func recordingSince(e *audio.Engine) int64 {
	if t := e.RecordingSince(); !t.IsZero() {
		return t.UnixMilli()
	}
	return 0
}

// ToggleRecording começa a gravar a conversa ou, se já está gravando, para e
// salva o arquivo.
func (api *API) ToggleRecording() (RecordingDTO, error) {
	a := api.app
	if !a.engine.RecordingSince().IsZero() {
		path, seconds := a.stopRecording()
		return RecordingDTO{Path: path, Seconds: seconds}, nil
	}
	if !a.client.IsConnected() {
		return RecordingDTO{}, errors.New("conecte-se a um servidor para gravar a conversa")
	}
	path, err := a.startRecording(time.Now())
	if err != nil {
		return RecordingDTO{}, err
	}
	return RecordingDTO{Recording: true, Path: path}, nil
}

// OpenRecordingsFolder abre a pasta das gravações no gerenciador de arquivos.
func (api *API) OpenRecordingsFolder() error {
	dir := api.app.opts.RecordingsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("não foi possível criar a pasta das gravações: %w", err)
	}
	opener := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "windows":
		opener = "explorer"
	}
	if err := exec.Command(opener, dir).Start(); err != nil {
		return fmt.Errorf("não foi possível abrir a pasta das gravações: %w", err)
	}
	return nil
}

func (a *App) startRecording(now time.Time) (string, error) {
	dir := a.opts.RecordingsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("não foi possível criar a pasta das gravações: %w", err)
	}
	name := now.Format("2006-01-02 15.04.05")
	if server := strings.TrimSpace(a.client.Snapshot().Server.Name); server != "" {
		name += " - " + safeFileName(server)
	}
	path := filepath.Join(dir, name+".wav")
	if err := a.engine.StartRecording(path, 0); err != nil {
		return "", err
	}
	a.client.SetRecording(true)
	a.addLog("info", "Gravando a conversa em "+path+" — os outros veem o indicador de gravação ao lado do seu nome.")
	a.markDirty()
	return path, nil
}

// stopRecording encerra a gravação em andamento (se houver) e devolve o
// arquivo salvo e a duração em segundos.
func (a *App) stopRecording() (string, int) {
	path, duration, err := a.engine.StopRecording()
	if path == "" {
		return "", 0
	}
	if a.client.IsConnected() {
		a.client.SetRecording(false)
	}
	seconds := int(duration.Round(time.Second) / time.Second)
	if err != nil {
		a.addLog("error", fmt.Sprintf("Gravação encerrada com erro (%v). Arquivo: %s", err, path))
	} else {
		a.addLog("success", fmt.Sprintf("Gravação salva (%d:%02d): %s", seconds/60, seconds%60, path))
	}
	a.markDirty()
	return path, seconds
}
