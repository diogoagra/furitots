package backend

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"furito-ts/audio"
	"furito-ts/ts3"
)

type fakeEmitter struct {
	mu       sync.Mutex
	states   []State
	messages []Message
	talking  []Talking
	pokes    int
	lastPoke PokeDTO
	other    map[string][]any // demais eventos, por nome
}

func (f *fakeEmitter) Emit(event string, data any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch v := data.(type) {
	case State:
		f.states = append(f.states, v)
	case Message:
		f.messages = append(f.messages, v)
	case Talking:
		f.talking = append(f.talking, v)
	case PokeDTO:
		f.pokes++
		f.lastPoke = v
	default:
		if f.other == nil {
			f.other = make(map[string][]any)
		}
		f.other[event] = append(f.other[event], data)
	}
}

// eventsNamed devolve os dados dos eventos com esse nome, na ordem de chegada.
func (f *fakeEmitter) eventsNamed(event string) []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any(nil), f.other[event]...)
}

func (f *fakeEmitter) lastState() (State, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) == 0 {
		return State{}, false
	}
	return f.states[len(f.states)-1], true
}

func (f *fakeEmitter) findMessage(pred func(Message) bool) (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.messages {
		if pred(m) {
			return m, true
		}
	}
	return Message{}, false
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado esperando: %s", what)
}

