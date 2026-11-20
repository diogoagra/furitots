package backend

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"furito-ts/ts3"
)

// ---- tipos extras trocados com a interface ----

type GroupEntry struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	IconID uint32 `json:"iconId"`
	Type   int    `json:"type"` // 1 = normal (atribuível)
}

type GroupsDTO struct {
	Server  []GroupEntry `json:"server"`
	Channel []GroupEntry `json:"channel"`
}

type Bookmark struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Host           string `json:"host"`
	Nickname       string `json:"nickname"`
	DefaultChannel string `json:"defaultChannel"`
	IdentityID     string `json:"identityId"` // "" = a identidade padrão
}

type HotkeysDTO struct {
	PTT          string `json:"ptt"`
	ToggleMute   string `json:"toggleMute"`
	ToggleDeafen string `json:"toggleDeafen"`
	SoundStop    string `json:"soundStop"` // para o som do soundboard que estiver tocando
	Global       bool   `json:"global"`
	// Errors: por ação, por que o atalho não pôde ser registrado. Os atalhos
	// dos sons do soundboard usam a ação "sound:<id do som>".
	Errors map[string]string `json:"errors"`
}

// soundHotkeyPrefix + id do som é a ação de atalho que toca aquele som.
const soundHotkeyPrefix = "sound:"

type IdentityDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	UID           string `json:"uid"`
	SecurityLevel int    `json:"securityLevel"`
	// ServerUID é como o servidor atual nos identifica, quando difere de UID:
	// servidores TeamSpeak 6 usam o SHA-256 da mesma chave pública em vez do
	// SHA-1 que o cliente TeamSpeak 3 mostra.
	ServerUID string `json:"serverUid"`
}

type WhisperDTO struct {
	Kind string `json:"kind"` // "" | client | channel
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type ChannelSpec struct {
	Name        string `json:"name"`
	ParentID    int    `json:"parentId"`
	Topic       string `json:"topic"`
	Description string `json:"description"`
	// ChangeDescription: na edição, a descrição só é alterada quando true.
	ChangeDescription bool   `json:"changeDescription"`
	Password          string `json:"password"`
	ChangePassword    bool   `json:"changePassword"`
	Kind              string `json:"kind"` // temporary | semi | permanent
	MaxClients        int    `json:"maxClients"`
	Codec             int    `json:"codec"`
	CodecQuality      int    `json:"codecQuality"`
	NeededTalkPower   int    `json:"neededTalkPower"`
	IsDefault         bool   `json:"isDefault"`
}

func (s ChannelSpec) toTS3() (ts3.ChannelSpec, error) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return ts3.ChannelSpec{}, errors.New("informe o nome do canal")
	}
	codec := s.Codec
	if codec != 5 {
		codec = 4
	}
	quality := s.CodecQuality
	if quality < 0 {
		quality = 0
	}
	if quality > 10 {
		quality = 10
	}
	return ts3.ChannelSpec{
		Name: name, ParentID: s.ParentID, Topic: s.Topic,
		Description: s.Description, ChangeDescription: s.ChangeDescription,
		Password: s.Password, ChangePassword: s.ChangePassword,
		Permanent: s.Kind == "permanent", SemiPermanent: s.Kind == "semi",
		MaxClients: s.MaxClients, Codec: codec, CodecQuality: quality,
		NeededTalkPower: s.NeededTalkPower, IsDefault: s.IsDefault,
	}, nil
}

type PermDTO struct {
	Name    string `json:"name"`
	Value   int    `json:"value"`
	Negated bool   `json:"negated"`
	Skip    bool   `json:"skip"`
}

type FileEntryDTO struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix s
	IsDir    bool   `json:"isDir"`
}

type PokeDTO struct {
	FromName string `json:"fromName"`
	Message  string `json:"message"`
}

// OpenPicker abre o seletor de arquivos do sistema; SavePicker, o de salvar.
type (
	OpenPicker func(title string, multiple bool) ([]string, error)
	SavePicker func(title, defaultName string) (string, error)
)

