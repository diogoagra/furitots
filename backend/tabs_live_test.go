package backend

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"furito-ts/audio"
	"furito-ts/ts3"
)

// observer é um segundo app, conectado a um dos servidores, que conta os
// pacotes de voz que recebe.
type observer struct {
	app   *App
	voice atomic.Int64
}

func newObserver(t *testing.T, host, nickname string) *observer {
	t.Helper()
	o := &observer{}
	o.app, _ = newTestApp(t)
	c := o.app.current()
	push := c.client.OnVoice
	c.client.OnVoice = func(from, seq uint16, codec byte, opusData []byte) {
		if len(opusData) > 0 {
			o.voice.Add(1)
		}
		push(from, seq, codec, opusData)
	}
	if err := o.app.API().Connect(ConnectRequest{Host: host, Nickname: nickname}); err != nil {
		t.Fatal(err)
	}
	return o
}

// sees devolve como este observador enxerga o cliente com esse apelido.
func (o *observer) sees(nickname string) (ts3.ClientInfo, bool) {
	for _, cl := range o.app.current().client.Snapshot().Clients {
		if cl.Nickname == nickname {
			return cl, true
		}
	}
	return ts3.ClientInfo{}, false
}

// Um app em dois servidores ao mesmo tempo: a voz vai só para o servidor da
// aba do microfone, o outro nos mostra com o microfone desativado, e o
// bate-papo de cada aba fica no servidor dela.
//
//	FURITO_TABS_HOST_A=127.0.0.1:19987 FURITO_TABS_HOST_B=127.0.0.1:19988 go test ./backend/ -run LiveTwoServers -v
func TestLiveTwoServers(t *testing.T) {
	hostA, hostB := os.Getenv("FURITO_TABS_HOST_A"), os.Getenv("FURITO_TABS_HOST_B")
	if hostA == "" || hostB == "" {
		t.Skip("defina FURITO_TABS_HOST_A e FURITO_TABS_HOST_B (dois servidores diferentes)")
	}
	const me = "DuasAbas"
	obsA, obsB := newObserver(t, hostA, "ObservadorA"), newObserver(t, hostB, "ObservadorB")

	app, _ := newTestApp(t)
	b := app.Bridge()
	tabA := app.current()
	if err := tabA.api().Connect(ConnectRequest{Host: hostA, Nickname: me}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "conectar ao servidor A", 12*time.Second, tabA.connected)
	idB, err := tabA.api().ConnectNewTab(ConnectRequest{Host: hostB, Nickname: me})
	if err != nil {
		t.Fatal(err)
	}
	tabB := app.connByID(idB)
	if tabB == nil || tabB == tabA {
		t.Fatalf("a segunda conexão deveria abrir outra aba (aba %d)", idB)
	}
	waitFor(t, "conectar ao servidor B", 12*time.Second, tabB.connected)
	if app.current() != tabB {
		t.Error("a aba nova deveria ficar à vista")
	}
	if app.talker() != tabA {
		t.Fatal("o microfone deveria continuar na primeira aba")
	}

	hardware := func(o *observer, want bool) func() bool {
		return func() bool { cl, ok := o.sees(me); return ok && cl.InputHardware == want }
	}
	waitFor(t, "o servidor A nos mostrar com o microfone em uso", 8*time.Second, hardware(obsA, true))
	waitFor(t, "o servidor B nos mostrar com o microfone desativado", 8*time.Second, hardware(obsB, false))

	// fala por um instante e conta o que cada servidor entregou ao observador
	app.engine.SetVADMode(audio.VADPTT)
	speak := func() (a, b int64) {
		obsA.voice.Store(0)
		obsB.voice.Store(0)
		stop, done := make(chan struct{}), make(chan struct{})
		app.engine.SetPTT(true)
		go feedVoice(app, true, stop, done)
		time.Sleep(1500 * time.Millisecond)
		close(stop)
		<-done
		app.engine.SetPTT(false)
		time.Sleep(400 * time.Millisecond)
		return obsA.voice.Load(), obsB.voice.Load()
	}
	if a, bb := speak(); a < 20 || bb != 0 {
		t.Errorf("com o microfone na aba A: %d pacotes no servidor A e %d no B (esperado: vários e nenhum)", a, bb)
	}

	if _, err := b.Call(tabB.id, "SetMicTab", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "o servidor B nos mostrar com o microfone em uso", 8*time.Second, hardware(obsB, true))
	waitFor(t, "o servidor A nos mostrar com o microfone desativado", 8*time.Second, hardware(obsA, false))
	if a, bb := speak(); a != 0 || bb < 20 {
		t.Errorf("com o microfone na aba B: %d pacotes no servidor A e %d no B (esperado: nenhum e vários)", a, bb)
	}

	// o bate-papo de uma aba fica no servidor dela
	if _, err := b.Call(tabB.id, "SendText", raw(t, 2, 0, "só no servidor B")); err != nil {
		t.Fatal(err)
	}
	got := func(c *conn, text string) bool {
		res, _ := c.api().GetMessages(), 0
		for _, m := range res {
			if m.Kind == "chat" && m.Text == text {
				return true
			}
		}
		return false
	}
	waitFor(t, "a mensagem chegar ao observador do servidor B", 5*time.Second, func() bool { return got(obsB.app.current(), "só no servidor B") })
	if !got(tabB, "só no servidor B") || got(tabA, "só no servidor B") || got(obsA.app.current(), "só no servidor B") {
		t.Error("a mensagem deveria aparecer só na aba e no servidor B")
	}

	// fechar a aba A sai só do servidor A
	if _, err := b.Call(tabA.id, "CloseTab", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sair do servidor A", 5*time.Second, func() bool { _, ok := obsA.sees(me); return !ok })
	if _, ok := obsB.sees(me); !ok || !tabB.connected() {
		t.Error("fechar uma aba não deveria derrubar a outra")
	}
	if tabs := app.tabList(); len(tabs) != 1 || tabs[0].ID != tabB.id || !tabs[0].Mic {
		t.Errorf("abas depois de fechar a A: %+v", tabs)
	}
}
