package backend

import (
	"strings"
	"testing"
	"time"

	"furito-ts/ts3"
)

// newTab abre mais uma aba no app de teste.
func newTab(t *testing.T, app *App) *conn {
	t.Helper()
	c, err := app.openTab()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// up faz de conta que o servidor aceitou a conexão da aba.
func up(c *conn) {
	c.app.mu.Lock()
	c.lastRequest = ConnectRequest{Host: "exemplo.local", Nickname: "Furico"}
	c.app.mu.Unlock()
	c.onConnected()
}

// leave faz de conta que a pessoa saiu do servidor da aba.
func leave(c *conn) {
	c.cancelReconnect(true)
	c.onDisconnected("desconectado", false)
}

func tabOf(t *testing.T, app *App, c *conn) TabDTO {
	t.Helper()
	for _, tab := range app.tabList() {
		if tab.ID == c.id {
			return tab
		}
	}
	t.Fatalf("aba %d não está na lista", c.id)
	return TabDTO{}
}

// O microfone vai para um servidor só. Fica com a primeira conexão; uma aba
// nova não o toma de quem já está falando; a pessoa o passa com um clique.
func TestMicStaysOnTheChosenTab(t *testing.T) {
	app, _ := newTestApp(t)
	a := app.current()
	b := newTab(t, app)

	up(a)
	if app.talker() != a {
		t.Fatal("a primeira conexão deveria ficar com o microfone")
	}
	up(b)
	if app.talker() != a {
		t.Fatal("uma conexão nova não pode tomar o microfone de quem já fala em outra")
	}
	if !tabOf(t, app, a).Mic || tabOf(t, app, b).Mic {
		t.Errorf("resumo das abas com o microfone errado: %+v", app.tabList())
	}

	if _, err := app.Bridge().Call(b.id, "SetMicTab", nil); err != nil {
		t.Fatal(err)
	}
	if app.talker() != b || !tabOf(t, app, b).Mic || tabOf(t, app, a).Mic {
		t.Fatalf("o microfone deveria ter ido para a aba escolhida: %+v", app.tabList())
	}

	// aba parada não recebe o microfone
	c := newTab(t, app)
	if _, err := app.Bridge().Call(c.id, "SetMicTab", nil); err == nil {
		t.Error("uma aba sem conexão não deveria aceitar o microfone")
	}
	if app.talker() != b {
		t.Error("o pedido recusado não deveria mexer no microfone")
	}
}

// Quando a pessoa sai do servidor em que falava, o microfone só muda sozinho
// se sobrar uma única conexão. Com duas ou mais ele fica sem destino até ela
// escolher: a voz não vai para um servidor que ela não escolheu.
func TestMicDoesNotJumpToAnotherServer(t *testing.T) {
	app, _ := newTestApp(t)
	a := app.current()
	b, c := newTab(t, app), newTab(t, app)
	up(a)
	up(b)
	up(c)

	leave(a) // sobram duas: ninguém fica com o microfone
	if got := app.talker(); got != nil {
		t.Fatalf("com duas conexões de pé o microfone não deveria ir sozinho para a aba %d", got.id)
	}
	app.sendVoice([]byte{1, 2, 3}) // sem destino: não vai a lugar nenhum (e não quebra)

	leave(b) // sobra uma, mas ninguém tinha o microfone: continua sem destino
	if app.talker() != nil {
		t.Fatal("o microfone sem destino não deveria aparecer em outra aba sem pedido")
	}
	if _, err := app.Bridge().Call(c.id, "SetMicTab", nil); err != nil || app.talker() != c {
		t.Fatalf("a pessoa escolhe a aba e o microfone vai: %v", err)
	}

	up(b)
	if _, err := app.Bridge().Call(b.id, "SetMicTab", nil); err != nil {
		t.Fatal(err)
	}
	leave(b) // falava em b e sobrou só c: o microfone vai para ela
	if app.talker() != c {
		t.Fatal("sobrando uma única conexão, o microfone deveria ir para ela")
	}

	// a próxima conexão, com o microfone livre, fica com ele
	leave(c)
	if app.talker() != nil {
		t.Fatal("sem nenhuma conexão o microfone fica sem destino")
	}
	up(a)
	if app.talker() != a {
		t.Fatal("com o microfone livre, a conexão que chega fica com ele")
	}
}

// Queda de conexão (com nova tentativa marcada) não tira o microfone da aba:
// ele espera a reconexão.
func TestMicWaitsForReconnect(t *testing.T) {
	app, _ := newTestApp(t)
	a := app.current()
	b := newTab(t, app)
	up(a)
	up(b)

	a.onDisconnected("o servidor parou de responder", true)
	app.mu.Lock()
	status := a.status
	app.mu.Unlock()
	if status != statusReconnecting {
		t.Fatalf("a queda deveria marcar uma nova tentativa (estado %q)", status)
	}
	if app.talker() != a {
		t.Fatal("o microfone deveria esperar a reconexão da aba em que estava")
	}
	a.cancelReconnect(true)

	// expulso do servidor: não há volta, e com uma conexão sobrando ela fica com o microfone
	up(a)
	a.onDisconnected("você foi expulso do servidor", true)
	if app.talker() != b {
		t.Fatal("sem volta para a aba do microfone e com uma só conexão de pé, ela fica com ele")
	}
}

// Cada aba tem o seu registro e o seu bate-papo. O que chega numa aba fora da
// vista conta como não lido e avisa a barra de abas, sem publicar o estado
// inteiro; ao abrir a aba, zera.
func TestMessagesAndUnreadPerTab(t *testing.T) {
	app, em := newTestApp(t)
	a := app.current()
	b := newTab(t, app)
	waitFor(t, "estado inicial", time.Second, func() bool { _, ok := em.lastState(); return ok })

	a.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: 3, InvokerName: "Leko", Text: "na aba A"})
	b.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: 3, InvokerName: "Zé", Text: "na aba B"})
	b.addLog("info", "linha de registro não conta como não lida")

	msgsA, _ := app.Bridge().Call(a.id, "GetMessages", nil)
	msgsB, _ := app.Bridge().Call(b.id, "GetMessages", nil)
	la, lb := msgsA.([]Message), msgsB.([]Message)
	if len(la) != 1 || la[0].Text != "na aba A" || la[0].Tab != a.id {
		t.Errorf("mensagens da aba A: %+v", la)
	}
	if len(lb) != 2 || lb[0].Text != "na aba B" || lb[0].Tab != b.id || lb[0].ID == la[0].ID {
		t.Errorf("mensagens da aba B: %+v", lb)
	}
	if tabOf(t, app, a).Unread != 0 || tabOf(t, app, b).Unread != 1 {
		t.Errorf("não lidas: %+v", app.tabList())
	}
	waitFor(t, "aviso de não lidas à barra de abas", time.Second, func() bool {
		for _, ev := range em.eventsNamed("tabs") {
			for _, tab := range ev.([]TabDTO) {
				if tab.ID == b.id && tab.Unread == 1 {
					return true
				}
			}
		}
		return false
	})

	res, err := app.Bridge().Call(b.id, "SelectTab", nil)
	st, _ := res.(State)
	if err != nil || st.Tab != b.id || len(st.Tabs) != 2 {
		t.Fatalf("SelectTab: %+v, %v", st, err)
	}
	if app.current() != b || tabOf(t, app, b).Unread != 0 {
		t.Errorf("abrir a aba deveria pô-la à vista e zerar as não lidas: %+v", app.tabList())
	}
	// agora é a aba A que está fora da vista
	a.onTextMessage(ts3.TextMessage{TargetMode: 1, InvokerID: 9, InvokerName: "Ana", Text: "privado"})
	if tabOf(t, app, a).Unread != 1 {
		t.Errorf("mensagem na aba A fora da vista deveria contar: %+v", app.tabList())
	}
	// o registro geral do app (soundboard, atalhos) cai na aba à vista
	app.addLog("warn", "aviso do app")
	msgsB, _ = app.Bridge().Call(b.id, "GetMessages", nil)
	if lb = msgsB.([]Message); lb[len(lb)-1].Text != "aviso do app" {
		t.Errorf("o aviso do app deveria ir para a aba à vista: %+v", lb)
	}
}

