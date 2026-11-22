package backend

import (
	"errors"
	"fmt"
	"time"

	"furito-ts/audio"
)

// ---- Tipos trocados com a interface (o contrato do frontend) ----

type State struct {
	// Tab é a conexão (aba) que este estado descreve; o que é do app (áudio,
	// identidades, favoritos, atalhos) vem junto, igual em todas.
	Tab    int    `json:"tab"`
	Status string `json:"status"` // disconnected | connecting | connected | reconnecting
	Error  string `json:"error"`
	Host   string `json:"host"`

	ReconnectAttempt int                `json:"reconnectAttempt"` // 0 = não está reconectando
	AutoReconnect    bool               `json:"autoReconnect"`
	Identity         IdentityDTO        `json:"identity"`
	Identities       []IdentityEntryDTO `json:"identities"`
	AppVersion       string             `json:"appVersion"` // só o número (ex.: 1.0.0)
	AppAuthor        string             `json:"appAuthor"`
	AppSite          string             `json:"appSite"` // site do autor
	Screen           ScreenDTO          `json:"screen"`
	Platform         string             `json:"platform"`  // darwin | windows | linux
	PokePopup        bool               `json:"pokePopup"` // cutucadas abrem uma janela (senão, só o som)
	Images           ImagesDTO          `json:"images"`    // imagens de fora em descrições e no banner
	Groups           GroupsDTO          `json:"groups"`
	Bookmarks        []Bookmark         `json:"bookmarks"`
	Hotkeys          HotkeysDTO         `json:"hotkeys"`

	Server   ServerDTO    `json:"server"`
	Self     SelfDTO      `json:"self"`
	Channels []ChannelDTO `json:"channels"` // em ordem de exibição
	Clients  []ClientDTO  `json:"clients"`  // ordenados por apelido
	Audio    AudioDTO     `json:"audio"`
}

type ServerDTO struct {
	Name           string `json:"name"`
	WelcomeMessage string `json:"welcomeMessage"`
	Version        string `json:"version"`
	Platform       string `json:"platform"`
	MaxClients     int    `json:"maxClients"`
	UID            string `json:"uid"`
	BannerURL      string `json:"bannerUrl"`
	BannerLink     string `json:"bannerLink"`
}

type SelfDTO struct {
	ClientID    int    `json:"clientId"`
	ChannelID   int    `json:"channelId"`
	Nickname    string `json:"nickname"`
	Muted       bool   `json:"muted"`
	Deafened    bool   `json:"deafened"`
	Away        bool   `json:"away"`
	AwayMessage string `json:"awayMessage"`

	Whisper       WhisperDTO `json:"whisper"`
	TalkRequested bool       `json:"talkRequested"`

	// RecordingSince: quando a gravação da conversa começou (unix ms); 0 = não
	// está gravando.
	RecordingSince int64 `json:"recordingSince"`
}

type ChannelDTO struct {
	ID              int    `json:"id"`
	ParentID        int    `json:"parentId"`
	Depth           int    `json:"depth"`
	Name            string `json:"name"`
	Topic           string `json:"topic"`
	Description     string `json:"description"`
	Codec           int    `json:"codec"`
	CodecQuality    int    `json:"codecQuality"`
	MaxClients      int    `json:"maxClients"`
	IsDefault       bool   `json:"isDefault"`
	HasPassword     bool   `json:"hasPassword"`
	IsPermanent     bool   `json:"isPermanent"`
	IsSemiPermanent bool   `json:"isSemiPermanent"`
	NeededTalkPower int    `json:"neededTalkPower"`
	IconID          uint32 `json:"iconId"`
	IsTemporary     bool   `json:"isTemporary"`
}

type GroupDTO struct {
	Name   string `json:"name"`
	IconID uint32 `json:"iconId"`
}

