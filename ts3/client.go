package ts3

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ConnectOptions são os dados de uma conexão.
type ConnectOptions struct {
	Host            string
	Nickname        string
	ServerPassword  string
	DefaultChannel  string
	ChannelPassword string

	// Como o cliente se apresenta. Vazio = a versão padrão, assinada pela
	// TeamSpeak (DefaultVersion): a assinatura vale para o par versão +
	// plataforma, e servidores que a conferem recusam qualquer outro texto.
	Version     string
	Platform    string
	VersionSign string
	// MetaData é um texto livre que os outros clientes recebem junto com a
	// nossa entrada (client_meta_data).
	MetaData string
}

// Versão padrão com que o cliente se apresenta: um trio (versão, plataforma,
// assinatura) emitido pela TeamSpeak e aceito por qualquer servidor.
const (
	DefaultVersion     = "3.?.? [Build: 5680278000]"
	DefaultPlatform    = "Linux"
	DefaultVersionSign = "Hjd+N58Gv3ENhoKmGYy2bNRBsNNgm5kpiaQWxOj5HN2DXttG6REjymSwJtpJ8muC2gSwRuZi0R+8Laan5ts5CQ=="
)

// Client é um cliente TeamSpeak (protocolo TS3, aceito também pelo TS6 Server).
//
// Os callbacks devem ser definidos antes de Connect. São chamados a partir das
// goroutines da conexão, nunca com travas internas seguradas.
type Client struct {
	identity *Identity

	mu            sync.RWMutex
	sess          *session
	nickname      string
	server        ServerInfo
	channels      map[int]*Channel
	clients       map[uint16]*ClientInfo
	serverGroups  map[int]GroupInfo
	channelGroups map[int]GroupInfo

	ftMu     sync.Mutex
	ftNext   uint16
	ftWait   map[uint16]chan ftResult
	ftSem    chan struct{} // uma transferência por vez
	ftPace   time.Duration // intervalo mínimo entre pedidos (só quem segura ftSem mexe)
	ftLast   time.Time
	ftFloods atomic.Int64 // quantas vezes o servidor reclamou de excesso

	req requester

	// OnStateChanged avisa que canais/clientes/servidor mudaram (use Snapshot).
	OnStateChanged func()
	// OnConnected é chamado quando o servidor aceita a conexão.
	OnConnected func()
	// OnDisconnected é chamado uma vez por conexão encerrada.
	OnDisconnected func(reason string, isError bool)
	// OnTextMessage entrega mensagens de texto (inclusive o eco das nossas).
	OnTextMessage func(TextMessage)
	// OnVoice entrega um pacote de voz. opus vazio = o cliente parou de falar.
	OnVoice func(from uint16, seq uint16, codec byte, opus []byte)
	// OnLog entrega eventos legíveis (nível: info, success, warn, error). O
	// texto é do app, com nomes e motivos escritos por terceiros no meio: quem
	// o mostra não deve interpretar formatação nele, além do [b] dos nomes.
	OnLog func(level, text string)
	// OnWelcome entrega a mensagem de boas-vindas do servidor, que é BBCode
	// escrito pelo dono do servidor. Sem ele, a mensagem vai para OnLog.
	OnWelcome func(text string)
	// OnPoke é chamado quando alguém nos cutuca.
	OnPoke func(fromName, message string)
	// OnPluginCommand entrega os comandos de plugin de outros clientes
	// (SendPluginCommand). Os nossos próprios, que o servidor devolve nos
	// envios para o canal, não são entregues.
	OnPluginCommand func(cmd PluginCommand)
	// OnNotify entrega eventos com os nomes do pacote de sons do TeamSpeak
	// (ex.: CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS, YOU_SWITCHED_CHANNEL).
	OnNotify func(event string)
	// OnClientEvent entrega, de forma estruturada, quem conectou ou saiu do
	// servidor, quem entrou ou saiu do nosso canal e as nossas trocas de canal.
	OnClientEvent func(ClientEvent)
	// Logf, se definido, recebe o tráfego de comandos para depuração.
	Logf func(format string, args ...any)
}

func NewClient(ident *Identity) *Client {
	return &Client{
		identity:      ident,
		channels:      make(map[int]*Channel),
		clients:       make(map[uint16]*ClientInfo),
		serverGroups:  make(map[int]GroupInfo),
		channelGroups: make(map[int]GroupInfo),
		ftWait:        make(map[uint16]chan ftResult),
		ftSem:         make(chan struct{}, 1),
		ftPace:        60 * time.Millisecond,
	}
}

