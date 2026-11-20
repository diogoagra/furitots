package backend

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"furito-ts/ts3"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{IdentityPath: filepath.Join(dir, "id.json"), SettingsPath: filepath.Join(dir, "cfg", "s.json"), DisableAudioDevices: true}
}

func TestBookmarksHotkeysAndPersistence(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()

	if _, err := api.SaveBookmark(Bookmark{Host: "  "}); err == nil {
		t.Error("favorito sem endereço deveria ser recusado")
	}
	id, err := api.SaveBookmark(Bookmark{Name: "Rushback", Host: "ts.exemplo.com:9987", Nickname: "Furico", DefaultChannel: "Afk"})
	if err != nil || id == "" {
		t.Fatalf("salvar favorito: %q %v", id, err)
	}
	id2, _ := api.SaveBookmark(Bookmark{Host: "outro.exemplo.com"})
	if _, err := api.SaveBookmark(Bookmark{ID: id, Name: "Rushback TS", Host: "ts.exemplo.com:9987"}); err != nil {
		t.Fatal(err)
	}
	api.DeleteBookmark(id2)
	if err := api.ConnectBookmark("nao-existe"); err == nil {
		t.Error("favorito inexistente deveria dar erro")
	}

	if err := api.SetHotkey("ptt", "F13"); err != nil {
		t.Errorf("atalho válido recusado: %v", err)
	}
	if err := api.SetHotkey("toggleMute", "KeyM"); err == nil {
		t.Error("letra sozinha deveria ser recusada")
	}
	if err := api.SetHotkey("voar", "F1"); err == nil {
		t.Error("ação desconhecida deveria ser recusada")
	}
	st := api.GetState()
	if st.Hotkeys.PTT != "F13" || st.Hotkeys.ToggleMute != "" || st.Hotkeys.Errors["toggleMute"] == "" {
		t.Errorf("estado dos atalhos: %+v", st.Hotkeys)
	}
	api.SetAutoReconnect(false)
	api.SetVADMode("ptt")
	if err := api.SetNickname("  Furico Caolho  "); err != nil {
		t.Fatal(err)
	}
	app.Shutdown()

	app2, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app2.Shutdown()
	st = app2.API().GetState()
	if len(st.Bookmarks) != 1 || st.Bookmarks[0].Name != "Rushback TS" || st.Bookmarks[0].ID != id {
		t.Errorf("favoritos não persistiram: %+v", st.Bookmarks)
	}
	if st.Hotkeys.PTT != "F13" || st.AutoReconnect || st.Audio.VADMode != "ptt" {
		t.Errorf("ajustes não persistiram: atalhos=%+v reconectar=%v modo=%s", st.Hotkeys, st.AutoReconnect, st.Audio.VADMode)
	}
	if got := app2.API().GetSettings().Nickname; got != "Furico Caolho" {
		t.Errorf("apelido salvo = %q", got)
	}
	if st.Identity.UID == "" || st.Identity.SecurityLevel < 8 {
		t.Errorf("identidade no estado: %+v", st.Identity)
	}
}

func TestChatHistory(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.historyKey = "servidor-A"
	app.mu.Unlock()
	app.addMessage(Message{Kind: "chat", TargetMode: 2, InvokerName: "Leko", Text: "bora jogar?"})
	app.addMessage(Message{Kind: "chat", TargetMode: 3, InvokerName: "Furico", Text: "aviso geral", Self: true})
	app.addMessage(Message{Kind: "chat", TargetMode: 1, InvokerName: "Leko", Text: "segredo"}) // privado: não é guardado
	app.addLog("info", "linha de log")                                                         // log: não é guardado
	app.Shutdown()

	app2, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app2.Shutdown()
	app2.loadHistory("servidor-B")
	if n := len(app2.API().GetMessages()); n != 0 {
		t.Fatalf("o histórico é por servidor; outro servidor trouxe %d linhas", n)
	}
	app2.loadHistory("servidor-A")
	msgs := app2.API().GetMessages()
	if len(msgs) != 2 || !msgs[0].History || msgs[0].Text != "bora jogar?" || msgs[1].TargetMode != 3 || !msgs[1].Self {
		t.Fatalf("histórico carregado: %+v", msgs)
	}
	if time.Since(time.UnixMilli(msgs[0].Time)) > time.Minute {
		t.Errorf("a hora original deveria ser mantida: %v", time.UnixMilli(msgs[0].Time))
	}
	// linhas vindas do histórico não são gravadas de novo
	app2.loadHistory("servidor-A")
	if err := app2.API().ClearHistory(); err != nil {
		t.Fatal(err)
	}
	before := len(app2.API().GetMessages())
	app2.loadHistory("servidor-A")
	if len(app2.API().GetMessages()) != before {
		t.Error("depois de apagar, não deveria haver histórico")
	}
}