func TestCloseTab(t *testing.T) {
	app, _ := newTestApp(t)
	a := app.current()
	b, c := newTab(t, app), newTab(t, app)
	up(a)
	up(b)
	app.selectTab(c)

	if _, err := app.Bridge().Call(c.id, "CloseTab", nil); err != nil {
		t.Fatal(err)
	}
	if tabs := app.tabList(); len(tabs) != 2 || tabs[0].ID != a.id || tabs[1].ID != b.id {
		t.Fatalf("abas depois de fechar a terceira: %+v", tabs)
	}
	if app.current() != b {
		t.Errorf("fechada a aba à vista, fica à vista a vizinha de antes (aba %d)", app.current().id)
	}
	if _, err := app.Bridge().Call(c.id, "GetState", nil); err == nil || !strings.Contains(err.Error(), "aba foi fechada") {
		t.Errorf("pedido para a aba fechada deveria ser recusado: %v", err)
	}

	// fechar a aba do microfone, sobrando uma conexão: o microfone vai para ela
	app.mu.Lock()
	a.status = statusConnected // a conexão de faz de conta continua "de pé" até fecharmos
	app.mu.Unlock()
	if app.talker() != a {
		t.Fatal("preparo: o microfone deveria estar na aba A")
	}
	if _, err := app.Bridge().Call(a.id, "CloseTab", nil); err != nil {
		t.Fatal(err)
	}
	if tabs := app.tabList(); len(tabs) != 1 || tabs[0].ID != b.id || app.current() != b {
		t.Fatalf("abas depois de fechar a primeira: %+v", tabs)
	}
	if app.talker() != b {
		t.Error("fechada a aba do microfone e sobrando uma conexão, ela fica com o microfone")
	}

	// a última aba não some: fica desconectada
	leave(b) // (no app de verdade é o próprio fechar que derruba a conexão)
	if _, err := app.Bridge().Call(b.id, "CloseTab", nil); err != nil {
		t.Fatal(err)
	}
	if tabs := app.tabList(); len(tabs) != 1 || tabs[0].ID != b.id || tabs[0].Status != statusDisconnected {
		t.Errorf("a última aba deveria continuar, desconectada: %+v", tabs)
	}
}