func (a *App) SetOpenPicker(p OpenPicker) { a.openPicker.Store(&p) }
func (a *App) SetSavePicker(p SavePicker) { a.savePicker.Store(&p) }

// ---------------------------------------------------------------- clientes

// clientByID acha um cliente visível pelo id da conexão.
func (a *App) clientByID(clientID int) (ts3.ClientInfo, error) {
	for _, cl := range a.client.Snapshot().Clients {
		if int(cl.ID) == clientID {
			return cl, nil
		}
	}
	return ts3.ClientInfo{}, errors.New("cliente não encontrado")
}

func (api *API) SetClientVolume(clientID int, percent float64) error {
	cl, err := api.app.clientByID(clientID)
	if err != nil {
		return err
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 200 {
		percent = 200
	}
	api.app.updateSettings(func(s *Settings) {
		if percent == 100 {
			delete(s.ClientVolumes, cl.UID)
		} else {
			s.ClientVolumes[cl.UID] = percent
		}
	})
	return nil
}

func (api *API) SetClientMuted(clientID int, muted bool) error {
	cl, err := api.app.clientByID(clientID)
	if err != nil {
		return err
	}
	api.app.updateSettings(func(s *Settings) {
		if muted {
			s.ClientMuted[cl.UID] = true
		} else {
			delete(s.ClientMuted, cl.UID)
		}
	})
	return nil
}

// SetContact marca um cliente como amigo, bloqueado ou nenhum dos dois.
// Bloqueados ficam mudos para nós e as mensagens deles são ignoradas.
func (api *API) SetContact(clientID int, status string) error {
	cl, err := api.app.clientByID(clientID)
	if err != nil {
		return err
	}
	if status != "friend" && status != "blocked" {
		status = ""
	}
	api.app.updateSettings(func(s *Settings) {
		if status == "" {
			delete(s.Contacts, cl.UID)
		} else {
			s.Contacts[cl.UID] = status
		}
	})
	return nil
}

func (api *API) MoveClient(clientID, channelID int, password string) error {
	return api.app.client.MoveClient(uint16(clientID), channelID, password)
}

func (api *API) KickClient(clientID int, fromServer bool, reason string) error {
	return api.app.client.KickClient(uint16(clientID), fromServer, reason)
}

func (api *API) BanClient(clientID, seconds int, reason string) error {
	if seconds < 0 {
		seconds = 0
	}
	return api.app.client.BanClient(uint16(clientID), seconds, reason)
}

func (api *API) PokeClient(clientID int, message string) error {
	return api.app.client.PokeClient(uint16(clientID), message)
}

func (api *API) SetClientTalker(clientID int, on bool) error {
	return api.app.client.SetClientTalker(uint16(clientID), on)
}

func (api *API) RequestTalk(on bool, message string) error {
	return api.app.client.RequestTalk(on, message)
}

func (api *API) AddServerGroup(clientID, groupID int) error {
	cl, err := api.app.clientByID(clientID)
	if err != nil {
		return err
	}
	return api.app.client.AddServerGroup(cl.DatabaseID, groupID)
}

func (api *API) RemoveServerGroup(clientID, groupID int) error {
	cl, err := api.app.clientByID(clientID)
	if err != nil {
		return err
	}
	return api.app.client.RemoveServerGroup(cl.DatabaseID, groupID)
}

func (api *API) SetChannelGroup(clientID, groupID int) error {
	cl, err := api.app.clientByID(clientID)
	if err != nil {
		return err
	}
	return api.app.client.SetChannelGroup(cl.DatabaseID, cl.ChannelID, groupID)
}

// SetWhisperTarget passa a enviar a voz só para um cliente ou canal
// (sussurro) em vez do canal atual. kind "" volta ao normal.
func (api *API) SetWhisperTarget(kind string, id int) error {
	w := WhisperDTO{}
	snap := api.app.client.Snapshot()
	switch kind {
	case "client":
		for _, cl := range snap.Clients {
			if int(cl.ID) == id {
				w = WhisperDTO{Kind: kind, ID: id, Name: cl.Nickname}
			}
		}
	case "channel":
		for _, oc := range snap.Channels {
			if oc.Channel.ID == id {
				w = WhisperDTO{Kind: kind, ID: id, Name: oc.Channel.Name}
			}
		}
	case "":
	default:
		return errors.New("alvo de sussurro inválido")
	}
	if kind != "" && w.Kind == "" {
		return errors.New("alvo do sussurro não encontrado")
	}
	api.app.whisper.Store(&w)
	api.app.markDirty()
	return nil
}

// sendVoice manda um quadro de voz: para o canal ou, sussurrando, só para o alvo.
func (a *App) sendVoice(opusData []byte) {
	codec := byte(a.codec.Load())
	w := a.whisper.Load()
	if w == nil || w.Kind == "" {
		a.client.SendVoice(opusData, codec)
		return
	}
	if w.Kind == "client" {
		a.client.SendWhisper(opusData, codec, []uint16{uint16(w.ID)}, nil)
	} else {
		a.client.SendWhisper(opusData, codec, nil, []int{w.ID})
	}
}

// ---------------------------------------------------------------- canais e grupos

func (api *API) CreateChannel(spec ChannelSpec) error {
	s, err := spec.toTS3()
	if err != nil {
		return err
	}
	return api.app.client.CreateChannel(s)
}

func (api *API) EditChannel(channelID int, spec ChannelSpec) error {
	s, err := spec.toTS3()
	if err != nil {
		return err
	}
	return api.app.client.EditChannel(channelID, s)
}

func (api *API) DeleteChannel(channelID int) error {
	return api.app.client.DeleteChannel(channelID)
}

func (api *API) GetGroupPermissions(kind string, groupID int) ([]PermDTO, error) {
	perms, err := api.app.client.GroupPermissions(kind == "server", groupID)
	if err != nil {
		return nil, err
	}
	out := make([]PermDTO, 0, len(perms))
	for _, p := range perms {
		out = append(out, PermDTO{Name: p.Name, Value: p.Value, Negated: p.Negated, Skip: p.Skip})
	}
	return out, nil
}

func (api *API) SetGroupPermission(kind string, groupID int, name string, value int, negated, skip bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("informe o nome da permissão")
	}
	return api.app.client.SetGroupPermission(kind == "server", groupID, ts3.Permission{Name: name, Value: value, Negated: negated, Skip: skip})
}

