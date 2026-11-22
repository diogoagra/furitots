package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"furito-ts/ts3"
)

// Scripts: pequenas automações escritas pelo usuário em JavaScript ("quando
// alguém entrar no canal, mande uma mensagem", "toque um som", "a cada hora,
// avise…").
//
// O código roda na interface, num Web Worker isolado por script (ver
// frontend/script-worker.js e frontend/scripts.js): sem acesso à janela, à
// rede ou à ponte com o backend. Daqui saem os eventos que os scripts recebem
// ("script:event") e por aqui passa tudo o que eles pedem (ScriptCall) — a
// lista de ações abaixo é o limite do que um script consegue fazer.

const (
	maxScripts    = 50
	maxScriptCode = 256 << 10
	maxScriptName = 60

	// Mensagens, cutucadas e ações de moderação de scripts: até
	// scriptTalkBurst seguidas, depois uma a cada scriptTalkEvery, somando
	// todos os scripts. O servidor bloqueia (e pode banir) quem manda comandos
	// demais em pouco tempo.
	scriptTalkBurst = 4
	scriptTalkEvery = 5 * time.Second

	maxMessageRunes = 1024
	maxPokeRunes    = 100

	// Sons do soundboard tocados por scripts (para o canal ou só para nós):
	// até scriptSoundBurst seguidos, depois um a cada scriptSoundEvery.
	scriptSoundBurst = 5
	scriptSoundEvery = 3 * time.Second
)

// ScriptDTO é um script do usuário.
type ScriptDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Enabled bool   `json:"enabled"`
	// Moderation: o usuário permitiu que este script mova, expulse, bana e dê
	// a palavra a outras pessoas. Sem isso, essas ações são recusadas.
	Moderation bool `json:"moderation"`
}

// ScriptEvent é o que um script recebe em ts.on(tipo, …).
type ScriptEvent struct {
	Tab     int            `json:"tab"` // a conexão (aba) em que aconteceu
	Type    string         `json:"type"`
	Client  *ScriptClient  `json:"client,omitempty"`
	Channel *ScriptChannel `json:"channel,omitempty"` // o canal do evento (o nosso, nas entradas e saídas)
	From    *ScriptChannel `json:"from,omitempty"`
	To      *ScriptChannel `json:"to,omitempty"`
	Reason  string         `json:"reason,omitempty"`
	Invoker string         `json:"invoker,omitempty"`
	Text    string         `json:"text,omitempty"`
	Target  string         `json:"target,omitempty"` // private | channel | server
	Server  string         `json:"server,omitempty"` // nome do servidor
}

// ScriptClient é um cliente como os scripts o veem.
type ScriptClient struct {
	ID           int      `json:"id"`
	Nickname     string   `json:"nickname"`
	UID          string   `json:"uid"`
	ChannelID    int      `json:"channelId"`
	Away         bool     `json:"away"`
	AwayMessage  string   `json:"awayMessage"`
	InputMuted   bool     `json:"inputMuted"`
	OutputMuted  bool     `json:"outputMuted"`
	ServerGroups []string `json:"serverGroups"`
	Country      string   `json:"country"`
	IsTalker     bool     `json:"isTalker"`    // recebeu a palavra num canal moderado
	TalkRequest  bool     `json:"talkRequest"` // está pedindo a palavra
	Self         bool     `json:"self"`
}

// ScriptChannel é um canal como os scripts o veem.
type ScriptChannel struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ParentID    int    `json:"parentId"`
	Topic       string `json:"topic"`
	HasPassword bool   `json:"hasPassword"`
	IsDefault   bool   `json:"isDefault"`
}

// ScriptSelf é a resposta de ts.getSelf().
type ScriptSelf struct {
	Connected   bool   `json:"connected"`
	ID          int    `json:"id"`
	Nickname    string `json:"nickname"`
	ChannelID   int    `json:"channelId"`
	ChannelName string `json:"channelName"`
	Server      string `json:"server"`
	Muted       bool   `json:"muted"`
	Deafened    bool   `json:"deafened"`
	Away        bool   `json:"away"`
	AwayMessage string `json:"awayMessage"`
}

