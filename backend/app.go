// Package backend liga o cliente TeamSpeak, o motor de áudio e a interface.
// Não depende do Wails: a janela entra só como um Emitter de eventos.
package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"furito-ts/audio"
	"furito-ts/hotkey"
	"furito-ts/screencap"
	"furito-ts/ts3"
)

// Emitter entrega eventos para a interface.
type Emitter interface {
	Emit(event string, data any)
}

// Options configura o App.
type Options struct {
	IdentityPath string // padrão: ~/.ts_furito_identity.json
	SettingsPath string // padrão: <config do usuário>/FuritoTS/settings.json
	// DisableAudioDevices impede abrir microfone/alto-falantes (testes).
	DisableAudioDevices bool
	// SoundPacksZip: os pacotes de sons dos avisos que vêm com o app, num zip
	// com soundpacks/<pacote>/ (settings.ini e os .wav). Vazio = nenhum.
	SoundPacksZip []byte
	// ExampleScript: o código do script de exemplos que entra na lista de
	// scripts na primeira abertura (frontend/script-examples.js). Vazio = nenhum.
	ExampleScript string
	// Debug imprime o tráfego de comandos (sem senhas).
	Debug bool
	// ReconnectDelay fixa a espera entre tentativas de reconexão (testes).
	ReconnectDelay time.Duration
	// RecordingsDir: onde ficam as gravações de conversa.
	// Padrão: ~/Music/Furito TS.
	RecordingsDir string
}

// FilePicker abre o seletor de arquivos do sistema e devolve os escolhidos.
type FilePicker func() ([]string, error)

const (
	statusDisconnected = "disconnected"
	statusConnecting   = "connecting"
	statusConnected    = "connected"
	statusReconnecting = "reconnecting"

	maxReconnectAttempts = 12

	maxMessages = 1000
)

type App struct {
	opts       Options
	client     *ts3.Client
	engine     *audio.Engine
	emitter    atomic.Pointer[emitterBox]
	codec      atomic.Uint32 // codec do canal atual (4 = Opus Voice, 5 = Opus Music)
	picker     atomic.Pointer[FilePicker]
	openPicker atomic.Pointer[OpenPicker]
	dirPicker  atomic.Pointer[DirPicker]
	quit       atomic.Pointer[func()]
	savePicker atomic.Pointer[SavePicker]
	whisper    atomic.Pointer[WhisperDTO]
	hotkeys    *hotkey.Manager
	historyDir string
	historyMu  sync.Mutex // um de cada vez nos arquivos de histórico

	identMu      sync.Mutex
	identity     *ts3.Identity    // a identidade em uso (a de identActive)
	idents       []*identityEntry // todas as identidades
	identDefault string           // id da padrão
	identActive  string           // id da que está em uso

	soundDir  string
	iconDir   string
	avatarDir string
	packs     []soundPack
	packDir   string // cópia dos pacotes de sons, na pasta de dados do app
	scripts   scriptStore

	iconMu   sync.Mutex
	icons    map[uint32]string // id → data URL ("" = falhou nesta conexão)
	iconWait map[uint32]chan struct{}
	avatars  map[string]string // uid + marca do avatar → data URL ("" = falhou nesta conexão); protegido por iconMu
	selfID   atomic.Uint32
	// calibrating: uma medida da voz (API.CalibrateVoice) está em andamento.
	calibrating atomic.Bool
	dirty       chan struct{}
	stop        chan struct{}
	stopOnce    sync.Once

	mu          sync.Mutex
	status      string
	lastError   string
	host        string
	away        bool
	awayMessage string
	settings    Settings
	messages    []Message
	nextMsgID   int
	saveTimer   *time.Timer
	notifyCache map[string][]int16

	hotkeyErrs       map[string]string
	lastRequest      ConnectRequest // inclui senhas; só em memória
	reconnectAttempt int
	reconnectTimer   *time.Timer
	userDisconnect   bool
	lastChannel      int
	historyKey       string
	devicesAt        time.Time
	devicesIn        []audio.DeviceInfo
	devicesOut       []audio.DeviceInfo
}

type emitterBox struct{ e Emitter }