type ClientDTO struct {
	ID             int      `json:"id"`
	ChannelID      int      `json:"channelId"`
	Nickname       string   `json:"nickname"`
	UID            string   `json:"uid"`
	DatabaseID     int      `json:"databaseId"`
	Type           int      `json:"type"`
	InputMuted     bool     `json:"inputMuted"`
	OutputMuted    bool     `json:"outputMuted"`
	Away           bool     `json:"away"`
	AwayMessage    string   `json:"awayMessage"`
	TalkPower      int      `json:"talkPower"`
	IsRecording    bool     `json:"isRecording"`
	Country        string   `json:"country"`
	ServerGroups   []string `json:"serverGroups"`
	ChannelGroup   string   `json:"channelGroup"`
	Version        string   `json:"version"`
	Platform       string   `json:"platform"`
	Furito         bool     `json:"furito"`         // é um cliente Furito (anunciou a versão em client_meta_data)
	ConnectedSince int64    `json:"connectedSince"` // unix ms; 0 = desconhecido
	Description    string   `json:"description"`
	// Avatar: marca do avatar do cliente ("" = não tem). A imagem vem por
	// GetAvatar; a marca muda quando o avatar muda.
	Avatar string `json:"avatar"`

	// NamePrefix/NameSuffix: nomes de grupos mostrados junto do apelido na
	// árvore ("[1850+] " antes, " [Staff]" depois), conforme o grupo.
	NamePrefix string `json:"namePrefix"`
	NameSuffix string `json:"nameSuffix"`

	Icons              []IconRef  `json:"icons"`  // à direita do apelido, na ordem do TeamSpeak
	Groups             []GroupDTO `json:"groups"` // grupos do servidor, com ícone
	ChannelGroupIcon   uint32     `json:"channelGroupIcon"`
	InputHardware      bool       `json:"inputHardware"`
	OutputHardware     bool       `json:"outputHardware"`
	IsChannelCommander bool       `json:"isChannelCommander"`
	IsPrioritySpeaker  bool       `json:"isPrioritySpeaker"`
	TalkRequest        bool       `json:"talkRequest"`
	TalkRequestMessage string     `json:"talkRequestMessage"`
	IsTalker           bool       `json:"isTalker"`
	ServerGroupIDs     []int      `json:"serverGroupIds"`
	ChannelGroupID     int        `json:"channelGroupId"`

	// Preferências locais (por identidade do cliente).
	Volume       float64 `json:"volume"` // 0..200, 100 = normal
	MutedLocally bool    `json:"mutedLocally"`
	Contact      string  `json:"contact"` // "" | friend | blocked
}

type AudioDTO struct {
	Effect  string             `json:"effect"`
	Effects []audio.EffectInfo `json:"effects"` // vozes disponíveis, na ordem do menu

	VADMode                   string  `json:"vadMode"` // auto | level
	VADThreshold              float64 `json:"vadThreshold"`
	VADFloor                  float64 `json:"vadFloor"` // detecção de voz: nível mínimo para abrir o microfone (0 = sem piso)
	NoiseSuppression          bool    `json:"noiseSuppression"`
	NoiseMode                 string  `json:"noiseMode"` // tipo da supressão: neural (RNNoise) | classic
	AutoGain                  bool    `json:"autoGain"`
	EchoCancellation          bool    `json:"echoCancellation"`
	EchoCancellationAvailable bool    `json:"echoCancellationAvailable"`
	NormalizeVoices           bool    `json:"normalizeVoices"`
	MicTest                   bool    `json:"micTest"`
	// MicMode é o "Modo do Microfone" do macOS: standard | wide | isolation
	// ("" se indisponível). Só vale com o cancelamento de eco ligado.
	MicMode string `json:"micMode"`

	OutputVolume      float64    `json:"outputVolume"`
	SoundboardVolume  float64    `json:"soundboardVolume"`
	SoundboardPlaying bool       `json:"soundboardPlaying"`
	Sounds            []SoundDTO `json:"sounds"`

	CaptureAvailable bool   `json:"captureAvailable"`
	InputDevice      string `json:"inputDevice"`
	OutputDevice     string `json:"outputDevice"`

	// Avisos sonoros do TeamSpeak (pacotes do cliente TS3 instalado).
	NotifySounds bool            `json:"notifySounds"`
	NotifyPack   string          `json:"notifyPack"`
	NotifyPacks  []NotifyPackDTO `json:"notifyPacks"` // vazio = nenhum pacote de sons guardado

	InputDevices  []audio.DeviceInfo `json:"inputDevices"`
	OutputDevices []audio.DeviceInfo `json:"outputDevices"`
	InputChoice   string             `json:"inputChoice"` // "" = padrão do sistema
	OutputChoice  string             `json:"outputChoice"`
}

type NotifyPackDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SoundDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
	// Hotkey: atalho global que toca este som ("" = nenhum); HotkeyError: por
	// que o sistema não aceitou registrá-lo.
	Hotkey      string `json:"hotkey"`
	HotkeyError string `json:"hotkeyError"`
}

