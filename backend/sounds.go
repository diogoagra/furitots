package backend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"furito-ts/audio"
)

const (
	maxCustomSounds = 80
	maxSoundFile    = 64 << 20 // 64 MB por arquivo
	maxSoundName    = 40
)

// soundListLocked devolve os sons do soundboard (todos adicionados pelo
// usuário). Chamar com a.mu travado.
func (a *App) soundListLocked() []SoundDTO {
	out := make([]SoundDTO, 0, len(a.settings.Sounds))
	for _, s := range a.settings.Sounds {
		action := soundHotkeyPrefix + s.ID
		out = append(out, SoundDTO{ID: s.ID, Name: s.Name, Hotkey: a.settings.Hotkeys[action], HotkeyError: a.hotkeyErrs[action]})
	}
	return out
}

func (a *App) soundPath(s SoundSetting) string {
	// Só o nome do arquivo é guardado: o som nunca aponta para fora da pasta.
	return filepath.Join(a.soundDir, filepath.Base(s.File))
}

func (a *App) playSound(id string) error {
	a.mu.Lock()
	var found *SoundSetting
	for i := range a.settings.Sounds {
		if a.settings.Sounds[i].ID == id {
			s := a.settings.Sounds[i]
			found = &s
		}
	}
	a.mu.Unlock()
	if found == nil {
		return errors.New("som não encontrado")
	}
	return a.engine.Soundboard().PlayFile(a.soundPath(*found))
}

func cleanSoundName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > maxSoundName {
		name = string(r[:maxSoundName])
	}
	return name
}

// AddSoundFiles adiciona arquivos de áudio ao soundboard (ex.: arrastados
// para a janela) e registra o resultado no log.
func (a *App) AddSoundFiles(paths []string) {
	added, err := a.addSoundFiles(paths)
	if added > 0 {
		a.addLog("success", fmt.Sprintf("%d som(ns) adicionado(s) ao soundboard", added))
	}
	if err != nil {
		a.addLog("error", "Soundboard: "+err.Error())
	}
}

// addSoundFiles copia os arquivos para a pasta do app e os registra. Arquivos
// que não são áudio (ou não puderem ser lidos) são recusados; os demais entram.
func (a *App) addSoundFiles(paths []string) (int, error) {
	if err := os.MkdirAll(a.soundDir, 0o700); err != nil {
		return 0, fmt.Errorf("não foi possível criar a pasta de sons: %w", err)
	}
	added := 0
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	for _, src := range paths {
		a.mu.Lock()
		full := len(a.settings.Sounds) >= maxCustomSounds
		a.mu.Unlock()
		if full {
			fail(fmt.Errorf("limite de %d sons atingido", maxCustomSounds))
			break
		}

		info, err := os.Stat(src)
		if err != nil || info.IsDir() {
			fail(fmt.Errorf("não foi possível abrir %s", filepath.Base(src)))
			continue
		}
		if info.Size() > maxSoundFile {
			fail(fmt.Errorf("%s é grande demais (máximo 64 MB)", filepath.Base(src)))
			continue
		}
		// Decodifica antes de aceitar: garante que é áudio que sabemos tocar.
		if _, err := audio.DecodeAudioFile(src); err != nil {
			fail(err)
			continue
		}

		id := fmt.Sprintf("u%x", time.Now().UnixNano())
		file := id + strings.ToLower(filepath.Ext(src))
		if err := copyFile(src, filepath.Join(a.soundDir, file)); err != nil {
			fail(fmt.Errorf("não foi possível copiar %s: %w", filepath.Base(src), err))
			continue
		}
		name := cleanSoundName(strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
		if name == "" {
			name = "Som"
		}

		a.mu.Lock()
		a.settings.Sounds = append(a.settings.Sounds, SoundSetting{ID: id, Name: name, File: file})
		a.scheduleSaveLocked()
		a.mu.Unlock()
		added++

		// Deixa pronto em memória para o primeiro toque sair na hora.
		go a.engine.Soundboard().Load(filepath.Join(a.soundDir, file))
	}
	if added > 0 {
		a.markDirty()
	}
	return added, firstErr
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

func (a *App) removeSound(id string) error {
	a.mu.Lock()
	idx := -1
	for i, s := range a.settings.Sounds {
		if s.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return errors.New("som não encontrado")
	}
	removed := a.settings.Sounds[idx]
	a.settings.Sounds = append(a.settings.Sounds[:idx:idx], a.settings.Sounds[idx+1:]...)
	// o atalho do som vai embora com ele
	action := soundHotkeyPrefix + id
	delete(a.settings.Hotkeys, action)
	delete(a.hotkeyErrs, action)
	a.scheduleSaveLocked()
	a.mu.Unlock()
	a.hotkeys.Set(action, "")

	path := a.soundPath(removed)
	a.engine.Soundboard().Forget(path)
	os.Remove(path) // é a nossa cópia; o arquivo original do usuário fica intacto
	a.markDirty()
	return nil
}

func (a *App) renameSound(id, name string) error {
	name = cleanSoundName(name)
	if name == "" {
		return errors.New("informe um nome")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.settings.Sounds {
		if a.settings.Sounds[i].ID == id {
			a.settings.Sounds[i].Name = name
			a.scheduleSaveLocked()
			a.markDirty()
			return nil
		}
	}
	return errors.New("som não encontrado")
}