// NewApp carrega identidade e ajustes e prepara cliente e áudio.
func NewApp(opts Options) (*App, error) {
	home, _ := os.UserHomeDir()
	if opts.IdentityPath == "" {
		opts.IdentityPath = defaultIdentityPath(runtime.GOOS, home, os.UserConfigDir)
	}
	if opts.SettingsPath == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			dir = home
		}
		opts.SettingsPath = filepath.Join(DataDir(dir), "settings.json")
	}
	if opts.RecordingsDir == "" {
		opts.RecordingsDir = filepath.Join(home, "Music", "Furito TS")
	}

	idents, identDefault, err := loadIdentities(opts.IdentityPath)
	if err != nil {
		return nil, err
	}
	ident := findIdentity(idents, identDefault).Ident

	a := &App{
		opts:      opts,
		status:    statusDisconnected,
		settings:  loadSettings(opts.SettingsPath),
		dirty:     make(chan struct{}, 1),
		stop:      make(chan struct{}),
		soundDir:  filepath.Join(filepath.Dir(opts.SettingsPath), "sounds"),
		iconDir:   filepath.Join(filepath.Dir(opts.SettingsPath), "icons"),
		avatarDir: filepath.Join(filepath.Dir(opts.SettingsPath), "avatars"),
		packDir:   filepath.Join(filepath.Dir(opts.SettingsPath), "soundpacks"),
		icons:     make(map[uint32]string),
		avatars:   make(map[string]string),
		iconWait:  make(map[uint32]chan struct{}),

		notifyCache:  make(map[string][]int16),
		hotkeyErrs:   make(map[string]string),
		identity:     ident,
		idents:       idents,
		identDefault: identDefault,
		identActive:  identDefault,
		historyDir:   filepath.Join(filepath.Dir(opts.SettingsPath), "history"),
	}
	if err := installBundledPacks(opts.SoundPacksZip, a.packDir); err != nil {
		log.Printf("pacotes de sons do app: %v", err) // segue com o que já estiver na pasta
	}
	a.packs = packsIn(a.packDir)
	a.scripts.load(filepath.Join(filepath.Dir(opts.SettingsPath), "scripts.json"), opts.ExampleScript)
	a.hotkeys = hotkey.New(a.onHotkey)
	for action, combo := range a.settings.Hotkeys {
		if !a.validHotkeyAction(action) {
			delete(a.settings.Hotkeys, action) // atalho de um som que não existe mais
			continue
		}
		if err := a.hotkeys.Set(action, combo); err != nil {
			a.hotkeyErrs[action] = err.Error()
		}
	}
	// Pacote salvo que não existe mais: volta para o primeiro disponível.
	if len(a.packs) > 0 {
		found := false
		for _, p := range a.packs {
			found = found || p.ID == a.settings.NotifyPack
		}
		if !found {
			a.settings.NotifyPack = a.packs[0].ID
		}
	}
	a.codec.Store(4)

	a.client = ts3.NewClient(ident)
	if opts.Debug {
		a.client.Logf = func(format string, args ...any) { fmt.Printf("[ts3] "+format+"\n", args...) }
	}

	a.engine, err = audio.NewEngine(a.sendVoice)
	if err != nil {
		return nil, err
	}
	a.engine.SetEffect(audio.VoiceEffect(a.settings.Effect))
	a.engine.SetVADThreshold(a.settings.VADThreshold)
	a.engine.SetVADFloor(a.settings.VADFloor)
	a.engine.SetVADMode(a.settings.VADMode)
	a.engine.SetVolume(a.settings.OutputVolume)
	a.engine.SetNoiseMode(a.settings.noiseMode())
	a.engine.SetAutoGain(a.settings.AutoGain)
	a.engine.SetNormalizeVoices(a.settings.NormalizeVoices)
	a.engine.SetEchoCancellation(a.settings.EchoCancellation)
	a.engine.Soundboard().SetVolume(a.settings.SoundboardVolume)
	a.engine.SetInputDevice(a.settings.InputDevice)
	a.engine.SetOutputDevice(a.settings.OutputDevice)

	a.client.OnStateChanged = a.markDirty
	a.client.OnConnected = a.onConnected
	a.client.OnDisconnected = a.onDisconnected
	a.client.OnTextMessage = a.onTextMessage
	a.client.OnLog = func(level, text string) { a.addLog(level, text) }
	a.client.OnWelcome = func(text string) { a.addMessage(Message{Kind: "log", Level: "success", Text: text, Rich: true}) }
	a.client.OnNotify = a.notify
	a.client.OnClientEvent = a.onClientEvent
	a.client.OnPoke = a.onPoke
	a.client.OnPluginCommand = a.onPluginCommand
	a.client.OnVoice = func(from, seq uint16, _ byte, opusData []byte) {
		if uint32(from) != a.selfID.Load() {
			a.engine.PushVoice(from, seq, opusData)
		}
	}

	go a.emitLoop()
	return a, nil
}

// loadOrCreateIdentity só gera uma identidade nova quando o arquivo não
// existe. Arquivo corrompido é erro: sobrescrever perderia a identidade (e
// com ela os grupos/permissões nos servidores).
// defaultIdentityPath diz onde ficam as identidades. No macOS e no Linux é um
// arquivo oculto na pasta pessoal. No Windows um arquivo começado em ponto
// ficaria solto e visível no perfil, então vai para a pasta de dados do app
// (%AppData%\FuritoTS), junto dos ajustes.
func defaultIdentityPath(goos, home string, configDir func() (string, error)) string {
	if goos == "windows" {
		if dir, err := configDir(); err == nil && dir != "" {
			return filepath.Join(DataDir(dir), "identidades.json")
		}
	}
	return filepath.Join(home, ".ts_furito_identity.json")
}

// SetEmitter define para onde vão os eventos (a janela).
func (a *App) SetEmitter(e Emitter) {
	a.emitter.Store(&emitterBox{e: e})
	a.markDirty()
}

func (a *App) emit(event string, data any) {
	if box := a.emitter.Load(); box != nil && box.e != nil {
		box.e.Emit(event, data)
	}
}