type Message struct {
	ID          int    `json:"id"`
	Tab         int    `json:"tab"`        // a conexão (aba) a que a mensagem pertence
	Time        int64  `json:"time"`       // unix ms
	Kind        string `json:"kind"`       // log | chat
	Level       string `json:"level"`      // info | success | warn | error
	TargetMode  int    `json:"targetMode"` // 0 log, 1 privado, 2 canal, 3 servidor
	PeerID      int    `json:"peerId"`
	PeerName    string `json:"peerName"`
	InvokerID   int    `json:"invokerId"`
	InvokerName string `json:"invokerName"`
	Text        string `json:"text"`
	Self        bool   `json:"self"`
	History     bool   `json:"history"` // veio do histórico salvo
	HistoryTime int64  `json:"-"`
	// Rich: linha de registro cujo texto é BBCode de verdade (a mensagem de
	// boas-vindas). Nas demais a interface só reconhece o [b] dos nomes.
	Rich bool `json:"rich,omitempty"`
}

// ImagesDTO diz à interface o que fazer com as imagens de fora ([img] nas
// descrições e na mensagem de boas-vindas, e o banner do servidor).
type ImagesDTO struct {
	Mode    string `json:"mode"`    // ask | always | never
	Trusted bool   `json:"trusted"` // o usuário mandou carregar sempre as deste servidor
}

type Talking struct {
	Clients   []int   `json:"clients"`
	Self      bool    `json:"self"`
	MicLevel  float64 `json:"micLevel"`
	VoiceProb float64 `json:"voiceProb"` // 0..1: o quanto o detector vê voz
}

type ConnectRequest struct {
	Host            string `json:"host"`
	Nickname        string `json:"nickname"`
	ServerPassword  string `json:"serverPassword"`
	DefaultChannel  string `json:"defaultChannel"`
	ChannelPassword string `json:"channelPassword"`
	IdentityID      string `json:"identityId"` // "" = a identidade padrão
}

type ConnectSettings struct {
	Host           string `json:"host"`
	Nickname       string `json:"nickname"`
	DefaultChannel string `json:"defaultChannel"`
}

// API é o conjunto de métodos que a interface pode chamar. Cada valor age
// numa conexão (c): a interface chega aqui por Bridge.Call, que diz de que aba
// é o pedido. Só o que a UI pode chamar fica aqui.
type API struct {
	app *App
	c   *conn
}

// API devolve a API da conexão que está à vista.
func (a *App) API() *API { return a.current().api() }

func (c *conn) api() *API { return &API{app: c.app, c: c} }

func (api *API) GetState() State { return api.c.buildState() }

func (api *API) GetMessages() []Message {
	api.app.mu.Lock()
	defer api.app.mu.Unlock()
	return append([]Message{}, api.c.messages...)
}

func (api *API) GetSettings() ConnectSettings {
	api.app.mu.Lock()
	defer api.app.mu.Unlock()
	s := api.app.settings
	return ConnectSettings{Host: s.Host, Nickname: s.Nickname, DefaultChannel: s.DefaultChannel}
}

func (api *API) Connect(req ConnectRequest) error { return api.c.connect(req) }

// Disconnect sai do servidor (e desiste de reconectar, se estava tentando).
func (api *API) Disconnect() {
	api.c.cancelReconnect(true)
	api.c.client.Disconnect()
}

func (api *API) JoinChannel(channelID int, password string) {
	api.c.client.JoinChannel(channelID, password)
}

func (api *API) SendText(targetMode, target int, text string) error {
	if text == "" {
		return nil
	}
	if targetMode < 1 || targetMode > 3 {
		return errors.New("destino de mensagem inválido")
	}
	if !api.c.client.IsConnected() {
		return errors.New("não conectado")
	}
	api.c.client.SendTextMessage(targetMode, target, text)
	return nil
}

func (api *API) ToggleMute() bool {
	muted := !api.app.engine.Muted()
	api.app.engine.SetMuted(muted)
	api.app.publishMuteState()
	if muted {
		api.app.notify("SOUND_CAPTURE_MUTED")
	} else {
		api.app.notify("SOUND_CAPTURE_UNMUTED")
	}
	return muted
}

func (api *API) ToggleDeafen() bool {
	deafened := !api.app.engine.Deafened()
	api.app.engine.SetDeafened(deafened)
	api.app.publishMuteState()
	if deafened {
		api.app.notify("SOUND_PLAYBACK_MUTED")
	} else {
		api.app.notify("SOUND_PLAYBACK_UNMUTED")
	}
	return deafened
}

