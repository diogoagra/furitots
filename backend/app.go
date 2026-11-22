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
	"slices"
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

// App é o aplicativo: o áudio, os ajustes, as identidades, os scripts e a
// lista de conexões. O que pertence a um servidor só fica em conn.
type App struct {
	opts       Options
	engine     *audio.Engine
	emitter    atomic.Pointer[emitterBox]
	picker     atomic.Pointer[FilePicker]
	openPicker atomic.Pointer[OpenPicker]
	dirPicker  atomic.Pointer[DirPicker]
	quit       atomic.Pointer[func()]
	savePicker atomic.Pointer[SavePicker]
	hotkeys    *hotkey.Manager
	historyDir string
	historyMu  sync.Mutex // um de cada vez nos arquivos de histórico

	identMu      sync.Mutex
	idents       []*identityEntry // todas as identidades
	identDefault string           // id da padrão

	soundDir  string
	iconDir   string
	avatarDir string
	packs     []soundPack
	packDir   string // cópia dos pacotes de sons, na pasta de dados do app
	scripts   scriptStore

	iconMu   sync.Mutex
	icons    map[uint32]string // id → data URL; o que falhou fica na conexão (conn.iconFailed)
	iconWait map[uint32]chan struct{}
	avatars  map[string]string // uid + marca do avatar → data URL; protegido por iconMu
	// calibrating: uma medida da voz (API.CalibrateVoice) está em andamento.
	calibrating atomic.Bool
	dirty       chan struct{}
	viewDirty   atomic.Bool // o estado da conexão à vista precisa ser publicado de novo
	// mic é a conexão que recebe o que o microfone capta (nil = nenhuma). Fica
	// fora de mu: quem a lê é o áudio, a cada quadro transmitido.
	mic      atomic.Pointer[conn]
	stop     chan struct{}
	stopOnce sync.Once

	mu          sync.Mutex
	conns       []*conn // as conexões abertas, na ordem das abas; nunca vazia
	view        *conn   // a que a interface está mostrando
	rec         *conn   // a que está sendo gravada (nil = nenhuma)
	nextConnID  int
	away        bool
	awayMessage string
	settings    Settings
	nextMsgID   int // um contador só: o id de uma mensagem não se repete entre conexões
	saveTimer   *time.Timer
	notifyCache map[string][]int16

	hotkeyErrs map[string]string
	lastTabs   []TabDTO // o último resumo das abas publicado (só o laço de publicação usa)
	devicesAt  time.Time
	devicesIn  []audio.DeviceInfo
	devicesOut []audio.DeviceInfo
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

	a := &App{
		opts:      opts,
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
		idents:       idents,
		identDefault: identDefault,
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

	a.view = a.newConn() // sempre há uma conexão (uma aba), mesmo desconectada
	a.setMic(a.view)

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
func (c *conn) onPoke(from, message string) {
	c.app.mu.Lock()
	popup := c.app.settings.PokePopup
	c.app.mu.Unlock()
	if popup {
		c.app.emit("poke", PokeDTO{Tab: c.id, Server: c.client.ServerName(), FromName: from, Message: message})
	}
	if c.app.scripts.anyEnabled() {
		c.app.emit("script:event", ScriptEvent{Tab: c.id, Type: "poke", Text: message, Client: c.scriptSender(0, from, "")})
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
	a.hotkeys.Close()
	for _, c := range a.connList() {
		c.cancelReconnect(true)
		c.client.Disconnect()
	}
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

// markDirty avisa que algo do app mudou (ajustes, áudio, identidades): o
// estado da conexão à vista é publicado de novo.
func (a *App) markDirty() {
	a.viewDirty.Store(true)
	a.wake()
}

// wake acorda o laço que publica o estado.
func (a *App) wake() {
	select {
	case a.dirty <- struct{}{}:
	default:
	}
}

// publish publica o que mudou desde a última vez: o estado inteiro só da
// conexão que está à vista; das outras, só se aplica o que precisa valer mesmo
// sem ninguém olhando (ver conn.sync).
func (a *App) publish() {
	view := a.current()
	emit := a.viewDirty.Swap(false)
	for _, c := range a.connList() {
		if !c.dirty.Swap(false) {
			continue
		}
		if c == view {
			emit = true
		} else {
			c.sync(c.client.Snapshot())
		}
	}
	tabs := a.tabList()
	switch {
	case emit:
		a.lastTabs = tabs
		a.emit("state", view.buildState())
	case !sameTabs(tabs, a.lastTabs): // só o resumo mudou (uma aba fora da vista)
		a.lastTabs = tabs
		a.emit("tabs", tabs)
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
	var lastBusy []uint32
	lastSelf, lastTx, lastSB := false, false, false
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
			a.publish()
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
			view := a.current()
			talking := a.engine.TalkingClients(view.audioID())
			tx := a.engine.Transmitting()
			self := tx && a.talker() == view // o nosso nome só acende na aba em que falamos
			busy := a.engine.TalkingConns()
			level, prob := a.engine.MicLevel(), a.engine.VoiceProbability()
			if !a.engine.CaptureAvailable() {
				level, prob = 0, 0
			}
			if prob < 0.01 {
				prob = 0
			}
			if self == lastSelf && tx == lastTx && level == lastLevel && prob == lastProb && sameIDs(talking, lastTalking) && slices.Equal(busy, lastBusy) {
				continue
			}
			lastTalking, lastSelf, lastTx, lastLevel, lastProb, lastBusy = talking, self, tx, level, prob, busy
			ids := make([]int, len(talking))
			for i, id := range talking {
				ids[i] = int(id)
			}
			tabs := make([]int, len(busy))
			for i, id := range busy {
				tabs[i] = int(id)
			}
			a.emit("talking", Talking{Clients: ids, Self: self, Tx: tx, Tabs: tabs, MicLevel: level, VoiceProb: prob})
		}
	}
}

func (c *conn) onConnected() {
	c.selfID.Store(uint32(c.client.ClientID()))
	c.forgetFailedIcons()
	c.notify("CONNECTION_CONNECTED")
	snap := c.client.Snapshot()
	c.app.mu.Lock()
	c.status = statusConnected
	c.lastError = ""
	away, awayMsg := c.app.away, c.app.awayMessage
	reconnected := c.reconnectAttempt > 0
	c.reconnectAttempt = 0
	rejoin, rejoinPassword := c.lastChannel, c.lastRequest.ChannelPassword
	c.historyKey = snap.Server.UID
	if c.historyKey == "" {
		c.historyKey = c.host
	}
	historyKey := c.historyKey
	c.app.mu.Unlock()

	c.loadHistory(historyKey)
	if reconnected {
		c.addLog("success", "Reconectado")
		if rejoin > 0 {
			// volta para o canal em que estávamos
			go c.client.JoinChannel(rejoin, rejoinPassword)
		}
	}

	// Publica no servidor o estado que já estava escolhido na interface.
	c.app.claimMic(c)
	c.publishMuteState()
	if away {
		c.client.SetAway(true, awayMsg)
	}
	c.markDirty()
	c.scriptEvent(ScriptEvent{Type: "connected", Server: snap.Server.Name})

	if !c.app.opts.DisableAudioDevices {
		go c.app.startAudio()
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
	if !a.anyConnected() && !a.engine.MicTest() {
		a.engine.StopCapture()
	}
	a.markDirty()
}

func (c *conn) onDisconnected(reason string, isError bool) {
	c.selfID.Store(0)
	if c.app.recording() == c {
		c.app.stopRecording() // a conversa acabou: fecha o arquivo
	}
	if !c.app.anyConnected() && !c.app.engine.MicTest() {
		c.app.engine.StopCapture()
	}
	c.app.engine.ResetStreams(c.audioID())

	c.whisper.Store(&WhisperDTO{})
	c.app.mu.Lock()
	wasConnected := c.status == statusConnected
	wasRetrying := c.reconnectAttempt > 0
	c.status = statusDisconnected
	if isError {
		c.lastError = reason
	}
	// Reconecta sozinho quando a conexão cai — não quando o usuário saiu,
	// foi expulso ou banido.
	// (o bloqueio temporário do anti-flood também fala em "banido", mas passa sozinho)
	flood := strings.Contains(reason, "flood")
	removed := strings.Contains(reason, "expulso") || (strings.Contains(reason, "banido") && !flood)
	retry := isError && !removed && !c.userDisconnect && c.app.settings.AutoReconnect &&
		(wasConnected || wasRetrying) && c.reconnectAttempt < maxReconnectAttempts && c.lastRequest.Host != ""
	var delay time.Duration
	attempt := 0
	if retry {
		delay = c.scheduleReconnectLocked(reason)
		attempt = c.reconnectAttempt
	} else {
		c.reconnectAttempt = 0
	}
	c.app.mu.Unlock()
	if !retry {
		c.app.releaseMic(c) // enquanto houver nova tentativa, o microfone espera por ela
	}

	switch {
	case retry && wasConnected:
		c.addLog("error", "Desconectado: "+reason)
		c.notify("CONNECTION_LOST_CONNECTION")
		c.addLog("info", fmt.Sprintf("Reconectando em %d s (tentativa %d)…", int(delay.Seconds()+0.5), attempt))
	case retry:
		c.addLog("info", fmt.Sprintf("Ainda sem conexão (%s). Nova tentativa em %d s (tentativa %d)…", reason, int(delay.Seconds()+0.5), attempt))
	case isError && wasConnected:
		c.addLog("error", "Desconectado: "+reason)
		c.notify("CONNECTION_LOST_CONNECTION")
	case isError:
		c.addLog("error", "Falha ao conectar: "+reason)
	default:
		c.addLog("info", "Desconectado do servidor")
		if wasConnected {
			c.notify("CONNECTION_DISCONNECTED")
		}
	}
	if wasConnected {
		c.scriptEvent(ScriptEvent{Type: "disconnected", Reason: reason})
	}
	c.markDirty()
}

func (c *conn) onTextMessage(m ts3.TextMessage) {
	c.app.mu.Lock()
	blocked := c.app.settings.Contacts[m.InvokerUID] == "blocked"
	c.app.mu.Unlock()
	if blocked {
		return // contato bloqueado: as mensagens dele não aparecem
	}
	self := int(c.client.ClientID())
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
			for _, cl := range c.client.Snapshot().Clients {
				if int(cl.ID) == m.Target {
					msg.PeerName = cl.Nickname
				}
			}
		} else {
			msg.PeerID = m.InvokerID
			msg.PeerName = m.InvokerName
		}
	}
	c.addMessage(msg)
	// As nossas próprias mensagens não voltam para os scripts: evita que
	// respondam a si mesmos.
	if !msg.Self && c.app.scripts.anyEnabled() {
		target := map[int]string{1: "private", 2: "channel", 3: "server"}[m.TargetMode]
		c.app.emit("script:event", ScriptEvent{Tab: c.id, Type: "message", Text: m.Text, Target: target, Client: c.scriptSender(m.InvokerID, m.InvokerName, m.InvokerUID)})
	}
}

func (c *conn) addLog(level, text string) {
	c.addMessage(Message{Kind: "log", Level: level, Text: text})
}

// Limites do que fica guardado (e vai para a interface) de cada mensagem. O
// servidor de verdade não deixa passar de 1024 caracteres; um mal-intencionado
// manda o que quiser.
const (
	maxMessageText = 16 << 10
	maxMessageName = 256
)

func (c *conn) addMessage(m Message) {
	m.Text = clipBytes(m.Text, maxMessageText)
	m.InvokerName = clipBytes(m.InvokerName, maxMessageName)
	m.PeerName = clipBytes(m.PeerName, maxMessageName)
	m.Tab = c.id
	c.app.mu.Lock()
	c.app.nextMsgID++
	m.ID = c.app.nextMsgID
	m.Time = time.Now().UnixMilli()
	if m.History && m.HistoryTime != 0 {
		m.Time = m.HistoryTime
	}
	c.messages = append(c.messages, m)
	if len(c.messages) > maxMessages {
		c.messages = append(c.messages[:0], c.messages[len(c.messages)-maxMessages:]...)
	}
	unseen := c.app.view != c && m.Kind == "chat" && !m.Self && !m.History
	if unseen {
		c.unread++
	}
	c.app.mu.Unlock()
	if unseen {
		c.markDirty() // a barra de abas mostra que há mensagem nova
	}
	if !m.History {
		c.saveHistory(m)
	}
	c.app.emit("message", m)
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

// sync aplica o que o estado do servidor muda fora da tela: o canal em que
// estamos (para voltar a ele ao reconectar), o volume e o mudo escolhidos para
// cada pessoa e o codec do canal. Vale também para as conexões que não estão
// à vista.
func (c *conn) sync(snap ts3.Snapshot) {
	a := c.app
	type level struct {
		gain  float64
		muted bool
	}
	levels := make(map[uint16]level, len(snap.Clients))
	a.mu.Lock()
	if snap.Connected && snap.SelfChannelID != 0 {
		c.lastChannel = snap.SelfChannelID
	}
	for _, cl := range snap.Clients {
		gain := 1.0
		if v, ok := a.settings.ClientVolumes[cl.UID]; ok {
			gain = v / 100
		}
		levels[cl.ID] = level{gain, a.settings.ClientMuted[cl.UID] || a.settings.Contacts[cl.UID] == "blocked"}
	}
	a.mu.Unlock()
	for id, l := range levels {
		a.engine.SetClientGain(c.audioID(), id, l.gain)
		a.engine.SetClientMuted(c.audioID(), id, l.muted)
	}

	codec := uint32(4)
	for _, oc := range snap.Channels {
		if oc.Channel.ID == snap.SelfChannelID && oc.Channel.Codec == 5 {
			codec = 5
		}
	}
	if c.codec.Swap(codec) != codec {
		a.applyBitrate()
	}
}

// applyBitrate ajusta a taxa do codificador ao canal da conexão em que
// falamos: canal de música pede mais taxa; voz fica transparente com 64 kbit/s.
func (a *App) applyBitrate() {
	if c := a.talker(); c != nil && c.codec.Load() == 5 {
		a.engine.SetBitrate(96000)
	} else {
		a.engine.SetBitrate(64000)
	}
}

// buildState monta o estado desta conexão (e o do app) para a interface.
func (c *conn) buildState() State {
	snap := c.client.Snapshot()
	c.sync(snap)
	in, out := c.app.engine.DeviceNames()
	devIn, devOut := c.app.deviceLists()
	identity, identities := c.identityDTO(), c.app.identityList()
	tabs := c.app.tabList()
	recordingSince := c.recordingSince()
	whisper := WhisperDTO{}
	if w := c.whisper.Load(); w != nil {
		whisper = *w
	}

	c.app.mu.Lock()
	volumes := make(map[string]float64, len(c.app.settings.ClientVolumes))
	for k, v := range c.app.settings.ClientVolumes {
		volumes[k] = v
	}
	mutedLocally := make(map[string]bool, len(c.app.settings.ClientMuted))
	for k, v := range c.app.settings.ClientMuted {
		mutedLocally[k] = v
	}
	contacts := make(map[string]string, len(c.app.settings.Contacts))
	for k, v := range c.app.settings.Contacts {
		contacts[k] = v
	}
	hotkeyErrs := make(map[string]string, len(c.app.hotkeyErrs))
	for k, v := range c.app.hotkeyErrs {
		hotkeyErrs[k] = v
	}
	st := State{
		Tab:              c.id,
		Tabs:             tabs,
		Status:           c.status,
		Error:            c.lastError,
		Host:             c.host,
		ReconnectAttempt: c.reconnectAttempt,
		AutoReconnect:    c.app.settings.AutoReconnect,
		Identity:         identity,
		Identities:       identities,
		AppVersion:       AppVersion,
		AppAuthor:        AppVendor,
		AppSite:          AppSite,
		Screen:           c.app.settings.Screen.dto(),
		Platform:         runtime.GOOS,
		PokePopup:        c.app.settings.PokePopup,
		Images:           ImagesDTO{Mode: c.app.settings.RemoteImages, Trusted: snap.Server.UID != "" && c.app.settings.ImageServers[snap.Server.UID]},
		Bookmarks:        append([]Bookmark{}, c.app.settings.Bookmarks...),
		Groups:           GroupsDTO{Server: []GroupEntry{}, Channel: []GroupEntry{}},
		Hotkeys: HotkeysDTO{
			PTT:          c.app.settings.Hotkeys["ptt"],
			ToggleMute:   c.app.settings.Hotkeys["toggleMute"],
			ToggleDeafen: c.app.settings.Hotkeys["toggleDeafen"],
			SoundStop:    c.app.settings.Hotkeys["soundStop"],
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
			Nickname:       c.app.settings.Nickname,
			Muted:          c.app.engine.Muted(),
			Deafened:       c.app.engine.Deafened(),
			RecordingSince: recordingSince,
			Away:           c.app.away,
			AwayMessage:    c.app.awayMessage,
			Whisper:        whisper,
		},
		Channels: make([]ChannelDTO, 0, len(snap.Channels)),
		Clients:  make([]ClientDTO, 0, len(snap.Clients)),
		Audio: AudioDTO{
			Effect:                    string(c.app.engine.Effect()),
			Effects:                   audio.Effects(),
			VADMode:                   c.app.engine.VADMode(),
			VADThreshold:              c.app.engine.VADThreshold(),
			VADFloor:                  c.app.engine.VADFloor(),
			NoiseSuppression:          c.app.settings.NoiseSuppression,
			NoiseMode:                 c.app.settings.NoiseMode,
			AutoGain:                  c.app.engine.AutoGain(),
			EchoCancellation:          c.app.engine.EchoCancellation(),
			EchoCancellationAvailable: audio.EchoCancellationAvailable(),
			NormalizeVoices:           c.app.engine.NormalizeVoices(),
			MicTest:                   c.app.engine.MicTest(),
			MicMode:                   audio.MicrophoneMode(),
			OutputVolume:              c.app.engine.Volume(),
			SoundboardVolume:          c.app.engine.Soundboard().Volume(),
			SoundboardPlaying:         c.app.engine.Soundboard().IsPlaying(),
			Sounds:                    c.app.soundListLocked(),
			CaptureAvailable:          c.app.engine.CaptureAvailable(),
			InputDevice:               in,
			OutputDevice:              out,
			NotifySounds:              c.app.settings.NotifySounds,
			NotifyPack:                c.app.settings.NotifyPack,
			NotifyPacks:               []NotifyPackDTO{},
			InputDevices:              devIn,
			OutputDevices:             devOut,
			InputChoice:               c.app.settings.InputDevice,
			OutputChoice:              c.app.settings.OutputDevice,
		},
	}
	for _, p := range c.app.packs {
		st.Audio.NotifyPacks = append(st.Audio.NotifyPacks, NotifyPackDTO{ID: p.ID, Name: p.Name})
	}
	c.app.mu.Unlock()

	for id, g := range snap.ServerGroups {
		st.Groups.Server = append(st.Groups.Server, GroupEntry{ID: id, Name: g.Name, IconID: g.IconID, Type: g.Type})
	}
	for id, g := range snap.ChannelGroups {
		st.Groups.Channel = append(st.Groups.Channel, GroupEntry{ID: id, Name: g.Name, IconID: g.IconID, Type: g.Type})
	}
	sort.Slice(st.Groups.Server, func(i, j int) bool { return st.Groups.Server[i].ID < st.Groups.Server[j].ID })
	sort.Slice(st.Groups.Channel, func(i, j int) bool { return st.Groups.Channel[i].ID < st.Groups.Channel[j].ID })

	for _, oc := range snap.Channels {
		ch := oc.Channel
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

// connectOptions traduz um pedido de conexão para o cliente de protocolo. É o
// mesmo para a primeira conexão e para as reconexões: nas duas o cliente se
// apresenta como Furito.
func connectOptions(req ConnectRequest) ts3.ConnectOptions {
	return ts3.ConnectOptions{
		Host:            strings.TrimSpace(req.Host),
		Nickname:        ts3.NormalizeNickname(req.Nickname),
		ServerPassword:  req.ServerPassword,
		DefaultChannel:  req.DefaultChannel,
		ChannelPassword: req.ChannelPassword,
		MetaData:        furitoMeta(),
	}
}

func (c *conn) connect(req ConnectRequest) error {
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		return errors.New("informe o endereço do servidor")
	}
	nickname := ts3.NormalizeNickname(req.Nickname)

	// Encerra a conexão anterior antes de mudar o estado, para os eventos
	// dela não se misturarem com os da nova.
	c.cancelReconnect(false)
	c.client.Disconnect()
	if err := c.useIdentity(req.IdentityID); err != nil {
		return err
	}

	c.app.mu.Lock()
	c.status = statusConnecting
	c.lastError = ""
	c.userDisconnect = false
	c.lastRequest = req
	c.lastChannel = 0
	c.host = req.Host
	c.app.settings.Host = req.Host
	c.app.settings.Nickname = nickname
	c.app.settings.DefaultChannel = req.DefaultChannel
	c.app.scheduleSaveLocked()
	c.app.mu.Unlock()
	// Emite na hora (sem agrupar): numa rede local a conexão termina antes
	// do agrupamento e a interface nunca veria o estado "conectando".
	if c == c.app.current() {
		c.app.emit("state", c.buildState())
	} else {
		c.markDirty()
	}
	c.addLog("info", fmt.Sprintf("Tentando conectar-se ao servidor [b]%s[/b]", req.Host))

	err := c.client.Connect(connectOptions(req))
	if err != nil {
		c.app.mu.Lock()
		c.status = statusDisconnected
		c.lastError = err.Error()
		c.app.mu.Unlock()
		c.addLog("error", "Falha ao conectar: "+err.Error())
		c.markDirty()
		return err
	}
	return nil
}

// publishMuteState conta a todos os servidores como estão o microfone e os
// alto-falantes: mutar vale para o app inteiro.
func (a *App) publishMuteState() {
	for _, c := range a.connList() {
		c.publishMuteState()
	}
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
