package backend

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"furito-ts/ts3"
)

// conn é uma conexão com um servidor: tudo o que uma aba tem de seu. O áudio,
// os ajustes, as identidades e os scripts são do App e valem para todas.
type conn struct {
	app    *App
	id     int // número da aba; não se repete enquanto o app está aberto
	client *ts3.Client

	selfID  atomic.Uint32
	codec   atomic.Uint32 // codec do canal atual (4 = Opus Voice, 5 = Opus Music)
	whisper atomic.Pointer[WhisperDTO]
	dirty   atomic.Bool // mudou desde a última vez que o estado foi publicado

	// protegidos por app.mu:
	status           string
	lastError        string
	host             string
	messages         []Message
	lastRequest      ConnectRequest // inclui senhas; só em memória
	reconnectAttempt int
	reconnectTimer   *time.Timer
	userDisconnect   bool
	lastChannel      int
	historyKey       string
	silenced         bool // "silenciar esta aba": as vozes deste servidor não tocam
	unread           int  // mensagens de bate-papo recebidas com a aba fora da vista

	// protegidos por app.identMu:
	identity *ts3.Identity // a identidade em uso nesta conexão (a de identID)
	identID  string

	// protegidos por app.iconMu: o que este servidor não tem (ou mandou
	// estragado). Vale até a próxima conexão.
	iconFailed   map[uint32]bool
	avatarFailed map[string]bool
}

// audioID é o número desta conexão para o motor de áudio.
func (c *conn) audioID() uint32 { return uint32(c.id) }

// newConn cria uma conexão desconectada, com a identidade padrão, e a põe no
// fim da lista.
func (a *App) newConn() *conn {
	a.identMu.Lock()
	entry := findIdentity(a.idents, a.identDefault)
	a.identMu.Unlock()

	c := &conn{
		app:          a,
		status:       statusDisconnected,
		identity:     entry.Ident,
		identID:      entry.ID,
		iconFailed:   make(map[uint32]bool),
		avatarFailed: make(map[string]bool),
	}
	c.codec.Store(4)
	c.client = ts3.NewClient(entry.Ident)
	if a.opts.Debug {
		c.client.Logf = func(format string, args ...any) { fmt.Printf("[ts3] "+format+"\n", args...) }
	}
	c.client.OnStateChanged = c.markDirty
	c.client.OnConnected = c.onConnected
	c.client.OnDisconnected = c.onDisconnected
	c.client.OnTextMessage = c.onTextMessage
	c.client.OnLog = func(level, text string) { c.addLog(level, text) }
	c.client.OnWelcome = func(text string) { c.addMessage(Message{Kind: "log", Level: "success", Text: text, Rich: true}) }
	c.client.OnNotify = c.notify
	c.client.OnClientEvent = c.onClientEvent
	c.client.OnPoke = c.onPoke
	c.client.OnPluginCommand = c.onPluginCommand
	c.client.OnVoice = func(from, seq uint16, _ byte, opusData []byte) {
		if uint32(from) != c.selfID.Load() {
			a.engine.PushVoice(c.audioID(), from, seq, opusData)
		}
	}

	a.mu.Lock()
	a.nextConnID++
	c.id = a.nextConnID
	a.conns = append(a.conns, c)
	a.mu.Unlock()
	return c
}

// current devolve a conexão que a interface está mostrando.
func (a *App) current() *conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.view
}

// connByID acha uma conexão pelo número da aba; 0 é a que está à vista.
func (a *App) connByID(id int) *conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == 0 {
		return a.view
	}
	for _, c := range a.conns {
		if c.id == id {
			return c
		}
	}
	return nil
}

// connList devolve as conexões, na ordem das abas.
func (a *App) connList() []*conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*conn(nil), a.conns...)
}

// anyConnected diz se alguma conexão está de pé.
func (a *App) anyConnected() bool {
	for _, c := range a.connList() {
		if c.client.IsConnected() {
			return true
		}
	}
	return false
}

// markDirty avisa que o estado desta conexão mudou.
func (c *conn) markDirty() {
	c.dirty.Store(true)
	c.app.wake()
}

// notify toca o aviso sonoro de um evento desta conexão. A que está à vista
// toca todos; das outras só toca o que é com a pessoa (cutucada, ser movida,
// expulsa ou banida, a conexão cair ou voltar): o entra-e-sai de um servidor
// que ela não está vendo não diria nada.
func (c *conn) notify(event string) {
	if c != c.app.current() && !notifyFromAnyTab(event) {
		return
	}
	c.app.notify(event)
}

func notifyFromAnyTab(event string) bool {
	return event == "OTHER_RECEIVED_POKE" || strings.HasPrefix(event, "YOU_") || strings.HasPrefix(event, "CONNECTION_")
}

// publishMuteState conta a este servidor como estão o microfone e os
// alto-falantes. Aba silenciada aparece com os alto-falantes mutados.
func (c *conn) publishMuteState() {
	a := c.app
	a.mu.Lock()
	silenced := c.silenced
	a.mu.Unlock()
	c.client.SetMuteState(a.engine.Muted(), a.engine.Deafened() || silenced)
}

// addLog põe uma linha no registro da conexão que está à vista: é onde vão os
// avisos que não são de um servidor (soundboard, atalhos, dispositivos).
func (a *App) addLog(level, text string) { a.current().addLog(level, text) }