func (api *API) RemoveGroupPermission(kind string, groupID int, name string) error {
	return api.app.client.RemoveGroupPermission(kind == "server", groupID, name)
}

// ---------------------------------------------------------------- arquivos do canal

func cleanRemotePath(p string) string {
	p = path.Clean("/" + strings.ReplaceAll(p, "\\", "/"))
	return p
}

func (api *API) ListChannelFiles(channelID int, dir, password string) ([]FileEntryDTO, error) {
	files, err := api.app.client.ListFiles(channelID, cleanRemotePath(dir), password)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntryDTO, 0, len(files))
	for _, f := range files {
		out = append(out, FileEntryDTO{Name: f.Name, Size: f.Size, Modified: f.Modified.Unix(), IsDir: f.IsDir})
	}
	return out, nil
}

// DownloadChannelFile baixa um arquivo do canal para onde o usuário escolher.
// Devolve o caminho salvo ("" se ele cancelou).
func (api *API) DownloadChannelFile(channelID int, remote, password string) (string, error) {
	save := api.app.savePicker.Load()
	if save == nil || *save == nil {
		return "", errors.New("seletor de arquivos indisponível")
	}
	remote = cleanRemotePath(remote)
	target, err := (*save)("Salvar arquivo do canal", safeFileName(path.Base(remote)))
	if err != nil || target == "" {
		return "", err
	}
	data, err := api.app.client.DownloadFile(channelID, remote, password)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", fmt.Errorf("não foi possível salvar: %w", err)
	}
	return target, nil
}