// ---------------------------------------------------------------- conexão

// NormalizeNickname aplica as regras do servidor (3 a 30 caracteres).
func NormalizeNickname(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) > 30 {
		runes = runes[:30]
	}
	if len(runes) < 3 {
		return "FuritoUser"
	}
	return string(runes)
}

func resolveAddr(host string) (*net.UDPAddr, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("informe o endereço do servidor")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return net.ResolveUDPAddr("udp", host)
	}
	name := strings.Trim(host, "[]")
	// Sem porta: tenta o registro SRV do TeamSpeak antes da porta padrão.
	if net.ParseIP(name) == nil && name != "localhost" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, srvs, err := net.DefaultResolver.LookupSRV(ctx, "ts3", "udp", name)
		cancel()
		if err == nil && len(srvs) > 0 {
			target := strings.TrimSuffix(srvs[0].Target, ".")
			if addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(target, strconv.Itoa(int(srvs[0].Port)))); err == nil {
				return addr, nil
			}
		}
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(name, "9987"))
}

// Connect inicia uma conexão. Só devolve erro para falhas imediatas (endereço
// inválido); o resultado chega por OnConnected / OnDisconnected.
func (c *Client) Connect(opts ConnectOptions) error {
	c.Disconnect()

	addr, err := resolveAddr(opts.Host)
	if err != nil {
		return fmt.Errorf("não foi possível resolver %q: %w", opts.Host, err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}

	s := newSession(c, conn, opts)
	c.mu.Lock()
	c.sess = s
	c.nickname = NormalizeNickname(opts.Nickname)
	c.resetStateLocked()
	c.mu.Unlock()

	go s.readLoop()
	go s.maintain()
	s.sendInit0()
	return nil
}

// Disconnect avisa o servidor e encerra a conexão atual, se houver.
func (c *Client) Disconnect() {
	s := c.session()
	if s == nil {
		return
	}
	if s.connected.Load() {
		id := s.sendCommand(BuildCommand("clientdisconnect", Param{"reasonid", "8"}, Param{"reasonmsg", "saindo"}))
		s.waitAck(ptCommand, id, 500*time.Millisecond)
	}
	c.endSession(s, "desconectado", false)
}

func (c *Client) session() *session {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sess
}

func (c *Client) resetStateLocked() {
	c.server = ServerInfo{}
	c.channels = make(map[int]*Channel)
	c.clients = make(map[uint16]*ClientInfo)
	c.serverGroups = make(map[int]GroupInfo)
	c.channelGroups = make(map[int]GroupInfo)
}

func (c *Client) endSession(s *session, reason string, isError bool) {
	fired := false
	s.endOnce.Do(func() {
		fired = true
		s.connected.Store(false)
		close(s.done)
		s.conn.Close()
	})
	if !fired {
		return
	}
	c.mu.Lock()
	if c.sess == s {
		c.sess = nil
		c.resetStateLocked()
	}
	c.mu.Unlock()

	if c.OnDisconnected != nil {
		c.OnDisconnected(reason, isError)
	}
	c.stateChanged()
}

func (c *Client) stateChanged() {
	if c.OnStateChanged != nil {
		c.OnStateChanged()
	}
}

func (c *Client) log(level, text string) {
	if c.OnLog != nil {
		c.OnLog(level, text)
	}
}

func (c *Client) notify(event string) {
	if c.OnNotify != nil {
		c.OnNotify(event)
	}
}

func (c *Client) debugf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

var secretParam = regexp.MustCompile(`((?:password|cpw|token)=)\S+`)

func (c *Client) debugOut(cmd string) {
	if c.Logf != nil {
		c.Logf("-> %s", clip(secretParam.ReplaceAllString(cmd, "$1<oculto>"), 400))
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---------------------------------------------------------------- consultas

// IsConnected indica se o servidor já aceitou a conexão.
func (c *Client) IsConnected() bool {
	s := c.session()
	return s != nil && s.connected.Load()
}

// ClientID devolve nosso id de cliente (0 se desconectado).
func (c *Client) ClientID() uint16 {
	if s := c.session(); s != nil {
		return uint16(s.clientID.Load())
	}
	return 0
}

// Snapshot devolve uma cópia consistente do estado atual.
func (c *Client) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := Snapshot{
		Server:        c.server,
		ServerGroups:  make(map[int]GroupInfo, len(c.serverGroups)),
		ChannelGroups: make(map[int]GroupInfo, len(c.channelGroups)),
	}
	if c.sess != nil {
		snap.Connected = c.sess.connected.Load()
		snap.SelfID = uint16(c.sess.clientID.Load())
	}
	for k, v := range c.serverGroups {
		snap.ServerGroups[k] = v
	}
	for k, v := range c.channelGroups {
		snap.ChannelGroups[k] = v
	}
	snap.Channels = orderChannels(c.channels)
	snap.Clients = make([]ClientInfo, 0, len(c.clients))
	for _, cl := range c.clients {
		cp := *cl
		cp.ServerGroups = append([]int(nil), cl.ServerGroups...)
		snap.Clients = append(snap.Clients, cp)
		if cl.ID == snap.SelfID {
			snap.SelfChannelID = cl.ChannelID
		}
	}
	sort.Slice(snap.Clients, func(i, j int) bool {
		a, b := strings.ToLower(snap.Clients[i].Nickname), strings.ToLower(snap.Clients[j].Nickname)
		if a != b {
			return a < b
		}
		return snap.Clients[i].ID < snap.Clients[j].ID
	})
	return snap
}

// ---------------------------------------------------------------- comandos

func (c *Client) send(cmd string) bool {
	s := c.session()
	if s == nil || !s.connected.Load() {
		return false
	}
	s.sendCommand(cmd)
	return true
}

// JoinChannel move o próprio cliente para um canal.
func (c *Client) JoinChannel(channelID int, password string) {
	if channelID <= 0 {
		return
	}
	params := []Param{
		{"clid", strconv.Itoa(int(c.ClientID()))},
		{"cid", strconv.Itoa(channelID)},
	}
	if password != "" {
		params = append(params, Param{"cpw", HashPassword(password)})
	}
	c.send(BuildCommand("clientmove", params...))
}

// SendTextMessage envia texto: targetMode 1 = privado (target = clid),
// 2 = canal atual, 3 = servidor.
func (c *Client) SendTextMessage(targetMode, target int, msg string) {
	c.send(BuildCommand("sendtextmessage",
		Param{"targetmode", strconv.Itoa(targetMode)},
		Param{"target", strconv.Itoa(target)},
		Param{"msg", msg},
	))
}

// backgroundCode marca comandos que o app envia por conta própria; a resposta
// de erro deles é ignorada.
const backgroundCode = "bg"

// RequestChannelDescription pede a descrição de um canal (chega por estado).
func (c *Client) RequestChannelDescription(channelID int) {
	c.send(BuildCommand("channelgetdescription", Param{"cid", strconv.Itoa(channelID)}, Param{"return_code", backgroundCode}))
}

// RequestClientInfo pede versão/plataforma e tempo de conexão de um cliente.
func (c *Client) RequestClientInfo(clientID uint16) {
	id := strconv.Itoa(int(clientID))
	c.send(BuildCommand("clientgetvariables", Param{"clid", id}, Param{"return_code", backgroundCode}))
	c.send(BuildCommand("getconnectioninfo", Param{"clid", id}, Param{"return_code", backgroundCode}))
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// SetMuteState publica no servidor se microfone/alto-falantes estão mutados.
func (c *Client) SetMuteState(inputMuted, outputMuted bool) {
	c.send(BuildCommand("clientupdate",
		Param{"client_input_muted", boolParam(inputMuted)},
		Param{"client_output_muted", boolParam(outputMuted)},
	))
}

// SetInputHardware publica se o microfone está em uso nesta conexão. Com
// várias conexões abertas fala-se em uma por vez: nas outras o microfone
// aparece como desativado (não como mutado), igual ao cliente oficial.
func (c *Client) SetInputHardware(on bool) {
	c.send(BuildCommand("clientupdate", Param{"client_input_hardware", boolParam(on)}))
}

// ServerName devolve o nome do servidor ("" se ainda não o sabemos).
func (c *Client) ServerName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.server.Name
}

// SetRecording publica se estamos gravando a conversa: os outros clientes
// mostram o indicador de gravação ao lado do nosso nome, como faz o cliente
// oficial.
func (c *Client) SetRecording(on bool) {
	c.send(BuildCommand("clientupdate", Param{"client_is_recording", boolParam(on)}))
}

// SetAway publica o estado de ausente.
func (c *Client) SetAway(away bool, message string) {
	c.send(BuildCommand("clientupdate",
		Param{"client_away", boolParam(away)},
		Param{"client_away_message", message},
	))
}

// SendVoice envia um quadro Opus de 20 ms. opus vazio sinaliza fim da fala.
func (c *Client) SendVoice(opus []byte, codec byte) {
	s := c.session()
	if s == nil || !s.connected.Load() {
		return
	}
	buf := make([]byte, 3+len(opus))
	buf[2] = codec
	copy(buf[3:], opus)
	s.sendPacket(ptVoice, 0, buf)
}

// ---------------------------------------------------------------- eventos

var errorTranslations = []struct{ needle, text string }{
	{"invalid server password", "senha do servidor incorreta"},
	{"invalid channel password", "senha do canal incorreta"},
	{"invalid password", "senha incorreta"},
	{"nickname is already in use", "este apelido já está em uso"},
	{"banned", "você está banido deste servidor"},
	{"maximum", "o servidor ou canal está cheio"},
	{"max clients", "o servidor ou canal está cheio"},
	{"insufficient client permissions", "permissão insuficiente"},
	{"security level", "nível de segurança da identidade insuficiente"},
	{"too many clones", "muitas conexões do mesmo endereço"},
	{"flood", "muitos comandos em sequência (proteção anti-flood)"},
	{"already member of channel", "você já está neste canal"},
}

func translateError(msg string) string {
	low := strings.ToLower(msg)
	for _, t := range errorTranslations {
		if strings.Contains(low, t.needle) {
			return t.text
		}
	}
	return msg
}

func quoted(name string) string {
	return `[b]"` + name + `"[/b]`
}

func (c *Client) handleCommand(s *session, line string) {
	c.debugf("<- %s", clip(line, 400))
	name, items := ParseCommand(line)
	if len(items) == 0 {
		return
	}
	first := items[0]
	// Antes de a chave da sessão existir só chega a resposta do handshake.
	// Tudo o que vem nessa fase está cifrado com a chave fixa do protocolo,
	// que é pública: aceitar um "initserver" (ou qualquer outro comando) agora
	// abriria uma sessão inteira com quem só sabe mandar pacotes.
	if !s.cryptoInit && name != "initivexpand2" && name != "initivexpand" {
		c.debugf("comando %q ignorado: chegou antes da troca de chaves", name)
		return
	}
	if c.requestLine(name, items) {
		return
	}

	switch name {
	case "initivexpand2":
		s.setupCrypto2(first)
		return
	case "initivexpand":
		s.setupCrypto(first)
		return
	case "notifystartdownload", "notifystartupload":
		c.ftDeliver(uint16(atoi(first["clientftfid"])), ftResult{
			key:  first["ftkey"],
			port: atoi(first["port"]),
			size: int64(atoi(first["size"])),
			ip:   first["ip"],
		})
		return
	case "notifystatusfiletransfer":
		msg := translateError(first["msg"])
		if msg == "" {
			msg = "transferência recusada pelo servidor"
		}
		c.ftDeliver(uint16(atoi(first["clientftfid"])), ftResult{err: &ServerError{ID: atoi(first["status"]), Message: msg}})
		return
	case "error":
		if first["return_code"] == backgroundCode {
			// Pedido automático do app (detalhes de um cliente, descrição de
			// canal…): se o servidor negar, não é erro para mostrar nem tocar.
			return
		}
		if rc := first["return_code"]; strings.HasPrefix(rc, "ft") {
			// Resposta a um pedido de transferência de arquivo: não vai para o log.
			if id := first["id"]; id != "" && id != "0" {
				n, _ := strconv.Atoi(rc[2:])
				c.ftDeliver(uint16(n), ftResult{err: &ServerError{ID: atoi(id), Message: translateError(first["msg"]), Extra: first["extra_msg"]}})
			}
			return
		}
		if id := first["id"]; id != "" && id != "0" {
			msg := translateError(first["msg"])
			if extra := first["extra_msg"]; extra != "" {
				msg += " (" + extra + ")"
			}
			if !s.connected.Load() {
				c.endSession(s, "o servidor recusou a conexão: "+msg, true)
			} else {
				c.log("error", "Erro do servidor: "+msg)
				if strings.Contains(first["msg"], "insufficient client permissions") {
					c.notify("SERVER_INSUFFICIENT_PERMISSIONS")
				} else {
					c.notify("SERVER_ERROR")
				}
			}
		}
		return
	}

	var post []func()
	changed := false
	logf := func(level, format string, args ...any) {
		text := fmt.Sprintf(format, args...)
		post = append(post, func() { c.log(level, text) })
	}
	sound := func(event string) {
		post = append(post, func() { c.notify(event) })
	}
	clientEvent := func(kind string, cl *ClientInfo, from, to int, reason, invoker string) {
		ev := ClientEvent{Kind: kind, Client: *cl, From: from, To: to, Reason: reason, Invoker: invoker}
		ev.Client.ServerGroups = append([]int(nil), cl.ServerGroups...)
		post = append(post, func() {
			if c.OnClientEvent != nil {
				c.OnClientEvent(ev)
			}
		})
	}

	c.mu.Lock()
	held := true
	defer func() {
		// Se algo quebrar com a trava tomada, ela é solta antes de o erro
		// seguir (quem o trata é o readLoop): presa, travaria a interface.
		if r := recover(); r != nil {
			if held {
				c.mu.Unlock()
			}
			panic(r)
		}
	}()
	if c.sess != s {
		held = false
		c.mu.Unlock()
		return
	}
	self := uint16(s.clientID.Load())
	chanName := func(id int) string {
		if ch, ok := c.channels[id]; ok {
			return ch.Name
		}
		return "canal " + strconv.Itoa(id)
	}
	selfChannel := func() int {
		if me, ok := c.clients[self]; ok {
			return me.ChannelID
		}
		return 0
	}

	switch name {
	case "initserver":
		if s.connected.Load() {
			break // repetido: a sessão já está aberta
		}
		s.clientID.Store(uint32(atoi(first["aclid"])))
		c.server = ServerInfo{
			Name:           first["virtualserver_name"],
			WelcomeMessage: first["virtualserver_welcomemessage"],
			Version:        first["virtualserver_version"],
			Platform:       first["virtualserver_platform"],
			MaxClients:     atoi(first["virtualserver_maxclients"]),
			UID:            first["virtualserver_unique_identifier"],
			BannerURL:      first["virtualserver_hostbanner_gfx_url"],
			BannerLink:     first["virtualserver_hostbanner_url"],
		}
		if nick := first["acn"]; nick != "" {
			c.nickname = nick
		}
		s.connectedAt = time.Now()
		s.connected.Store(true)
		changed = true
		welcome := c.server.WelcomeMessage
		serverName := c.server.Name
		post = append(post, func() {
			s.sendCommand("channelsubscribeall return_code=" + backgroundCode)
			if welcome != "" {
				if c.OnWelcome != nil {
					c.OnWelcome(welcome)
				} else {
					c.log("success", welcome)
				}
			}
			c.log("info", "Conectado ao servidor: "+quoted(serverName))
			if c.OnConnected != nil {
				c.OnConnected()
			}
		})

	case "notifyserveredited":
		if v, ok := first["virtualserver_name"]; ok {
			c.server.Name = v
		}
		if v, ok := first["virtualserver_welcomemessage"]; ok {
			c.server.WelcomeMessage = v
		}
		if v, ok := first["virtualserver_maxclients"]; ok {
			c.server.MaxClients = atoi(v)
		}
		if v, ok := first["virtualserver_hostbanner_gfx_url"]; ok {
			c.server.BannerURL = v
		}
		if v, ok := first["virtualserver_hostbanner_url"]; ok {
			c.server.BannerLink = v
		}
		changed = true

	case "channellist", "notifychannelcreated":
		for _, it := range items {
			idStr, ok := it["cid"]
			if !ok {
				continue
			}
			ch := &Channel{ID: atoi(idStr), MaxClients: -1}
			if _, known := c.channels[ch.ID]; !known && len(c.channels) >= maxChannels {
				continue
			}
			applyChannelFields(ch, it)
			c.channels[ch.ID] = ch
			changed = true
			if name == "notifychannelcreated" {
				// A inscrição feita ao conectar só vale para os canais que já
				// existiam; sem isto, quem entra num canal novo some da árvore.
				cid := ch.ID
				post = append(post, func() {
					s.sendCommand(BuildCommand("channelsubscribe", Param{"cid", itoa(cid)}, Param{"return_code", backgroundCode}))
				})
			}
		}

	case "channellistfinished":
		changed = true

	case "notifychanneledited":
		for _, it := range items {
			if ch, ok := c.channels[atoi(it["cid"])]; ok {
				applyChannelFields(ch, it)
				changed = true
			}
		}

	case "notifychanneldescriptionchanged":
		for _, it := range items {
			cid := atoi(it["cid"])
			if ch, ok := c.channels[cid]; ok && ch.DescriptionSet {
				// Já estávamos mostrando a descrição: busca a nova.
				post = append(post, func() { c.RequestChannelDescription(cid) })
			}
		}

	case "notifychannelmoved":
		for _, it := range items {
			if ch, ok := c.channels[atoi(it["cid"])]; ok {
				if v, ok := it["cpid"]; ok {
					ch.ParentID = atoi(v)
				}
				if v, ok := it["order"]; ok {
					ch.Order = atoi(v)
				}
				changed = true
			}
		}

	case "notifychanneldeleted":
		for _, it := range items {
			cid := atoi(it["cid"])
			if _, ok := c.channels[cid]; ok {
				delete(c.channels, cid)
				changed = true
			}
		}

	case "notifycliententerview":
		for _, it := range items {
			idStr, ok := it["clid"]
			if !ok {
				continue
			}
			clid := uint16(atoi(idStr))
			cl, known := c.clients[clid]
			if !known {
				cl = &ClientInfo{ID: clid}
				c.clients[clid] = cl
			}
			cl.ChannelID = atoi(it["ctid"])
			applyClientFields(cl, it)
			if clid == self && cl.ConnectedSince.IsZero() {
				// O servidor não informa o tempo da nossa própria conexão.
				cl.ConnectedSince = s.connectedAt
			}
			changed = true
			// reasonid 0 + cfid 0 = acabou de conectar (2 = ficou visível por inscrição)
			if !known && clid != self && it["reasonid"] == "0" && it["cfid"] == "0" {
				logf("info", "%s conectou ao canal %s", quoted(cl.Nickname), quoted(chanName(cl.ChannelID)))
			}
			if !known && clid != self && cl.ChannelID == selfChannel() {
				switch it["reasonid"] {
				case "0":
					if it["cfid"] == "0" {
						sound("CLIENT_CONNECTION_CONNECTED_CURRENT_CHANNEL")
					} else {
						sound("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_APPEARS")
					}
				case "1":
					sound("CLIENT_MOVED_TO_CURRENT_CHANNEL_APPEARS")
				case "4":
					sound("CLIENT_WAS_KICKED_FROM_CHANNEL_TO_CURRENT_CHANNEL_APPEARS")
				}
			}
			// reasonid 2 (ficou visível por inscrição) não é movimentação de ninguém
			if rid := it["reasonid"]; !known && clid != self && (rid == "0" || rid == "1" || rid == "4") {
				from, reason := atoi(it["cfid"]), moveReason(rid)
				if rid == "0" && from == 0 {
					reason = "connected"
					clientEvent(ClientConnected, cl, 0, cl.ChannelID, reason, "")
				}
				if cl.ChannelID == selfChannel() {
					clientEvent(ClientJoined, cl, from, cl.ChannelID, reason, it["invokername"])
				}
			}
		}

	case "notifyclientleftview":
		for _, it := range items {
			clid := uint16(atoi(it["clid"]))
			reason := it["reasonid"]
			msg := it["reasonmsg"]
			if clid == self {
				text, isErr := "desconectado", false
				switch reason {
				case "5":
					text, isErr = fmt.Sprintf("você foi expulso do servidor por %q", it["invokername"]), true
					sound("YOU_WERE_KICKED_FROM_SERVER")
				case "6":
					text, isErr = fmt.Sprintf("você foi banido do servidor por %q", it["invokername"]), true
					sound("YOU_WERE_BANNED")
				case "7", "11":
					text, isErr = "o servidor foi encerrado", true
				case "3":
					text, isErr = "conexão perdida", true
				}
				if msg != "" && isErr {
					text += " (" + msg + ")"
				}
				post = append(post, func() { c.endSession(s, text, isErr) })
				continue
			}
			cl, ok := c.clients[clid]
			if !ok {
				continue
			}
			if cl.ChannelID == selfChannel() {
				switch reason {
				case "8":
					sound("CLIENT_CONNECTION_DISCONNECTED_CURRENT_CHANNEL")
				case "3":
					sound("CLIENT_CONNECTION_LOST_CONNECTION_CURRENT_CHANNEL")
				case "5":
					sound("CLIENT_WAS_KICKED_FROM_SERVER_CURRENT_CHANNEL")
				case "6":
					sound("CLIENT_WAS_BANNED_CURRENT_CHANNEL")
				case "0":
					sound("CLIENT_SWITCHED_FROM_CURRENT_CHANNEL_DISAPPEARS")
				case "1":
					sound("CLIENT_MOVED_FROM_CURRENT_CHANNEL_DISAPPEARS")
				case "4":
					sound("CLIENT_WAS_KICKED_FROM_CHANNEL_FROM_CURRENT_CHANNEL_DISAPPEARS")
				}
			}
			gone := map[string]string{"8": "disconnected", "3": "lost", "5": "server-kick", "6": "banned"}[reason]
			if cl.ChannelID == selfChannel() {
				why := gone
				if why == "" {
					why = moveReason(reason) // só saiu da nossa vista: foi para um canal que não enxergamos
				}
				clientEvent(ClientLeft, cl, cl.ChannelID, atoi(it["ctid"]), why, it["invokername"])
			}
			if gone != "" {
				clientEvent(ClientDisconnected, cl, cl.ChannelID, 0, gone, it["invokername"])
			}
			delete(c.clients, clid)
			changed = true
			suffix := ""
			if msg != "" {
				suffix = " (" + msg + ")"
			}
			switch reason {
			case "8":
				logf("info", "%s desconectou%s", quoted(cl.Nickname), suffix)
			case "3":
				logf("info", "%s perdeu a conexão", quoted(cl.Nickname))
			case "5":
				logf("warn", "%s foi expulso do servidor por %s%s", quoted(cl.Nickname), quoted(it["invokername"]), suffix)
			case "6":
				logf("warn", "%s foi banido do servidor por %s%s", quoted(cl.Nickname), quoted(it["invokername"]), suffix)
			}
		}

	case "notifyclientmoved":
		for _, it := range items {
			clid := uint16(atoi(it["clid"]))
			cl, ok := c.clients[clid]
			if !ok {
				continue
			}
			from, to := cl.ChannelID, atoi(it["ctid"])
			mine := selfChannel()
			cl.ChannelID = to
			changed = true
			if clid != self && from != mine && to != mine {
				continue // movimentação que não envolve o nosso canal
			}
			reason := it["reasonid"]
			switch {
			case clid == self:
				clientEvent(SelfMoved, cl, from, to, moveReason(reason), it["invokername"])
			case to == mine:
				clientEvent(ClientJoined, cl, from, to, moveReason(reason), it["invokername"])
			default:
				clientEvent(ClientLeft, cl, from, to, moveReason(reason), it["invokername"])
			}
			switch {
			case clid == self && reason == "1":
				sound("YOU_WERE_MOVED_TO_DIFFERENT_CHANNEL")
			case clid == self && reason == "4":
				sound("YOU_WERE_KICKED_FROM_CHANNEL")
			case clid == self:
				sound("YOU_SWITCHED_CHANNEL")
			case to == mine && reason == "1":
				sound("CLIENT_MOVED_TO_CURRENT_CHANNEL_STAYS")
			case to == mine && reason == "4":
				sound("CLIENT_WAS_KICKED_FROM_CHANNEL_TO_CURRENT_CHANNEL_STAYS")
			case to == mine:
				sound("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS")
			case reason == "1":
				sound("CLIENT_MOVED_FROM_CURRENT_CHANNEL_STAYS")
			case reason == "4":
				sound("CLIENT_WAS_KICKED_FROM_CHANNEL_FROM_CURRENT_CHANNEL_STAYS")
			default:
				sound("CLIENT_SWITCHED_FROM_CURRENT_CHANNEL_STAYS")
			}
			who := quoted(cl.Nickname)
			if clid == self {
				who = "Você"
			}
			switch it["reasonid"] {
			case "1":
				logf("info", "%s foi movido para o canal %s por %s", who, quoted(chanName(to)), quoted(it["invokername"]))
			case "4":
				why := ""
				if msg := it["reasonmsg"]; msg != "" {
					why = " (" + msg + ")"
				}
				logf("warn", "%s foi expulso do canal %s por %s%s", who, quoted(chanName(from)), quoted(it["invokername"]), why)
			default:
				logf("info", "%s mudou do canal %s para o canal %s", who, quoted(chanName(from)), quoted(chanName(to)))
			}
		}

	case "notifyclientupdated":
		for _, it := range items {
			clid := uint16(atoi(it["clid"]))
			if cl, ok := c.clients[clid]; ok {
				applyClientFields(cl, it)
				if clid == self {
					c.nickname = cl.Nickname
				}
				changed = true
			}
		}

	case "notifyconnectioninfo":
		clid := uint16(atoi(first["clid"]))
		if cl, ok := c.clients[clid]; ok {
			if ms, err := strconv.ParseInt(first["connection_connected_time"], 10, 64); err == nil {
				cl.ConnectedSince = time.Now().Add(-time.Duration(ms) * time.Millisecond)
				changed = true
			}
		}

	case "notifyservergrouplist":
		for _, it := range items {
			if _, known := c.serverGroups[atoi(it["sgid"])]; !known && len(c.serverGroups) >= maxGroups {
				continue
			}
			c.serverGroups[atoi(it["sgid"])] = GroupInfo{Name: it["name"], IconID: iconID(it["iconid"]), SortID: atoi(it["sortid"]), NameMode: atoi(it["namemode"]), Type: atoi(it["type"])}
		}
		changed = true

	case "notifychannelgrouplist":
		for _, it := range items {
			if _, known := c.channelGroups[atoi(it["cgid"])]; !known && len(c.channelGroups) >= maxGroups {
				continue
			}
			c.channelGroups[atoi(it["cgid"])] = GroupInfo{Name: it["name"], IconID: iconID(it["iconid"]), SortID: atoi(it["sortid"]), NameMode: atoi(it["namemode"]), Type: atoi(it["type"])}
		}
		changed = true

	case "notifyservergroupclientadded", "notifyservergroupclientdeleted":
		for _, it := range items {
			cl, ok := c.clients[uint16(atoi(it["clid"]))]
			if !ok {
				continue
			}
			sgid := atoi(it["sgid"])
			groups := cl.ServerGroups[:0:0]
			for _, g := range cl.ServerGroups {
				if g != sgid {
					groups = append(groups, g)
				}
			}
			if name == "notifyservergroupclientadded" {
				groups = append(groups, sgid)
			}
			cl.ServerGroups = groups
			changed = true
		}

	case "notifyclientchannelgroupchanged":
		for _, it := range items {
			if cl, ok := c.clients[uint16(atoi(it["clid"]))]; ok {
				cl.ChannelGroupID = atoi(it["cgid"])
				changed = true
			}
		}

	case "notifyplugincmd":
		cmd := PluginCommand{
			Name:     first["name"],
			Data:     first["data"],
			FromID:   uint16(atoi(first["invokerid"])),
			FromName: first["invokername"],
			FromUID:  first["invokeruid"],
		}
		if cmd.FromID == uint16(s.clientID.Load()) {
			break // eco do nosso próprio envio ao canal
		}
		post = append(post, func() {
			if c.OnPluginCommand != nil {
				c.OnPluginCommand(cmd)
			}
		})

	case "notifyclientpoke":
		logf("warn", "%s cutucou você: %s", quoted(first["invokername"]), first["msg"])
		sound("OTHER_RECEIVED_POKE")
		from, text := first["invokername"], first["msg"]
		post = append(post, func() {
			if c.OnPoke != nil {
				c.OnPoke(from, text)
			}
		})

	case "notifytextmessage":
		msg := TextMessage{
			TargetMode:  atoi(first["targetmode"]),
			Target:      atoi(first["target"]),
			InvokerID:   atoi(first["invokerid"]),
			InvokerName: first["invokername"],
			InvokerUID:  first["invokeruid"],
			Text:        first["msg"],
		}
		post = append(post, func() {
			if c.OnTextMessage != nil {
				c.OnTextMessage(msg)
			}
		})
	}
	held = false
	c.mu.Unlock()

	for _, f := range post {
		f()
	}
	if changed {
		c.stateChanged()
	}
}