func TestTabLimit(t *testing.T) {
	app, _ := newTestApp(t)
	for len(app.tabList()) < maxTabs {
		newTab(t, app)
	}
	if _, err := app.openTab(); err == nil || !strings.Contains(err.Error(), "limite") {
		t.Errorf("passar do limite de abas deveria ser recusado: %v", err)
	}
	// conectar em nova aba com a aba atual ocupada e o limite atingido: recusado, nada muda
	up(app.current())
	before := app.current()
	if _, err := app.API().ConnectNewTab(ConnectRequest{Host: "exemplo.local"}); err == nil {
		t.Error("ConnectNewTab deveria respeitar o limite de abas")
	}
	if app.current() != before || len(app.tabList()) != maxTabs {
		t.Error("o pedido recusado não deveria trocar de aba nem criar outra")
	}
	if _, err := app.API().ConnectNewTab(ConnectRequest{Host: "  "}); err == nil {
		t.Error("endereço vazio deveria ser recusado")
	}
}

// Silenciar uma aba cala só aquele servidor.
func TestSilenceOneTab(t *testing.T) {
	app, _ := newTestApp(t)
	a := app.current()
	b := newTab(t, app)
	if _, err := app.Bridge().Call(b.id, "SetTabSilenced", raw(t, true)); err != nil {
		t.Fatal(err)
	}
	if !app.engine.ConnMuted(b.audioID()) || app.engine.ConnMuted(a.audioID()) {
		t.Error("só a aba silenciada deveria ficar calada no áudio")
	}
	if !tabOf(t, app, b).Silenced || tabOf(t, app, a).Silenced {
		t.Errorf("resumo das abas: %+v", app.tabList())
	}
	app.Bridge().Call(b.id, "SetTabSilenced", raw(t, false))
	if app.engine.ConnMuted(b.audioID()) || tabOf(t, app, b).Silenced {
		t.Error("voltar a ouvir deveria desfazer o silêncio")
	}
	// fechar uma aba silenciada não deixa o silêncio para trás
	app.Bridge().Call(b.id, "SetTabSilenced", raw(t, true))
	app.Bridge().Call(b.id, "CloseTab", nil)
	if app.engine.ConnMuted(b.audioID()) {
		t.Error("a aba fechada não deveria continuar marcada como silenciada no áudio")
	}
}