// O script de exemplos que vem com o app: entra na lista uma vez, desligado.
const (
	exampleScriptID   = "exemplos"
	exampleScriptName = "Exemplos: todas as funções"
)

// scriptStore guarda os scripts (scripts.json, na pasta de dados do app).
type scriptStore struct {
	mu     sync.Mutex
	path   string
	list   []ScriptDTO
	seeded bool // o script de exemplos já foi posto na lista

	soundTokens float64
	soundLast   time.Time
}

type scriptFile struct {
	Scripts []ScriptDTO `json:"scripts"`
	// Seeded: o script de exemplos já entrou na lista uma vez. Se o usuário o
	// apagar, ele não volta sozinho.
	Seeded bool `json:"seeded"`
}

// load lê os scripts guardados. Na primeira vez (inclusive para quem já
// tinha scripts de antes), põe no começo da lista o script de exemplos,
// desligado; example é o código dele ("" = não há).
func (st *scriptStore) load(path, example string) {
	st.path = path
	var f scriptFile
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Unmarshal(data, &f) != nil {
			return // arquivo ilegível: fica como está, sem gravar nada por cima
		}
	case !errors.Is(err, os.ErrNotExist):
		return
	}
	for _, s := range f.Scripts {
		if s.ID != "" && len(st.list) < maxScripts {
			st.list = append(st.list, s)
		}
	}
	st.seeded = f.Seeded
	if !st.seeded && example != "" && len(st.list) < maxScripts {
		st.list = append([]ScriptDTO{{ID: exampleScriptID, Name: exampleScriptName, Code: example}}, st.list...)
		st.seeded = true
		st.saveLocked() // se não der para gravar agora, tenta de novo na próxima abertura
	}
}

// saveLocked grava a lista: primeiro num arquivo ao lado, depois troca, para
// uma queda no meio não deixar o arquivo pela metade.
func (st *scriptStore) saveLocked() error {
	data, err := json.MarshalIndent(scriptFile{Scripts: st.list, Seeded: st.seeded}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// running devolve o script com esse id, se existir e estiver ligado.
func (st *scriptStore) running(id string) (ScriptDTO, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, s := range st.list {
		if s.ID == id {
			return s, s.Enabled
		}
	}
	return ScriptDTO{}, false
}

func (st *scriptStore) anyEnabled() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, s := range st.list {
		if s.Enabled {
			return true
		}
	}
	return false
}

// allowSound diz se mais um som de script pode tocar agora.
func (st *scriptStore) allowSound(now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.soundLast.IsZero() {
		st.soundTokens = scriptSoundBurst
	} else {
		st.soundTokens = min(scriptSoundBurst, st.soundTokens+float64(now.Sub(st.soundLast))/float64(scriptSoundEvery))
	}
	st.soundLast = now
	if st.soundTokens < 1 {
		return false
	}
	st.soundTokens--
	return true
}

// allowScriptTalk diz se mais uma mensagem, cutucada ou ação de moderação de
// script pode sair agora por esta conexão. O limite existe por causa do
// bloqueio por flood, que é de cada servidor: por isso é contado por conexão.
func (c *conn) allowScriptTalk(now time.Time) bool {
	c.talkMu.Lock()
	defer c.talkMu.Unlock()
	if c.talkLast.IsZero() {
		c.talkTokens = scriptTalkBurst
	} else {
		c.talkTokens = min(scriptTalkBurst, c.talkTokens+float64(now.Sub(c.talkLast))/float64(scriptTalkEvery))
	}
	c.talkLast = now
	if c.talkTokens < 1 {
		return false
	}
	c.talkTokens--
	return true
}

// ---------------------------------------------------------------- API: gerência

// GetScripts devolve os scripts do usuário.
func (api *API) GetScripts() []ScriptDTO {
	st := &api.app.scripts
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]ScriptDTO{}, st.list...)
}