// SetFilePicker define como abrir o seletor de arquivos (vem da janela).
func (a *App) SetFilePicker(p FilePicker) { a.picker.Store(&p) }

// onPoke: alguém nos cutucou. O som e a linha no registro já saíram (vêm do
// cliente de protocolo); aqui só se decide se a interface abre uma janela.
func (a *App) onPoke(from, message string) {
	a.mu.Lock()
	popup := a.settings.PokePopup
	a.mu.Unlock()
	if popup {
		a.emit("poke", PokeDTO{FromName: from, Message: message})
	}
	if a.scripts.anyEnabled() {
		a.emit("script:event", ScriptEvent{Type: "poke", Text: message, Client: a.scriptSender(0, from, "")})
	}
}

// SetQuit define como encerrar o aplicativo (vem da janela).
func (a *App) SetQuit(quit func()) { a.quit.Store(&quit) }

// Quit encerra o aplicativo.
func (a *App) Quit() {
	if q := a.quit.Load(); q != nil && *q != nil {
		(*q)()
	}
}

// EmitMenu repassa uma ação do menu nativo para a interface.
func (a *App) EmitMenu(action string) { a.emit("menu", action) }

// Shutdown desconecta e libera o áudio.
// NoticeCoupling: o que o motor de áudio aprendeu sobre o microfone ouvir os
// avisos sonoros ("unknown", "heard", "silent"). Usado pelos testes.
func (a *App) NoticeCoupling() string {
	return a.engine.NoticeCoupling() + " (" + a.engine.NoticeMeasurements() + ")"
}

func (a *App) Shutdown() {
	a.stopRecording()
	screencap.Stop()
	a.cancelReconnect(true)
	a.hotkeys.Close()
	a.client.Disconnect()
	a.stopOnce.Do(func() { close(a.stop) })
	a.engine.Close()
	a.mu.Lock()
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.mu.Unlock()
	a.saveSettingsNow()
}

// ---------------------------------------------------------------- eventos

func (a *App) markDirty() {
	select {
	case a.dirty <- struct{}{}:
	default:
	}
}

func sameIDs(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[uint16]bool, len(a))
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
	}
	return true
}

// emitLoop publica o estado (com um pequeno agrupamento de mudanças) e, a
// 10 Hz, quem está falando e o nível do microfone.
func (a *App) emitLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastTalking []uint16
	lastSelf, lastSB := false, false
	lastLevel, lastProb := -1.0, -1.0
	lastMode, modeTick := "", 0

	for {
		select {
		case <-a.stop:
			return
		case <-a.dirty:
			select { // junta rajadas (ex.: lista de canais ao conectar)
			case <-time.After(30 * time.Millisecond):
			case <-a.stop:
				return
			}
			select {
			case <-a.dirty:
			default:
			}
			a.emit("state", a.buildState())
		case <-ticker.C:
			if sb := a.engine.Soundboard().IsPlaying(); sb != lastSB {
				lastSB = sb
				a.markDirty()
			}
			if modeTick++; modeTick%10 == 0 { // o usuário troca o modo fora do app
				if mode := audio.MicrophoneMode(); mode != lastMode {
					lastMode = mode
					a.markDirty()
				}
			}
			talking := a.engine.TalkingClients()
			self := a.engine.Transmitting()
			level, prob := a.engine.MicLevel(), a.engine.VoiceProbability()
			if !a.engine.CaptureAvailable() {
				level, prob = 0, 0
			}
			if prob < 0.01 {
				prob = 0
			}
			if self == lastSelf && level == lastLevel && prob == lastProb && sameIDs(talking, lastTalking) {
				continue
			}
			lastTalking, lastSelf, lastLevel, lastProb = talking, self, level, prob
			ids := make([]int, len(talking))
			for i, id := range talking {
				ids[i] = int(id)
			}
			a.emit("talking", Talking{Clients: ids, Self: self, MicLevel: level, VoiceProb: prob})
		}
	}
}

func (a *App) onConnected() {
	a.selfID.Store(uint32(a.client.ClientID()))
	a.forgetFailedIcons()
	a.notify("CONNECTION_CONNECTED")
	snap := a.client.Snapshot()
	a.mu.Lock()
	a.status = statusConnected
	a.lastError = ""
	away, awayMsg := a.away, a.awayMessage
	reconnected := a.reconnectAttempt > 0
	a.reconnectAttempt = 0
	rejoin, rejoinPassword := a.lastChannel, a.lastRequest.ChannelPassword
	a.historyKey = snap.Server.UID
	if a.historyKey == "" {
		a.historyKey = a.host
	}
	historyKey := a.historyKey
	a.mu.Unlock()

	a.loadHistory(historyKey)
	if reconnected {
		a.addLog("success", "Reconectado")
		if rejoin > 0 {
			// volta para o canal em que estávamos
			go a.client.JoinChannel(rejoin, rejoinPassword)
		}
	}

	// Publica no servidor o estado que já estava escolhido na interface.
	if a.engine.Muted() || a.engine.Deafened() {
		a.client.SetMuteState(a.engine.Muted(), a.engine.Deafened())
	}
	if away {
		a.client.SetAway(true, awayMsg)
	}
	a.markDirty()
	a.scriptEvent(ScriptEvent{Type: "connected", Server: snap.Server.Name})

	if !a.opts.DisableAudioDevices {
		go a.startAudio()
	}
}

