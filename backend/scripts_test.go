package backend

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"furito-ts/ts3"
)

// scriptEvents devolve os eventos entregues aos scripts até agora.
func scriptEvents(em *fakeEmitter) []ScriptEvent {
	var out []ScriptEvent
	for _, ev := range em.eventsNamed("script:event") {
		out = append(out, ev.(ScriptEvent))
	}
	return out
}

// Criar, editar, ligar, apagar — e tudo continua lá ao reabrir o app.
func TestScriptsStorage(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()
	if n := len(api.GetScripts()); n != 0 {
		t.Fatalf("começa sem scripts: %d", n)
	}
	if _, err := api.SaveScript(ScriptDTO{Name: "   ", Code: "x"}); err == nil {
		t.Error("script sem nome deveria ser recusado")
	}
	if _, err := api.SaveScript(ScriptDTO{Name: strings.Repeat("n", maxScriptName+1)}); err == nil {
		t.Error("nome longo demais deveria ser recusado")
	}
	if _, err := api.SaveScript(ScriptDTO{Name: "grande", Code: strings.Repeat("x", maxScriptCode+1)}); err == nil {
		t.Error("script grande demais deveria ser recusado")
	}
	if _, err := api.SaveScript(ScriptDTO{ID: "nao-existe", Name: "x"}); err == nil {
		t.Error("atualizar um script que não existe deveria dar erro")
	}

	a, err := api.SaveScript(ScriptDTO{Name: "  Boas-vindas  ", Code: "ts.on('clientJoined', () => {})"})
	if err != nil || a.ID == "" || a.Name != "Boas-vindas" || a.Enabled {
		t.Fatalf("criar: %+v %v", a, err)
	}
	if a.Moderation {
		t.Error("um script novo não nasce com permissão de moderar")
	}
	b, err := api.SaveScript(ScriptDTO{Name: "Outro", Code: "// nada", Enabled: true})
	if err != nil || b.ID == a.ID || !b.Enabled {
		t.Fatalf("criar o segundo: %+v %v", b, err)
	}
	a.Code, a.Moderation = "ts.log('novo')", true
	if got, err := api.SaveScript(a); err != nil || got.Code != "ts.log('novo')" || got.ID != a.ID || !got.Moderation {
		t.Fatalf("atualizar: %+v %v", got, err)
	}
	if err := api.SetScriptEnabled(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := api.SetScriptEnabled("nao-existe", true); err == nil {
		t.Error("ligar um script que não existe deveria dar erro")
	}
	if err := api.DeleteScript(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := api.DeleteScript(b.ID); err == nil {
		t.Error("apagar duas vezes deveria dar erro")
	}
	app.Shutdown()

	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	list := again.API().GetScripts()
	if len(list) != 1 || list[0].ID != a.ID || list[0].Name != "Boas-vindas" || list[0].Code != "ts.log('novo')" || !list[0].Enabled || !list[0].Moderation {
		t.Errorf("ao reabrir: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.SettingsPath), "scripts.json.tmp")); err == nil {
		t.Error("o arquivo temporário da gravação não deveria sobrar")
	}

	for i := len(list); i < maxScripts; i++ {
		if _, err := again.API().SaveScript(ScriptDTO{Name: "s"}); err != nil {
			t.Fatalf("script %d: %v", i, err)
		}
	}
	if _, err := again.API().SaveScript(ScriptDTO{Name: "um a mais"}); err == nil {
		t.Errorf("o limite de %d scripts deveria valer", maxScripts)
	}
}

// O script de exemplos entra na lista uma vez só, desligado e sem permissão
// de moderar — também para quem já tinha scripts —, e não volta depois de
// apagado.
func TestExampleScriptSeeded(t *testing.T) {
	const example = "// exemplos\nts.log('oi');\n"
	opts := testOptions(t)
	path := filepath.Join(filepath.Dir(opts.SettingsPath), "scripts.json")
	open := func() (*App, []ScriptDTO) {
		t.Helper()
		app, err := NewApp(opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(app.Shutdown)
		return app, app.API().GetScripts()
	}

	// instalação de antes dos exemplos: já tem um script do usuário
	old, _ := open()
	mine, err := old.API().SaveScript(ScriptDTO{Name: "meu", Code: "// meu", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	old.Shutdown()

	opts.ExampleScript = example
	app, list := open()
	if len(list) != 2 || list[0].ID != exampleScriptID || list[0].Name != exampleScriptName || list[0].Code != example || list[0].Enabled || list[0].Moderation {
		t.Fatalf("o script de exemplos deveria entrar primeiro, desligado e sem moderar: %+v", list)
	}
	if list[1].ID != mine.ID || !list[1].Enabled {
		t.Errorf("o script do usuário deveria continuar como estava: %+v", list[1])
	}
	if _, err := app.API().ScriptCall(exampleScriptID, "getSelf", nil); err == nil {
		t.Error("os exemplos vêm desligados: não agem enquanto o usuário não ligar")
	}
	// o usuário edita os exemplos: ao reabrir vale a edição dele, sem duplicar
	edited := list[0]
	edited.Code = "// do meu jeito"
	if _, err := app.API().SaveScript(edited); err != nil {
		t.Fatal(err)
	}
	app.Shutdown()
	opts.ExampleScript = example + "// versão nova do app\n"
	app, list = open()
	if len(list) != 2 || list[0].Code != "// do meu jeito" {
		t.Fatalf("reabrir não deveria repor nem duplicar os exemplos: %+v", list)
	}
	// apagou: não volta
	if err := app.API().DeleteScript(exampleScriptID); err != nil {
		t.Fatal(err)
	}
	app.Shutdown()
	if _, list = open(); len(list) != 1 || list[0].ID != mine.ID {
		t.Fatalf("os exemplos apagados não deveriam voltar: %+v", list)
	}

	// sem exemplos para semear (testes, por exemplo), a lista começa vazia
	empty := testOptions(t)
	if app, err := NewApp(empty); err != nil || len(app.API().GetScripts()) != 0 {
		t.Errorf("sem script de exemplos a lista começa vazia: %v", err)
	} else {
		app.Shutdown()
	}

	// arquivo ilegível: fica como está (nada é gravado por cima)
	broken := testOptions(t)
	broken.ExampleScript = example
	brokenPath := filepath.Join(filepath.Dir(broken.SettingsPath), "scripts.json")
	os.MkdirAll(filepath.Dir(brokenPath), 0o700)
	os.WriteFile(brokenPath, []byte(`{"scripts": [ {"id": "x", "name": "pela metade"`), 0o600)
	app2, err := NewApp(broken)
	if err != nil {
		t.Fatal(err)
	}
	app2.Shutdown()
	if data, _ := os.ReadFile(brokenPath); string(data) != `{"scripts": [ {"id": "x", "name": "pela metade"` {
		t.Errorf("um scripts.json ilegível não deveria ser regravado: %q", data)
	}
	_ = path
}

// O script de exemplos promete mostrar tudo: cada evento e cada função que o
// worker oferece aos scripts tem de aparecer nele. Quem acrescentar uma
// função acrescenta o exemplo.
func TestExampleScriptCoversEverything(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("..", "frontend", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	worker, example := read("script-worker.js"), read("script-examples.js")

	events := regexp.MustCompile(`const EVENTS = \[([^\]]+)\]`).FindStringSubmatch(worker)
	if events == nil {
		t.Fatal("não achei a lista de eventos no worker")
	}
	names := regexp.MustCompile(`'(\w+)'`).FindAllStringSubmatch(events[1], -1)
	if len(names) < 9 {
		t.Fatalf("lista de eventos curta demais: %v", names)
	}
	for _, m := range names {
		if !strings.Contains(example, "ts.on('"+m[1]+"'") {
			t.Errorf("falta no script de exemplos o evento %q", m[1])
		}
	}

	start := strings.Index(worker, "const ts = Object.freeze({")
	end := strings.Index(worker, "const consoleApi")
	if start < 0 || end < start {
		t.Fatal("não achei o objeto ts no worker")
	}
	funcs := regexp.MustCompile(`(?m)^    (\w+)[:(]`).FindAllStringSubmatch(worker[start:end], -1)
	if len(funcs) < 25 {
		t.Fatalf("achei só %d funções no objeto ts", len(funcs))
	}
	for _, m := range funcs {
		if !strings.Contains(example, "ts."+m[1]+"(") {
			t.Errorf("falta no script de exemplos a função ts.%s", m[1])
		}
	}

	// e ele vem seguro: ninguém na lista de confiança, boas-vindas desligadas
	for _, want := range []string{"const CONFIANCA = [];", "const BOAS_VINDAS = false;"} {
		if !strings.Contains(example, want) {
			t.Errorf("o script de exemplos deveria vir com %q", want)
		}
	}
	// o código das ações do backend conhece tudo o que o worker pede
	backend, _ := os.ReadFile("scripts.go")
	for _, m := range regexp.MustCompile(`call\('(\w+)'`).FindAllStringSubmatch(worker, -1) {
		if !strings.Contains(string(backend), `case "`+m[1]+`"`) {
			t.Errorf("o worker pede a ação %q, que o backend não conhece", m[1])
		}
	}
}

// Só script ligado age, só as ações da lista existem, e cada uma confere o
// que recebe.
func TestScriptCallRules(t *testing.T) {
	app, _ := newTestApp(t)
	api := app.API()
	sc, _ := api.SaveScript(ScriptDTO{Name: "teste", Code: "//"})
	if _, err := api.ScriptCall(sc.ID, "getSelf", nil); err == nil {
		t.Error("script desligado não pode agir")
	}
	if _, err := api.ScriptCall("nao-existe", "getSelf", nil); err == nil {
		t.Error("script inexistente não pode agir")
	}
	api.SetScriptEnabled(sc.ID, true)
	call := func(action string, args map[string]any) (any, error) { return api.ScriptCall(sc.ID, action, args) }

	for _, action := range []string{"DeleteChannel", "kick", "connect", "", "getSelf "} {
		if _, err := call(action, nil); err == nil || !strings.Contains(err.Error(), "desconhecida") {
			t.Errorf("ação %q deveria ser recusada como desconhecida: %v", action, err)
		}
	}

	// desconectado: consultas respondem vazias, mensagens são recusadas
	self, err := call("getSelf", nil)
	if s, ok := self.(ScriptSelf); err != nil || !ok || s.Connected || s.ID != 0 {
		t.Errorf("getSelf desconectado: %+v %v", self, err)
	}
	if cl, err := call("getClients", nil); err != nil || len(cl.([]*ScriptClient)) != 0 {
		t.Errorf("getClients desconectado: %+v %v", cl, err)
	}
	if ch, err := call("getChannels", nil); err != nil || len(ch.([]ScriptChannel)) != 0 {
		t.Errorf("getChannels desconectado: %+v %v", ch, err)
	}
	for action, args := range map[string]map[string]any{
		"sendChannelMessage": {"text": "oi"},
		"sendServerMessage":  {"text": "oi"},
		"sendPrivateMessage": {"clientId": 3.0, "text": "oi"},
		"poke":               {"clientId": 3.0, "text": "oi"},
		"switchChannel":      {"channelId": 2.0},
	} {
		if _, err := call(action, args); err == nil || !strings.Contains(err.Error(), "não conectado") {
			t.Errorf("%s desconectado deveria falhar com \"não conectado\": %v", action, err)
		}
	}
	if _, err := call("sendChannelMessage", map[string]any{"text": "   "}); err == nil || !strings.Contains(err.Error(), "vazia") {
		t.Errorf("mensagem vazia: %v", err)
	}
	if _, err := call("sendChannelMessage", map[string]any{"text": strings.Repeat("ç", maxMessageRunes+1)}); err == nil || !strings.Contains(err.Error(), "longa") {
		t.Errorf("mensagem longa demais: %v", err)
	}
	if _, err := call("sendChannelMessage", map[string]any{"text": 42.0}); err == nil {
		t.Error("texto que não é texto deveria ser recusado")
	}

	// moderação: recusada enquanto o usuário não der essa permissão ao script
	moderation := map[string]map[string]any{
		"moveClient":      {"clientId": 3.0, "channelId": 2.0},
		"kickFromChannel": {"clientId": 3.0, "reason": "teste"},
		"kickFromServer":  {"clientId": 3.0, "reason": "teste"},
		"ban":             {"clientId": 3.0, "seconds": 60.0, "reason": "teste"},
		"setTalker":       {"clientId": 3.0, "talker": true},
	}
	for action, args := range moderation {
		if _, err := call(action, args); err == nil || !strings.Contains(err.Error(), "permissão para moderar") {
			t.Errorf("%s sem \"Pode moderar\" deveria ser recusado: %v", action, err)
		}
	}
	sc.Enabled, sc.Moderation = true, true
	if _, err := api.SaveScript(sc); err != nil {
		t.Fatal(err)
	}
	for action, args := range moderation {
		want := "não conectado"
		if action == "moveClient" {
			want = "canal não encontrado" // desconectado não há canais
		}
		if _, err := call(action, args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s desconectado deveria falhar com %q: %v", action, want, err)
		}
	}
	// banir exige a duração escrita: esquecer o número não vira banimento permanente
	for _, args := range []map[string]any{{"clientId": 3.0}, {"clientId": 3.0, "seconds": -1.0}, {"clientId": 3.0, "seconds": 1.5}, {"clientId": 3.0, "seconds": "60"}} {
		if _, err := call("ban", args); err == nil || !strings.Contains(err.Error(), "duração") {
			t.Errorf("ban com %v deveria pedir a duração: %v", args, err)
		}
	}

	// o próprio estado
	if _, err := call("setMuted", map[string]any{"muted": true}); err != nil || !app.engine.Muted() {
		t.Errorf("setMuted(true): %v", err)
	}
	call("setMuted", map[string]any{"muted": true}) // de novo: continua mutado
	if !app.engine.Muted() {
		t.Error("setMuted(true) duas vezes não pode desmutar")
	}
	// um script fecha o microfone e os alto-falantes, mas não abre: ligar o
	// microfone de alguém sem a pessoa pedir é escuta
	if _, err := call("setMuted", map[string]any{"muted": false}); !errors.Is(err, errScriptUnmute) || !app.engine.Muted() {
		t.Errorf("um script não pode desmutar o microfone: %v (mutado=%v)", err, app.engine.Muted())
	}
	call("setDeafened", map[string]any{"deafened": true})
	if _, err := call("setDeafened", map[string]any{"deafened": false}); !errors.Is(err, errScriptUnmute) || !app.engine.Deafened() {
		t.Errorf("um script não pode desmutar os alto-falantes: %v (mutados=%v)", err, app.engine.Deafened())
	}
	call("setAway", map[string]any{"away": true, "message": "já volto"})
	self, _ = call("getSelf", nil)
	if s := self.(ScriptSelf); !s.Muted || !s.Deafened || !s.Away || s.AwayMessage != "já volto" {
		t.Errorf("estado depois das ações: %+v", s)
	}
	// quem abre é o usuário; com tudo já aberto, pedir "desmutar" não é erro
	app.API().ToggleMute()
	app.API().ToggleDeafen()
	if _, err := call("setMuted", map[string]any{"muted": false}); err != nil {
		t.Errorf("setMuted(false) com o microfone já aberto: %v", err)
	}
	if _, err := call("setDeafened", map[string]any{"deafened": false}); err != nil {
		t.Errorf("setDeafened(false) com os alto-falantes já abertos: %v", err)
	}

	// sons do soundboard, pelo nome
	tone := filepath.Join(t.TempDir(), "Vinheta.wav")
	writeTone(t, tone, 0.4)
	if n, err := app.addSoundFiles([]string{tone}); n != 1 || err != nil {
		t.Fatalf("adicionar som: %d %v", n, err)
	}
	if names, err := call("getSounds", nil); err != nil || len(names.([]string)) != 1 || names.([]string)[0] != "Vinheta" {
		t.Errorf("getSounds: %+v %v", names, err)
	}
	if _, err := call("playSound", map[string]any{"name": "não existe"}); err == nil || !strings.Contains(err.Error(), "não há som") {
		t.Errorf("som inexistente: %v", err)
	}
	if _, err := call("playSound", map[string]any{"name": " vinheta ", "local": true}); err != nil {
		t.Fatalf("playSound local: %v", err)
	}
	if app.engine.Soundboard().IsPlaying() {
		t.Error("som local não passa pelo soundboard (não é transmitido)")
	}
	if lvl, _ := playedOut(app); lvl < 500 {
		t.Errorf("o som local deveria sair nos alto-falantes (nível %.0f)", lvl)
	}
	if _, err := call("playSound", map[string]any{"name": "VINHETA"}); err != nil || !app.engine.Soundboard().IsPlaying() {
		t.Errorf("playSound deveria tocar pelo soundboard: %v", err)
	}
	if _, err := call("stopSound", nil); err != nil || app.engine.Soundboard().IsPlaying() {
		t.Errorf("stopSound: %v", err)
	}
}

// Mensagens e cutucadas de scripts têm limite: uma rajada curta e depois um
// ritmo lento, para o servidor não bloquear o usuário por flood.
func TestScriptTalkLimit(t *testing.T) {
	var st scriptStore
	st.load(filepath.Join(t.TempDir(), "scripts.json"), "")
	now := time.Now()
	for i := 0; i < scriptTalkBurst; i++ {
		if !st.allowTalk(now) {
			t.Fatalf("a mensagem %d da rajada deveria passar", i+1)
		}
	}
	if st.allowTalk(now) || st.allowTalk(now.Add(scriptTalkEvery/2)) {
		t.Error("depois da rajada, a próxima mensagem deveria esperar")
	}
	now = now.Add(scriptTalkEvery/2 + scriptTalkEvery/2 + time.Millisecond)
	if !st.allowTalk(now) {
		t.Error("passado o intervalo, uma mensagem deveria passar")
	}
	if st.allowTalk(now) {
		t.Error("…e só uma")
	}
	now = now.Add(time.Hour)
	passed := 0
	for i := 0; i < 20; i++ {
		if st.allowTalk(now) {
			passed++
		}
	}
	if passed != scriptTalkBurst {
		t.Errorf("depois de um tempo parado a rajada volta ao tamanho normal, sem acumular: passaram %d", passed)
	}
}

// Os scripts só recebem eventos quando há algum ligado, e recebem cada
// movimentação com quem, de onde, para onde e por quê.
func TestScriptEvents(t *testing.T) {
	app, em := newTestApp(t)
	api := app.API()
	ana := ts3.ClientInfo{ID: 7, Nickname: "Ana", UID: "uidAna", ChannelID: 1}

	app.onClientEvent(ts3.ClientEvent{Kind: ts3.ClientJoined, Client: ana, To: 1, Reason: "switched"})
	app.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: 7, InvokerName: "Ana", Text: "oi"})
	app.onPoke("Ana", "acorda")
	if evs := scriptEvents(em); len(evs) != 0 {
		t.Fatalf("sem script ligado não há eventos: %+v", evs)
	}

	sc, _ := api.SaveScript(ScriptDTO{Name: "teste", Code: "//", Enabled: true})
	app.onClientEvent(ts3.ClientEvent{Kind: ts3.ClientConnected, Client: ana, To: 1, Reason: "connected"})
	app.onClientEvent(ts3.ClientEvent{Kind: ts3.ClientJoined, Client: ana, From: 2, To: 1, Reason: "moved", Invoker: "Admin"})
	app.onClientEvent(ts3.ClientEvent{Kind: ts3.ClientLeft, Client: ana, From: 1, To: 2, Reason: "switched"})
	app.onClientEvent(ts3.ClientEvent{Kind: ts3.ClientDisconnected, Client: ana, From: 1, Reason: "lost"})
	app.onClientEvent(ts3.ClientEvent{Kind: ts3.SelfMoved, Client: ts3.ClientInfo{ID: 1, Nickname: "Eu"}, From: 1, To: 2, Reason: "switched"})
	app.onClientEvent(ts3.ClientEvent{Kind: "outra coisa", Client: ana})
	app.onTextMessage(ts3.TextMessage{TargetMode: 1, InvokerID: 7, InvokerName: "Ana", InvokerUID: "uidAna", Text: "oi"})
	app.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: 7, InvokerName: "Ana", Text: "no canal"})
	app.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: int(app.client.ClientID()), InvokerName: "Eu", Text: "minha própria mensagem"})
	app.onPoke("Ana", "acorda")

	evs := scriptEvents(em)
	var got []string
	for _, ev := range evs {
		line := ev.Type
		if ev.Client != nil {
			line += " " + ev.Client.Nickname
		}
		if ev.Reason != "" {
			line += " (" + ev.Reason + ")"
		}
		if ev.Target != "" {
			line += " " + ev.Target
		}
		if ev.Text != "" {
			line += ": " + ev.Text
		}
		got = append(got, line)
	}
	want := []string{
		"clientConnected Ana (connected)", "clientJoined Ana (moved)", "clientLeft Ana (switched)", "clientDisconnected Ana (lost)",
		"channelChanged Eu (switched)", "message Ana private: oi", "message Ana channel: no canal", "poke Ana: acorda",
	}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("eventos:\n got %q\nwant %q", got, want)
	}
	joined, left, moved := evs[1], evs[2], evs[4]
	if joined.Channel == nil || joined.Channel.ID != 1 || joined.From == nil || joined.From.ID != 2 || joined.Invoker != "Admin" || joined.Client.UID != "uidAna" || joined.Client.ID != 7 {
		t.Errorf("clientJoined: %+v cliente=%+v canal=%+v", joined, joined.Client, joined.Channel)
	}
	if left.Channel == nil || left.Channel.ID != 1 || left.To == nil || left.To.ID != 2 {
		t.Errorf("clientLeft: o canal do evento é o nosso (1), o destino é o 2: canal=%+v destino=%+v", left.Channel, left.To)
	}
	if moved.Channel == nil || moved.Channel.ID != 2 || moved.From == nil || moved.From.ID != 1 {
		t.Errorf("channelChanged: o canal do evento é o novo: canal=%+v origem=%+v", moved.Channel, moved.From)
	}
	if evs[3].To != nil {
		t.Errorf("quem saiu do servidor não tem canal de destino: %+v", evs[3].To)
	}

	// contato bloqueado: as mensagens dele não chegam aos scripts
	app.updateSettings(func(s *Settings) { s.Contacts = map[string]string{"uidChato": "blocked"} })
	app.onTextMessage(ts3.TextMessage{TargetMode: 2, InvokerID: 9, InvokerName: "Chato", InvokerUID: "uidChato", Text: "spam"})
	if n := len(scriptEvents(em)); n != len(evs) {
		t.Error("mensagem de contato bloqueado não deveria virar evento")
	}

	api.SetScriptEnabled(sc.ID, false)
	app.onPoke("Ana", "de novo")
	if n := len(scriptEvents(em)); n != len(evs) {
		t.Error("com o script desligado os eventos param")
	}
}