func (api *API) SetAway(away bool, message string) {
	api.app.mu.Lock()
	changed := api.app.away != away
	api.app.away = away
	api.app.awayMessage = message
	api.app.mu.Unlock()
	for _, c := range api.app.connList() { // ausente vale para o app inteiro
		c.client.SetAway(away, message)
	}
	api.app.markDirty()
	if changed && away {
		api.app.notify("STATUS_SET_AWAY")
	} else if changed {
		api.app.notify("STATUS_SET_PRESENT")
	}
}

// GetIcon devolve a imagem (data URL) de um ícone guardado no servidor.
func (api *API) GetIcon(id int64) (string, error) { return api.c.icon(uint32(id)) }

// GetAvatar devolve a imagem (data URL) do avatar de um cliente.
func (api *API) GetAvatar(clientID int) (string, error) { return api.c.avatar(clientID) }

// SetNotifySounds liga/desliga os avisos sonoros.
func (api *API) SetNotifySounds(on bool) {
	api.app.updateSettings(func(s *Settings) { s.NotifySounds = on })
}

// SetNotifyPack escolhe o pacote de avisos (ex.: "default", "default_male").
func (api *API) SetNotifyPack(id string) {
	api.app.updateSettings(func(s *Settings) { s.NotifyPack = id })
	api.app.notify("SOUND_PLAYBACK_UNMUTED") // amostra do pacote escolhido
}

func (api *API) SetEffect(effect string) {
	api.app.engine.SetEffect(audio.VoiceEffect(effect))
	applied := string(api.app.engine.Effect())
	api.app.updateSettings(func(s *Settings) { s.Effect = applied })
}

func (api *API) SetVADThreshold(value float64) {
	if value < 50 {
		value = 50
	}
	if value > 8000 {
		value = 8000
	}
	api.app.engine.SetVADThreshold(value)
	api.app.updateSettings(func(s *Settings) { s.VADThreshold = value })
}

// SetVADFloor define a sensibilidade da detecção de voz: o nível, na escala do
// medidor do microfone, que a voz precisa atingir para abrir o microfone. Zero
// desliga o piso.
func (api *API) SetVADFloor(value float64) {
	api.app.engine.SetVADFloor(value)
	applied := api.app.engine.VADFloor()
	api.app.updateSettings(func(s *Settings) { s.VADFloor = applied })
}

// VoiceCalibration é o resultado de CalibrateVoice.
type VoiceCalibration struct {
	Voice float64 `json:"voice"` // nível da voz medida
	Floor float64 `json:"floor"` // sensibilidade escolhida para ela
}

// calibrationTime é quanto dura a medida de CalibrateVoice.
var calibrationTime = 5 * time.Second

// CalibrateVoice ouve o microfone por alguns segundos, com a pessoa falando
// normalmente, e ajusta a sensibilidade da detecção de voz para a voz dela: o
// que chega bem mais fraco (a TV, alguém no cômodo) deixa de abrir o
// microfone. Nada é transmitido durante a medida. Funciona mesmo desconectado
// (o microfone é aberto só para medir). Sem voz suficiente devolve erro e a
// sensibilidade fica como estava.
func (api *API) CalibrateVoice() (VoiceCalibration, error) {
	a := api.app
	if !a.calibrating.CompareAndSwap(false, true) {
		return VoiceCalibration{}, errors.New("a calibração já está em andamento")
	}
	defer a.calibrating.Store(false)

	opened := false
	if !a.opts.DisableAudioDevices && !a.engine.CaptureAvailable() {
		if err := a.engine.StartCapture(); err != nil {
			return VoiceCalibration{}, fmt.Errorf("não consegui abrir o microfone: %w", err)
		}
		opened = true
		a.markDirty()
	}
	voice, floor, err := a.engine.CalibrateVoice(calibrationTime)
	if opened && !a.anyConnected() && !a.engine.MicTest() {
		a.engine.StopCapture()
		a.markDirty()
	}
	if err != nil {
		return VoiceCalibration{}, errors.New("não ouvi voz suficiente: fale normalmente durante a medida")
	}
	a.engine.SetVADFloor(floor)
	applied := a.engine.VADFloor()
	a.updateSettings(func(s *Settings) { s.VADFloor = applied })
	return VoiceCalibration{Voice: voice, Floor: applied}, nil
}

func (api *API) SetOutputVolume(percent float64) {
	api.app.engine.SetVolume(percent)
	applied := api.app.engine.Volume()
	api.app.updateSettings(func(s *Settings) { s.OutputVolume = applied })
}