// UploadChannelFile envia para o canal os arquivos que o usuário escolher.
func (api *API) UploadChannelFile(channelID int, dir, password string) (int, error) {
	open := api.app.openPicker.Load()
	if open == nil || *open == nil {
		return 0, errors.New("seletor de arquivos indisponível")
	}
	paths, err := (*open)("Enviar arquivo para o canal", true)
	if err != nil || len(paths) == 0 {
		return 0, err
	}
	return api.app.uploadFiles(channelID, dir, password, paths)
}

func (a *App) uploadFiles(channelID int, dir, password string, paths []string) (int, error) {
	sent := 0
	for _, local := range paths {
		info, err := os.Stat(local)
		if err != nil || info.IsDir() {
			return sent, fmt.Errorf("não foi possível abrir %s", filepath.Base(local))
		}
		if info.Size() > 256<<20 {
			return sent, fmt.Errorf("%s é grande demais (máximo 256 MB)", filepath.Base(local))
		}
		data, err := os.ReadFile(local)
		if err != nil {
			return sent, err
		}
		remote := cleanRemotePath(path.Join(dir, filepath.Base(local)))
		if err := a.client.UploadFile(channelID, remote, password, data); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

func (api *API) DeleteChannelFile(channelID int, remote, password string) error {
	return api.app.client.DeleteFile(channelID, cleanRemotePath(remote), password)
}

func (api *API) CreateChannelDirectory(channelID int, remote, password string) error {
	return api.app.client.CreateDirectory(channelID, cleanRemotePath(remote), password)
}

// ---------------------------------------------------------------- apelido, chave, identidade

// SetNickname troca o apelido (agora, se conectado, e nas próximas conexões).
func (api *API) SetNickname(name string) error {
	name = ts3.NormalizeNickname(name)
	if api.app.client.IsConnected() {
		if err := api.app.client.SetNickname(name); err != nil {
			return err
		}
	}
	api.app.updateSettings(func(s *Settings) { s.Nickname = name })
	return nil
}

func (api *API) UsePrivilegeKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("informe a chave de privilégio")
	}
	return api.app.client.UsePrivilegeKey(key)
}

// ---------------------------------------------------------------- favoritos e reconexão

func (api *API) SaveBookmark(b Bookmark) (string, error) {
	b.Host = strings.TrimSpace(b.Host)
	if b.Host == "" {
		return "", errors.New("informe o endereço do servidor")
	}
	if strings.TrimSpace(b.Name) == "" {
		b.Name = b.Host
	}
	if b.ID == "" {
		b.ID = fmt.Sprintf("b%x", time.Now().UnixNano())
	}
	api.app.updateSettings(func(s *Settings) {
		for i := range s.Bookmarks {
			if s.Bookmarks[i].ID == b.ID {
				s.Bookmarks[i] = b
				return
			}
		}
		s.Bookmarks = append(s.Bookmarks, b)
	})
	return b.ID, nil
}

func (api *API) DeleteBookmark(id string) {
	api.app.updateSettings(func(s *Settings) {
		kept := s.Bookmarks[:0:0]
		for _, b := range s.Bookmarks {
			if b.ID != id {
				kept = append(kept, b)
			}
		}
		s.Bookmarks = kept
	})
}

func (api *API) ConnectBookmark(id string) error {
	api.app.mu.Lock()
	var found *Bookmark
	for i := range api.app.settings.Bookmarks {
		if api.app.settings.Bookmarks[i].ID == id {
			b := api.app.settings.Bookmarks[i]
			found = &b
		}
	}
	nickname := api.app.settings.Nickname
	api.app.mu.Unlock()
	if found == nil {
		return errors.New("favorito não encontrado")
	}
	if found.Nickname != "" {
		nickname = found.Nickname
	}
	return api.app.connect(ConnectRequest{Host: found.Host, Nickname: nickname, DefaultChannel: found.DefaultChannel, IdentityID: found.IdentityID})
}