// O código que roda os scripts sai com a política que proíbe rede dentro do
// worker; os outros arquivos da interface, não.
func TestScriptSandboxHeader(t *testing.T) {
	h := ScriptSandbox(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	get := func(path string) http.Header {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Body.String() != "ok" {
			t.Errorf("%s: a resposta deveria passar adiante: %q", path, rec.Body.String())
		}
		return rec.Header()
	}
	csp := get(ScriptWorkerPath).Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "connect-src") || strings.Contains(csp, "http") {
		t.Errorf("política do worker: %q", csp)
	}
	// As outras respostas levam a política da página: só o código do próprio
	// app roda (sem eval, sem script embutido), nada de conexões para fora,
	// de páginas embutidas nem de formulários.
	for _, path := range []string{"/", "/index.html", "/app.js", "/scripts.js", "/script-worker.js.map", "/__screen/frame.jpg"} {
		v := get(path).Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self';", "connect-src 'self'", "object-src 'none'", "frame-src 'none'", "base-uri 'none'", "form-action 'none'"} {
			if !strings.Contains(v, want) {
				t.Errorf("%s: a política da página deveria ter %q: %q", path, want, v)
			}
		}
		if strings.Contains(v, "unsafe-eval") || strings.Contains(v, "script-src 'self' ") || strings.Contains(v, "*") {
			t.Errorf("%s: a política da página não pode liberar eval, scripts de fora nem curingas: %q", path, v)
		}
	}
	// a interface não pode depender do que a política proíbe: script embutido,
	// atributos de evento e endereços javascript:
	index, _ := os.ReadFile(filepath.Join("..", "frontend", "index.html"))
	if m := regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>\s*[^<\s]|\son[a-z]+\s*=|javascript:`).Find(index); m != nil {
		t.Errorf("frontend/index.html tem código embutido, que a política da página bloqueia: %q", m)
	}
	for _, name := range []string{"app.js", "bridge.js", "screen.js", "scripts.js"} {
		src, _ := os.ReadFile(filepath.Join("..", "frontend", name))
		if m := regexp.MustCompile(`\beval\(|new Function\(|\.innerHTML\s*=\s*[^;]*\son[a-z]+=`).Find(src); m != nil {
			t.Errorf("frontend/%s usa algo que a política da página bloqueia: %q", name, m)
		}
	}
	// o arquivo existe na interface com esse nome (o endereço é fixo dos dois lados)
	if _, err := os.Stat(filepath.Join("..", "frontend", strings.TrimPrefix(ScriptWorkerPath, "/"))); err != nil {
		t.Errorf("o código dos workers deveria estar em frontend%s: %v", ScriptWorkerPath, err)
	}
	data, _ := os.ReadFile(filepath.Join("..", "frontend", "scripts.js"))
	if !strings.Contains(string(data), "new Worker('"+strings.TrimPrefix(ScriptWorkerPath, "/")+"')") {
		t.Error("frontend/scripts.js deveria criar os workers a partir de " + ScriptWorkerPath)
	}
}

// Contra um servidor real: alguém entra no nosso canal, o evento chega aos
// scripts e a resposta do script chega a quem entrou.
//
//	FURITO_TEST_HOST=127.0.0.1:9987 go test ./backend/ -run LiveScripts -v
func TestLiveScripts(t *testing.T) {
	host := liveHost(t)
	start := func(name string) (*App, *fakeEmitter) {
		app, err := NewApp(testOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		em := &fakeEmitter{}
		app.SetEmitter(em)
		t.Cleanup(app.Shutdown)
		return app, em
	}
	connect := func(app *App, em *fakeEmitter, name string) int {
		if err := app.API().Connect(ConnectRequest{Host: host, Nickname: name}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, name+" conectar", 12*time.Second, func() bool {
			st, _ := em.lastState()
			return st.Status == "connected" && st.Self.ClientID != 0
		})
		st, _ := em.lastState()
		return st.Self.ClientID
	}
	find := func(em *fakeEmitter, typ, nick string) (ScriptEvent, bool) {
		for _, ev := range scriptEvents(em) {
			if ev.Type == typ && (nick == "" || (ev.Client != nil && ev.Client.Nickname == nick)) {
				return ev, true
			}
		}
		return ScriptEvent{}, false
	}

	me, meEm := start("FuritoScript")
	sc, err := me.API().SaveScript(ScriptDTO{Name: "boas-vindas", Code: "// o código roda na interface; aqui vale o caminho dos eventos e das ações", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	call := func(action string, args map[string]any) (any, error) { return me.API().ScriptCall(sc.ID, action, args) }
	connect(me, meEm, "FuritoScript")
	waitFor(t, "evento connected", 3*time.Second, func() bool { ev, ok := find(meEm, "connected", ""); return ok && ev.Server != "" })
	self, _ := call("getSelf", nil)
	if s := self.(ScriptSelf); !s.Connected || s.Nickname != "FuritoScript" || s.ChannelID == 0 || s.ChannelName == "" || s.Server == "" {
		t.Errorf("getSelf conectado: %+v", s)
	}

	time.Sleep(1200 * time.Millisecond)
	other, otherEm := start("FuritoVisita")
	otherID := connect(other, otherEm, "FuritoVisita")

	// o outro conectou no canal padrão, que é o nosso
	waitFor(t, "evento clientJoined", 4*time.Second, func() bool { _, ok := find(meEm, "clientJoined", "FuritoVisita"); return ok })
	ev, _ := find(meEm, "clientJoined", "FuritoVisita")
	if ev.Client.ID != otherID || ev.Client.UID == "" || ev.Reason != "connected" || ev.Channel == nil || ev.Channel.Name == "" || ev.Client.Self {
		t.Errorf("clientJoined: %+v cliente=%+v canal=%+v", ev, ev.Client, ev.Channel)
	}
	if _, ok := find(meEm, "clientConnected", "FuritoVisita"); !ok {
		t.Error("faltou o evento clientConnected")
	}
	clients, _ := call("getClients", nil)
	seen := false
	for _, cl := range clients.([]*ScriptClient) {
		seen = seen || (cl.ID == otherID && cl.Nickname == "FuritoVisita" && !cl.Self)
	}
	if !seen {
		t.Errorf("getClients deveria listar o visitante: %+v", clients)
	}
	if ch, _ := call("getChannels", nil); len(ch.([]ScriptChannel)) == 0 {
		t.Error("getChannels deveria listar os canais do servidor")
	}

	// o que um script de boas-vindas faria: responde no canal e em privado
	if _, err := call("sendChannelMessage", map[string]any{"text": "Bem-vindo, " + ev.Client.Nickname + "!"}); err != nil {
		t.Fatalf("sendChannelMessage: %v", err)
	}
	if _, err := call("sendPrivateMessage", map[string]any{"clientId": float64(otherID), "text": "regras na descrição do canal"}); err != nil {
		t.Fatalf("sendPrivateMessage: %v", err)
	}
	waitFor(t, "o visitante receber as duas mensagens", 4*time.Second, func() bool {
		_, a := otherEm.findMessage(func(m Message) bool {
			return m.TargetMode == 2 && m.Text == "Bem-vindo, FuritoVisita!" && m.InvokerName == "FuritoScript"
		})
		_, b := otherEm.findMessage(func(m Message) bool { return m.TargetMode == 1 && m.Text == "regras na descrição do canal" })
		return a && b
	})
	if _, err := call("sendPrivateMessage", map[string]any{"clientId": 60000.0, "text": "oi"}); err == nil || !strings.Contains(err.Error(), "não encontrado") {
		t.Errorf("mensagem para quem não está no servidor: %v", err)
	}
	if _, err := call("sendPrivateMessage", map[string]any{"clientId": float64(self.(ScriptSelf).ID), "text": "oi"}); err == nil {
		t.Error("mensagem privada para si mesmo deveria ser recusada")
	}

	// a própria mensagem não volta como evento; a do outro, sim (com os dados dele)
	other.API().SendText(2, 0, "!hora")
	waitFor(t, "evento message", 4*time.Second, func() bool {
		for _, e := range scriptEvents(meEm) {
			if e.Type == "message" && e.Text == "!hora" {
				return e.Target == "channel" && e.Client.ID == otherID && e.Client.Nickname == "FuritoVisita" && e.Client.ChannelID != 0
			}
		}
		return false
	})
	for _, e := range scriptEvents(meEm) {
		if e.Type == "message" && strings.HasPrefix(e.Text, "Bem-vindo") {
			t.Error("a nossa própria mensagem não deveria voltar como evento")
		}
	}

	// limite contra flood: a rajada passa, o excesso é recusado sem chegar ao servidor
	refused := 0
	for i := 0; i < scriptTalkBurst+2; i++ {
		if _, err := call("sendChannelMessage", map[string]any{"text": "rajada"}); err != nil {
			if !errors.Is(err, errScriptFlood) {
				t.Fatalf("erro inesperado na rajada: %v", err)
			}
			refused++
		}
	}
	if refused < 3 { // duas já tinham saído antes da rajada
		t.Errorf("o limite deveria recusar o excesso: só %d recusadas", refused)
	}

	// o visitante sai: clientLeft e clientDisconnected
	other.API().Disconnect()
	waitFor(t, "eventos de saída", 4*time.Second, func() bool {
		l, a := find(meEm, "clientLeft", "FuritoVisita")
		d, b := find(meEm, "clientDisconnected", "FuritoVisita")
		return a && b && l.Reason == "disconnected" && d.Reason == "disconnected"
	})
	me.API().Disconnect()
	waitFor(t, "evento disconnected", 4*time.Second, func() bool { _, ok := find(meEm, "disconnected", ""); return ok })
}

// Moderação por script contra um servidor em que temos uma chave de
// privilégio (o mesmo servidor descartável do TestLiveAdmin; cada chave vale
// uma vez, então um servidor novo por teste):
//
//	FURITO_ICON_TEST_HOST=127.0.0.1:19987 FURITO_ICON_TEST_TOKEN=... go test ./backend/ -run LiveScriptModeration -v
func TestLiveScriptModeration(t *testing.T) {
	host, token := os.Getenv("FURITO_ICON_TEST_HOST"), os.Getenv("FURITO_ICON_TEST_TOKEN")
	if host == "" || token == "" {
		t.Skip("defina FURITO_ICON_TEST_HOST e FURITO_ICON_TEST_TOKEN")
	}
	start := func() (*App, *fakeEmitter) {
		app, err := NewApp(testOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		em := &fakeEmitter{}
		app.SetEmitter(em)
		t.Cleanup(app.Shutdown)
		return app, em
	}
	connect := func(app *App, em *fakeEmitter, name string) int {
		t.Helper()
		if err := app.API().Connect(ConnectRequest{Host: host, Nickname: name}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, name+" conectar", 12*time.Second, func() bool {
			st, _ := em.lastState()
			return st.Status == "connected" && st.Self.ClientID != 0 && len(st.Channels) > 0
		})
		st, _ := em.lastState()
		return st.Self.ClientID
	}
	state := func(em *fakeEmitter) State { st, _ := em.lastState(); return st }
	clientOf := func(em *fakeEmitter, id int) ClientDTO {
		for _, cl := range state(em).Clients {
			if cl.ID == id {
				return cl
			}
		}
		return ClientDTO{}
	}
	logged := func(em *fakeEmitter, text string) bool {
		_, ok := em.findMessage(func(m Message) bool { return m.Kind == "log" && strings.Contains(m.Text, text) })
		return ok
	}

	admin, adminEm := start()
	api := admin.API()
	sc, err := api.SaveScript(ScriptDTO{Name: "moderador", Code: "// as ações são pedidas pelo teste", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	call := func(action string, args map[string]any) error {
		// o limite contra flood tem teste próprio; aqui cada passo começa com folga
		admin.scripts.mu.Lock()
		admin.scripts.talkTokens = scriptTalkBurst
		admin.scripts.mu.Unlock()
		_, err := api.ScriptCall(sc.ID, action, args)
		return err
	}
	adminID := connect(admin, adminEm, "FuritoModerador")
	time.Sleep(1200 * time.Millisecond)
	visitor, visitorEm := start()
	vid := float64(connect(visitor, visitorEm, "FuritoAlvo"))
	waitFor(t, "ver o alvo", 3*time.Second, func() bool { return clientOf(adminEm, int(vid)).ID != 0 })
	lobby := clientOf(adminEm, int(vid)).ChannelID

	// 1. o script ainda não tem a permissão do usuário: nada chega ao servidor
	if err := call("kickFromServer", map[string]any{"clientId": vid, "reason": "sem permissão do usuário"}); err == nil || !strings.Contains(err.Error(), "permissão para moderar") {
		t.Fatalf("sem \"Pode moderar\": %v", err)
	}
	sc.Moderation = true
	if _, err := api.SaveScript(sc); err != nil {
		t.Fatal(err)
	}

	// 2. o usuário permitiu, mas ele mesmo não tem a permissão no servidor: o servidor nega e o erro volta ao script
	if err := call("kickFromServer", map[string]any{"clientId": vid, "reason": "sem permissão no servidor"}); err == nil {
		t.Fatal("convidado não deveria conseguir expulsar")
	} else {
		t.Logf("expulsar sem permissão no servidor → %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if st := state(visitorEm); st.Status != "connected" || logged(adminEm, `Script "moderador"`) {
		t.Fatalf("a expulsão negada não deveria ter efeito nem ficar no registro (alvo: %s)", st.Status)
	}

	if err := api.UsePrivilegeKey(token); err != nil {
		t.Fatalf("usar chave de privilégio: %v", err)
	}
	waitFor(t, "virar Server Admin", 3*time.Second, func() bool {
		return strings.Contains(strings.Join(clientOf(adminEm, adminID).ServerGroups, ","), "Server Admin")
	})
	// canal moderado: quem entra como convidado só fala se receber a palavra
	if err := api.CreateChannel(ChannelSpec{Name: "Sala do Script", Kind: "permanent", MaxClients: -1, Codec: 4, CodecQuality: 6, NeededTalkPower: 50}); err != nil {
		t.Fatalf("criar canal: %v", err)
	}
	var sala int
	waitFor(t, "canal criado aparecer", 3*time.Second, func() bool {
		for _, ch := range state(adminEm).Channels {
			if ch.Name == "Sala do Script" {
				sala = ch.ID
			}
		}
		return sala != 0
	})

	// 3. mover
	if err := call("moveClient", map[string]any{"clientId": vid, "channelId": 60000.0}); err == nil || !strings.Contains(err.Error(), "canal não encontrado") {
		t.Errorf("mover para canal inexistente: %v", err)
	}
	if err := call("moveClient", map[string]any{"clientId": vid, "channelId": float64(sala)}); err != nil {
		t.Fatalf("moveClient: %v", err)
	}
	waitFor(t, "alvo no canal novo", 3*time.Second, func() bool {
		return clientOf(adminEm, int(vid)).ChannelID == sala && state(visitorEm).Self.ChannelID == sala
	})
	if !logged(adminEm, `Script "moderador" moveu [b]"FuritoAlvo"[/b] para o canal "Sala do Script"`) {
		t.Error("a ação do script deveria ficar no registro do servidor")
	}

	// 4. dar e tirar a palavra (o canal é moderado)
	if err := call("setTalker", map[string]any{"clientId": vid, "talker": true}); err != nil {
		t.Fatalf("setTalker: %v", err)
	}
	waitFor(t, "palavra concedida", 3*time.Second, func() bool { return clientOf(adminEm, int(vid)).IsTalker })
	if !logged(adminEm, `Script "moderador" deu a palavra a [b]"FuritoAlvo"[/b]`) {
		t.Error("dar a palavra deveria ficar no registro")
	}
	clients, _ := api.ScriptCall(sc.ID, "getClients", nil)
	talker := false
	for _, cl := range clients.([]*ScriptClient) {
		talker = talker || (cl.ID == int(vid) && cl.IsTalker)
	}
	if !talker {
		t.Error("getClients deveria mostrar quem tem a palavra")
	}
	if err := call("setTalker", map[string]any{"clientId": vid, "talker": false}); err != nil {
		t.Fatalf("setTalker(false): %v", err)
	}
	waitFor(t, "palavra retirada", 3*time.Second, func() bool { return !clientOf(adminEm, int(vid)).IsTalker })

	// 5. expulsar do canal: volta para o canal padrão
	if err := call("kickFromChannel", map[string]any{"clientId": vid, "reason": "50% de chance"}); err != nil {
		t.Fatalf("kickFromChannel: %v", err)
	}
	waitFor(t, "alvo de volta ao canal padrão", 3*time.Second, func() bool { return state(visitorEm).Self.ChannelID == lobby })
	if !logged(adminEm, `expulsou [b]"FuritoAlvo"[/b] do canal (50% de chance)`) {
		t.Error("o motivo (texto livre, com %) deveria aparecer como foi escrito no registro")
	}

	// 6. ninguém modera a si mesmo, nem quem não está no servidor
	for _, id := range []float64{float64(adminID), 60000} {
		if err := call("kickFromServer", map[string]any{"clientId": id}); err == nil || !strings.Contains(err.Error(), "não encontrado") {
			t.Errorf("expulsar o cliente %v deveria ser recusado: %v", id, err)
		}
	}

	// 7. expulsar do servidor
	if err := call("kickFromServer", map[string]any{"clientId": vid, "reason": "tchau"}); err != nil {
		t.Fatalf("kickFromServer: %v", err)
	}
	waitFor(t, "alvo expulso do servidor", 4*time.Second, func() bool { return state(visitorEm).Status != "connected" })
	if !logged(visitorEm, "expulso do servidor") {
		t.Error("o alvo deveria ver que foi expulso")
	}

	// 8. banir por 2 s (o servidor é descartável; o banimento pega o endereço de onde o alvo conecta)
	time.Sleep(1500 * time.Millisecond)
	vid = float64(connect(visitor, visitorEm, "FuritoAlvo"))
	waitFor(t, "ver o alvo de novo", 3*time.Second, func() bool { return clientOf(adminEm, int(vid)).ID != 0 })
	if err := call("ban", map[string]any{"clientId": vid, "reason": "sem duração"}); err == nil || !strings.Contains(err.Error(), "duração") {
		t.Errorf("ban sem duração: %v", err)
	}
	if st := state(visitorEm); st.Status != "connected" {
		t.Fatal("ban sem duração não pode banir")
	}
	if err := call("ban", map[string]any{"clientId": vid, "seconds": 2.0, "reason": "teste"}); err != nil {
		t.Fatalf("ban: %v", err)
	}
	waitFor(t, "alvo banido", 4*time.Second, func() bool { return state(visitorEm).Status != "connected" })
	if !logged(visitorEm, "banido") || !logged(adminEm, `baniu [b]"FuritoAlvo"[/b] por 2 s (teste)`) {
		t.Error("o banimento deveria aparecer para o alvo e no registro de quem moderou")
	}
}

// Sons tocados por scripts têm ritmo limitado: um script não inunda o canal.
func TestScriptSoundRateLimit(t *testing.T) {
	var st scriptStore
	now := time.Unix(1_000_000, 0)
	allowed := 0
	for i := 0; i < 50; i++ {
		if st.allowSound(now) {
			allowed++
		}
	}
	if allowed != scriptSoundBurst {
		t.Errorf("%d sons seguidos passaram; o limite da rajada é %d", allowed, scriptSoundBurst)
	}
	if st.allowSound(now.Add(scriptSoundEvery / 2)) {
		t.Error("meio intervalo depois ainda não cabe outro som")
	}
	if !st.allowSound(now.Add(2 * scriptSoundEvery)) {
		t.Error("passado o intervalo, mais um som deveria caber")
	}
	if !st.allowSound(now.Add(time.Hour)) || st.soundTokens > scriptSoundBurst {
		t.Errorf("a reserva não passa da rajada: %v", st.soundTokens)
	}
}