func identByName(t *testing.T, api *API, name string) IdentityEntryDTO {
	t.Helper()
	for _, e := range api.GetState().Identities {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("identidade %q não está na lista: %+v", name, api.GetState().Identities)
	return IdentityEntryDTO{}
}

// Várias identidades: criar, renomear, escolher a padrão, exportar, importar,
// aumentar o nível e apagar — e tudo continua lá ao reabrir o app.
func TestMultipleIdentities(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()

	st := api.GetState()
	if len(st.Identities) != 1 || !st.Identities[0].IsDefault || !st.Identities[0].InUse || st.Identity.UID != st.Identities[0].UID {
		t.Fatalf("estado inicial: uma identidade, padrão e em uso; veio %+v / %+v", st.Identities, st.Identity)
	}
	first := st.Identities[0]
	if err := api.DeleteIdentity(first.ID); err == nil {
		t.Error("a única identidade não pode ser apagada")
	}

	// criar e renomear
	if _, err := api.CreateIdentity("   "); err == nil {
		t.Error("identidade sem nome deveria ser recusada")
	}
	secondID, err := api.CreateIdentity("Alt")
	if err != nil {
		t.Fatal(err)
	}
	second := identByName(t, api, "Alt")
	if second.ID != secondID || second.UID == first.UID || second.SecurityLevel < 8 || second.IsDefault || second.InUse {
		t.Errorf("identidade nova inesperada: %+v", second)
	}
	if err := api.UpdateIdentity(secondID, "Conta secundária", "  Fulano  "); err != nil {
		t.Fatal(err)
	}
	if e := identByName(t, api, "Conta secundária"); e.Nickname != "Fulano" {
		t.Errorf("apelido da identidade = %q", e.Nickname)
	}
	if err := api.UpdateIdentity("nao-existe", "X", ""); err == nil {
		t.Error("renomear identidade inexistente deveria falhar")
	}

	// a padrão passa a ser a em uso quando não há conexão
	if err := api.SetDefaultIdentity(secondID); err != nil {
		t.Fatal(err)
	}
	st = api.GetState()
	if st.Identity.ID != secondID || st.Identity.UID != second.UID || st.Identity.Name != "Conta secundária" {
		t.Errorf("identidade em uso depois de trocar a padrão: %+v", st.Identity)
	}

	// exportar e reimportar: a mesma identidade não entra duas vezes
	if _, err := api.ExportIdentity(first.ID); err == nil {
		t.Error("sem seletor de arquivos deveria dar erro")
	}
	exported := filepath.Join(t.TempDir(), "principal.json")
	app.SetSavePicker(func(title, name string) (string, error) { return exported, nil })
	if path, err := api.ExportIdentity(first.ID); err != nil || path != exported {
		t.Fatalf("exportar: %q %v", path, err)
	}
	if ident, err := ts3.LoadIdentity(exported); err != nil || ident.Uid != first.UID {
		t.Fatalf("o arquivo exportado não é a identidade pedida: %v", err)
	}
	if _, err := app.importIdentity(exported); err == nil || !strings.Contains(err.Error(), "já está na lista") {
		t.Errorf("importar uma identidade que já existe: %v", err)
	}

	// importar outra (formato do app) e um arquivo inválido
	other, _ := ts3.GenerateIdentity()
	otherFile := filepath.Join(t.TempDir(), "do-notebook.json")
	ts3.SaveIdentity(other, otherFile)
	bad := filepath.Join(t.TempDir(), "lixo.json")
	os.WriteFile(bad, []byte("{}"), 0o600)
	if _, err := app.importIdentity(bad); err == nil {
		t.Error("arquivo inválido deveria ser recusado")
	}
	app.SetOpenPicker(func(title string, multiple bool) ([]string, error) { return []string{otherFile}, nil })
	thirdID, err := api.ImportIdentity()
	if err != nil || thirdID == "" {
		t.Fatalf("importar: %q %v", thirdID, err)
	}
	third := identByName(t, api, "do-notebook")
	if third.UID != other.Uid || third.InUse || third.IsDefault {
		t.Errorf("identidade importada inesperada: %+v", third)
	}
	if api.GetState().Identity.ID != secondID {
		t.Error("importar não pode trocar a identidade em uso")
	}

	// nível de segurança de uma identidade específica
	level, err := api.ImproveIdentity(thirdID, third.SecurityLevel+2)
	if err != nil || level < third.SecurityLevel+2 {
		t.Errorf("aumentar nível: %d (era %d) %v", level, third.SecurityLevel, err)
	}
	if identByName(t, api, "Principal").SecurityLevel != first.SecurityLevel {
		t.Error("aumentar o nível de uma identidade mexeu em outra")
	}
	if _, err := api.ImproveIdentity(thirdID, 99); err == nil {
		t.Error("nível absurdo deveria ser recusado")
	}

	// apagar a padrão: outra vira a padrão e a em uso
	if err := api.DeleteIdentity(secondID); err != nil {
		t.Fatal(err)
	}
	st = api.GetState()
	if len(st.Identities) != 2 || st.Identity.ID == secondID || !identByName(t, api, st.Identity.Name).IsDefault {
		t.Errorf("depois de apagar a padrão: %+v / em uso %+v", st.Identities, st.Identity)
	}

	// favorito com identidade própria
	bid, _ := api.SaveBookmark(Bookmark{Name: "Com identidade", Host: "exemplo.org", IdentityID: thirdID})
	app.Shutdown()

	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	st = again.API().GetState()
	if len(st.Identities) != 2 || identByName(t, again.API(), "do-notebook").SecurityLevel != level {
		t.Errorf("ao reabrir: %+v", st.Identities)
	}
	if len(st.Bookmarks) != 1 || st.Bookmarks[0].ID != bid || st.Bookmarks[0].IdentityID != thirdID {
		t.Errorf("favorito ao reabrir: %+v", st.Bookmarks)
	}
	// nenhuma chave em arquivo legível por outros usuários
	if info, err := os.Stat(opts.IdentityPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("permissões do arquivo de identidades: %v %v", info.Mode().Perm(), err)
	}
}

// Quem já usava o app tem um arquivo com uma identidade só (e talvez uma
// ".anterior", guardada por uma importação): as duas entram na lista nova e o
// arquivo original fica copiado.
func TestIdentityMigrationFromSingleFile(t *testing.T) {
	opts := testOptions(t)
	current, _ := ts3.GenerateIdentity()
	previous, _ := ts3.GenerateIdentity()
	ts3.SaveIdentity(current, opts.IdentityPath)
	ts3.SaveIdentity(previous, opts.IdentityPath+".anterior")
	legacy, _ := os.ReadFile(opts.IdentityPath)

	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	st := app.API().GetState()
	if len(st.Identities) != 2 {
		t.Fatalf("esperadas 2 identidades depois da conversão, vieram %+v", st.Identities)
	}
	if e := identByName(t, app.API(), "Principal"); e.UID != current.Uid || !e.IsDefault || !e.InUse || e.SecurityLevel != current.SecurityLevel() {
		t.Errorf("a identidade que estava em uso deveria continuar sendo a padrão: %+v", e)
	}
	if e := identByName(t, app.API(), "Anterior"); e.UID != previous.Uid {
		t.Errorf("a identidade guardada deveria entrar na lista: %+v", e)
	}
	if copyData, err := os.ReadFile(opts.IdentityPath + ".v1"); err != nil || string(copyData) != string(legacy) {
		t.Errorf("o arquivo antigo deveria ficar copiado em .v1: %v", err)
	}
	app.Shutdown()

	// segunda abertura: já no formato novo, nada muda
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if got := again.API().GetState(); len(got.Identities) != 2 || got.Identity.UID != current.Uid {
		t.Errorf("ao reabrir depois da conversão: %+v", got.Identities)
	}

	// arquivo ilegível: o app não abre por cima (não gera identidade nova)
	broken := testOptions(t)
	os.WriteFile(broken.IdentityPath, []byte("isto não é json"), 0o600)
	if _, err := NewApp(broken); err == nil {
		t.Error("arquivo de identidades ilegível deveria impedir a abertura, não ser substituído")
	}
	if data, _ := os.ReadFile(broken.IdentityPath); string(data) != "isto não é json" {
		t.Error("o arquivo ilegível foi alterado")
	}
}

// A versão que os outros clientes Furito mostram vem do campo livre
// client_meta_data.
func TestFuritoVersionMarker(t *testing.T) {
	if got := furitoVersion(furitoMeta()); got != AppVersion {
		t.Errorf("a marca enviada deveria ser lida de volta: %q", got)
	}
	for meta, want := range map[string]string{
		"":                                  "",
		"outro cliente":                     "",
		"furito/":                           "",
		"furito/1.2.3":                      "1.2.3",
		"furito/2.0.0-beta tela":            "2.0.0-beta",
		"furito/1.0;x=1":                    "1.0",
		"furito/<b>1</b>":                   "",
		"furito/" + strings.Repeat("9", 40): "",
	} {
		if got := furitoVersion(meta); got != want {
			t.Errorf("furitoVersion(%q) = %q, esperado %q", meta, got, want)
		}
	}
}

// Identidade descartável no formato do cliente TeamSpeak 3, gerada só para
// este teste.
const (
	ts3SampleIdentity = "173VEOZWqQokNi7yVv9k+aceL/Frzr1nPGF+cmElWQJJRH09bVtBXUIBCS9CFxR9B1RYE31UVHxcSQEgZH0Gd3JTWUIHQkJ9AHR8FhB2PiJQLQBDXQB5CQNxSWQKe01HIUl4D318QGVWVUNJRC9WZTZ3M0IvcHZ2QXlYYjl5eFgzN0wyZnRDTkJvUWg5bFRJNlRwWjE2Sg=="
	ts3SampleUID      = "ZhiLTy2EKgpLixSSTEB7V6FdSEk="
)

// A importação aceita o .ini que o cliente TeamSpeak 3 exporta.
func TestImportTS3IdentityFile(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	api := app.API()
	original := api.GetState().Identity

	ini := filepath.Join(t.TempDir(), "ts3.ini")
	os.WriteFile(ini, []byte("[Identity]\r\nid=Usuario Normal\r\nidentity=\""+ts3SampleIdentity+"\"\r\nnickname=Fulano\r\nphonetic_nickname=\r\n"), 0o600)
	app.SetOpenPicker(func(title string, multiple bool) ([]string, error) { return []string{ini}, nil })
	id, err := api.ImportIdentity()
	if err != nil || id == "" {
		t.Fatalf("importar o .ini do TeamSpeak 3: %q %v", id, err)
	}
	// o nome e o apelido vêm do .ini
	got := identByName(t, api, "Usuario Normal")
	if got.ID != id || got.UID != ts3SampleUID || got.Nickname != "Fulano" {
		t.Errorf("identidade importada do .ini: %+v", got)
	}
	if got.SecurityLevel < 8 {
		t.Errorf("nível de segurança %d: servidores exigem 8 ou mais", got.SecurityLevel)
	}
	if now := api.GetState().Identity; now.UID != original.UID {
		t.Errorf("importar não pode trocar a identidade em uso: %+v", now)
	}
	if err := api.SetDefaultIdentity(id); err != nil {
		t.Fatal(err)
	}
	if now := api.GetState().Identity; now.UID != ts3SampleUID {
		t.Errorf("depois de tornar padrão, em uso = %+v", now)
	}
	app.Shutdown()
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if now := again.API().GetState().Identity; now.UID != ts3SampleUID || now.Name != "Usuario Normal" {
		t.Errorf("ao reabrir, em uso = %+v", now)
	}
	if len(again.API().GetState().Identities) != 2 {
		t.Errorf("ao reabrir deveriam estar as duas identidades: %+v", again.API().GetState().Identities)
	}
}

// Identidade real exportada pelo TeamSpeak 3 (arquivo indicado por variável de
// ambiente): o servidor aceita a conexão e nos vê com o UID dela.
func TestLiveImportedTS3Identity(t *testing.T) {
	file := os.Getenv("FURITO_TS3_IDENTITY_FILE")
	if file == "" {
		t.Skip("defina FURITO_TEST_HOST e FURITO_TS3_IDENTITY_FILE")
	}
	host := liveHost(t)
	app, err := NewApp(testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	em := &fakeEmitter{}
	app.SetEmitter(em)
	t.Cleanup(app.Shutdown)
	id, err := app.importIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	// conecta escolhendo a identidade importada, sem torná-la a padrão
	if err := app.API().Connect(ConnectRequest{Host: host, Nickname: "FuritoImport", IdentityID: id}); err != nil {
		t.Fatal(err)
	}
	want := app.API().GetState().Identity
	if want.ID != id {
		t.Fatalf("a conexão deveria usar a identidade pedida: %+v", want)
	}
	waitFor(t, "conectar com a identidade importada", 12*time.Second, func() bool {
		st, _ := em.lastState()
		return st.Status == "connected" && st.Self.ClientID != 0
	})
	var seen string
	waitFor(t, "aparecer na lista de clientes", 5*time.Second, func() bool {
		st, _ := em.lastState()
		for _, c := range st.Clients {
			if c.ID == st.Self.ClientID {
				seen = c.UID
			}
		}
		return seen != ""
	})
	// Servidores TS3 identificam pelo SHA-1 da chave pública (o UID que o
	// cliente TS3 mostra); servidores TS6, pelo SHA-256 da mesma chave.
	app.identMu.Lock()
	long := sha256.Sum256([]byte(app.identity.OmegaStr))
	app.identMu.Unlock()
	ts6UID := base64.StdEncoding.EncodeToString(long[:])
	t.Logf("conectado; nível de segurança %d; UID visto pelo servidor é o da identidade importada: formato TS3=%v, formato TS6=%v",
		want.SecurityLevel, seen == want.UID, seen == ts6UID)
	if seen != want.UID && seen != ts6UID {
		t.Errorf("o servidor nos vê com um UID que não é o da identidade importada")
	}
	st, _ := em.lastState()
	if seen == ts6UID && st.Identity.ServerUID != ts6UID {
		t.Errorf("o estado deveria informar o UID que o servidor usa (serverUid=%q)", st.Identity.ServerUID)
	}
}

// Dois clientes Furito no mesmo servidor: cada um mostra o outro (e a si
// mesmo) com a versão do app, não com a versão assinada que vai ao servidor.
func TestLiveFuritoVersionShown(t *testing.T) {
	host := liveHost(t)
	start := func(name string) (*App, *fakeEmitter) {
		app, err := NewApp(testOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		em := &fakeEmitter{}
		app.SetEmitter(em)
		t.Cleanup(app.Shutdown)
		if err := app.API().Connect(ConnectRequest{Host: host, Nickname: name}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, name+" conectar", 12*time.Second, func() bool {
			st, _ := em.lastState()
			return st.Status == "connected" && st.Self.ClientID != 0
		})
		return app, em
	}
	_, emA := start("FuritoVerA")
	_, emB := start("FuritoVerB")
	seen := func(em *fakeEmitter, nick string) ClientDTO {
		st, _ := em.lastState()
		for _, c := range st.Clients {
			if c.Nickname == nick {
				return c
			}
		}
		return ClientDTO{}
	}
	for _, c := range []struct {
		em   *fakeEmitter
		nick string
	}{{emA, "FuritoVerB"}, {emB, "FuritoVerA"}, {emA, "FuritoVerA"}} {
		waitFor(t, "ver "+c.nick+" como cliente Furito", 5*time.Second, func() bool { return seen(c.em, c.nick).Furito })
		got := seen(c.em, c.nick)
		if got.Version != AppVersion || got.Platform != AppVendor {
			t.Errorf("%s aparece como %q %q, esperado %q %q", c.nick, got.Version, got.Platform, AppVersion, AppVendor)
		}
	}
	st, _ := emA.lastState()
	t.Logf("versão mostrada para clientes Furito: %s %s; versão do app no estado: %s", seen(emA, "FuritoVerB").Version, seen(emA, "FuritoVerB").Platform, st.AppVersion)
}

func TestChannelSpecValidation(t *testing.T) {
	if _, err := (ChannelSpec{Name: "   "}).toTS3(); err == nil {
		t.Error("canal sem nome deveria ser recusado")
	}
	s, err := (ChannelSpec{Name: " Sala ", Kind: "semi", Codec: 9, CodecQuality: 50, MaxClients: -1}).toTS3()
	if err != nil || s.Name != "Sala" || !s.SemiPermanent || s.Permanent || s.Codec != 4 || s.CodecQuality != 10 {
		t.Errorf("conversão: %+v %v", s, err)
	}
}

// Volume por pessoa, mutar só para mim e contatos, contra um servidor real:
//
//	FURITO_TEST_HOST=127.0.0.1:9987 go test ./backend/ -run LiveContacts -v
func TestLiveContacts(t *testing.T) {
	host := liveHost(t)
	start := func(name string) (*App, *fakeEmitter) {
		app, err := NewApp(testOptions(t))
		if err != nil {
			t.Fatal(err)
		}
		em := &fakeEmitter{}
		app.SetEmitter(em)
		t.Cleanup(app.Shutdown)
		if err := app.API().Connect(ConnectRequest{Host: host, Nickname: name}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, name+" conectar", 12*time.Second, func() bool {
			st, _ := em.lastState()
			return st.Status == "connected" && st.Self.ClientID != 0
		})
		return app, em
	}
	me, meEm := start("FuritoEu")
	time.Sleep(1200 * time.Millisecond)
	other, otherEm := start("FuritoOutro")
	ost, _ := otherEm.lastState()
	otherID := ost.Self.ClientID
	find := func() ClientDTO {
		st, _ := meEm.lastState()
		for _, cl := range st.Clients {
			if cl.ID == otherID {
				return cl
			}
		}
		return ClientDTO{}
	}
	waitFor(t, "ver o outro cliente", 3*time.Second, func() bool { return find().ID != 0 })
	api := me.API()

	if err := api.SetClientVolume(otherID, 40); err != nil {
		t.Fatal(err)
	}
	if err := api.SetClientMuted(otherID, true); err != nil {
		t.Fatal(err)
	}
	if err := api.SetContact(otherID, "friend"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "preferências aparecerem no estado", 2*time.Second, func() bool {
		cl := find()
		return cl.Volume == 40 && cl.MutedLocally && cl.Contact == "friend"
	})
	if err := api.SetClientVolume(9999, 50); err == nil {
		t.Error("cliente inexistente deveria dar erro")
	}

	// bloqueado: as mensagens dele não aparecem
	api.SetContact(otherID, "blocked")
	other.API().SendText(2, 0, "mensagem de bloqueado")
	time.Sleep(700 * time.Millisecond)
	if _, got := meEm.findMessage(func(m Message) bool { return strings.Contains(m.Text, "mensagem de bloqueado") }); got {
		t.Error("mensagem de contato bloqueado apareceu")
	}
	api.SetContact(otherID, "")
	other.API().SendText(2, 0, "mensagem liberada")
	waitFor(t, "mensagem após desbloquear", 3*time.Second, func() bool {
		_, got := meEm.findMessage(func(m Message) bool { return strings.Contains(m.Text, "mensagem liberada") })
		return got
	})
	waitFor(t, "contato removido aparecer no estado", 2*time.Second, func() bool { return find().Contact == "" })
}

// Ajustes que só valem no Windows, testados aqui pela lógica (sem Windows).
func TestWindowsPathsAndNames(t *testing.T) {
	cfg := func() (string, error) { return `C:\Users\ana\AppData\Roaming`, nil }
	if got := defaultIdentityPath("windows", `C:\Users\ana`, cfg); !strings.Contains(got, "AppData") || !strings.Contains(got, "FuritoTS") || strings.Contains(got, ".ts_furito") {
		t.Errorf("identidades no Windows deveriam ficar na pasta de dados do app: %q", got)
	}
	if got := defaultIdentityPath("darwin", "/Users/ana", cfg); got != "/Users/ana/.ts_furito_identity.json" {
		t.Errorf("identidades no macOS: %q", got)
	}

	// A pasta de dados já teve outro nome (quando o app se chamava TeamSpeak
	// Furito): quem vem de lá continua com os ajustes, e não abre zerado.
	cfgDir := t.TempDir()
	if got := DataDir(cfgDir); got != filepath.Join(cfgDir, "FuritoTS") {
		t.Errorf("instalação nova: pasta de dados %q", got)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "FuritoTS")); err == nil {
		t.Error("perguntar pela pasta de dados não deveria criá-la")
	}
	old := filepath.Join(cfgDir, "TeamSpeakFurito")
	os.MkdirAll(filepath.Join(old, "history"), 0o700)
	os.WriteFile(filepath.Join(old, "settings.json"), []byte(`{"nickname":"Ana"}`), 0o600)
	got := DataDir(cfgDir)
	if data, err := os.ReadFile(filepath.Join(got, "settings.json")); got != filepath.Join(cfgDir, "FuritoTS") || err != nil || !strings.Contains(string(data), "Ana") {
		t.Errorf("a pasta antiga deveria ter virado a nova, com os ajustes dentro: %q (%v)", got, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("a pasta antiga deveria ter sido renomeada, não copiada")
	}
	// As duas existem (o app antigo foi aberto de novo depois): vale a nova, intacta.
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "settings.json"), []byte(`{"nickname":"Outro"}`), 0o600)
	if data, _ := os.ReadFile(filepath.Join(DataDir(cfgDir), "settings.json")); !strings.Contains(string(data), "Ana") {
		t.Errorf("com as duas pastas, deveria valer a nova: %s", data)
	}

	for in, want := range map[string]string{
		"relatório final.pdf": "relatório final.pdf",
		`a<b>c:d"e/f\g|h?i*j`: "a_b_c_d_e_f_g_h_i_j",
		"CON":                 "_CON",
		"nul.txt":             "_nul.txt",
		"com1.log":            "_com1.log",
		"console.txt":         "console.txt",
		"termina em ponto. ":  "termina em ponto",
		"":                    "arquivo",
		"...":                 "arquivo",
		"com\ttab":            "com_tab",
	} {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, esperado %q", in, got, want)
		}
	}
}