// Modos de Settings.RemoteImages.
const (
	ImagesAsk    = "ask"
	ImagesAlways = "always"
	ImagesNever  = "never"
)

// maxImageServers limita a lista de servidores com imagens liberadas.
const maxImageServers = 500

// SetRemoteImages escolhe o que fazer com as imagens de fora: "ask" (só com
// um clique, ou sozinhas nos servidores liberados), "always" ou "never".
func (api *API) SetRemoteImages(mode string) error {
	if mode != ImagesAsk && mode != ImagesAlways && mode != ImagesNever {
		return errors.New("opção de imagens desconhecida")
	}
	api.app.updateSettings(func(s *Settings) { s.RemoteImages = mode })
	return nil
}

// SetServerImages liga ou desliga o carregamento automático das imagens no
// servidor em que estamos (guardado pela identidade do servidor).
func (api *API) SetServerImages(on bool) error {
	uid := api.app.client.Snapshot().Server.UID
	if !api.app.client.IsConnected() || uid == "" {
		return errors.New("conecte-se ao servidor para escolher isto")
	}
	api.app.updateSettings(func(s *Settings) {
		if !on {
			delete(s.ImageServers, uid)
		} else if len(s.ImageServers) < maxImageServers {
			s.ImageServers[uid] = true
		}
	})
	return nil
}

// SetPokePopup escolhe se uma cutucada abre uma janela (true) ou só toca o
// aviso sonoro e aparece no registro do servidor (false).
func (api *API) SetPokePopup(on bool) {
	api.app.updateSettings(func(s *Settings) { s.PokePopup = on })
}

func (api *API) SetAutoReconnect(on bool) {
	api.app.updateSettings(func(s *Settings) { s.AutoReconnect = on })
	if !on {
		api.app.cancelReconnect(false)
	}
}

// cancelReconnect interrompe as tentativas de reconexão. userAction indica
// que foi o usuário quem pediu para desconectar.
func (a *App) cancelReconnect(userAction bool) {
	a.mu.Lock()
	if a.reconnectTimer != nil {
		a.reconnectTimer.Stop()
		a.reconnectTimer = nil
	}
	a.reconnectAttempt = 0
	if userAction {
		a.userDisconnect = true
	}
	if a.status == statusReconnecting {
		a.status = statusDisconnected
	}
	a.mu.Unlock()
	a.markDirty()
}

// scheduleReconnectLocked agenda a próxima tentativa, com espera crescente.
// reason é o motivo da última falha. Chamar com a.mu travado. Devolve a espera.
func (a *App) scheduleReconnectLocked(reason string) time.Duration {
	a.reconnectAttempt++
	delay := time.Duration(1<<min(a.reconnectAttempt, 5)) * time.Second // 2, 4, 8, 16, 32 s
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	// Quando o servidor responde recusando (em vez de não responder), insistir
	// rápido demais aciona a proteção anti-flood dele.
	if strings.Contains(reason, "recusou") && delay < 6*time.Second {
		delay = 6 * time.Second
	}
	if strings.Contains(reason, "flood") {
		delay = 30 * time.Second
	}
	if a.opts.ReconnectDelay > 0 {
		delay = a.opts.ReconnectDelay
	}
	a.status = statusReconnecting
	a.reconnectTimer = time.AfterFunc(delay, a.reconnectNow)
	return delay
}

func (a *App) reconnectNow() {
	a.mu.Lock()
	if a.status != statusReconnecting || a.userDisconnect {
		a.mu.Unlock()
		return
	}
	req := a.lastRequest
	a.mu.Unlock()

	err := a.client.Connect(ts3.ConnectOptions{
		Host: req.Host, Nickname: ts3.NormalizeNickname(req.Nickname), ServerPassword: req.ServerPassword,
		DefaultChannel: req.DefaultChannel, ChannelPassword: req.ChannelPassword,
	})
	if err != nil {
		a.onDisconnected(err.Error(), true) // conta como tentativa falha
	}
}