func (a *App) startAudio() {
	if err := a.engine.StartPlayback(); err != nil {
		a.addLog("error", err.Error())
	}
	if err := a.engine.StartCapture(); err != nil {
		a.addLog("warn", err.Error()+" — você consegue ouvir, mas não falar.")
	}
	// A conexão pode ter caído enquanto os dispositivos abriam.
	if !a.client.IsConnected() && !a.engine.MicTest() {
		a.engine.StopCapture()
	}
	a.markDirty()
}

func (a *App) onDisconnected(reason string, isError bool) {
	a.selfID.Store(0)
	a.stopRecording() // a conversa acabou: fecha o arquivo
	if !a.engine.MicTest() {
		a.engine.StopCapture()
	}
	a.engine.ResetStreams()

	a.whisper.Store(&WhisperDTO{})
	a.mu.Lock()
	wasConnected := a.status == statusConnected
	wasRetrying := a.reconnectAttempt > 0
	a.status = statusDisconnected
	if isError {
		a.lastError = reason
	}
	// Reconecta sozinho quando a conexão cai — não quando o usuário saiu,
	// foi expulso ou banido.
	// (o bloqueio temporário do anti-flood também fala em "banido", mas passa sozinho)
	flood := strings.Contains(reason, "flood")
	removed := strings.Contains(reason, "expulso") || (strings.Contains(reason, "banido") && !flood)
	retry := isError && !removed && !a.userDisconnect && a.settings.AutoReconnect &&
		(wasConnected || wasRetrying) && a.reconnectAttempt < maxReconnectAttempts && a.lastRequest.Host != ""
	var delay time.Duration
	attempt := 0
	if retry {
		delay = a.scheduleReconnectLocked(reason)
		attempt = a.reconnectAttempt
	} else {
		a.reconnectAttempt = 0
	}
	a.mu.Unlock()

	switch {
	case retry && wasConnected:
		a.addLog("error", "Desconectado: "+reason)
		a.notify("CONNECTION_LOST_CONNECTION")
		a.addLog("info", fmt.Sprintf("Reconectando em %d s (tentativa %d)…", int(delay.Seconds()+0.5), attempt))
	case retry:
		a.addLog("info", fmt.Sprintf("Ainda sem conexão (%s). Nova tentativa em %d s (tentativa %d)…", reason, int(delay.Seconds()+0.5), attempt))
	case isError && wasConnected:
		a.addLog("error", "Desconectado: "+reason)
		a.notify("CONNECTION_LOST_CONNECTION")
	case isError:
		a.addLog("error", "Falha ao conectar: "+reason)
	default:
		a.addLog("info", "Desconectado do servidor")
		if wasConnected {
			a.notify("CONNECTION_DISCONNECTED")
		}
	}
	if wasConnected {
		a.scriptEvent(ScriptEvent{Type: "disconnected", Reason: reason})
	}
	a.markDirty()
}

func (a *App) onTextMessage(m ts3.TextMessage) {
	a.mu.Lock()
	blocked := a.settings.Contacts[m.InvokerUID] == "blocked"
	a.mu.Unlock()
	if blocked {
		return // contato bloqueado: as mensagens dele não aparecem
	}
	self := int(a.client.ClientID())
	msg := Message{
		Kind:        "chat",
		Level:       "info",
		TargetMode:  m.TargetMode,
		InvokerID:   m.InvokerID,
		InvokerName: m.InvokerName,
		Text:        m.Text,
		Self:        m.InvokerID == self,
	}
	if m.TargetMode == 1 {
		// Conversa privada: o "par" é sempre o outro lado.
		if msg.Self {
			msg.PeerID = m.Target
			for _, cl := range a.client.Snapshot().Clients {
				if int(cl.ID) == m.Target {
					msg.PeerName = cl.Nickname
				}
			}
		} else {
			msg.PeerID = m.InvokerID
			msg.PeerName = m.InvokerName
		}
	}
	a.addMessage(msg)
	// As nossas próprias mensagens não voltam para os scripts: evita que
	// respondam a si mesmos.
	if !msg.Self && a.scripts.anyEnabled() {
		target := map[int]string{1: "private", 2: "channel", 3: "server"}[m.TargetMode]
		a.emit("script:event", ScriptEvent{Type: "message", Text: m.Text, Target: target, Client: a.scriptSender(m.InvokerID, m.InvokerName, m.InvokerUID)})
	}
}

func (a *App) addLog(level, text string) {
	a.addMessage(Message{Kind: "log", Level: level, Text: text})
}

// Limites do que fica guardado (e vai para a interface) de cada mensagem. O
// servidor de verdade não deixa passar de 1024 caracteres; um mal-intencionado
// manda o que quiser.
const (
	maxMessageText = 16 << 10
	maxMessageName = 256
)