// SaveScript cria (ID vazio) ou atualiza um script e devolve como ficou.
func (api *API) SaveScript(s ScriptDTO) (ScriptDTO, error) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		return ScriptDTO{}, errors.New("dê um nome ao script")
	}
	if utf8.RuneCountInString(s.Name) > maxScriptName {
		return ScriptDTO{}, fmt.Errorf("nome longo demais (máximo %d caracteres)", maxScriptName)
	}
	if len(s.Code) > maxScriptCode {
		return ScriptDTO{}, fmt.Errorf("script grande demais (máximo %d KB)", maxScriptCode>>10)
	}
	st := &api.app.scripts
	st.mu.Lock()
	defer st.mu.Unlock()
	if s.ID == "" {
		if len(st.list) >= maxScripts {
			return ScriptDTO{}, fmt.Errorf("limite de %d scripts atingido", maxScripts)
		}
		s.ID = fmt.Sprintf("s%x", time.Now().UnixNano())
		st.list = append(st.list, s)
	} else {
		found := false
		for i := range st.list {
			if st.list[i].ID == s.ID {
				st.list[i], found = s, true
			}
		}
		if !found {
			return ScriptDTO{}, errors.New("script não encontrado")
		}
	}
	if err := st.saveLocked(); err != nil {
		return ScriptDTO{}, fmt.Errorf("não foi possível gravar os scripts: %w", err)
	}
	return s, nil
}

// DeleteScript apaga um script.
func (api *API) DeleteScript(id string) error {
	st := &api.app.scripts
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.list {
		if st.list[i].ID == id {
			st.list = append(st.list[:i:i], st.list[i+1:]...)
			return st.saveLocked()
		}
	}
	return errors.New("script não encontrado")
}

// SetScriptEnabled liga ou desliga um script.
func (api *API) SetScriptEnabled(id string, on bool) error {
	st := &api.app.scripts
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.list {
		if st.list[i].ID == id {
			st.list[i].Enabled = on
			return st.saveLocked()
		}
	}
	return errors.New("script não encontrado")
}

// ---------------------------------------------------------------- eventos

// scriptEvent entrega um evento aos scripts (se houver algum ligado).
func (c *conn) scriptEvent(ev ScriptEvent) {
	if c.app.scripts.anyEnabled() {
		ev.Tab = c.id
		c.app.emit("script:event", ev)
	}
}

func scriptClient(cl ts3.ClientInfo, snap ts3.Snapshot) *ScriptClient {
	out := &ScriptClient{
		ID: int(cl.ID), Nickname: cl.Nickname, UID: cl.UID, ChannelID: cl.ChannelID,
		Away: cl.Away, AwayMessage: cl.AwayMessage, InputMuted: cl.InputMuted, OutputMuted: cl.OutputMuted,
		ServerGroups: make([]string, 0, len(cl.ServerGroups)), Country: cl.Country,
		IsTalker: cl.IsTalker, TalkRequest: cl.TalkRequest,
		Self: cl.ID == snap.SelfID && snap.SelfID != 0,
	}
	for _, id := range cl.ServerGroups {
		if g, ok := snap.ServerGroups[id]; ok {
			out.ServerGroups = append(out.ServerGroups, g.Name)
		}
	}
	return out
}

func scriptChannelFrom(ch ts3.Channel) ScriptChannel {
	return ScriptChannel{ID: ch.ID, Name: ch.Name, ParentID: ch.ParentID, Topic: ch.Topic, HasPassword: ch.HasPassword, IsDefault: ch.IsDefault}
}

// scriptChannel devolve o canal com esse id (nil para 0; só o id se o canal
// não está na nossa lista).
func scriptChannel(id int, snap ts3.Snapshot) *ScriptChannel {
	if id == 0 {
		return nil
	}
	for _, oc := range snap.Channels {
		if oc.Channel.ID == id {
			ch := scriptChannelFrom(oc.Channel)
			return &ch
		}
	}
	return &ScriptChannel{ID: id}
}

