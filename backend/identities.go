package backend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"furito-ts/ts3"
)

// Várias identidades, como no cliente TeamSpeak 3: cada uma é uma chave (o
// servidor reconhece a pessoa por ela), com um nome para a lista e um apelido
// sugerido. Uma é a padrão; favoritos e a janela de conexão podem escolher
// outra. Ficam todas num arquivo só (Options.IdentityPath).

type identityEntry struct {
	ID       string
	Name     string
	Nickname string
	Ident    *ts3.Identity
}

// IdentityEntryDTO é uma identidade da lista, sem a chave.
type IdentityEntryDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Nickname      string `json:"nickname"`
	UID           string `json:"uid"`
	SecurityLevel int    `json:"securityLevel"`
	IsDefault     bool   `json:"isDefault"`
	InUse         bool   `json:"inUse"` // é a da conexão atual (ou a que será usada)
}

type identitiesFile struct {
	Version    int                `json:"version"`
	Default    string             `json:"default"`
	Identities []identityFileItem `json:"identities"`

	// Formato antigo: uma identidade só, direto na raiz do arquivo.
	LegacyKey    []byte `json:"private_key,omitempty"`
	LegacyOffset uint64 `json:"key_offset,omitempty"`
}

type identityFileItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Nickname   string `json:"nickname,omitempty"`
	PrivateKey []byte `json:"private_key"`
	KeyOffset  uint64 `json:"key_offset"`
}

func newIdentityID() string {
	var b [6]byte
	rand.Read(b[:])
	return "i" + hex.EncodeToString(b[:])
}

// loadIdentities lê as identidades de path. Se o arquivo não existe, cria uma.
// Se está no formato antigo (uma identidade só), converte: a antiga vira a
// padrão, a de path+".anterior" (guardada por importações antigas) entra na
// lista também, e o arquivo original fica copiado em path+".v1".
func loadIdentities(path string) (list []*identityEntry, def string, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		ident, err := ts3.GenerateIdentity()
		if err != nil {
			return nil, "", fmt.Errorf("falha ao gerar a identidade: %w", err)
		}
		e := &identityEntry{ID: newIdentityID(), Name: "Principal", Ident: ident}
		list = []*identityEntry{e}
		if err := saveIdentities(path, list, e.ID); err != nil {
			return nil, "", fmt.Errorf("falha ao salvar a identidade em %s: %w", path, err)
		}
		return list, e.ID, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("não foi possível ler as identidades em %s: %w", path, err)
	}
	var file identitiesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, "", fmt.Errorf("não foi possível ler as identidades em %s: %w", path, err)
	}

	if len(file.Identities) == 0 && len(file.LegacyKey) > 0 {
		ident, err := ts3.IdentityFromKey(file.LegacyKey, file.LegacyOffset)
		if err != nil {
			return nil, "", fmt.Errorf("não foi possível ler a identidade em %s: %w", path, err)
		}
		e := &identityEntry{ID: newIdentityID(), Name: "Principal", Ident: ident}
		list = []*identityEntry{e}
		if old, err := ts3.LoadIdentity(path + ".anterior"); err == nil && old.Uid != ident.Uid {
			list = append(list, &identityEntry{ID: newIdentityID(), Name: "Anterior", Ident: old})
		}
		// Guarda o arquivo antigo antes de regravar no formato novo.
		if err := os.WriteFile(path+".v1", data, 0o600); err != nil {
			return nil, "", fmt.Errorf("não foi possível guardar a cópia da identidade antiga: %w", err)
		}
		if err := saveIdentities(path, list, e.ID); err != nil {
			return nil, "", fmt.Errorf("falha ao converter as identidades em %s: %w", path, err)
		}
		return list, e.ID, nil
	}

	for _, it := range file.Identities {
		ident, err := ts3.IdentityFromKey(it.PrivateKey, it.KeyOffset)
		if err != nil {
			return nil, "", fmt.Errorf("identidade %q ilegível em %s: %w", it.Name, path, err)
		}
		id := it.ID
		if id == "" {
			id = newIdentityID()
		}
		list = append(list, &identityEntry{ID: id, Name: it.Name, Nickname: it.Nickname, Ident: ident})
	}
	if len(list) == 0 {
		return nil, "", fmt.Errorf("o arquivo %s não contém nenhuma identidade", path)
	}
	def = file.Default
	if findIdentity(list, def) == nil {
		def = list[0].ID
	}
	return list, def, nil
}