// A aba à vista toca todos os avisos sonoros; das outras só toca o que é com
// a pessoa.
func TestNotificationsFromBackgroundTabs(t *testing.T) {
	opts := testOptions(t)
	opts.SoundPacksZip = zipOf(t, mutePacks(t))
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	a := app.current()
	b := newTab(t, app)

	a.notify("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS")
	if lvl := playedLevel(app); lvl < 500 {
		t.Errorf("alguém entrou no canal da aba à vista: deveria tocar (nível %.0f)", lvl)
	}
	b.notify("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS")
	if lvl := playedLevel(app); lvl != 0 {
		t.Errorf("alguém entrou no canal de uma aba fora da vista: não deveria tocar (nível %.0f)", lvl)
	}
	b.notify("OTHER_RECEIVED_POKE")
	if lvl := playedLevel(app); lvl < 500 {
		t.Errorf("cutucada numa aba fora da vista: deveria tocar (nível %.0f)", lvl)
	}

	for event, want := range map[string]bool{
		"OTHER_RECEIVED_POKE": true, "YOU_WERE_KICKED_FROM_SERVER": true, "YOU_WERE_MOVED_TO_DIFFERENT_CHANNEL": true,
		"CONNECTION_LOST_CONNECTION": true, "CONNECTION_CONNECTED": true,
		"CLIENT_MOVED_TO_CURRENT_CHANNEL_APPEARS": false, "CLIENT_WAS_KICKED_FROM_SERVER_CURRENT_CHANNEL": false, "SERVER_ERROR": false,
	} {
		if got := notifyFromAnyTab(event); got != want {
			t.Errorf("%s vindo de uma aba fora da vista: toca=%v, esperado %v", event, got, want)
		}
	}
}

// A cutucada diz em que servidor foi, e uma identidade em uso numa conexão
// aberta não pode ser apagada, esteja a aba à vista ou não.
func TestPokeAndIdentityKnowTheirTab(t *testing.T) {
	app, em := newTestApp(t)
	b := newTab(t, app)
	b.onPoke("Leko", "acorda")
	em.mu.Lock()
	pokes, poke := em.pokes, em.lastPoke
	em.mu.Unlock()
	if pokes != 1 || poke.Tab != b.id || poke.FromName != "Leko" {
		t.Errorf("cutucada: %d, %+v", pokes, poke)
	}

	other, err := app.API().CreateIdentity("Segunda")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.useIdentity(other); err != nil {
		t.Fatal(err)
	}
	up(b)
	if err := app.API().DeleteIdentity(other); err == nil || !strings.Contains(err.Error(), "em uso") {
		t.Errorf("identidade em uso numa aba fora da vista não deveria ser apagada: %v", err)
	}
	leave(b)
	if err := app.API().DeleteIdentity(other); err != nil {
		t.Errorf("com a aba parada a identidade pode ser apagada: %v", err)
	}
	if st := b.buildState(); st.Identity.ID == other || st.Identity.ID == "" {
		t.Errorf("a aba que usava a identidade apagada deveria voltar para a padrão: %+v", st.Identity)
	}
}

// Os eventos de script dizem de que conexão vieram, e o que um script pede
// vale para a conexão dele. O som "para o canal" sai pelo microfone, que está
// num servidor só: de outra conexão ele é recusado.
func TestScriptsKnowTheirTab(t *testing.T) {
	app, em := newTestApp(t)
	sc, err := app.API().SaveScript(ScriptDTO{Name: "teste", Code: "// nada", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	a := app.current()
	b := newTab(t, app)
	up(a)
	up(b)

	b.onPoke("Leko", "oi")
	b.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: 3, InvokerName: "Zé", Text: "olá"})
	b.onClientEvent(ts3.ClientEvent{Kind: ts3.ClientJoined, Client: ts3.ClientInfo{ID: 3, Nickname: "Zé"}, To: 1})
	seen := map[string]int{}
	for _, ev := range em.eventsNamed("script:event") {
		if e := ev.(ScriptEvent); e.Tab == b.id {
			seen[e.Type]++
		} else if e.Type != "connected" || e.Tab != a.id {
			t.Errorf("evento com a aba errada: %+v", e)
		}
	}
	for _, kind := range []string{"connected", "poke", "message", "clientJoined"} {
		if seen[kind] != 1 {
			t.Errorf("evento %q da aba B: %d vezes, esperado 1 (%v)", kind, seen[kind], seen)
		}
	}

	if _, err := b.api().ScriptCall(sc.ID, "playSound", map[string]any{"name": "Vinheta"}); err != errScriptSoundElsewhere {
		t.Errorf("som para o canal pedido fora da aba do microfone deveria ser recusado: %v", err)
	}
	// na aba do microfone, e só para nós em qualquer aba, o pedido segue (e aqui falha por não haver o som)
	for _, try := range []struct {
		c     *conn
		local bool
	}{{a, false}, {b, true}} {
		_, err := try.c.api().ScriptCall(sc.ID, "playSound", map[string]any{"name": "Vinheta", "local": try.local})
		if err == nil || err == errScriptSoundElsewhere || !strings.Contains(err.Error(), "não há som") {
			t.Errorf("aba %d, local=%v: %v", try.c.id, try.local, err)
		}
	}
}