// ---------------------------------------------------------------- atalhos e dispositivos

func (a *App) onHotkey(action string, down bool) {
	switch action {
	case "ptt":
		a.engine.SetPTT(down)
	case "toggleMute":
		if down {
			a.API().ToggleMute()
		}
	case "toggleDeafen":
		if down {
			a.API().ToggleDeafen()
		}
	case "soundStop":
		if down {
			a.API().SoundboardStop()
		}
	default:
		if id, ok := strings.CutPrefix(action, soundHotkeyPrefix); ok && down {
			if err := a.API().SoundboardPlay(id); err != nil {
				a.addLog("error", "Soundboard: "+err.Error())
			}
		}
	}
}

// validHotkeyAction diz se action é uma ação que aceita atalho: as fixas ou
// "sound:<id>" de um som que existe no soundboard.
func (a *App) validHotkeyAction(action string) bool {
	switch action {
	case "ptt", "toggleMute", "toggleDeafen", "soundStop":
		return true
	}
	id, ok := strings.CutPrefix(action, soundHotkeyPrefix)
	if !ok {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.settings.Sounds {
		if s.ID == id {
			return true
		}
	}
	return false
}

// StartHotkeys registra os atalhos no sistema. Só pode ser chamado com a
// janela já em execução.
func (a *App) StartHotkeys() {
	errs := a.hotkeys.Start()
	a.mu.Lock()
	for action, err := range errs {
		a.hotkeyErrs[action] = err.Error()
	}
	a.mu.Unlock()
	a.markDirty()
}

// SetHotkey define (ou, com combo vazio, remove) o atalho de uma ação: "ptt",
// "toggleMute", "toggleDeafen", "soundStop" ou "sound:<id>" para tocar um som
// do soundboard.
func (api *API) SetHotkey(action, combo string) error {
	a := api.app
	if !a.validHotkeyAction(action) {
		return errors.New("ação de atalho desconhecida")
	}
	err := a.hotkeys.Set(action, combo)
	a.mu.Lock()
	switch {
	case err != nil:
		a.hotkeyErrs[action] = err.Error()
	case combo == "" && strings.HasPrefix(action, soundHotkeyPrefix):
		delete(a.hotkeyErrs, action)
		delete(a.settings.Hotkeys, action) // som sem atalho não deixa rastro nos ajustes
		a.scheduleSaveLocked()
	default:
		delete(a.hotkeyErrs, action)
		a.settings.Hotkeys[action] = combo
		a.scheduleSaveLocked()
	}
	a.mu.Unlock()
	a.markDirty()
	return err
}

// SetPTT é o apertar-para-falar vindo da própria janela (quando não há
// atalho global).
func (api *API) SetPTT(down bool) { api.app.engine.SetPTT(down) }

func (api *API) SetInputDevice(name string) error {
	err := api.app.engine.SetInputDevice(name)
	api.app.updateSettings(func(s *Settings) { s.InputDevice = name })
	return err
}

func (api *API) SetOutputDevice(name string) error {
	err := api.app.engine.SetOutputDevice(name)
	api.app.updateSettings(func(s *Settings) { s.OutputDevice = name })
	return err
}

// ---------------------------------------------------------------- histórico

type historyLine struct {
	Time        int64  `json:"t"`
	TargetMode  int    `json:"m"`
	InvokerName string `json:"n"`
	Text        string `json:"x"`
	Self        bool   `json:"s,omitempty"`
}

const historyLoad = 200

// Limites do histórico em disco. Quem escreve nele são os outros (e o
// servidor): sem limite, um servidor mal-intencionado mandando mensagens sem
// parar encheria o disco do usuário.
const (
	maxHistoryText = 8 << 10 // bytes guardados de cada mensagem
	maxHistoryName = 256     // bytes guardados do apelido de quem escreveu
	maxHistoryFile = 2 << 20 // bytes por servidor; ao passar disso, ficam só as mensagens mais recentes
)

// clipBytes corta s em no máximo max bytes, sem partir um caractere ao meio.
func clipBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func (a *App) historyFile(key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(a.historyDir, hex.EncodeToString(sum[:8])+".jsonl")
}

// saveHistory acrescenta uma mensagem de canal ou de servidor ao histórico
// do servidor atual.
func (a *App) saveHistory(m Message) {
	a.mu.Lock()
	key := a.historyKey
	a.mu.Unlock()
	if key == "" || m.Kind != "chat" || (m.TargetMode != 2 && m.TargetMode != 3) {
		return
	}
	line, err := json.Marshal(historyLine{Time: m.Time, TargetMode: m.TargetMode,
		InvokerName: clipBytes(m.InvokerName, maxHistoryName), Text: clipBytes(m.Text, maxHistoryText), Self: m.Self})
	if err != nil || os.MkdirAll(a.historyDir, 0o700) != nil {
		return
	}
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	file := a.historyFile(key)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(append(line, '\n'))
	info, err := f.Stat()
	f.Close()
	if err == nil && info.Size() > maxHistoryFile {
		trimHistory(file)
	}
}

// trimHistory reduz um histórico que passou do limite às mensagens mais
// recentes: no máximo historyLoad (as que a interface mostra ao reconectar) e
// metade do limite em bytes. Grava num arquivo ao lado e troca, para uma queda
// no meio não deixar o histórico pela metade.
func trimHistory(file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	start, size := len(lines), 0
	for start > 0 && len(lines)-start < historyLoad && size+len(lines[start-1])+1 <= maxHistoryFile/2 {
		start--
		size += len(lines[start]) + 1
	}
	kept := bytes.Join(lines[start:], []byte("\n"))
	if len(kept) > 0 {
		kept = append(kept, '\n')
	}
	tmp := file + ".tmp"
	if os.WriteFile(tmp, kept, 0o600) != nil {
		return
	}
	if os.Rename(tmp, file) != nil {
		os.Remove(tmp)
	}
}

// loadHistory entrega à interface as últimas mensagens guardadas do servidor.
func (a *App) loadHistory(key string) {
	a.historyMu.Lock()
	lines := readHistory(a.historyFile(key))
	a.historyMu.Unlock()
	for _, l := range lines {
		a.addMessage(Message{Kind: "chat", Level: "info", History: true, HistoryTime: l.Time,
			TargetMode: l.TargetMode, InvokerName: l.InvokerName, Text: l.Text, Self: l.Self})
	}
}

// readHistory lê as últimas historyLoad mensagens de um arquivo de histórico.
func readHistory(file string) []historyLine {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []historyLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var l historyLine
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Text != "" {
			lines = append(lines, l)
			if len(lines) > historyLoad*2 {
				lines = append(lines[:0], lines[len(lines)-historyLoad:]...)
			}
		}
	}
	if len(lines) > historyLoad {
		lines = lines[len(lines)-historyLoad:]
	}
	return lines
}

// ClearHistory apaga todo o histórico de conversas guardado.
func (api *API) ClearHistory() error {
	api.app.historyMu.Lock()
	defer api.app.historyMu.Unlock()
	entries, err := os.ReadDir(api.app.historyDir)
	if err != nil {
		return nil // não há histórico
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			os.Remove(filepath.Join(api.app.historyDir, e.Name()))
		}
	}
	return nil
}

// safeFileName torna um nome vindo do servidor aceitável como nome de arquivo
// em qualquer sistema: sem separadores de pasta, sem os caracteres que o
// Windows proíbe e sem os nomes reservados dele (CON, NUL, COM1…).
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, r == 0x7f, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteByte('_')
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			// Caracteres que invertem a direção do texto: com eles
			// "fotos<U+202E>gnp.exe" apareceria como "fotosexe.png" na
			// janela de salvar. Ficam de fora.
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	base := strings.ToUpper(out)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		out = "_" + out
	}
	if out == "" {
		return "arquivo"
	}
	return out
}