// saveIdentities grava todas as identidades de uma vez, trocando o arquivo só
// depois de escrito por inteiro: uma queda no meio não pode perder chaves.
func saveIdentities(path string, list []*identityEntry, def string) error {
	file := identitiesFile{Version: 2, Default: def}
	for _, e := range list {
		key, err := e.Ident.MarshalKey()
		if err != nil {
			return err
		}
		file.Identities = append(file.Identities, identityFileItem{
			ID: e.ID, Name: e.Name, Nickname: e.Nickname, PrivateKey: key, KeyOffset: e.Ident.KeyOffset,
		})
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	// Na primeira abertura a pasta pode não existir ainda (no Windows as
	// identidades ficam em %AppData%\FuritoTS).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func findIdentity(list []*identityEntry, id string) *identityEntry {
	for _, e := range list {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (a *App) saveIdentitiesLocked() error {
	return saveIdentities(a.opts.IdentityPath, a.idents, a.identDefault)
}

// identityDTO descreve a identidade em uso.
func (a *App) identityDTO() IdentityDTO {
	a.identMu.Lock()
	defer a.identMu.Unlock()
	dto := IdentityDTO{ID: a.identActive, UID: a.identity.Uid, SecurityLevel: a.identity.SecurityLevel()}
	if e := findIdentity(a.idents, a.identActive); e != nil {
		dto.Name = e.Name
	}
	return dto
}

func (a *App) identityList() []IdentityEntryDTO {
	a.identMu.Lock()
	defer a.identMu.Unlock()
	out := make([]IdentityEntryDTO, 0, len(a.idents))
	for _, e := range a.idents {
		out = append(out, IdentityEntryDTO{
			ID: e.ID, Name: e.Name, Nickname: e.Nickname, UID: e.Ident.Uid, SecurityLevel: e.Ident.SecurityLevel(),
			IsDefault: e.ID == a.identDefault, InUse: e.ID == a.identActive,
		})
	}
	return out
}

func (a *App) idle() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status == statusDisconnected
}

// useIdentity põe a identidade id ("" = a padrão) em uso na próxima conexão.
// Trocar de identidade encerra a conexão atual.
func (a *App) useIdentity(id string) error {
	a.identMu.Lock()
	if id == "" {
		id = a.identDefault
	}
	e := findIdentity(a.idents, id)
	same := e != nil && a.identActive == e.ID
	a.identMu.Unlock()
	if e == nil {
		return errors.New("identidade não encontrada (ela pode ter sido apagada)")
	}
	if same {
		return nil
	}
	a.client.SetIdentity(e.Ident) // desconecta, se houver conexão
	a.identMu.Lock()
	a.identity, a.identActive = e.Ident, e.ID
	a.identMu.Unlock()
	a.markDirty()
	return nil
}

func cleanIdentityName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("informe um nome para a identidade")
	}
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	return name, nil
}

// CreateIdentity gera uma identidade nova (outra pessoa, do ponto de vista dos
// servidores) e devolve o id dela.
func (api *API) CreateIdentity(name string) (string, error) {
	name, err := cleanIdentityName(name)
	if err != nil {
		return "", err
	}
	ident, err := ts3.GenerateIdentity()
	if err != nil {
		return "", fmt.Errorf("falha ao gerar a identidade: %w", err)
	}
	a := api.app
	a.identMu.Lock()
	e := &identityEntry{ID: newIdentityID(), Name: name, Ident: ident}
	a.idents = append(a.idents, e)
	err = a.saveIdentitiesLocked()
	if err != nil {
		a.idents = a.idents[:len(a.idents)-1]
	}
	a.identMu.Unlock()
	if err != nil {
		return "", err
	}
	a.markDirty()
	return e.ID, nil
}