func (a *App) addMessage(m Message) {
	m.Text = clipBytes(m.Text, maxMessageText)
	m.InvokerName = clipBytes(m.InvokerName, maxMessageName)
	m.PeerName = clipBytes(m.PeerName, maxMessageName)
	a.mu.Lock()
	a.nextMsgID++
	m.ID = a.nextMsgID
	m.Time = time.Now().UnixMilli()
	if m.History && m.HistoryTime != 0 {
		m.Time = m.HistoryTime
	}
	a.messages = append(a.messages, m)
	if len(a.messages) > maxMessages {
		a.messages = append(a.messages[:0], a.messages[len(a.messages)-maxMessages:]...)
	}
	a.mu.Unlock()
	if !m.History {
		a.saveHistory(m)
	}
	a.emit("message", m)
}

// ---------------------------------------------------------------- estado

// deviceLists devolve os dispositivos de áudio, consultando o sistema no
// máximo a cada 3 s.
func (a *App) deviceLists() (in, out []audio.DeviceInfo) {
	if a.opts.DisableAudioDevices {
		return []audio.DeviceInfo{}, []audio.DeviceInfo{}
	}
	a.mu.Lock()
	fresh := time.Since(a.devicesAt) < 3*time.Second && a.devicesIn != nil
	in, out = a.devicesIn, a.devicesOut
	a.mu.Unlock()
	if fresh {
		return in, out
	}
	in, out = a.engine.ListDevices()
	a.mu.Lock()
	a.devicesAt, a.devicesIn, a.devicesOut = time.Now(), in, out
	a.mu.Unlock()
	return in, out
}

