package backend

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"furito-ts/audio"
)

// Avisos sonoros ("usuário entrou no seu canal", "conectado", "microfone
// mutado"…). O app traz o próprio pacote de sons (builtinsounds.go), gravado
// na pasta de dados (soundpacks/) ao abrir; o formato é o dos pacotes de sons
// do TeamSpeak 3 (uma pasta com settings.ini e os .wav), e o usuário pode
// importar outros pacotes nesse formato, que ficam na mesma pasta.

type soundPack struct {
	ID     string
	Name   string
	events map[string]string // evento → caminho do .wav
}

var (
	playCall = regexp.MustCompile(`play\("([^"]+)"\)`)
	packName = regexp.MustCompile(`(?m)^\s*name\s*=\s*(.+?)\s*$`)
)

// packsIn lista os pacotes de sons válidos de uma pasta (cada subpasta com um
// settings.ini é um pacote).
func packsIn(root string) []soundPack {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var packs []soundPack
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if p, ok := loadSoundPack(root, e.Name()); ok {
			packs = append(packs, p)
		}
	}
	return packs
}

// Limites da cópia de um pacote: são poucos arquivos .wav pequenos; o que
// passar disso não é um pacote de sons.
const (
	maxPackFile  = 20 << 20
	maxPackTotal = 200 << 20
)

// copySoundPack copia um pacote (o settings.ini e os .wav, nas subpastas em
// que estão) para dst. O que já está lá igual não é regravado.
func copySoundPack(src fs.FS, dst string) error {
	var total int64
	return fs.WalkDir(src, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		ext := strings.ToLower(path.Ext(name))
		if !d.Type().IsRegular() || (ext != ".wav" && ext != ".ini") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxPackFile {
			return nil
		}
		if total += info.Size(); total > maxPackTotal {
			return errors.New("pasta grande demais para ser um pacote de sons")
		}
		data, err := fs.ReadFile(src, name)
		if err != nil {
			return err
		}
		// Compara o conteúdo, não só o tamanho: um som do app refeito com a
		// mesma duração precisa substituir o antigo.
		if old, err := os.ReadFile(target); err == nil && bytes.Equal(old, data) {
			return nil
		}
		return os.WriteFile(target, data, 0o600)
	})
}

var packIDChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// packID transforma o nome de uma pasta no identificador do pacote guardado.
func packID(name string) string {
	id := strings.Trim(packIDChars.ReplaceAllString(name, "_"), "_")
	if id == "" {
		return "pacote"
	}
	return id
}

// bundledPacksDir é a pasta, dentro do zip embutido, que contém os pacotes.
const bundledPacksDir = "soundpacks"