func newTestApp(t *testing.T) (*App, *fakeEmitter) {
	t.Helper()
	dir := t.TempDir()
	app, err := NewApp(Options{
		IdentityPath:        filepath.Join(dir, "identity.json"),
		SettingsPath:        filepath.Join(dir, "settings.json"),
		RecordingsDir:       filepath.Join(dir, "gravacoes"),
		DisableAudioDevices: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	em := &fakeEmitter{}
	app.SetEmitter(em)
	t.Cleanup(app.Shutdown)
	return app, em
}

func TestInitialStateAndMessages(t *testing.T) {
	app, em := newTestApp(t)
	waitFor(t, "estado inicial", time.Second, func() bool { _, ok := em.lastState(); return ok })
	st, _ := em.lastState()
	if st.Status != "disconnected" || st.Audio.Effect != "normal" || st.Audio.OutputVolume != 100 {
		t.Errorf("estado inicial inesperado: %+v", st)
	}
	if st.Channels == nil || st.Clients == nil {
		t.Error("channels/clients devem ser listas vazias, não null")
	}

	app.current().onTextMessage(ts3.TextMessage{TargetMode: 1, Target: 0, InvokerID: 7, InvokerName: "Zé", Text: "oi"})
	app.addLog("info", "linha de log")
	msgs := app.API().GetMessages()
	if len(msgs) != 2 || msgs[0].ID == 0 || msgs[0].ID == msgs[1].ID {
		t.Fatalf("ids devem ser únicos e > 0: %+v", msgs)
	}
	if msgs[0].PeerID != 7 || msgs[0].PeerName != "Zé" || msgs[0].Self {
		t.Errorf("privado recebido: par deve ser o remetente: %+v", msgs[0])
	}
	if msgs[1].Kind != "log" || msgs[1].TargetMode != 0 {
		t.Errorf("log: %+v", msgs[1])
	}
}

func TestSettingsPersist(t *testing.T) {
	dir := t.TempDir()
	opts := Options{IdentityPath: filepath.Join(dir, "id.json"), SettingsPath: filepath.Join(dir, "s.json"), DisableAudioDevices: true}
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()
	api.SetVADThreshold(1234)
	api.SetOutputVolume(40)
	api.SetEffect("robot")
	api.SetEffect("inexistente") // inválido volta para normal
	api.SetVADThreshold(99999)   // limitado a 8000
	app.Shutdown()

	app2, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app2.Shutdown()
	st := app2.API().GetState()
	if st.Audio.VADThreshold != 8000 || st.Audio.OutputVolume != 40 || st.Audio.Effect != "normal" {
		t.Errorf("ajustes não persistiram: %+v", st.Audio)
	}
}

func TestCorruptIdentityIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.json")
	os.WriteFile(path, []byte("{corrompido"), 0o600)
	_, err := NewApp(Options{IdentityPath: path, SettingsPath: filepath.Join(dir, "s.json"), DisableAudioDevices: true})
	if err == nil {
		t.Fatal("identidade corrompida deve dar erro")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{corrompido" {
		t.Error("o arquivo de identidade não pode ser sobrescrito")
	}
}

func TestConnectValidation(t *testing.T) {
	app, _ := newTestApp(t)
	if err := app.API().Connect(ConnectRequest{Host: "  "}); err == nil {
		t.Error("host vazio deve dar erro")
	}
	if err := app.API().SendText(2, 0, "oi"); err == nil {
		t.Error("enviar texto desconectado deve dar erro")
	}
}

// Teste ao vivo: FURITO_TEST_HOST=127.0.0.1:9987 go test ./backend/ -run Live -v
func TestLiveServer(t *testing.T) {
	host := liveHost(t)
	app, em := newTestApp(t)
	api := app.API()

	if err := api.Connect(ConnectRequest{Host: host, Nickname: "FuritoTeste"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "conectar", 12*time.Second, func() bool {
		st, _ := em.lastState()
		if st.Status != "connected" || len(st.Channels) == 0 || st.Self.ClientID == 0 {
			return false
		}
		for _, cl := range st.Clients {
			if cl.ID == st.Self.ClientID && cl.ChannelID == st.Self.ChannelID && cl.ChannelGroup != "" {
				return true
			}
		}
		return false
	})
	sawConnecting := false
	em.mu.Lock()
	for _, s := range em.states {
		if s.Status == "connecting" {
			sawConnecting = true
		}
	}
	em.mu.Unlock()
	st, _ := em.lastState()
	t.Logf("conectado: servidor=%q versão=%q canais=%d clientes=%d self=%d canal=%d nick=%q (passou por connecting=%v)",
		st.Server.Name, st.Server.Version, len(st.Channels), len(st.Clients), st.Self.ClientID, st.Self.ChannelID, st.Self.Nickname, sawConnecting)
	if !sawConnecting {
		t.Error("a interface precisa receber o estado connecting")
	}

	text := `teste C:\dir | [b]x[/b]`
	if err := api.SendText(2, 0, text); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "eco do chat de canal", 3*time.Second, func() bool {
		_, ok := em.findMessage(func(m Message) bool { return m.Kind == "chat" && m.TargetMode == 2 && m.Text == text && m.Self })
		return ok
	})

	api.ToggleMute()
	api.SetAway(true, "volto já")
	waitFor(t, "mute/away refletidos pelo servidor", 3*time.Second, func() bool {
		st, _ := em.lastState()
		if !st.Self.Muted || !st.Self.Away {
			return false
		}
		for _, cl := range st.Clients {
			if cl.ID == st.Self.ClientID {
				return cl.InputMuted && cl.Away && cl.AwayMessage == "volto já"
			}
		}
		return false
	})

	api.RequestClientInfo(st.Self.ClientID)
	api.RequestChannelInfo(st.Self.ChannelID)
	time.Sleep(800 * time.Millisecond)
	st, _ = em.lastState()
	for _, cl := range st.Clients {
		if cl.ID == st.Self.ClientID {
			t.Logf("próprio cliente: versão=%q plataforma=%q connectedSince=%d grupos=%v grupoCanal=%q", cl.Version, cl.Platform, cl.ConnectedSince, cl.ServerGroups, cl.ChannelGroup)
			if cl.Version == "" || cl.ConnectedSince == 0 || len(cl.ServerGroups) == 0 {
				t.Error("detalhes do próprio cliente incompletos")
			}
		}
	}
	for _, ch := range st.Channels {
		if ch.ID == st.Self.ChannelID {
			t.Logf("canal atual: %q descrição=%q", ch.Name, ch.Description)
			if ch.Description == "" {
				t.Error("descrição do canal não chegou")
			}
		}
	}

	api.Disconnect()
	waitFor(t, "desconectar", 3*time.Second, func() bool {
		st, _ := em.lastState()
		return st.Status == "disconnected" && len(st.Channels) == 0 && st.Error == ""
	})
	if _, ok := em.findMessage(func(m Message) bool { return m.Kind == "log" && strings.Contains(m.Text, "Desconectado do servidor") }); !ok {
		t.Error("faltou a linha de log de desconexão")
	}

	// Porta sem servidor: o erro precisa chegar à interface.
	time.Sleep(time.Second)
	api.Connect(ConnectRequest{Host: "127.0.0.1:9", Nickname: "FuritoTeste"})
	waitFor(t, "erro de conexão visível", 13*time.Second, func() bool {
		st, _ := em.lastState()
		return st.Status == "disconnected" && st.Error != ""
	})
	st, _ = em.lastState()
	t.Logf("erro mostrado: %q", st.Error)
}

// writeTone grava um WAV curto para usar como som do usuário.
func writeTone(t *testing.T, path string, seconds float64) {
	t.Helper()
	const rate = 48000
	n := int(rate * seconds)
	buf := make([]byte, 44+n*2)
	copy(buf, "RIFF")
	le := func(off int, v uint32, size int) {
		for i := 0; i < size; i++ {
			buf[off+i] = byte(v >> (8 * i))
		}
	}
	le(4, uint32(36+n*2), 4)
	copy(buf[8:], "WAVEfmt ")
	le(16, 16, 4)
	le(20, 1, 2)
	le(22, 1, 2)
	le(24, rate, 4)
	le(28, rate*2, 4)
	le(32, 2, 2)
	le(34, 16, 2)
	copy(buf[36:], "data")
	le(40, uint32(n*2), 4)
	for i := 0; i < n; i++ {
		v := int16(8000)
		if (i/60)%2 == 0 {
			v = -8000
		}
		buf[44+i*2], buf[45+i*2] = byte(v), byte(uint16(v)>>8)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSoundboardCustomFiles(t *testing.T) {
	dir := t.TempDir()
	opts := Options{IdentityPath: filepath.Join(dir, "id.json"), SettingsPath: filepath.Join(dir, "cfg", "s.json"), DisableAudioDevices: true}
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()

	builtin := len(api.GetState().Audio.Sounds)
	if builtin != 0 {
		t.Fatalf("o soundboard começa vazio (só arquivos do usuário), vieram %d", builtin)
	}

	src := filepath.Join(dir, "Minha Risada.wav")
	writeTone(t, src, 0.2)
	notAudio := filepath.Join(dir, "planilha.txt")
	os.WriteFile(notAudio, []byte("não é áudio"), 0o600)

	added, err := app.addSoundFiles([]string{src, notAudio, filepath.Join(dir, "nao-existe.mp3")})
	if added != 1 || err == nil {
		t.Fatalf("deveria adicionar 1 e reportar erro dos outros: added=%d err=%v", added, err)
	}
	sounds := api.GetState().Audio.Sounds
	custom := sounds[len(sounds)-1]
	if len(sounds) != builtin+1 || custom.Builtin || custom.Name != "Minha Risada" {
		t.Fatalf("lista inesperada: %+v", sounds)
	}

	// O original pode sumir: o app toca a cópia dele.
	os.Remove(src)
	if err := api.SoundboardPlay(custom.ID); err != nil {
		t.Fatalf("tocar som do usuário: %v", err)
	}
	if !api.GetState().Audio.SoundboardPlaying {
		t.Error("deveria estar tocando")
	}
	api.SoundboardStop()

	if err := api.SoundboardRename(custom.ID, "  Risada do Zé  "); err != nil {
		t.Fatal(err)
	}
	if err := api.SoundboardPlay("inexistente"); err == nil {
		t.Error("id desconhecido deve dar erro")
	}
	api.SetSoundboardVolume(35)
	api.SetNoiseSuppression(false)
	api.SetVADMode("level")
	api.SetNormalizeVoices(false)
	app.Shutdown()

	// Reabrindo: som, nome e ajustes continuam lá.
	app2, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app2.Shutdown()
	st := app2.API().GetState()
	last := st.Audio.Sounds[len(st.Audio.Sounds)-1]
	if last.ID != custom.ID || last.Name != "Risada do Zé" {
		t.Errorf("som não persistiu: %+v", last)
	}
	if st.Audio.SoundboardVolume != 35 || st.Audio.NoiseSuppression || st.Audio.VADMode != "level" || st.Audio.NormalizeVoices || !st.Audio.AutoGain {
		t.Errorf("ajustes não persistiram: %+v", st.Audio)
	}
	if len(st.Audio.Effects) < 10 {
		t.Errorf("lista de efeitos curta: %d", len(st.Audio.Effects))
	}
	if err := app2.API().SoundboardPlay(custom.ID); err != nil {
		t.Errorf("tocar após reabrir: %v", err)
	}

	files, _ := os.ReadDir(filepath.Join(dir, "cfg", "sounds"))
	if len(files) != 1 {
		t.Fatalf("esperava 1 arquivo na pasta de sons, há %d", len(files))
	}
	if err := app2.API().SoundboardRemove(custom.ID); err != nil {
		t.Fatal(err)
	}
	files, _ = os.ReadDir(filepath.Join(dir, "cfg", "sounds"))
	if len(files) != 0 || len(app2.API().GetState().Audio.Sounds) != builtin {
		t.Errorf("remoção incompleta: %d arquivos, %d sons", len(files), len(app2.API().GetState().Audio.Sounds))
	}
}

func TestSoundPackParsing(t *testing.T) {
	root := t.TempDir()
	pack := filepath.Join(root, "meu")
	os.MkdirAll(pack, 0o755)
	os.MkdirAll(filepath.Join(root, "default"), 0o755)
	writeTone(t, filepath.Join(pack, "connected.wav"), 0.05)
	writeTone(t, filepath.Join(pack, "neutral_switched_tocurrentchannel.wav"), 0.05)
	writeTone(t, filepath.Join(root, "default", "click.wav"), 0.05)
	ini := `[info]
name = Meu Pacote
[soundfiles]
CONNECTION_CONNECTED = play("connected.wav")
CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS = play("${clientType}_switched_tocurrentchannel.wav")
SPECIAL_CLICK = play("../default/click.wav")
FORA_DA_PASTA = play("../../etc/passwd")
SEM_ARQUIVO = play("nao_existe.wav")
VAZIO =
# COMENTADO = play("connected.wav")
`
	os.WriteFile(filepath.Join(pack, "settings.ini"), []byte(ini), 0o644)

	p, ok := loadSoundPack(root, "meu")
	if !ok || p.Name != "Meu Pacote" {
		t.Fatalf("pacote não carregou: %+v", p)
	}
	for _, ev := range []string{"CONNECTION_CONNECTED", "CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS", "SPECIAL_CLICK"} {
		if p.events[ev] == "" {
			t.Errorf("evento %s sem som", ev)
		}
	}
	for _, ev := range []string{"FORA_DA_PASTA", "SEM_ARQUIVO", "VAZIO", "# COMENTADO"} {
		if p.events[ev] != "" {
			t.Errorf("evento %s não deveria ter som: %s", ev, p.events[ev])
		}
	}
	if _, ok := loadSoundPack(root, "default"); ok {
		t.Error("pasta sem settings.ini não é um pacote")
	}
}

func TestClientIconsOrderAndSniff(t *testing.T) {
	snap := ts3.Snapshot{
		ServerGroups: map[int]ts3.GroupInfo{
			6:  {Name: "Server Admin", IconID: 300, SortID: 0},
			20: {Name: "Druid", IconID: 4000000001, SortID: 5},
			21: {Name: "VIP", IconID: 123456, SortID: 2},
			8:  {Name: "Guest", IconID: 0},
		},
		ChannelGroups: map[int]ts3.GroupInfo{5: {Name: "Channel Admin", IconID: 100}},
	}
	cl := ts3.ClientInfo{ServerGroups: []int{20, 8, 6, 21}, ChannelGroupID: 5, IconID: 777}
	var ids []uint32
	for _, ic := range clientIcons(cl, snap) {
		ids = append(ids, ic.ID)
	}
	want := []uint32{100, 300, 123456, 4000000001, 777}
	if len(ids) != len(want) {
		t.Fatalf("ícones = %v, esperado %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ícones = %v, esperado %v", ids, want)
		}
	}

	cases := map[string]string{
		"\x89PNG\r\n\x1a\nresto":             "image/png",
		"GIF89a...":                          "image/gif",
		"\xff\xd8\xff\xe0":                   "image/jpeg",
		"<?xml version=\"1.0\"?><svg></svg>": "image/svg+xml",
		"<html><script>alert(1)</script>":    "",
		"texto qualquer":                     "",
	}
	for data, mime := range cases {
		if got := sniffImage([]byte(data)); got != mime {
			t.Errorf("sniffImage(%q) = %q, esperado %q", data, got, mime)
		}
	}
}

func TestClientNameAffixes(t *testing.T) {
	snap := ts3.Snapshot{
		ServerGroups: map[int]ts3.GroupInfo{
			10: {Name: "1850+", NameMode: 1, SortID: 3},
			11: {Name: "Líder", NameMode: 1, SortID: 1},
			12: {Name: "Staff", NameMode: 2},
			8:  {Name: "Guest", NameMode: 0},
		},
		ChannelGroups: map[int]ts3.GroupInfo{5: {Name: "Channel Admin", NameMode: 0}, 6: {Name: "Dono", NameMode: 2}},
	}
	prefix, suffix := clientNameAffixes(ts3.ClientInfo{ServerGroups: []int{10, 8, 12, 11}, ChannelGroupID: 6}, snap)
	if prefix != "[Líder][1850+] " || suffix != " [Staff][Dono]" {
		t.Errorf("prefixo=%q sufixo=%q", prefix, suffix)
	}
	prefix, suffix = clientNameAffixes(ts3.ClientInfo{ServerGroups: []int{8}, ChannelGroupID: 5}, snap)
	if prefix != "" || suffix != "" {
		t.Errorf("sem grupos de nome não deve haver prefixo: %q %q", prefix, suffix)
	}
}

var (
	liveMu   sync.Mutex
	liveLast time.Time // quando o último teste ao vivo terminou
)

// liveHost devolve o servidor dos testes ao vivo (ou pula o teste). Os testes
// ao vivo abrem várias conexões; emendadas, acionam a proteção do servidor
// contra excesso de conexões ("flood prevention"), que bloqueia o endereço por
// minutos: com 8 s entre os testes, a sétima conexão em 40 s já foi barrada.
// Por isso cada teste espera 20 s depois do anterior.
func liveHost(t *testing.T) string {
	t.Helper()
	host := os.Getenv("FURITO_TEST_HOST")
	if host == "" {
		t.Skip("defina FURITO_TEST_HOST")
	}
	liveMu.Lock()
	if wait := 20*time.Second - time.Since(liveLast); !liveLast.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	liveMu.Unlock()
	t.Cleanup(func() {
		liveMu.Lock()
		liveLast = time.Now()
		liveMu.Unlock()
	})
	return host
}

// A supressão de ruído tem dois ajustes: ligada ou não, e o tipo (neural, com
// o RNNoise, ou a clássica). Os dois persistem; quem tem um arquivo de ajustes
// de antes do RNNoise, que só guardava o liga/desliga, passa a usar a neural.
func TestNoiseSuppressionSettings(t *testing.T) {
	dir := t.TempDir()
	opts := Options{IdentityPath: filepath.Join(dir, "id.json"), SettingsPath: filepath.Join(dir, "s.json"), DisableAudioDevices: true}
	check := func(app *App, on bool, kind, engine string) {
		t.Helper()
		au := app.API().GetState().Audio
		if au.NoiseSuppression != on || au.NoiseMode != kind || app.engine.NoiseMode() != engine {
			t.Errorf("supressão ligada=%v tipo=%q motor=%q; esperado ligada=%v tipo=%q motor=%q",
				au.NoiseSuppression, au.NoiseMode, app.engine.NoiseMode(), on, kind, engine)
		}
	}

	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()
	check(app, true, audio.NoiseNeural, audio.NoiseNeural) // o padrão
	api.SetNoiseMode(audio.NoiseClassic)
	check(app, true, audio.NoiseClassic, audio.NoiseClassic)
	api.SetNoiseSuppression(false) // desligada: o tipo escolhido fica guardado
	check(app, false, audio.NoiseClassic, audio.NoiseOff)
	app.Shutdown()

	app, err = NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api = app.API()
	check(app, false, audio.NoiseClassic, audio.NoiseOff)
	api.SetNoiseSuppression(true)
	check(app, true, audio.NoiseClassic, audio.NoiseClassic)
	api.SetNoiseMode("qualquer coisa") // valor desconhecido: neural
	check(app, true, audio.NoiseNeural, audio.NoiseNeural)
	app.Shutdown()

	// Arquivos de ajustes de antes do RNNoise.
	for _, old := range []struct {
		json   string
		on     bool
		engine string
	}{
		{`{"noiseSuppression": true}`, true, audio.NoiseNeural},
		{`{"noiseSuppression": false}`, false, audio.NoiseOff},
	} {
		if err := os.WriteFile(opts.SettingsPath, []byte(old.json), 0o600); err != nil {
			t.Fatal(err)
		}
		app, err = NewApp(opts)
		if err != nil {
			t.Fatal(err)
		}
		check(app, old.on, audio.NoiseNeural, old.engine)
		app.Shutdown()
	}
}

// A janela "Sobre" mostra a versão, o autor e o site: os três vêm no estado,
// cada um no seu campo.
func TestAboutInfoInState(t *testing.T) {
	app, _ := newTestApp(t)
	st := app.API().GetState()
	if st.AppVersion != AppVersion || st.AppAuthor != AppVendor || st.AppSite != AppSite {
		t.Errorf("sobre: versão %q, autor %q, site %q", st.AppVersion, st.AppAuthor, st.AppSite)
	}
	if strings.Contains(st.AppVersion, " ") || !strings.HasPrefix(st.AppSite, "https://") {
		t.Errorf("a versão é só o número e o site é um endereço https: %q, %q", st.AppVersion, st.AppSite)
	}
}

// Os ajustes têm mapas (contatos, volumes, atalhos). Gravar em disco enquanto
// outra goroutine os altera não pode derrubar o programa (em Go, ler e
// escrever o mesmo mapa ao mesmo tempo é erro fatal).
func TestSettingsSaveDoesNotRaceWithChanges(t *testing.T) {
	app, _ := newTestApp(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			app.saveSettingsNow()
		}
	}()
	for i := 0; i < 300; i++ {
		uid := fmt.Sprintf("uid-%d", i%7)
		app.updateSettings(func(s *Settings) {
			s.Contacts[uid] = "friend"
			s.ClientVolumes[uid] = float64(i)
			s.ClientMuted[uid] = i%2 == 0
			s.Hotkeys["ptt"] = fmt.Sprintf("F%d", 1+i%12)
		})
	}
	<-done
	app.saveSettingsNow()
	saved := loadSettings(app.opts.SettingsPath)
	if saved.Contacts["uid-0"] != "friend" || len(saved.ClientVolumes) != 7 {
		t.Errorf("ajustes gravados incompletos: %+v", saved.Contacts)
	}
	if _, err := os.Stat(app.opts.SettingsPath + ".tmp"); err == nil {
		t.Error("sobrou o arquivo temporário dos ajustes")
	}
}

// A sensibilidade da detecção de voz é guardada entre execuções; valores fora
// da escala viram "sem mínimo".
func TestVADFloorSetting(t *testing.T) {
	dir := t.TempDir()
	opts := Options{IdentityPath: filepath.Join(dir, "id.json"), SettingsPath: filepath.Join(dir, "s.json"), DisableAudioDevices: true}
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()
	if got := api.GetState().Audio.VADFloor; got != 0 {
		t.Errorf("o padrão é sem mínimo; veio %v", got)
	}
	for in, want := range map[float64]float64{10: 50, 99999: 8000, -3: 0, 700: 700} {
		api.SetVADFloor(in)
		if got := api.GetState().Audio.VADFloor; got != want || app.engine.VADFloor() != want {
			t.Errorf("SetVADFloor(%v): estado %v, motor %v; esperado %v", in, got, app.engine.VADFloor(), want)
		}
	}
	api.SetVADFloor(700)
	app.Shutdown()

	app, err = NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := app.API().GetState().Audio.VADFloor; got != 700 || app.engine.VADFloor() != 700 {
		t.Errorf("a sensibilidade não persistiu: estado %v, motor %v", got, app.engine.VADFloor())
	}
	app.Shutdown()

	// um arquivo de ajustes com um valor absurdo não vira um microfone que nunca abre
	os.WriteFile(opts.SettingsPath, []byte(`{"vadFloor": 123456}`), 0o600)
	app, err = NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	if got := app.API().GetState().Audio.VADFloor; got != 0 {
		t.Errorf("valor fora da escala no arquivo: %v, esperado 0", got)
	}
}

// feedVoice entrega ao motor, como um microfone, frames de voz sintética (ou
// só o chiado do ambiente) até stop fechar; done fecha quando ela termina (um
// microfone é uma fonte só: duas ao mesmo tempo não valem).
func feedVoice(app *App, voice bool, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	rng := rand.New(rand.NewSource(1))
	frame := make([]int16, audio.FrameSize)
	phase, n := 0.0, 0
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := range frame {
			v := rng.NormFloat64() * 40
			if voice {
				sec := float64(n) / audio.SampleRate
				phase += 2 * math.Pi * 220 * (1 + 0.02*math.Sin(2*math.Pi*5*sec)) / audio.SampleRate
				var s float64
				for h := 1; h <= 12; h++ {
					s += math.Sin(float64(h)*phase) / float64(h)
				}
				v += 6000 * (0.25 + 0.75*0.5*(1-math.Cos(2*math.Pi*3*sec))) * s / 1.8
			}
			frame[i] = int16(v)
			n++
		}
		app.engine.FeedCapture(frame)
		time.Sleep(2 * time.Millisecond)
	}
}

// "Calibrar com a minha voz": mede a voz que chega ao microfone, ajusta a
// sensibilidade e a guarda; sem voz, devolve erro e não mexe em nada.
func TestCalibrateVoice(t *testing.T) {
	old := calibrationTime
	calibrationTime = 700 * time.Millisecond
	defer func() { calibrationTime = old }()

	app, _ := newTestApp(t)
	api := app.API()

	// Sem voz: erro, e a sensibilidade fica como estava.
	api.SetVADFloor(300)
	stop, done := make(chan struct{}), make(chan struct{})
	go feedVoice(app, false, stop, done)
	if _, err := api.CalibrateVoice(); err == nil {
		t.Error("calibrar sem voz deveria dar erro")
	}
	close(stop)
	<-done
	if got := api.GetState().Audio.VADFloor; got != 300 {
		t.Errorf("calibração sem voz mexeu na sensibilidade: %v", got)
	}

	// Com voz: mede, ajusta e guarda. Uma segunda calibração ao mesmo tempo é recusada.
	stop, done = make(chan struct{}), make(chan struct{})
	defer func() { close(stop); <-done }()
	go feedVoice(app, true, stop, done)
	second := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, err := api.CalibrateVoice()
		second <- err
	}()
	res, err := api.CalibrateVoice()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-second; err == nil {
		t.Error("duas calibrações ao mesmo tempo: a segunda deveria ser recusada")
	}
	t.Logf("voz %.0f, mínimo %.0f", res.Voice, res.Floor)
	if res.Voice < 2000 || res.Voice > 6000 || res.Floor < 400 || res.Floor > 1600 {
		t.Errorf("medida fora do esperado para a voz sintética: voz %.0f, mínimo %.0f", res.Voice, res.Floor)
	}
	if got := api.GetState().Audio.VADFloor; got != res.Floor || app.engine.VADFloor() != res.Floor {
		t.Errorf("a sensibilidade não foi aplicada: estado %v, motor %v, calibração %v", got, app.engine.VADFloor(), res.Floor)
	}
	app.mu.Lock()
	saved := app.settings.VADFloor
	app.mu.Unlock()
	if saved != res.Floor {
		t.Errorf("a sensibilidade não foi guardada nos ajustes: %v", saved)
	}
}
