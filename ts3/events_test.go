package ts3

import (
	"fmt"
	"strings"
	"testing"
)

// As movimentações de clientes chegam como eventos estruturados: quem entrou
// ou saiu do nosso canal, quem conectou ou saiu do servidor e as nossas
// próprias trocas de canal — sem confundir com quem só ficou visível.
func TestClientEvents(t *testing.T) {
	c := NewClient(nil)
	s := &session{c: c, done: make(chan struct{}), cryptoInit: true}
	s.clientID.Store(1)
	c.sess = s

	var got []string
	c.OnClientEvent = func(ev ClientEvent) {
		line := fmt.Sprintf("%s %s %d>%d %s", ev.Kind, ev.Client.Nickname, ev.From, ev.To, ev.Reason)
		if ev.Invoker != "" {
			line += " por " + ev.Invoker
		}
		got = append(got, line)
	}
	expect := func(what string, lines []string, want ...string) {
		t.Helper()
		got = nil
		for _, l := range lines {
			c.handleCommand(s, l)
		}
		if strings.Join(got, " | ") != strings.Join(want, " | ") {
			t.Errorf("%s:\n got %q\nwant %q", what, got, want)
		}
	}

	expect("a nossa entrada e quem já estava no servidor não são eventos", []string{
		`channellist cid=1 cpid=0 channel_name=Lobby|cid=2 cpid=0 channel_name=Sala|cid=3 cpid=0 channel_name=Outra`,
		`notifycliententerview cfid=0 ctid=1 reasonid=0 clid=1 client_nickname=Eu`,
		`notifycliententerview cfid=0 ctid=1 reasonid=2 clid=5 client_nickname=Antigo`,
		`notifycliententerview cfid=0 ctid=2 reasonid=2 clid=6 client_nickname=NaSala`,
	})

	expect("alguém conecta direto no nosso canal", []string{
		`notifycliententerview cfid=0 ctid=1 reasonid=0 clid=7 client_nickname=Ana client_unique_identifier=uidAna`,
	}, "connected Ana 0>1 connected", "joined Ana 0>1 connected")

	expect("alguém conecta em outro canal", []string{
		`notifycliententerview cfid=0 ctid=2 reasonid=0 clid=8 client_nickname=Beto`,
	}, "connected Beto 0>2 connected")

	expect("troca de canal para o nosso, saindo dele e sem nos envolver", []string{
		`notifyclientmoved ctid=1 reasonid=0 clid=8`,
		`notifyclientmoved ctid=3 reasonid=0 clid=8`,
		`notifyclientmoved ctid=2 reasonid=0 clid=8`,
	}, "joined Beto 2>1 switched", "left Beto 1>3 switched")

	expect("movido e expulso do canal por alguém", []string{
		`notifyclientmoved ctid=1 reasonid=1 clid=8 invokerid=9 invokername=Admin`,
		`notifyclientmoved ctid=2 reasonid=4 clid=8 invokerid=9 invokername=Admin`,
	}, "joined Beto 2>1 moved por Admin", "left Beto 1>2 kicked por Admin")

	expect("aparece vindo de um canal que não enxergávamos", []string{
		`notifycliententerview cfid=9 ctid=1 reasonid=0 clid=10 client_nickname=Caio`,
	}, "joined Caio 9>1 switched")

	expect("some indo para um canal que não enxergamos (não saiu do servidor)", []string{
		`notifyclientleftview cfid=1 ctid=9 reasonid=0 clid=10`,
	}, "left Caio 1>9 switched")

	expect("desconecta do nosso canal e de outro canal", []string{
		`notifyclientleftview cfid=1 ctid=0 reasonid=8 reasonmsg=tchau clid=7`,
		`notifyclientleftview cfid=2 ctid=0 reasonid=3 clid=8`,
	}, "left Ana 1>0 disconnected", "disconnected Ana 1>0 disconnected", "disconnected Beto 2>0 lost")

	expect("banido do servidor estando no nosso canal", []string{
		`notifyclientleftview cfid=1 ctid=0 reasonid=6 clid=5 invokername=Admin`,
	}, "left Antigo 1>0 banned por Admin", "disconnected Antigo 1>0 banned por Admin")

	expect("nós mudamos de canal: quem já estava lá não vira evento de entrada", []string{
		`notifyclientmoved ctid=2 reasonid=0 clid=1`,
	}, "self Eu 1>2 switched")

	expect("depois da troca, o nosso canal é o novo", []string{
		`notifyclientmoved ctid=1 reasonid=0 clid=6`,
		`notifyclientmoved ctid=2 reasonid=0 clid=6`,
	}, "left NaSala 2>1 switched", "joined NaSala 1>2 switched")

	expect("fomos movidos por alguém", []string{
		`notifyclientmoved ctid=1 reasonid=1 clid=1 invokername=Admin`,
	}, "self Eu 2>1 moved por Admin")
}

// Quem é expulso de um canal vê o motivo no registro, como já via na
// expulsão do servidor.
func TestChannelKickLogShowsReason(t *testing.T) {
	c := NewClient(nil)
	s := &session{c: c, done: make(chan struct{}), cryptoInit: true}
	s.clientID.Store(1)
	c.sess = s
	var lines []string
	c.OnLog = func(level, text string) { lines = append(lines, level+": "+text) }
	for _, l := range []string{
		`channellist cid=1 cpid=0 channel_name=Lobby|cid=2 cpid=0 channel_name=Sala`,
		`notifycliententerview cfid=0 ctid=2 reasonid=0 clid=1 client_nickname=Eu`,
		`notifycliententerview cfid=0 ctid=2 reasonid=2 clid=5 client_nickname=Beto`,
		`notifyclientmoved ctid=1 reasonid=4 clid=1 invokerid=9 invokername=Admin reasonmsg=olha\sa\slinguagem`,
		`notifyclientmoved ctid=1 reasonid=4 clid=5 invokerid=9 invokername=Admin`,
	} {
		c.handleCommand(s, l)
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, `warn: Você foi expulso do canal [b]"Sala"[/b] por [b]"Admin"[/b] (olha a linguagem)`) {
		t.Errorf("a expulsão do canal deveria mostrar o motivo:\n%s", got)
	}
	if !strings.Contains(got, `warn: [b]"Beto"[/b] foi expulso do canal [b]"Sala"[/b] por [b]"Admin"[/b]`) || strings.Contains(got, `"Admin"[/b] ()`) {
		t.Errorf("sem motivo, a linha não leva parênteses:\n%s", got)
	}
}
