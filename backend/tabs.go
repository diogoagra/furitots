package backend

import (
	"errors"
	"fmt"
	"strings"
)

// maxTabs limita quantas conexões ficam abertas ao mesmo tempo.
const maxTabs = 8

// TabDTO resume uma conexão para a barra de abas da interface.
type TabDTO struct {
	ID       int    `json:"id"`
	Name     string `json:"name"` // nome do servidor; o endereço enquanto não se sabe o nome
	Host     string `json:"host"`
	Status   string `json:"status"`   // disconnected | connecting | connected | reconnecting
	Mic      bool   `json:"mic"`      // é para este servidor que vai o que falamos
	Silenced bool   `json:"silenced"` // "silenciar esta aba": não ouvimos este servidor
	Unread   int    `json:"unread"`   // mensagens de bate-papo que chegaram com a aba fora da vista
}

// tabList devolve o resumo das abas, na ordem em que aparecem.
func (a *App) tabList() []TabDTO {
	mic := a.talker()
	a.mu.Lock()
	conns := append([]*conn(nil), a.conns...)
	out := make([]TabDTO, len(conns))
	for i, c := range conns {
		out[i] = TabDTO{ID: c.id, Host: c.host, Status: c.status, Mic: c == mic, Silenced: c.silenced, Unread: c.unread}
	}
	a.mu.Unlock()
	for i, c := range conns {
		if out[i].Name = c.client.ServerName(); out[i].Name == "" {
			out[i].Name = out[i].Host
		}
	}
	return out
}

func sameTabs(a, b []TabDTO) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// openTab cria uma aba nova, desconectada, no fim da lista.
func (a *App) openTab() (*conn, error) {
	a.mu.Lock()
	n := len(a.conns)
	a.mu.Unlock()
	if n >= maxTabs {
		return nil, fmt.Errorf("limite de %d abas: feche uma para abrir outra", maxTabs)
	}
	return a.newConn(), nil
}

// selectTab põe a conexão c à vista: é dela o estado que a interface recebe.
func (a *App) selectTab(c *conn) {
	a.mu.Lock()
	a.view = c
	c.unread = 0
	a.mu.Unlock()
	a.markDirty()
}

// removeTab tira da lista uma conexão já encerrada. A última aba nunca sai:
// o app sempre tem uma, mesmo desconectada.
func (a *App) removeTab(c *conn) {
	a.mu.Lock()
	if len(a.conns) <= 1 {
		a.mu.Unlock()
		a.markDirty()
		return
	}
	for i, o := range a.conns {
		if o != c {
			continue
		}
		a.conns = append(a.conns[:i:i], a.conns[i+1:]...)
		if a.view == c { // fica à vista a vizinha: a de antes, ou a primeira
			a.view = a.conns[max(i-1, 0)]
			a.view.unread = 0
		}
		break
	}
	a.mu.Unlock()
	a.engine.ResetStreams(c.audioID())
	a.engine.SetConnMuted(c.audioID(), false)
	a.markDirty()
}

// connected diz se a conexão está de pé.
func (c *conn) connected() bool {
	c.app.mu.Lock()
	defer c.app.mu.Unlock()
	return c.status == statusConnected
}

// releaseMic é chamado quando a conexão c deixou de servir para falar: a
// pessoa saiu do servidor, fechou a aba, ou a conexão acabou sem volta. O
// microfone só muda de servidor sozinho quando sobra uma única conexão de pé;
// com duas ou mais ele fica sem destino até a pessoa escolher, para a voz
// não ir parar num servidor que ela não escolheu.
func (a *App) releaseMic(c *conn) {
	if a.talker() != c {
		return
	}
	var live []*conn
	for _, o := range a.connList() {
		if o != c && o.connected() {
			live = append(live, o)
		}
	}
	if len(live) == 1 {
		a.setMic(live[0])
	} else {
		a.setMic(nil)
	}
}

// claimMic é chamado quando a conexão c acaba de ser aceita pelo servidor: ela
// fica com o microfone se ninguém o tem (ou se quem tinha já não tem conexão
// nenhuma); senão avisa o servidor que ali o microfone está desativado, como
// faz o cliente TeamSpeak nas abas em que não se fala.
func (a *App) claimMic(c *conn) {
	mic := a.talker()
	if mic == c {
		return
	}
	if mic == nil || mic.idle() {
		a.setMic(c)
		return
	}
	c.client.SetInputHardware(false)
}

// ---------------------------------------------------------------- o que a interface chama

// ConnectNewTab conecta a um servidor sem sair dos outros: abre uma aba nova,
// que passa a ser a que está à vista. Se a aba deste pedido está parada (sem
// conexão), a conexão é feita nela mesma, para não sobrar aba vazia. Devolve o
// número da aba usada.
func (api *API) ConnectNewTab(req ConnectRequest) (int, error) {
	a, c := api.app, api.c
	if strings.TrimSpace(req.Host) == "" {
		return 0, errors.New("informe o endereço do servidor")
	}
	created := false
	if !c.idle() {
		var err error
		if c, err = a.openTab(); err != nil {
			return 0, err
		}
		created = true
	}
	prev := a.current()
	a.selectTab(c)
	if err := c.connect(req); err != nil {
		if created {
			a.selectTab(prev)
			a.removeTab(c)
		}
		return 0, err
	}
	return c.id, nil
}

// ConnectBookmarkNewTab conecta a um favorito numa aba nova (ver ConnectNewTab).
func (api *API) ConnectBookmarkNewTab(id string) (int, error) {
	req, err := api.app.bookmarkRequest(id)
	if err != nil {
		return 0, err
	}
	return api.ConnectNewTab(req)
}

// SelectTab põe esta aba à vista e devolve o estado dela.
func (api *API) SelectTab() State {
	api.app.selectTab(api.c)
	return api.c.buildState()
}

// CloseTab sai do servidor desta aba e a fecha. A última aba não some: fica
// desconectada.
func (api *API) CloseTab() {
	a, c := api.app, api.c
	c.cancelReconnect(true)
	c.client.Disconnect()
	a.releaseMic(c) // já sem efeito se a queda da conexão cuidou disso
	if a.recording() == c {
		a.stopRecording()
	}
	a.removeTab(c)
}

// SetMicTab passa o microfone para esta aba: é para o servidor dela que vai o
// que falamos (e o soundboard). Os outros servidores passam a nos mostrar com
// o microfone desativado.
func (api *API) SetMicTab() error {
	if api.c.idle() {
		return errors.New("conecte esta aba a um servidor para falar nela")
	}
	api.app.setMic(api.c)
	return nil
}

// SetTabSilenced silencia (ou volta a ouvir) o servidor desta aba, sem sair
// dele. O servidor mostra os nossos alto-falantes como mutados enquanto isso.
func (api *API) SetTabSilenced(on bool) {
	a, c := api.app, api.c
	a.mu.Lock()
	c.silenced = on
	a.mu.Unlock()
	a.engine.SetConnMuted(c.audioID(), on)
	c.publishMuteState()
	c.markDirty()
	a.markDirty()
}