// UpdateIdentity troca o nome e o apelido sugerido de uma identidade.
func (api *API) UpdateIdentity(id, name, nickname string) error {
	name, err := cleanIdentityName(name)
	if err != nil {
		return err
	}
	nickname = strings.TrimSpace(nickname)
	if nickname != "" {
		nickname = ts3.NormalizeNickname(nickname)
	}
	a := api.app
	a.identMu.Lock()
	e := findIdentity(a.idents, id)
	if e == nil {
		a.identMu.Unlock()
		return errors.New("identidade não encontrada")
	}
	oldName, oldNick := e.Name, e.Nickname
	e.Name, e.Nickname = name, nickname
	if err = a.saveIdentitiesLocked(); err != nil {
		e.Name, e.Nickname = oldName, oldNick
	}
	a.identMu.Unlock()
	a.markDirty()
	return err
}

// SetDefaultIdentity escolhe a identidade usada quando a conexão não pede
// outra. Sem conexão aberta, ela passa a ser a identidade em uso na hora.
func (api *API) SetDefaultIdentity(id string) error {
	a := api.app
	a.identMu.Lock()
	if findIdentity(a.idents, id) == nil {
		a.identMu.Unlock()
		return errors.New("identidade não encontrada")
	}
	old := a.identDefault
	a.identDefault = id
	err := a.saveIdentitiesLocked()
	if err != nil {
		a.identDefault = old
	}
	a.identMu.Unlock()
	if err != nil {
		return err
	}
	a.markDirty()
	if a.idle() {
		return a.useIdentity(id)
	}
	return nil
}

// DeleteIdentity apaga uma identidade. A chave some de vez: quem quiser
// guardá-la exporta antes. A última identidade e a da conexão aberta não
// podem ser apagadas.
func (api *API) DeleteIdentity(id string) error {
	a := api.app
	idle := a.idle()
	a.identMu.Lock()
	if findIdentity(a.idents, id) == nil {
		a.identMu.Unlock()
		return errors.New("identidade não encontrada")
	}
	if len(a.idents) == 1 {
		a.identMu.Unlock()
		return errors.New("esta é a única identidade; crie ou importe outra antes de apagá-la")
	}
	if id == a.identActive && !idle {
		a.identMu.Unlock()
		return errors.New("esta identidade está em uso na conexão atual; desconecte antes de apagá-la")
	}
	oldList, oldDef := a.idents, a.identDefault
	kept := make([]*identityEntry, 0, len(a.idents)-1)
	for _, e := range a.idents {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	a.idents = kept
	if a.identDefault == id {
		a.identDefault = kept[0].ID
	}
	if err := a.saveIdentitiesLocked(); err != nil {
		a.idents, a.identDefault = oldList, oldDef
		a.identMu.Unlock()
		return err
	}
	wasActive := id == a.identActive
	def := a.identDefault
	a.identMu.Unlock()
	a.markDirty()
	if wasActive {
		return a.useIdentity(def)
	}
	return nil
}

// ExportIdentity salva uma cópia de uma identidade ("" = a em uso) onde o
// usuário escolher.
func (api *API) ExportIdentity(id string) (string, error) {
	a := api.app
	save := a.savePicker.Load()
	if save == nil || *save == nil {
		return "", errors.New("seletor de arquivos indisponível")
	}
	a.identMu.Lock()
	if id == "" {
		id = a.identActive
	}
	e := findIdentity(a.idents, id)
	a.identMu.Unlock()
	if e == nil {
		return "", errors.New("identidade não encontrada")
	}
	target, err := (*save)("Exportar identidade", "identidade-"+fileSafe(e.Name)+".json")
	if err != nil || target == "" {
		return "", err
	}
	a.identMu.Lock()
	defer a.identMu.Unlock()
	if err := ts3.SaveIdentity(e.Ident, target); err != nil {
		return "", err
	}
	return target, nil
}

func fileSafe(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "furito"
	}
	return b.String()
}