func (a *App) buildState() State {
	snap := a.client.Snapshot()
	in, out := a.engine.DeviceNames()
	devIn, devOut := a.deviceLists()
	identity := a.identityDTO()
	whisper := WhisperDTO{}
	if w := a.whisper.Load(); w != nil {
		whisper = *w
	}

	a.mu.Lock()
	if snap.Connected && snap.SelfChannelID != 0 {
		a.lastChannel = snap.SelfChannelID
	}
	volumes := make(map[string]float64, len(a.settings.ClientVolumes))
	for k, v := range a.settings.ClientVolumes {
		volumes[k] = v
	}
	mutedLocally := make(map[string]bool, len(a.settings.ClientMuted))
	for k, v := range a.settings.ClientMuted {
		mutedLocally[k] = v
	}
	contacts := make(map[string]string, len(a.settings.Contacts))
	for k, v := range a.settings.Contacts {
		contacts[k] = v
	}
	hotkeyErrs := make(map[string]string, len(a.hotkeyErrs))
	for k, v := range a.hotkeyErrs {
		hotkeyErrs[k] = v
	}
	st := State{
		Status:           a.status,
		Error:            a.lastError,
		Host:             a.host,
		ReconnectAttempt: a.reconnectAttempt,
		AutoReconnect:    a.settings.AutoReconnect,
		Identity:         identity,
		Identities:       a.identityList(),
		AppVersion:       AppVersion,
		AppAuthor:        AppVendor,
		AppSite:          AppSite,
		Screen:           a.settings.Screen.dto(),
		Platform:         runtime.GOOS,
		PokePopup:        a.settings.PokePopup,
		Images:           ImagesDTO{Mode: a.settings.RemoteImages, Trusted: snap.Server.UID != "" && a.settings.ImageServers[snap.Server.UID]},
		Bookmarks:        append([]Bookmark{}, a.settings.Bookmarks...),
		Groups:           GroupsDTO{Server: []GroupEntry{}, Channel: []GroupEntry{}},
		Hotkeys: HotkeysDTO{
			PTT:          a.settings.Hotkeys["ptt"],
			ToggleMute:   a.settings.Hotkeys["toggleMute"],
			ToggleDeafen: a.settings.Hotkeys["toggleDeafen"],
			SoundStop:    a.settings.Hotkeys["soundStop"],
			Global:       hotkey.Available(),
			Errors:       hotkeyErrs,
		},
		Server: ServerDTO{
			Name:           snap.Server.Name,
			WelcomeMessage: snap.Server.WelcomeMessage,
			Version:        snap.Server.Version,
			Platform:       snap.Server.Platform,
			MaxClients:     snap.Server.MaxClients,
			UID:            snap.Server.UID,
			BannerURL:      snap.Server.BannerURL,
			BannerLink:     snap.Server.BannerLink,
		},
		Self: SelfDTO{
			ClientID:       int(snap.SelfID),
			ChannelID:      snap.SelfChannelID,
			Nickname:       a.settings.Nickname,
			Muted:          a.engine.Muted(),
			Deafened:       a.engine.Deafened(),
			RecordingSince: recordingSince(a.engine),
			Away:           a.away,
			AwayMessage:    a.awayMessage,
			Whisper:        whisper,
		},
		Channels: make([]ChannelDTO, 0, len(snap.Channels)),
		Clients:  make([]ClientDTO, 0, len(snap.Clients)),
		Audio: AudioDTO{
			Effect:                    string(a.engine.Effect()),
			Effects:                   audio.Effects(),
			VADMode:                   a.engine.VADMode(),
			VADThreshold:              a.engine.VADThreshold(),
			VADFloor:                  a.engine.VADFloor(),
			NoiseSuppression:          a.settings.NoiseSuppression,
			NoiseMode:                 a.settings.NoiseMode,
			AutoGain:                  a.engine.AutoGain(),
			EchoCancellation:          a.engine.EchoCancellation(),
			EchoCancellationAvailable: audio.EchoCancellationAvailable(),
			NormalizeVoices:           a.engine.NormalizeVoices(),
			MicTest:                   a.engine.MicTest(),
			MicMode:                   audio.MicrophoneMode(),
			OutputVolume:              a.engine.Volume(),
			SoundboardVolume:          a.engine.Soundboard().Volume(),
			SoundboardPlaying:         a.engine.Soundboard().IsPlaying(),
			Sounds:                    a.soundListLocked(),
			CaptureAvailable:          a.engine.CaptureAvailable(),
			InputDevice:               in,
			OutputDevice:              out,
			NotifySounds:              a.settings.NotifySounds,
			NotifyPack:                a.settings.NotifyPack,
			NotifyPacks:               []NotifyPackDTO{},
			InputDevices:              devIn,
			OutputDevices:             devOut,
			InputChoice:               a.settings.InputDevice,
			OutputChoice:              a.settings.OutputDevice,
		},
	}
	for _, p := range a.packs {
		st.Audio.NotifyPacks = append(st.Audio.NotifyPacks, NotifyPackDTO{ID: p.ID, Name: p.Name})
	}
	a.mu.Unlock()

	for id, g := range snap.ServerGroups {
		st.Groups.Server = append(st.Groups.Server, GroupEntry{ID: id, Name: g.Name, IconID: g.IconID, Type: g.Type})
	}
	for id, g := range snap.ChannelGroups {
		st.Groups.Channel = append(st.Groups.Channel, GroupEntry{ID: id, Name: g.Name, IconID: g.IconID, Type: g.Type})
	}
	sort.Slice(st.Groups.Server, func(i, j int) bool { return st.Groups.Server[i].ID < st.Groups.Server[j].ID })
	sort.Slice(st.Groups.Channel, func(i, j int) bool { return st.Groups.Channel[i].ID < st.Groups.Channel[j].ID })

	codec := uint32(4)
	for _, oc := range snap.Channels {
		ch := oc.Channel
		if ch.ID == snap.SelfChannelID && ch.Codec == 5 {
			codec = 5
		}
		st.Channels = append(st.Channels, ChannelDTO{
			ID:              ch.ID,
			ParentID:        ch.ParentID,
			Depth:           oc.Depth,
			Name:            ch.Name,
			Topic:           ch.Topic,
			Description:     ch.Description,
			Codec:           ch.Codec,
			CodecQuality:    ch.CodecQuality,
			MaxClients:      ch.MaxClients,
			IsDefault:       ch.IsDefault,
			HasPassword:     ch.HasPassword,
			IsPermanent:     ch.IsPermanent,
			IsSemiPermanent: ch.IsSemiPermanent,
			NeededTalkPower: ch.NeededTalkPower,
			IconID:          ch.IconID,
			IsTemporary:     !ch.IsPermanent && !ch.IsSemiPermanent,
		})
	}
	if a.codec.Swap(codec) != codec {
		// Canal de música pede mais taxa; voz fica transparente com 64 kbit/s.
		if codec == 5 {
			a.engine.SetBitrate(96000)
		} else {
			a.engine.SetBitrate(64000)
		}
	}

	for _, cl := range snap.Clients {
		if cl.ID == snap.SelfID && snap.Connected && cl.UID != "" && cl.UID != st.Identity.UID {
			st.Identity.ServerUID = cl.UID
		}
		dto := ClientDTO{
			ID:           int(cl.ID),
			ChannelID:    cl.ChannelID,
			Nickname:     cl.Nickname,
			UID:          cl.UID,
			DatabaseID:   cl.DatabaseID,
			Type:         cl.Type,
			InputMuted:   cl.InputMuted,
			OutputMuted:  cl.OutputMuted,
			Away:         cl.Away,
			AwayMessage:  cl.AwayMessage,
			TalkPower:    cl.TalkPower,
			IsRecording:  cl.IsRecording,
			Country:      cl.Country,
			ServerGroups: make([]string, 0, len(cl.ServerGroups)),
			ChannelGroup: snap.ChannelGroups[cl.ChannelGroupID].Name,
			Version:      cl.Version,
			Platform:     cl.Platform,
			Description:  cl.Description,
			Avatar:       cl.Avatar,

			Icons:              clientIcons(cl, snap),
			Groups:             make([]GroupDTO, 0, len(cl.ServerGroups)),
			ChannelGroupIcon:   snap.ChannelGroups[cl.ChannelGroupID].IconID,
			InputHardware:      cl.InputHardware,
			OutputHardware:     cl.OutputHardware,
			IsChannelCommander: cl.IsChannelCommander,
			IsPrioritySpeaker:  cl.IsPrioritySpeaker,
			TalkRequest:        cl.TalkRequest,
			TalkRequestMessage: cl.TalkRequestMessage,
			IsTalker:           cl.IsTalker,
			ServerGroupIDs:     append([]int{}, cl.ServerGroups...),
			ChannelGroupID:     cl.ChannelGroupID,
			Volume:             100,
			MutedLocally:       mutedLocally[cl.UID],
			Contact:            contacts[cl.UID],
		}
		// Clientes Furito anunciam a versão de verdade em client_meta_data; a
		// versão "oficial" deles é só a assinatura exigida pelo servidor.
		if v := furitoVersion(cl.MetaData); v != "" {
			dto.Furito = true
			dto.Version, dto.Platform = v, AppVendor
		}
		if v, ok := volumes[cl.UID]; ok {
			dto.Volume = v
		}
		// Aplica no áudio o volume/mudo escolhidos para esta pessoa.
		a.engine.SetClientGain(cl.ID, dto.Volume/100)
		a.engine.SetClientMuted(cl.ID, dto.MutedLocally || dto.Contact == "blocked")
		if cl.ID == snap.SelfID {
			st.Self.TalkRequested = cl.TalkRequest
		}
		dto.NamePrefix, dto.NameSuffix = clientNameAffixes(cl, snap)
		for _, g := range cl.ServerGroups {
			if group, ok := snap.ServerGroups[g]; ok && group.Name != "" {
				dto.ServerGroups = append(dto.ServerGroups, group.Name)
				dto.Groups = append(dto.Groups, GroupDTO{Name: group.Name, IconID: group.IconID})
			}
		}
		if !cl.ConnectedSince.IsZero() {
			dto.ConnectedSince = cl.ConnectedSince.UnixMilli()
		}
		if cl.ID == snap.SelfID && cl.Nickname != "" {
			st.Self.Nickname = cl.Nickname
		}
		st.Clients = append(st.Clients, dto)
	}
	return st
}