// scriptSender descreve quem mandou uma mensagem ou cutucada: o cliente como
// está no servidor (achado pelo id ou, sem id, pelo apelido — que é único no
// servidor), ou só o que veio na mensagem, se ele já saiu.
func (c *conn) scriptSender(id int, name, uid string) *ScriptClient {
	snap := c.client.Snapshot()
	for _, cl := range snap.Clients {
		if (id != 0 && int(cl.ID) == id) || (id == 0 && cl.Nickname == name) {
			return scriptClient(cl, snap)
		}
	}
	return &ScriptClient{ID: id, Nickname: name, UID: uid, ServerGroups: []string{}}
}

// onClientEvent traduz as movimentações de clientes em eventos para os scripts.
func (c *conn) onClientEvent(ev ts3.ClientEvent) {
	if !c.app.scripts.anyEnabled() {
		return
	}
	snap := c.client.Snapshot()
	out := ScriptEvent{
		Client: scriptClient(ev.Client, snap),
		From:   scriptChannel(ev.From, snap),
		To:     scriptChannel(ev.To, snap),
		Reason: ev.Reason, Invoker: ev.Invoker,
	}
	switch ev.Kind {
	case ts3.ClientJoined:
		out.Type, out.Channel = "clientJoined", out.To
	case ts3.ClientLeft:
		out.Type, out.Channel = "clientLeft", out.From
	case ts3.ClientConnected:
		out.Type, out.Channel = "clientConnected", out.To
	case ts3.ClientDisconnected:
		out.Type, out.Channel = "clientDisconnected", out.From
	case ts3.SelfMoved:
		out.Type, out.Channel = "channelChanged", out.To
	default:
		return
	}
	out.Tab = c.id
	c.app.emit("script:event", out)
}

// ---------------------------------------------------------------- ações