// Primeira abertura num computador novo: a pasta onde ficam as identidades
// ainda não existe (no Windows é %AppData%\FuritoTS). O app precisa
// criá-la em vez de falhar ao abrir.
func TestFirstRunCreatesIdentityFolder(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		IdentityPath:        filepath.Join(dir, "ainda", "nao", "existe", "identidades.json"),
		SettingsPath:        filepath.Join(dir, "tambem-nao", "settings.json"),
		DisableAudioDevices: true,
	}
	app, err := NewApp(opts)
	if err != nil {
		t.Fatalf("a primeira abertura falhou: %v", err)
	}
	st := app.API().GetState()
	if len(st.Identities) != 1 || st.Identity.UID == "" {
		t.Errorf("deveria haver uma identidade recém-criada: %+v", st.Identities)
	}
	uid := st.Identity.UID
	app.Shutdown()
	if _, err := os.Stat(opts.IdentityPath); err != nil {
		t.Fatalf("o arquivo de identidades não foi gravado: %v", err)
	}
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if got := again.API().GetState().Identity.UID; got != uid {
		t.Errorf("na segunda abertura a identidade mudou: %q → %q", uid, got)
	}
}

// Cutucadas: por padrão abrem uma janela; com a opção desligada só o aviso
// sonoro toca (e a linha do registro continua), sem janela.
func TestPokePopupOption(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	em := &fakeEmitter{}
	app.SetEmitter(em)
	api := app.API()
	pokes := func() int { em.mu.Lock(); defer em.mu.Unlock(); return em.pokes }

	if !api.GetState().PokePopup {
		t.Fatal("por padrão a cutucada deveria abrir uma janela")
	}
	app.onPoke("Fulano", "oi")
	if pokes() != 1 {
		t.Errorf("com a opção ligada a interface deveria ser avisada: %d", pokes())
	}
	api.SetPokePopup(false)
	if api.GetState().PokePopup {
		t.Error("a opção desligada deveria aparecer no estado")
	}
	app.onPoke("Fulano", "oi de novo")
	if pokes() != 1 {
		t.Errorf("com a opção desligada nenhuma janela deveria ser pedida: %d avisos", pokes())
	}
	app.Shutdown()

	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if again.API().GetState().PokePopup {
		t.Error("a escolha deveria ser lembrada ao reabrir")
	}
	again.API().SetPokePopup(true)
	if !again.API().GetState().PokePopup {
		t.Error("religar a opção não funcionou")
	}
}