// ---------------------------------------------------------------- ações

func (a *App) connect(req ConnectRequest) error {
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		return errors.New("informe o endereço do servidor")
	}
	nickname := ts3.NormalizeNickname(req.Nickname)

	// Encerra a conexão anterior antes de mudar o estado, para os eventos
	// dela não se misturarem com os da nova.
	a.cancelReconnect(false)
	a.client.Disconnect()
	if err := a.useIdentity(req.IdentityID); err != nil {
		return err
	}

	a.mu.Lock()
	a.status = statusConnecting
	a.lastError = ""
	a.userDisconnect = false
	a.lastRequest = req
	a.lastChannel = 0
	a.host = req.Host
	a.settings.Host = req.Host
	a.settings.Nickname = nickname
	a.settings.DefaultChannel = req.DefaultChannel
	a.scheduleSaveLocked()
	a.mu.Unlock()
	// Emite na hora (sem agrupar): numa rede local a conexão termina antes
	// do agrupamento e a interface nunca veria o estado "conectando".
	a.emit("state", a.buildState())
	a.addLog("info", fmt.Sprintf("Tentando conectar-se ao servidor [b]%s[/b]", req.Host))

	err := a.client.Connect(ts3.ConnectOptions{
		Host:            req.Host,
		Nickname:        nickname,
		ServerPassword:  req.ServerPassword,
		DefaultChannel:  req.DefaultChannel,
		ChannelPassword: req.ChannelPassword,
		MetaData:        furitoMeta(),
	})
	if err != nil {
		a.mu.Lock()
		a.status = statusDisconnected
		a.lastError = err.Error()
		a.mu.Unlock()
		a.addLog("error", "Falha ao conectar: "+err.Error())
		a.markDirty()
		return err
	}
	return nil
}

func (a *App) publishMuteState() {
	a.client.SetMuteState(a.engine.Muted(), a.engine.Deafened())
	a.markDirty()
}

func (a *App) updateSettings(change func(s *Settings)) {
	a.mu.Lock()
	change(&a.settings)
	a.scheduleSaveLocked()
	a.mu.Unlock()
	a.markDirty()
}

// scheduleSaveLocked grava os ajustes meio segundo depois da última mudança
// (os sliders disparam muitas alterações seguidas).
func (a *App) scheduleSaveLocked() {
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.saveTimer = time.AfterFunc(500*time.Millisecond, a.saveSettingsNow)
}

// saveSettingsNow grava os ajustes em disco. O JSON é montado com a trava
// tomada: os ajustes têm mapas (contatos, volumes, atalhos), e ler um mapa
// enquanto outra goroutine o altera derruba o programa na hora.
func (a *App) saveSettingsNow() {
	a.mu.Lock()
	data, err := json.MarshalIndent(a.settings, "", "  ")
	a.mu.Unlock()
	if err == nil {
		writeSettings(a.opts.SettingsPath, data)
	}
}

// ---------------------------------------------------------------- ajustes