// ScriptCall executa, em nome de um script ligado, uma das ações que os
// scripts podem pedir. Tudo o que um script faz passa por aqui.
func (api *API) ScriptCall(id, action string, args map[string]any) (any, error) {
	a := api.app
	sc, on := a.scripts.running(id)
	if !on {
		return nil, errors.New("este script está desligado")
	}
	str := func(key string) string { s, _ := args[key].(string); return s }
	num := func(key string) int { f, _ := args[key].(float64); return int(f) }
	flag := func(key string) bool { b, _ := args[key].(bool); return b }

	switch action {
	case "sendChannelMessage":
		return nil, api.c.scriptTalk(func() { api.c.client.SendTextMessage(2, 0, str("text")) }, str("text"), maxMessageRunes)
	case "sendServerMessage":
		return nil, api.c.scriptTalk(func() { api.c.client.SendTextMessage(3, 0, str("text")) }, str("text"), maxMessageRunes)
	case "sendPrivateMessage":
		to, err := api.c.scriptTarget(num("clientId"))
		if err != nil {
			return nil, err
		}
		return nil, api.c.scriptTalk(func() { api.c.client.SendTextMessage(1, to, str("text")) }, str("text"), maxMessageRunes)
	case "poke":
		to, err := api.c.scriptTarget(num("clientId"))
		if err != nil {
			return nil, err
		}
		var pokeErr error
		if err := api.c.scriptTalk(func() { pokeErr = api.c.client.PokeClient(uint16(to), str("text")) }, str("text"), maxPokeRunes); err != nil {
			return nil, err
		}
		return nil, pokeErr

	// moderação: só para scripts em que o usuário marcou "Pode moderar"; o
	// servidor ainda decide se nós temos a permissão
	case "moveClient":
		ch := scriptChannel(num("channelId"), api.c.client.Snapshot())
		if sc.Moderation && (ch == nil || ch.Name == "") {
			return nil, errors.New("canal não encontrado")
		}
		return nil, api.c.scriptModerate(sc, num("clientId"), "moveu %s para o canal", func() string { return quote(ch.Name) }, func(to uint16) error {
			return api.c.client.MoveClient(to, ch.ID, str("password"))
		})
	case "kickFromChannel":
		return nil, api.c.scriptModerate(sc, num("clientId"), "expulsou %s do canal", reasonNote(str("reason")), func(to uint16) error {
			return api.c.client.KickClient(to, false, str("reason"))
		})
	case "kickFromServer":
		return nil, api.c.scriptModerate(sc, num("clientId"), "expulsou %s do servidor", reasonNote(str("reason")), func(to uint16) error {
			return api.c.client.KickClient(to, true, str("reason"))
		})
	case "ban":
		// A duração tem de vir escrita: esquecer o número não pode virar um
		// banimento permanente (que é o que 0 significa).
		secs, given := args["seconds"].(float64)
		if sc.Moderation && (!given || secs < 0 || secs != float64(int(secs))) {
			return nil, errors.New("informe a duração do banimento em segundos (0 = permanente)")
		}
		how := fmt.Sprintf("baniu %%s por %d s", int(secs))
		if secs == 0 {
			how = "baniu %s permanentemente"
		}
		return nil, api.c.scriptModerate(sc, num("clientId"), how, reasonNote(str("reason")), func(to uint16) error {
			return api.c.client.BanClient(to, int(secs), str("reason"))
		})
	case "setTalker":
		what := "tirou a palavra de %s"
		if flag("talker") {
			what = "deu a palavra a %s"
		}
		return nil, api.c.scriptModerate(sc, num("clientId"), what, nil, func(to uint16) error {
			return api.c.client.SetClientTalker(to, flag("talker"))
		})

	case "playSound":
		if !a.scripts.allowSound(time.Now()) {
			return nil, errScriptSoundFlood
		}
		// O som "para o canal" sai pelo microfone, que está num servidor só:
		// pedido por outra conexão, tocaria para as pessoas erradas.
		if !flag("local") && a.talker() != api.c {
			return nil, errScriptSoundElsewhere
		}
		return nil, a.scriptPlaySound(str("name"), flag("local"))
	case "stopSound":
		api.SoundboardStop()
		return nil, nil

	case "setAway":
		api.SetAway(flag("away"), str("message"))
		return nil, nil
	// Um script pode fechar o microfone e os alto-falantes, nunca abrir: ligar
	// o microfone de alguém sem que a pessoa peça é escuta. Quem abre é sempre
	// o usuário, pelo botão ou pelo atalho.
	case "setMuted":
		if !flag("muted") {
			if a.engine.Muted() {
				return nil, errScriptUnmute
			}
			return nil, nil
		}
		if !a.engine.Muted() {
			api.ToggleMute()
		}
		return nil, nil
	case "setDeafened":
		if !flag("deafened") {
			if a.engine.Deafened() {
				return nil, errScriptUnmute
			}
			return nil, nil
		}
		if !a.engine.Deafened() {
			api.ToggleDeafen()
		}
		return nil, nil
	case "switchChannel":
		if !api.c.client.IsConnected() {
			return nil, errors.New("não conectado")
		}
		id := num("channelId")
		if ch := scriptChannel(id, api.c.client.Snapshot()); ch == nil || ch.Name == "" {
			return nil, errors.New("canal não encontrado")
		}
		api.c.client.JoinChannel(id, str("password"))
		return nil, nil

	case "getSelf":
		return api.c.scriptSelf(), nil
	case "getClients":
		snap := api.c.client.Snapshot()
		out := make([]*ScriptClient, 0, len(snap.Clients))
		if snap.Connected {
			for _, cl := range snap.Clients {
				out = append(out, scriptClient(cl, snap))
			}
		}
		return out, nil
	case "getChannels":
		snap := api.c.client.Snapshot()
		out := make([]ScriptChannel, 0, len(snap.Channels))
		if snap.Connected {
			for _, oc := range snap.Channels {
				out = append(out, scriptChannelFrom(oc.Channel))
			}
		}
		return out, nil
	case "getSounds":
		a.mu.Lock()
		defer a.mu.Unlock()
		out := make([]string, 0, len(a.settings.Sounds))
		for _, s := range a.settings.Sounds {
			out = append(out, s.Name)
		}
		return out, nil
	}
	return nil, fmt.Errorf("ação desconhecida: %q", action)
}