// installBundledPacks grava em dir os pacotes de sons que vêm com o app: um
// zip com soundpacks/<pacote>/ (settings.ini e os .wav). O que já está lá
// igual não é regravado.
func installBundledPacks(zipData []byte, dir string) error {
	if len(zipData) == 0 {
		return nil
	}
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(zr, bundledPacksDir)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pack, err := fs.Sub(zr, path.Join(bundledPacksDir, e.Name()))
		if err == nil {
			err = copySoundPack(pack, filepath.Join(dir, packID(e.Name())))
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// DirPicker abre o seletor de pastas ("" = cancelado).
type DirPicker func(title string) (string, error)

// SetDirPicker define como abrir o seletor de pastas (vem da janela).
func (a *App) SetDirPicker(p DirPicker) { a.dirPicker.Store(&p) }

// ImportSoundPack copia para o app os pacotes de sons de uma pasta escolhida
// pelo usuário: a pasta de um pacote, a pasta "sound" do TeamSpeak 3 ou a
// própria instalação dele. Devolve quantos pacotes entraram (0 = cancelado).
func (api *API) ImportSoundPack() (int, error) {
	pick := api.app.dirPicker.Load()
	if pick == nil || *pick == nil {
		return 0, errors.New("seletor de pastas indisponível")
	}
	dir, err := (*pick)("Importar pacote de sons do TeamSpeak")
	if err != nil || dir == "" {
		return 0, err
	}
	return api.app.importSoundPacks(dir)
}

func (a *App) importSoundPacks(dir string) (int, error) {
	dir = filepath.Clean(dir)
	var imported []string
	add := func(root, name string) {
		if _, ok := loadSoundPack(root, name); !ok {
			return
		}
		id := packID(name)
		if err := copySoundPack(os.DirFS(filepath.Join(root, name)), filepath.Join(a.packDir, id)); err == nil {
			imported = append(imported, id)
		}
	}
	// a pasta escolhida é um pacote…
	add(filepath.Dir(dir), filepath.Base(dir))
	// …ou contém pacotes: a pasta "sound", a instalação do TeamSpeak 3 (Windows) ou o .app (macOS)
	if len(imported) == 0 {
		for _, root := range []string{dir, filepath.Join(dir, "sound"), filepath.Join(dir, "Contents", "SharedSupport", "sound")} {
			for _, p := range packsIn(root) {
				add(root, p.ID)
			}
			if len(imported) > 0 {
				break
			}
		}
	}
	if len(imported) == 0 {
		return 0, errors.New("esta pasta não tem um pacote de sons do TeamSpeak (uma pasta com settings.ini e os arquivos .wav)")
	}
	packs := packsIn(a.packDir)
	a.mu.Lock()
	a.packs = packs
	a.notifyCache = make(map[string][]int16)
	a.settings.NotifyPack = imported[0]
	a.settings.NotifySounds = true
	a.scheduleSaveLocked()
	a.mu.Unlock()
	a.markDirty()
	a.notify("CONNECTION_CONNECTED") // amostra do pacote importado (todo pacote válido tem este som)
	return len(imported), nil
}

func loadSoundPack(root, id string) (soundPack, bool) {
	dir := filepath.Join(root, id)
	data, err := os.ReadFile(filepath.Join(dir, "settings.ini"))
	if err != nil {
		return soundPack{}, false
	}
	p := soundPack{ID: id, Name: id, events: make(map[string]string)}
	if m := packName.FindSubmatch(data); m != nil {
		p.Name = string(m[1])
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.HasPrefix(key, "#") || strings.HasPrefix(key, "[") {
			continue
		}
		m := playCall.FindStringSubmatch(value)
		if m == nil {
			continue
		}
		// Não distinguimos amigos de bloqueados: todos são "neutral".
		file := strings.ReplaceAll(m[1], "${clientType}", "neutral")
		path := filepath.Clean(filepath.Join(dir, filepath.FromSlash(file)))
		if !strings.HasPrefix(path, filepath.Clean(root)+string(filepath.Separator)) {
			continue // o pacote só pode apontar para dentro da pasta de sons
		}
		if _, err := os.Stat(path); err == nil {
			p.events[key] = path
		}
	}
	// Pacotes sem o som de "conectado" (ex.: o de texto falado, que não usa
	// arquivos) não servem para os avisos.
	return p, p.events["CONNECTION_CONNECTED"] != ""
}

// notifyDeafened é o aviso de que os alto-falantes foram mutados: o único que
// toca com eles mutados.
const notifyDeafened = "SOUND_PLAYBACK_MUTED"

// notify toca o aviso sonoro de um evento, se os avisos estiverem ligados e
// o pacote tiver um som para ele. Com os alto-falantes mutados, os avisos
// ficam calados.
func (a *App) notify(event string) {
	if event != notifyDeafened && a.engine.Deafened() {
		return
	}
	a.mu.Lock()
	enabled, packID := a.settings.NotifySounds, a.settings.NotifyPack
	var path string
	for i := range a.packs {
		if a.packs[i].ID == packID {
			path = a.packs[i].events[event]
		}
	}
	pcm, cached := a.notifyCache[path]
	a.mu.Unlock()
	if !enabled || path == "" {
		return
	}
	if !cached {
		decoded, err := audio.DecodeAudioFile(path)
		if err != nil {
			return
		}
		pcm = decoded
		a.mu.Lock()
		a.notifyCache[path] = pcm
		a.mu.Unlock()
	}
	if !a.opts.DisableAudioDevices {
		if err := a.engine.StartPlayback(); err != nil {
			return
		}
	}
	if event == notifyDeafened {
		a.engine.PlayLocalDeafened(pcm)
	} else {
		a.engine.PlayLocal(pcm) // o motor descarta se os alto-falantes foram mutados nesse meio-tempo
	}
}