// Settings são as preferências salvas entre execuções (nunca senhas).
type Settings struct {
	Host           string  `json:"host"`
	Nickname       string  `json:"nickname"`
	DefaultChannel string  `json:"defaultChannel"`
	Effect         string  `json:"effect"`
	VADThreshold   float64 `json:"vadThreshold"`
	OutputVolume   float64 `json:"outputVolume"`

	// VADFloor: sensibilidade da detecção de voz — o nível que a voz precisa
	// atingir para abrir o microfone. 0 = sem piso.
	VADFloor         float64        `json:"vadFloor"`
	VADMode          string         `json:"vadMode"`
	NoiseSuppression bool           `json:"noiseSuppression"` // ligada ou desligada
	NoiseMode        string         `json:"noiseMode"`        // o tipo, quando ligada: neural (RNNoise) | classic
	AutoGain         bool           `json:"autoGain"`
	EchoCancellation bool           `json:"echoCancellation"`
	NormalizeVoices  bool           `json:"normalizeVoices"`
	SoundboardVolume float64        `json:"soundboardVolume"`
	Sounds           []SoundSetting `json:"sounds"`
	NotifySounds     bool           `json:"notifySounds"`
	NotifyPack       string         `json:"notifyPack"`

	AutoReconnect bool               `json:"autoReconnect"`
	Bookmarks     []Bookmark         `json:"bookmarks"`
	Hotkeys       map[string]string  `json:"hotkeys"` // ação → combinação
	InputDevice   string             `json:"inputDevice"`
	OutputDevice  string             `json:"outputDevice"`
	ClientVolumes map[string]float64 `json:"clientVolumes"` // por identidade do cliente
	ClientMuted   map[string]bool    `json:"clientMuted"`
	Contacts      map[string]string  `json:"contacts"` // friend | blocked
	Screen        ScreenSettings     `json:"screen"`   // compartilhamento de tela
	// RemoteImages: o que fazer com imagens de fora ([img] em descrições e na
	// mensagem de boas-vindas, banner do servidor). Buscar uma imagem revela o
	// endereço IP do usuário a quem a hospeda — e quem escolhe o endereço é
	// quem escreveu a descrição. "ask" (o padrão): a imagem só carrega com um
	// clique, ou sozinha nos servidores de ImageServers; "always"; "never".
	RemoteImages string `json:"remoteImages"`
	// ImageServers: servidores (pela identidade deles) em que o usuário mandou
	// carregar as imagens sempre.
	ImageServers map[string]bool `json:"imageServers"`
	// PokePopup: abrir uma janela quando alguém nos cutuca. Desligado, a
	// cutucada só toca o aviso sonoro e aparece no registro do servidor.
	PokePopup bool `json:"pokePopup"`
}

// SoundSetting é um som adicionado pelo usuário ao soundboard. O arquivo é
// uma cópia guardada na pasta de dados do app.
type SoundSetting struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

func loadSettings(path string) Settings {
	s := Settings{
		Host:         "localhost",
		Nickname:     "FuritoUser",
		Effect:       string(audio.EffectNormal),
		VADThreshold: audio.DefaultVAD,
		OutputVolume: 100,

		VADMode:          audio.VADAuto,
		NoiseSuppression: true,
		NoiseMode:        audio.NoiseNeural,
		AutoGain:         true,
		NormalizeVoices:  true,
		SoundboardVolume: 80,
		NotifySounds:     true,
		NotifyPack:       "default",
		AutoReconnect:    true,
		PokePopup:        true,
	}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &s)
	}
	if s.Hotkeys == nil {
		s.Hotkeys = make(map[string]string)
	}
	if s.ClientVolumes == nil {
		s.ClientVolumes = make(map[string]float64)
	}
	if s.ClientMuted == nil {
		s.ClientMuted = make(map[string]bool)
	}
	if s.Contacts == nil {
		s.Contacts = make(map[string]string)
	}
	if s.ImageServers == nil {
		s.ImageServers = make(map[string]bool)
	}
	if s.RemoteImages != ImagesAlways && s.RemoteImages != ImagesNever {
		s.RemoteImages = ImagesAsk
	}
	if s.SoundboardVolume < 0 || s.SoundboardVolume > 100 {
		s.SoundboardVolume = 80
	}
	if s.VADThreshold < 50 || s.VADThreshold > 8000 {
		s.VADThreshold = audio.DefaultVAD
	}
	if !(s.VADFloor > 0) || s.VADFloor > 8000 { // negativo, NaN ou fora da escala: sem piso
		s.VADFloor = 0
	}
	if s.OutputVolume < 0 || s.OutputVolume > 100 {
		s.OutputVolume = 100
	}
	if s.NoiseMode != audio.NoiseClassic {
		s.NoiseMode = audio.NoiseNeural
	}
	return s
}

// noiseMode traduz os dois ajustes da supressão de ruído (ligada? de que
// tipo?) para o modo do motor de áudio.
func (s *Settings) noiseMode() string {
	if !s.NoiseSuppression {
		return audio.NoiseOff
	}
	return s.NoiseMode
}

// writeSettings grava o arquivo de ajustes: primeiro num arquivo ao lado,
// depois troca, para uma queda no meio não deixar os ajustes (favoritos,
// contatos, atalhos) pela metade.
func writeSettings(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
	}
}
