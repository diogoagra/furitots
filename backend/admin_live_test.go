package backend

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Teste ao vivo dos recursos de administração, contra um servidor em que
// temos uma chave de privilégio (não roda por padrão):
//
//	FURITO_ICON_TEST_HOST=127.0.0.1:19987 FURITO_ICON_TEST_TOKEN=... go test ./backend/ -run LiveAdmin -v
//
// Se FURITO_TEST_CONTAINER tiver o nome do contêiner Docker do servidor, o
// teste também o pausa para provocar uma queda de conexão de verdade.
func TestLiveAdmin(t *testing.T) {
	host, token := os.Getenv("FURITO_ICON_TEST_HOST"), os.Getenv("FURITO_ICON_TEST_TOKEN")
	if host == "" || token == "" {
		t.Skip("defina FURITO_ICON_TEST_HOST e FURITO_ICON_TEST_TOKEN")
	}
	newApp := func(name string) (*App, *fakeEmitter, *API) {
		dir := t.TempDir()
		app, err := NewApp(Options{
			IdentityPath: filepath.Join(dir, "id.json"), SettingsPath: filepath.Join(dir, "cfg", "s.json"),
			DisableAudioDevices: true,
		})
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
			return st.Status == "connected" && st.Self.ClientID != 0 && len(st.Channels) > 0
		})
		return app, em, app.API()
	}
	state := func(em *fakeEmitter) State { st, _ := em.lastState(); return st }
	client := func(em *fakeEmitter, id int) ClientDTO {
		for _, cl := range state(em).Clients {
			if cl.ID == id {
				return cl
			}
		}
		return ClientDTO{}
	}
	channelByName := func(em *fakeEmitter, name string) ChannelDTO {
		for _, ch := range state(em).Channels {
			if ch.Name == name {
				return ch
			}
		}
		return ChannelDTO{}
	}
	groupByName := func(list []GroupEntry, name string) int {
		for _, g := range list {
			if g.Name == name && g.Type == 1 {
				return g.ID
			}
		}
		return 0
	}
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}

	admin, adminEm, a := newApp("FuritoAdmin")
	adminID := state(adminEm).Self.ClientID

	// sem privilégio, criar canal permanente é negado — e o erro volta para quem chamou
	if err := a.CreateChannel(ChannelSpec{Name: "Negado", Kind: "permanent", MaxClients: -1}); err == nil {
		t.Error("convidado não deveria conseguir criar canal permanente")
	} else {
		t.Logf("criar canal sem permissão → %v", err)
	}

	must("usar chave de privilégio", a.UsePrivilegeKey(token))
	waitFor(t, "virar Server Admin", 3*time.Second, func() bool {
		return strings.Contains(strings.Join(client(adminEm, adminID).ServerGroups, ","), "Server Admin")
	})

	// ---- canais
	must("criar canal", a.CreateChannel(ChannelSpec{Name: "Sala Teste", Topic: "tópico", Description: "[b]descrição[/b]", Kind: "permanent", MaxClients: -1, Codec: 4, CodecQuality: 7}))
	waitFor(t, "canal criado aparecer", 3*time.Second, func() bool { return channelByName(adminEm, "Sala Teste").ID != 0 })
	sala := channelByName(adminEm, "Sala Teste")
	if !sala.IsPermanent || sala.Topic != "tópico" || sala.CodecQuality != 7 || sala.MaxClients != -1 {
		t.Errorf("canal criado com propriedades erradas: %+v", sala)
	}
	must("criar subcanal com senha", a.CreateChannel(ChannelSpec{Name: "Sub Secreta", ParentID: sala.ID, Password: "abc", Kind: "semi", MaxClients: 5, Codec: 5, CodecQuality: 10}))
	waitFor(t, "subcanal aparecer", 3*time.Second, func() bool { return channelByName(adminEm, "Sub Secreta").ID != 0 })
	sub := channelByName(adminEm, "Sub Secreta")
	if sub.ParentID != sala.ID || !sub.HasPassword || !sub.IsSemiPermanent || sub.MaxClients != 5 || sub.Codec != 5 || sub.Depth != 1 {
		t.Errorf("subcanal com propriedades erradas: %+v", sub)
	}
	must("editar canal", a.EditChannel(sala.ID, ChannelSpec{Name: "Sala Renomeada", Topic: "novo", Description: "x", ChangeDescription: true, Kind: "permanent", MaxClients: 10, Codec: 4, CodecQuality: 5, NeededTalkPower: 0}))
	waitFor(t, "edição aparecer", 3*time.Second, func() bool {
		ch := channelByName(adminEm, "Sala Renomeada")
		return ch.ID == sala.ID && ch.Topic == "novo" && ch.MaxClients == 10
	})

	// ---- segundo cliente (convidado)
	viewer, viewEm, v := newApp("FuritoVisita")
	viewerID := state(viewEm).Self.ClientID
	var voiceIn atomic.Int64
	viewer.client.OnVoice = func(from, seq uint16, codec byte, opus []byte) {
		if len(opus) > 0 {
			voiceIn.Add(1)
		}
	}
	waitFor(t, "admin ver o visitante", 3*time.Second, func() bool { return client(adminEm, viewerID).ID != 0 })

	// ---- mover, cutucar
	must("mover cliente", a.MoveClient(viewerID, sala.ID, ""))
	waitFor(t, "visitante no canal novo", 3*time.Second, func() bool {
		return state(viewEm).Self.ChannelID == sala.ID && client(adminEm, viewerID).ChannelID == sala.ID
	})
	must("cutucar", a.PokeClient(viewerID, "olá"))
	waitFor(t, "cutucada chegar", 3*time.Second, func() bool {
		viewEm.mu.Lock()
		defer viewEm.mu.Unlock()
		return viewEm.pokes > 0
	})

	// ---- sussurro: em canais diferentes a voz normal não chega; o sussurro, sim
	frame := []byte{0xf8, 0xff, 0xfe}
	for i := 0; i < 10; i++ {
		admin.sendVoice(frame)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	normal := voiceIn.Load()
	must("definir alvo do sussurro", a.SetWhisperTarget("client", viewerID))
	if w := a.GetState().Self.Whisper; w.Kind != "client" || w.Name != "FuritoVisita" {
		t.Errorf("estado do sussurro: %+v", w)
	}
	for i := 0; i < 10; i++ {
		admin.sendVoice(frame)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	whisperClient := voiceIn.Load() - normal
	must("sussurrar para o canal", a.SetWhisperTarget("channel", sala.ID))
	for i := 0; i < 10; i++ {
		admin.sendVoice(frame)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	whisperChannel := voiceIn.Load() - normal - whisperClient
	must("parar sussurro", a.SetWhisperTarget("", 0))
	t.Logf("voz recebida pelo visitante em outro canal: normal=%d, sussurro ao cliente=%d, sussurro ao canal=%d (de 10 cada)", normal, whisperClient, whisperChannel)
	if normal != 0 || whisperClient != 10 || whisperChannel != 10 {
		t.Errorf("sussurro: normal=%d cliente=%d canal=%d", normal, whisperClient, whisperChannel)
	}

	// ---- grupos
	normalGroup := groupByName(state(adminEm).Groups.Server, "Normal")
	operator := groupByName(state(adminEm).Groups.Channel, "Operator")
	if normalGroup == 0 || operator == 0 {
		t.Fatalf("grupos padrão não encontrados: %+v", state(adminEm).Groups)
	}
	must("adicionar grupo de servidor", a.AddServerGroup(viewerID, normalGroup))
	waitFor(t, "grupo aparecer no cliente", 3*time.Second, func() bool {
		return strings.Contains(strings.Join(client(viewEm, viewerID).ServerGroups, ","), "Normal")
	})
	must("remover grupo de servidor", a.RemoveServerGroup(viewerID, normalGroup))
	waitFor(t, "grupo sumir do cliente", 3*time.Second, func() bool {
		return !strings.Contains(strings.Join(client(viewEm, viewerID).ServerGroups, ","), "Normal")
	})
	must("definir grupo de canal", a.SetChannelGroup(viewerID, operator))
	waitFor(t, "grupo de canal aparecer", 3*time.Second, func() bool { return client(adminEm, viewerID).ChannelGroup == "Operator" })

	// ---- permissões
	guest := groupByName(state(adminEm).Groups.Server, "Guest")
	perms, err := a.GetGroupPermissions("server", guest)
	must("listar permissões", err)
	t.Logf("grupo Guest: %d permissões", len(perms))
	if len(perms) < 10 {
		t.Errorf("lista de permissões curta demais: %d", len(perms))
	}
	must("definir permissão", a.SetGroupPermission("server", guest, "i_client_poke_power", 77, false, false))
	find := func(name string) (PermDTO, bool) {
		list, _ := a.GetGroupPermissions("server", guest)
		for _, p := range list {
			if p.Name == name {
				return p, true
			}
		}
		return PermDTO{}, false
	}
	if p, ok := find("i_client_poke_power"); !ok || p.Value != 77 {
		t.Errorf("permissão não foi gravada: %+v ok=%v", p, ok)
	}
	must("remover permissão", a.RemoveGroupPermission("server", guest, "i_client_poke_power"))
	if _, ok := find("i_client_poke_power"); ok {
		t.Error("permissão removida ainda aparece")
	}
	if _, err := v.GetGroupPermissions("server", guest); err == nil {
		t.Error("convidado não deveria poder listar permissões")
	}

	// ---- arquivos do canal
	local := filepath.Join(t.TempDir(), "relatorio final.txt")
	content := bytes.Repeat([]byte("conteúdo do arquivo de teste\n"), 500)
	os.WriteFile(local, content, 0o600)
	n, err := admin.uploadFiles(sala.ID, "/", "", []string{local})
	must("enviar arquivo", err)
	must("criar pasta", a.CreateChannelDirectory(sala.ID, "/docs", ""))
	files, err := a.ListChannelFiles(sala.ID, "/", "")
	must("listar arquivos", err)
	var gotFile, gotDir bool
	for _, f := range files {
		gotFile = gotFile || (f.Name == "relatorio final.txt" && f.Size == int64(len(content)) && !f.IsDir && f.Modified > 0)
		gotDir = gotDir || (f.Name == "docs" && f.IsDir)
	}
	if n != 1 || !gotFile || !gotDir {
		t.Errorf("arquivos do canal: enviados=%d lista=%+v", n, files)
	}
	data, err := admin.client.DownloadFile(sala.ID, "/relatorio final.txt", "")
	if err != nil || !bytes.Equal(data, content) {
		t.Errorf("download do arquivo: %d bytes, err=%v", len(data), err)
	}
	must("apagar arquivo", a.DeleteChannelFile(sala.ID, "/relatorio final.txt", ""))
	must("apagar pasta", a.DeleteChannelFile(sala.ID, "/docs", ""))
	if files, _ := a.ListChannelFiles(sala.ID, "/", ""); len(files) != 0 {
		t.Errorf("depois de apagar ainda há arquivos: %+v", files)
	}

	// ---- apelido
	must("trocar apelido", a.SetNickname("Admin Novo"))
	waitFor(t, "apelido novo aparecer para o outro", 3*time.Second, func() bool { return client(viewEm, adminID).Nickname == "Admin Novo" })
	// canal moderado: só fala quem tem poder de fala
	// A edição manda só o que mudou: o formulário chega com a descrição vazia
	// (ela ainda não tinha sido carregada) e mesmo assim ela não é apagada.
	must("tornar o canal moderado", a.EditChannel(sala.ID, ChannelSpec{Name: "Sala Renomeada", Topic: "novo", Description: "", Kind: "permanent", MaxClients: 10, Codec: 4, CodecQuality: 5, NeededTalkPower: 50}))
	waitFor(t, "canal ficar moderado", 3*time.Second, func() bool { return channelByName(adminEm, "Sala Renomeada").NeededTalkPower == 50 })
	v.RequestChannelInfo(sala.ID)
	waitFor(t, "descrição continuar lá depois da edição", 3*time.Second, func() bool { return channelByName(viewEm, "Sala Renomeada").Description == "x" })
	// O visitante volta a ser convidado no canal: o servidor só aceita o
	// pedido de quem ainda não tem poder de fala suficiente.
	must("voltar o grupo de canal para Guest", a.SetChannelGroup(viewerID, groupByName(state(adminEm).Groups.Channel, "Guest")))
	time.Sleep(600 * time.Millisecond)
	must("pedir a palavra", v.RequestTalk(true, "posso falar?"))
	waitFor(t, "pedido de palavra aparecer", 3*time.Second, func() bool {
		cl := client(adminEm, viewerID)
		return cl.TalkRequest && cl.TalkRequestMessage == "posso falar?" && state(viewEm).Self.TalkRequested
	})
	must("dar a palavra", a.SetClientTalker(viewerID, true))
	waitFor(t, "palavra concedida", 3*time.Second, func() bool { return client(adminEm, viewerID).IsTalker })

	// ---- queda de conexão de verdade: pausa o servidor
	if container := os.Getenv("FURITO_TEST_CONTAINER"); container != "" {
		if out, err := exec.Command("docker", "pause", container).CombinedOutput(); err != nil {
			t.Fatalf("docker pause: %v %s", err, out)
		}
		paused := true
		defer func() {
			if paused {
				exec.Command("docker", "unpause", container).Run()
			}
		}()
		waitFor(t, "visitante perceber a queda e entrar em reconexão", 25*time.Second, func() bool {
			st := state(viewEm)
			return st.Status == "reconnecting" && st.ReconnectAttempt >= 1
		})
		t.Logf("queda percebida; estado: %s tentativa %d", state(viewEm).Status, state(viewEm).ReconnectAttempt)
		exec.Command("docker", "unpause", container).Run()
		paused = false
		defer func() {
			if t.Failed() {
				st := state(viewEm)
				t.Logf("estado final do visitante: %s tentativa %d canal %d erro %q", st.Status, st.ReconnectAttempt, st.Self.ChannelID, st.Error)
				viewEm.mu.Lock()
				for _, m := range viewEm.messages[max(0, len(viewEm.messages)-12):] {
					t.Logf("  log do visitante: %s", m.Text)
				}
				viewEm.mu.Unlock()
			}
		}()
		waitFor(t, "reconectar sozinho e voltar ao canal", 90*time.Second, func() bool {
			st := state(viewEm)
			return st.Status == "connected" && st.ReconnectAttempt == 0 && st.Self.ChannelID == sala.ID
		})
		viewerID = state(viewEm).Self.ClientID
		waitFor(t, "admin também se recuperar", 90*time.Second, func() bool {
			st := state(adminEm)
			return st.Status == "connected" && client(adminEm, viewerID).ID != 0
		})
		t.Logf("reconectado: canal %d, novo id %d", state(viewEm).Self.ChannelID, viewerID)
	}

	// ---- expulsar do canal e do servidor
	must("expulsar do canal", a.KickClient(viewerID, false, "teste"))
	waitFor(t, "visitante voltar ao canal padrão", 3*time.Second, func() bool { return state(viewEm).Self.ChannelID != sala.ID && state(viewEm).Status == "connected" })
	must("expulsar do servidor", a.KickClient(viewerID, true, "tchau"))
	waitFor(t, "visitante ser desconectado sem tentar reconectar", 4*time.Second, func() bool {
		st := state(viewEm)
		return st.Status == "disconnected" && strings.Contains(st.Error, "expulso") && st.ReconnectAttempt == 0
	})
	time.Sleep(1500 * time.Millisecond)
	if st := state(viewEm); st.Status != "disconnected" {
		t.Errorf("expulso não deveria reconectar sozinho: %s", st.Status)
	}

	// ---- apagar canais
	must("apagar subcanal", a.DeleteChannel(sub.ID))
	must("apagar canal", a.DeleteChannel(sala.ID))
	waitFor(t, "canais sumirem", 3*time.Second, func() bool {
		return channelByName(adminEm, "Sala Renomeada").ID == 0 && channelByName(adminEm, "Sub Secreta").ID == 0
	})
}