// SetVADMode escolhe "auto" (detecção de voz) ou "level" (limiar de volume).
func (api *API) SetVADMode(mode string) {
	api.app.engine.SetVADMode(mode)
	applied := api.app.engine.VADMode()
	api.app.updateSettings(func(s *Settings) { s.VADMode = applied })
}

// SetNoiseSuppression liga ou desliga a supressão de ruído do microfone.
func (api *API) SetNoiseSuppression(on bool) {
	api.app.updateSettings(func(s *Settings) {
		s.NoiseSuppression = on
		api.app.engine.SetNoiseMode(s.noiseMode())
	})
}

// SetNoiseMode escolhe o tipo de supressão de ruído: "neural" (RNNoise, que
// tira também digitação e ruídos que mudam depressa) ou "classic" (a
// espectral, só para ruído constante). Vale quando a supressão está ligada.
func (api *API) SetNoiseMode(mode string) {
	if mode != audio.NoiseClassic {
		mode = audio.NoiseNeural
	}
	api.app.updateSettings(func(s *Settings) {
		s.NoiseMode = mode
		api.app.engine.SetNoiseMode(s.noiseMode())
	})
}

func (api *API) SetAutoGain(on bool) {
	api.app.engine.SetAutoGain(on)
	api.app.updateSettings(func(s *Settings) { s.AutoGain = on })
}

func (api *API) SetNormalizeVoices(on bool) {
	api.app.engine.SetNormalizeVoices(on)
	api.app.updateSettings(func(s *Settings) { s.NormalizeVoices = on })
}

// SetEchoCancellation liga o processamento de voz do macOS. Se o sistema
// recusar, volta ao áudio padrão e devolve o motivo.
func (api *API) SetEchoCancellation(on bool) error {
	err := api.app.engine.SetEchoCancellation(on)
	applied := api.app.engine.EchoCancellation()
	api.app.updateSettings(func(s *Settings) { s.EchoCancellation = applied })
	if err != nil {
		api.c.addLog("warn", err.Error())
	}
	return err
}

// ShowMicModes abre o seletor "Modo do Microfone" do macOS (onde fica o
// Isolamento de Voz). O sistema só o oferece com o cancelamento de eco ligado.
func (api *API) ShowMicModes() { audio.ShowMicrophoneModes() }

// SetMicTest liga o retorno da própria voz (tratada e com efeito) para ouvir
// como os outros vão escutar. Funciona mesmo desconectado.
func (api *API) SetMicTest(on bool) {
	api.app.engine.SetMicTest(on)
	switch {
	case on && !api.app.opts.DisableAudioDevices:
		go api.app.startAudio()
	case !on && !api.app.anyConnected():
		api.app.engine.StopCapture()
	}
	api.app.markDirty()
}

// SoundboardPlay toca um som pelo id (embutido ou adicionado pelo usuário).
func (api *API) SoundboardPlay(id string) error {
	if !api.app.opts.DisableAudioDevices {
		// Permite ouvir o som mesmo desconectado.
		if err := api.app.engine.StartPlayback(); err != nil {
			return err
		}
	}
	if err := api.app.playSound(id); err != nil {
		return err
	}
	api.app.markDirty()
	return nil
}

func (api *API) SoundboardStop() {
	api.app.engine.Soundboard().StopSound()
	api.app.markDirty()
}

func (api *API) SetSoundboardVolume(percent float64) {
	api.app.engine.Soundboard().SetVolume(percent)
	applied := api.app.engine.Soundboard().Volume()
	api.app.updateSettings(func(s *Settings) { s.SoundboardVolume = applied })
}

// SoundboardAddFiles abre o seletor de arquivos e adiciona os áudios
// escolhidos. Devolve quantos entraram.
func (api *API) SoundboardAddFiles() (int, error) {
	pick := api.app.picker.Load()
	if pick == nil || *pick == nil {
		return 0, errors.New("seletor de arquivos indisponível")
	}
	paths, err := (*pick)()
	if err != nil || len(paths) == 0 {
		return 0, err
	}
	return api.app.addSoundFiles(paths)
}

func (api *API) SoundboardRemove(id string) error { return api.app.removeSound(id) }

func (api *API) SoundboardRename(id, name string) error { return api.app.renameSound(id, name) }

func (api *API) RequestChannelInfo(channelID int) {
	api.c.client.RequestChannelDescription(channelID)
}

func (api *API) RequestClientInfo(clientID int) {
	if clientID > 0 && clientID <= 0xFFFF {
		api.c.client.RequestClientInfo(uint16(clientID))
	}
}