// Cada som do soundboard pode ter um atalho global; há também um atalho para
// parar o que estiver tocando.
func TestSoundHotkeys(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()
	dir := t.TempDir()
	for _, name := range []string{"porta.wav", "lobo.wav"} {
		testWAV(t, filepath.Join(dir, name))
	}
	if n, err := app.addSoundFiles([]string{filepath.Join(dir, "porta.wav"), filepath.Join(dir, "lobo.wav")}); err != nil || n != 2 {
		t.Fatalf("adicionar sons: %d %v", n, err)
	}
	sound := func(name string) SoundDTO {
		for _, s := range api.GetState().Audio.Sounds {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("som %q não está no soundboard", name)
		return SoundDTO{}
	}
	porta, lobo := sound("porta"), sound("lobo")

	if err := api.SetHotkey("sound:nao-existe", "F13"); err == nil {
		t.Error("atalho para um som que não existe deveria ser recusado")
	}
	if err := api.SetHotkey("sound:"+porta.ID, "KeyA"); err == nil {
		t.Error("tecla de digitar sozinha deveria ser recusada")
	}
	if err := api.SetHotkey("sound:"+porta.ID, "F13"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetHotkey("sound:"+lobo.ID, "Ctrl+Alt+Digit2"); err != nil {
		t.Fatal(err)
	}
	if err := api.SetHotkey("soundStop", "F15"); err != nil {
		t.Fatal(err)
	}
	if got := sound("porta"); got.Hotkey != "F13" || got.HotkeyError != "" {
		t.Errorf("atalho do som: %+v", got)
	}
	if st := api.GetState(); st.Hotkeys.SoundStop != "F15" {
		t.Errorf("atalho de parar: %+v", st.Hotkeys)
	}
	// o mesmo atalho em dois sons: o segundo é recusado e o motivo aparece no som
	if err := api.SetHotkey("sound:"+lobo.ID, "F13"); err == nil {
		t.Error("atalho repetido deveria ser recusado")
	}
	if got := sound("lobo"); got.Hotkey != "Ctrl+Alt+Digit2" || got.HotkeyError == "" {
		t.Errorf("o som deveria manter o atalho anterior e mostrar o erro: %+v", got)
	}

	// apertar o atalho toca o som; o atalho de parar para
	playing := func() bool { return api.GetState().Audio.SoundboardPlaying }
	app.onHotkey("sound:"+porta.ID, false) // soltar a tecla não faz nada
	if playing() {
		t.Error("soltar a tecla não deveria tocar o som")
	}
	app.onHotkey("sound:"+porta.ID, true)
	if !playing() {
		t.Error("o atalho deveria tocar o som")
	}
	app.onHotkey("soundStop", true)
	if playing() {
		t.Error("o atalho de parar deveria interromper o som")
	}

	// remover o atalho de um som; remover um som leva o atalho junto
	if err := api.SetHotkey("sound:"+lobo.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := sound("lobo"); got.Hotkey != "" || got.HotkeyError != "" {
		t.Errorf("atalho removido: %+v", got)
	}
	if err := api.SoundboardRemove(porta.ID); err != nil {
		t.Fatal(err)
	}
	if err := api.SetHotkey("sound:"+lobo.ID, "F13"); err != nil {
		t.Errorf("o atalho do som removido deveria ficar livre: %v", err)
	}
	app.Shutdown()

	// continua ao reabrir; atalho de som que não existe mais é descartado
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	st := again.API().GetState()
	if len(st.Audio.Sounds) != 1 || st.Audio.Sounds[0].Hotkey != "F13" || st.Hotkeys.SoundStop != "F15" {
		t.Errorf("ao reabrir: %+v / %+v", st.Audio.Sounds, st.Hotkeys)
	}
	again.mu.Lock()
	_, stale := again.settings.Hotkeys["sound:"+porta.ID]
	again.mu.Unlock()
	if stale {
		t.Error("o atalho do som removido deveria ter sumido dos ajustes")
	}
}

// Quem escreve no histórico são os outros (e o servidor): mensagens enormes e
// sem parar não podem encher o disco do usuário.
func TestChatHistoryIsBounded(t *testing.T) {
	app, _ := newTestApp(t)
	app.mu.Lock()
	app.historyKey = "servidor-hostil"
	app.mu.Unlock()
	file := app.historyFile("servidor-hostil")

	huge := strings.Repeat("<é", 40<<10) // 120 KB, com caracteres que o JSON expande
	var biggest int64
	for i := 0; i < 400; i++ {
		app.saveHistory(Message{Kind: "chat", TargetMode: 2, InvokerName: strings.Repeat("N", 5000), Text: huge, Time: int64(i)})
		if info, err := os.Stat(file); err == nil && info.Size() > biggest {
			biggest = info.Size()
		}
	}
	app.saveHistory(Message{Kind: "chat", TargetMode: 2, InvokerName: "Leko", Text: "a última", Time: 999})

	// o arquivo pode passar do limite por uma mensagem, que é quando ele é reduzido
	if limit := int64(maxHistoryFile + 6*maxHistoryText + 4096); biggest > limit {
		t.Errorf("o histórico chegou a %d bytes; o limite é %d", biggest, limit)
	}
	lines := readHistory(file)
	if len(lines) == 0 || len(lines) > historyLoad {
		t.Fatalf("%d mensagens lidas do histórico", len(lines))
	}
	if last := lines[len(lines)-1]; last.Text != "a última" || last.InvokerName != "Leko" {
		t.Errorf("a mensagem mais recente deveria ter ficado: %q de %q", clipBytes(last.Text, 40), last.InvokerName)
	}
	for _, l := range lines[:len(lines)-1] {
		if len(l.Text) > maxHistoryText || len(l.InvokerName) > maxHistoryName || !utf8.ValidString(l.Text) {
			t.Fatalf("mensagem guardada além do limite ou cortada no meio de um caractere: %d bytes, apelido %d", len(l.Text), len(l.InvokerName))
		}
	}
	if _, err := os.Stat(file + ".tmp"); err == nil {
		t.Error("sobrou o arquivo temporário da redução do histórico")
	}
}

// Imagens de fora: o padrão é só carregar com um clique; o usuário pode
// liberar um servidor (pela identidade dele) ou mudar o modo.
func TestRemoteImageSettings(t *testing.T) {
	app, _ := newTestApp(t)
	api := app.API()
	if st := app.buildState(); st.Images.Mode != ImagesAsk || st.Images.Trusted {
		t.Fatalf("o padrão deveria ser perguntar: %+v", st.Images)
	}
	if err := api.SetRemoteImages("sempre"); err == nil {
		t.Error("um modo desconhecido deveria ser recusado")
	}
	for _, mode := range []string{ImagesAlways, ImagesNever, ImagesAsk} {
		if err := api.SetRemoteImages(mode); err != nil || app.buildState().Images.Mode != mode {
			t.Errorf("modo %q: %v (ficou %q)", mode, err, app.buildState().Images.Mode)
		}
	}
	if err := api.SetServerImages(true); err == nil {
		t.Error("sem conexão não há servidor para liberar")
	}
	// um arquivo de ajustes com um modo inválido volta ao padrão seguro
	path := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(path, []byte(`{"remoteImages":"qualquer","imageServers":{"uidA":true}}`), 0o600)
	if s := loadSettings(path); s.RemoteImages != ImagesAsk || !s.ImageServers["uidA"] {
		t.Errorf("ajustes lidos: modo %q, servidores %v", s.RemoteImages, s.ImageServers)
	}
	if s := loadSettings(filepath.Join(t.TempDir(), "nao-existe.json")); s.RemoteImages != ImagesAsk || s.ImageServers == nil {
		t.Errorf("ajustes novos: modo %q", s.RemoteImages)
	}
}

// Nomes vindos do servidor viram nomes de arquivo sem separadores de pasta,
// sem controles e sem os caracteres que invertem a direção do texto.
func TestSafeFileNameStripsTricks(t *testing.T) {
	for in, want := range map[string]string{
		"fotos\u202egnp.exe":       "fotosgnp.exe",
		"a\u2066b\u2069c\u202ad":   "abcd",
		"../../etc/passwd":         ".._.._etc_passwd",
		"..":                       "arquivo",
		"C:\\Windows\\system.ini":  "C__Windows_system.ini",
		"nul.txt":                  "_nul.txt",
		"com1":                     "_com1",
		"a\x00b\x1fc\x7fd":         "a_b_c_d",
		"  relatório final.pdf . ": "relatório final.pdf",
		"":                         "arquivo",
	} {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, esperado %q", in, got, want)
		}
	}
}

// O que fica guardado de cada mensagem tem tamanho limitado, e a mensagem de
// boas-vindas chega marcada como texto formatado (as outras linhas do registro
// não são formatadas pela interface).
func TestMessagesAreClippedAndWelcomeIsRich(t *testing.T) {
	app, em := newTestApp(t)
	app.addMessage(Message{Kind: "chat", TargetMode: 2, InvokerName: strings.Repeat("N", 5000), PeerName: strings.Repeat("P", 5000), Text: strings.Repeat("é", 100000)})
	app.client.OnWelcome("[b]Bem-vindo[/b]")
	app.client.OnLog("info", `[b]"[img]http://x/y[/img]"[/b] conectou`)
	msgs := app.API().GetMessages()
	if n := len(msgs); n != 3 {
		t.Fatalf("%d mensagens", n)
	}
	if m := msgs[0]; len(m.Text) > maxMessageText || len(m.InvokerName) > maxMessageName || len(m.PeerName) > maxMessageName || !utf8.ValidString(m.Text) {
		t.Errorf("mensagem além do limite: texto %d, nome %d", len(m.Text), len(m.InvokerName))
	}
	if !msgs[1].Rich || msgs[1].Kind != "log" || msgs[1].Text != "[b]Bem-vindo[/b]" {
		t.Errorf("a mensagem de boas-vindas deveria vir marcada como formatada: %+v", msgs[1])
	}
	if msgs[2].Rich {
		t.Error("uma linha comum do registro não é texto formatado")
	}
	_ = em
}