// ImportIdentity acrescenta à lista uma identidade exportada por este app ou
// pelo cliente TeamSpeak 3 (arquivo .ini de Ferramentas → Identidades →
// Exportar). Devolve o id dela ("" se o usuário cancelou). Não troca a
// identidade em uso.
func (api *API) ImportIdentity() (string, error) {
	open := api.app.openPicker.Load()
	if open == nil || *open == nil {
		return "", errors.New("seletor de arquivos indisponível")
	}
	paths, err := (*open)("Importar identidade", false)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return api.app.importIdentity(paths[0])
}

// ImportIdentityFile importa a identidade de um arquivo (formato deste app ou
// .ini do TeamSpeak 3) e devolve o id dela na lista.
func (a *App) ImportIdentityFile(file string) (string, error) { return a.importIdentity(file) }

func (a *App) importIdentity(file string) (string, error) {
	ident, err := ts3.LoadIdentityFile(file)
	if err != nil {
		return "", fmt.Errorf("este arquivo não é uma identidade válida: %w", err)
	}
	// Servidores exigem nível 8 ou mais; uma identidade antiga com menos é
	// completada aqui (leva uma fração de segundo).
	if ident.SecurityLevel() < 8 {
		ident.ImproveSecurityLevel(8)
	}
	// O .ini do TeamSpeak 3 traz o nome da identidade e o apelido.
	var info ts3.TS3IdentityInfo
	if data, err := os.ReadFile(file); err == nil && !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		info = ts3.ParseTS3IdentityInfo(string(data))
	}
	name := info.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	if name, err = cleanIdentityName(name); err != nil {
		name = "Importada"
	}
	nickname := ""
	if info.Nickname != "" {
		nickname = ts3.NormalizeNickname(info.Nickname)
	}

	a.identMu.Lock()
	for _, e := range a.idents {
		if e.Ident.Uid == ident.Uid {
			a.identMu.Unlock()
			return "", fmt.Errorf("esta identidade já está na lista, com o nome %q", e.Name)
		}
	}
	e := &identityEntry{ID: newIdentityID(), Name: name, Nickname: nickname, Ident: ident}
	a.idents = append(a.idents, e)
	err = a.saveIdentitiesLocked()
	if err != nil {
		a.idents = a.idents[:len(a.idents)-1]
	}
	a.identMu.Unlock()
	if err != nil {
		return "", err
	}
	a.markDirty()
	return e.ID, nil
}

// ImproveIdentity aumenta o nível de segurança de uma identidade ("" = a em
// uso); servidores podem exigir um mínimo. Cada nível a mais dobra o tempo de
// cálculo.
func (api *API) ImproveIdentity(id string, level int) (int, error) {
	if level > 30 {
		return 0, errors.New("nível alto demais (máximo 30)")
	}
	a := api.app
	a.identMu.Lock()
	if id == "" {
		id = a.identActive
	}
	e := findIdentity(a.idents, id)
	if e == nil {
		a.identMu.Unlock()
		return 0, errors.New("identidade não encontrada")
	}
	work := *e.Ident // o cálculo roda numa cópia, sem travar a interface
	a.identMu.Unlock()

	if level > work.SecurityLevel() {
		work.ImproveSecurityLevel(level)
		a.identMu.Lock()
		old := e.Ident.KeyOffset
		e.Ident.KeyOffset = work.KeyOffset
		err := a.saveIdentitiesLocked()
		if err != nil {
			e.Ident.KeyOffset = old
		}
		a.identMu.Unlock()
		if err != nil {
			return 0, err
		}
		a.markDirty()
	}
	return work.SecurityLevel(), nil
}