// scriptTalk envia uma mensagem ou cutucada de script, se o texto for válido,
// houver conexão e o limite contra flood permitir.
func (c *conn) scriptTalk(send func(), text string, maxRunes int) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("mensagem vazia")
	}
	if utf8.RuneCountInString(text) > maxRunes {
		return fmt.Errorf("mensagem longa demais (máximo %d caracteres)", maxRunes)
	}
	if !c.client.IsConnected() {
		return errors.New("não conectado")
	}
	if !c.allowScriptTalk(time.Now()) {
		return errScriptFlood
	}
	send()
	return nil
}

var (
	errScriptSoundElsewhere = errors.New("o microfone está em outro servidor: daqui o som não chegaria a este canal (use ts.playSoundLocal para ouvir só você)")
	errScriptUnmute         = errors.New("por segurança, um script não liga o microfone nem os alto-falantes: ele só pode mutar; quem desmuta é você")
	errScriptSoundFlood     = fmt.Errorf("muitos sons em pouco tempo: os scripts podem tocar %d seguidos e depois um a cada %d s",
		scriptSoundBurst, int(scriptSoundEvery/time.Second))
)

var errScriptFlood = fmt.Errorf("muitas ações em pouco tempo: os scripts podem fazer %d seguidas (mensagens, cutucadas e moderação) e depois uma a cada %d s (proteção contra o bloqueio por flood do servidor)",
	scriptTalkBurst, int(scriptTalkEvery/time.Second))

func quote(name string) string { return `"` + name + `"` }

// reasonNote devolve, para o registro, o motivo que o script deu a uma ação.
func reasonNote(reason string) func() string {
	return func() string {
		if strings.TrimSpace(reason) == "" {
			return ""
		}
		return "(" + reason + ")"
	}
}

// scriptModerate executa uma ação de moderação pedida por um script: só se o
// usuário deu essa permissão ao script, o alvo é outra pessoa que está no
// servidor e o limite contra flood permite. O que foi feito fica no registro
// do servidor: done é a frase, com %s no lugar do apelido do alvo, e note um
// complemento opcional (texto livre, que não passa por formatação).
func (c *conn) scriptModerate(sc ScriptDTO, clientID int, done string, note func() string, act func(target uint16) error) error {
	if !sc.Moderation {
		return errors.New(`este script não tem permissão para moderar: marque "Pode moderar" nele, na janela de scripts`)
	}
	if _, err := c.scriptTarget(clientID); err != nil {
		return err
	}
	nick := ""
	for _, cl := range c.client.Snapshot().Clients {
		if int(cl.ID) == clientID {
			nick = cl.Nickname
		}
	}
	if !c.allowScriptTalk(time.Now()) {
		return errScriptFlood
	}
	if err := act(uint16(clientID)); err != nil {
		return err
	}
	line := fmt.Sprintf("Script %s "+done, quote(sc.Name), "[b]"+quote(nick)+"[/b]")
	if note != nil {
		if extra := note(); extra != "" {
			line += " " + extra
		}
	}
	c.addLog("warn", line)
	return nil
}

// scriptTarget confere que o destino de uma mensagem privada ou cutucada é um
// cliente que está no servidor (e não nós mesmos).
func (c *conn) scriptTarget(id int) (int, error) {
	if !c.client.IsConnected() {
		return 0, errors.New("não conectado")
	}
	snap := c.client.Snapshot()
	for _, cl := range snap.Clients {
		if int(cl.ID) == id && cl.ID != snap.SelfID {
			return id, nil
		}
	}
	return 0, errors.New("cliente não encontrado")
}

