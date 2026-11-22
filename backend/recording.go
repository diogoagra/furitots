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

// recordingSince diz desde quando esta conexão está sendo gravada (0 = não está).
func (c *conn) recordingSince() int64 {
	if c.app.recording() != c {
		return 0
	}
	if t := c.app.engine.RecordingSince(); !t.IsZero() {
		return t.UnixMilli()
	}
	return 0
}

// recording devolve a conexão cuja conversa está sendo gravada (nil = nenhuma).
func (a *App) recording() *conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rec
}

// ToggleRecording começa a gravar a conversa desta conexão ou, se ela já está
// sendo gravada, para e salva o arquivo. Grava-se uma conexão por vez: começar
// numa encerra a gravação que houver em outra.
func (api *API) ToggleRecording() (RecordingDTO, error) {
	a := api.app
	if a.recording() == api.c {
		path, seconds := a.stopRecording()
		return RecordingDTO{Path: path, Seconds: seconds}, nil
	}
	if !api.c.client.IsConnected() {
		return RecordingDTO{}, errors.New("conecte-se a um servidor para gravar a conversa")
	}
	path, err := api.c.startRecording(time.Now())
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

func (c *conn) startRecording(now time.Time) (string, error) {
	dir := c.app.opts.RecordingsDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("não foi possível criar a pasta das gravações: %w", err)
	}
	name := now.Format("2006-01-02 15.04.05")
	if server := strings.TrimSpace(c.client.Snapshot().Server.Name); server != "" {
		name += " - " + safeFileName(server)
	}
	path := filepath.Join(dir, name+".wav")
	if other := c.app.recording(); other != nil && other != c {
		c.app.stopRecording() // grava-se uma conexão por vez
	}
	if err := c.app.engine.StartRecording(path, c.audioID()); err != nil {
		return "", err
	}
	c.app.mu.Lock()
	c.app.rec = c
	c.app.mu.Unlock()
	c.client.SetRecording(true)
	c.addLog("info", "Gravando a conversa em "+path+" — os outros veem o indicador de gravação ao lado do seu nome.")
	c.markDirty()
	return path, nil
}

// stopRecording encerra a gravação em andamento (se houver) e devolve o
// arquivo salvo e a duração em segundos.
func (a *App) stopRecording() (string, int) {
	a.mu.Lock()
	c := a.rec
	a.rec = nil
	a.mu.Unlock()
	path, duration, err := a.engine.StopRecording()
	if path == "" || c == nil {
		return "", 0
	}
	if c.client.IsConnected() {
		c.client.SetRecording(false)
	}
	seconds := int(duration.Round(time.Second) / time.Second)
	if err != nil {
		c.addLog("error", fmt.Sprintf("Gravação encerrada com erro (%v). Arquivo: %s", err, path))
	} else {
		c.addLog("success", fmt.Sprintf("Gravação salva (%d:%02d): %s", seconds/60, seconds%60, path))
	}
	c.markDirty()
	return path, seconds
}