// scriptPlaySound toca um som do soundboard pelo nome: para o canal (como um
// clique no soundboard) ou só nos nossos alto-falantes.
func (a *App) scriptPlaySound(name string, local bool) error {
	want := strings.ToLower(strings.TrimSpace(name))
	a.mu.Lock()
	var found *SoundSetting
	for i := range a.settings.Sounds {
		if s := a.settings.Sounds[i]; found == nil && (strings.ToLower(s.Name) == want || s.ID == name) {
			found = &s
		}
	}
	volume := a.settings.SoundboardVolume / 100
	a.mu.Unlock()
	if found == nil {
		return fmt.Errorf("não há som chamado %q no soundboard", name)
	}
	if !a.opts.DisableAudioDevices {
		if err := a.engine.StartPlayback(); err != nil {
			return err
		}
	}
	if !local {
		if err := a.playSound(found.ID); err != nil {
			return err
		}
		a.markDirty()
		return nil
	}
	pcm, err := a.engine.Soundboard().Load(a.soundPath(*found))
	if err != nil {
		return err
	}
	scaled := make([]int16, len(pcm))
	for i, v := range pcm {
		scaled[i] = int16(float64(v) * volume)
	}
	a.engine.PlayLocal(scaled)
	return nil
}

func (c *conn) scriptSelf() ScriptSelf {
	snap := c.client.Snapshot()
	c.app.mu.Lock()
	out := ScriptSelf{Away: c.app.away, AwayMessage: c.app.awayMessage}
	c.app.mu.Unlock()
	out.Muted, out.Deafened = c.app.engine.Muted(), c.app.engine.Deafened()
	if !snap.Connected {
		return out
	}
	out.Connected, out.ID, out.ChannelID, out.Server = true, int(snap.SelfID), snap.SelfChannelID, snap.Server.Name
	if ch := scriptChannel(snap.SelfChannelID, snap); ch != nil {
		out.ChannelName = ch.Name
	}
	for _, cl := range snap.Clients {
		if cl.ID == snap.SelfID {
			out.Nickname = cl.Nickname
		}
	}
	return out
}

// ---------------------------------------------------------------- isolamento

// ScriptWorkerPath é o endereço, dentro da janela, do código que roda os
// scripts (frontend/script-worker.js).
const ScriptWorkerPath = "/script-worker.js"

// scriptWorkerCSP é a política de segurança entregue junto com esse código:
// dentro do worker nada de rede (nem buscar outros arquivos do app, nem
// importar código de fora); só o que o script precisa para ser compilado.
const scriptWorkerCSP = "default-src 'none'; script-src 'unsafe-eval'"

// pageCSP é a política de segurança da própria janela. A interface mostra
// textos escritos por outras pessoas (mensagens, descrições, apelidos) e os
// trata como texto; esta política é a segunda barreira, para o caso de um
// erro nesse tratamento: mesmo que um trecho de HTML de fora chegasse à
// página, não haveria como ele executar código (só os arquivos .js do próprio
// app rodam: nada de script embutido, de atributo onclick, de eval), carregar
// código de outro lugar, abrir conexões para fora, embutir outras páginas ou
// enviar formulários. Imagens de fora continuam possíveis (as das descrições,
// quando o usuário as libera); estilos embutidos também, porque as cores e os
// tamanhos do BBCode são escritos assim.
const pageCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https: http:; connect-src 'self'; worker-src 'self'; child-src 'self'; media-src 'self' blob: mediastream:; font-src 'self'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// ScriptSandbox acrescenta as políticas de segurança às respostas do servidor
// de arquivos da janela (vai como Middleware dele): a do isolamento, na
// resposta que entrega o código dos workers de script, e a da página em todas
// as outras.
func ScriptSandbox(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ScriptWorkerPath {
			w.Header().Set("Content-Security-Policy", scriptWorkerCSP)
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Content-Security-Policy", pageCSP)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
