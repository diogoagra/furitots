package backend

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"furito-ts/screencap"
)

// Uma oferta de vídeo de verdade (SDP) tem alguns milhares de caracteres, a
// maior parte repetitiva: comprimida, cabe com folga num comando de plugin.
func sampleSDP() string {
	var b strings.Builder
	b.WriteString("v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96 97 98 99 100 101 102\r\n")
	for pt := 96; pt < 130; pt++ {
		b.WriteString("a=rtpmap:" + itoa(pt) + " H264/90000\r\na=rtcp-fb:" + itoa(pt) + " goog-remb\r\na=rtcp-fb:" + itoa(pt) + " transport-cc\r\na=rtcp-fb:" + itoa(pt) + " ccm fir\r\na=rtcp-fb:" + itoa(pt) + " nack\r\na=rtcp-fb:" + itoa(pt) + " nack pli\r\na=fmtp:" + itoa(pt) + " level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f\r\n")
	}
	b.WriteString("a=candidate:1 1 udp 2113937151 0b9c5d8e-8d6e-4c1a-9d57-2f1c6f0c7a11.local 61474 typ host\r\na=fingerprint:sha-256 4A:79:94:16:44:3F:BD:05:41:5A:C7:20:F3:12:54:70:00:73:5D:33:00:2D:2C:80:9B:39:E1:9F:2D:A7:49:87\r\n")
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }

// encodeSignalUnchecked comprime sem os limites de tamanho de encodeSignal,
// para montar mensagens maliciosas nos testes.
func encodeSignalUnchecked(payload string) (string, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	w.Write([]byte(payload))
	w.Close()
	return "z" + base64.RawStdEncoding.EncodeToString(buf.Bytes()), nil
}

func TestSignalEncoding(t *testing.T) {
	for _, payload := range []string{`{"t":"hello"}`, `{"t":"offer","sid":"abc","sdp":"` + strings.ReplaceAll(sampleSDP(), "\r\n", `\r\n`) + `"}`, "acentuação: ção ã é / | \\ espaços"} {
		wire, err := encodeSignal(payload)
		if err != nil {
			t.Fatalf("codificar %d caracteres: %v", len(payload), err)
		}
		back, ok := decodeSignal(wire)
		if !ok || back != payload {
			t.Errorf("mensagem de %d caracteres não voltou igual (no fio: %d)", len(payload), len(wire))
		}
		if len(payload) > 2000 {
			t.Logf("oferta de %d caracteres vai em %d pelo servidor", len(payload), len(wire))
			if len(wire) > len(payload)/2 {
				t.Errorf("a oferta deveria comprimir bem: %d → %d", len(payload), len(wire))
			}
		}
	}
	if _, err := encodeSignal(""); err == nil {
		t.Error("mensagem vazia deveria ser recusada")
	}
	// ruído não comprime: acima do que o servidor entrega, é recusado aqui
	rng := rand.New(rand.NewSource(1))
	noise := make([]byte, 9000)
	for i := range noise {
		noise[i] = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"[rng.Intn(62)]
	}
	if _, err := encodeSignal(string(noise)); err == nil {
		t.Error("mensagem que não cabe no comando deveria ser recusada")
	}
	// o que vem dos outros clientes não é confiável
	for _, bad := range []string{"", "j", "x{}", "z!!!", "z" + strings.Repeat("A", 20)} {
		if _, ok := decodeSignal(bad); ok {
			t.Errorf("decodeSignal(%q) deveria falhar", bad)
		}
	}
	// bomba de descompressão: 1 MB de zeros cabe em poucos bytes comprimidos
	bomb, _ := encodeSignalUnchecked(strings.Repeat("0", 1<<20))
	if _, ok := decodeSignal(bomb); ok {
		t.Error("mensagem que descomprime acima do limite deveria ser descartada")
	}
}

func TestScreenSettings(t *testing.T) {
	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := app.API()
	sc := api.GetState().Screen
	if len(sc.IceServers) != 1 || sc.IceServers[0].URLs[0] != defaultStunURL || sc.RelayOnly || sc.Warned || sc.HasTurn {
		t.Errorf("ajustes iniciais: %+v", sc)
	}
	for _, bad := range [][4]string{{"http://x", "", "", ""}, {"", "stun:x:1", "", ""}, {"", "turn:", "", ""}, {"stun:a b", "", "", ""}} {
		if err := api.SetScreenSettings(bad[0], bad[1], bad[2], bad[3], false); err == nil {
			t.Errorf("deveria recusar %v", bad)
		}
	}
	if err := api.SetScreenSettings("", "", "", "", true); err == nil {
		t.Error("esconder o IP sem servidor de retransmissão deveria ser recusado")
	}
	if err := api.SetScreenSettings("none", "turn:relay.exemplo.org:3478", "furito", "segredo", true); err != nil {
		t.Fatal(err)
	}
	api.AcceptScreenWarning()
	sc = api.GetState().Screen
	if len(sc.IceServers) != 1 || sc.IceServers[0].URLs[0] != "turn:relay.exemplo.org:3478" || sc.IceServers[0].Username != "furito" || sc.IceServers[0].Credential != "segredo" || !sc.RelayOnly || !sc.Warned || !sc.HasTurn || sc.TurnUser != "furito" {
		t.Errorf("depois de configurar o TURN: %+v", sc)
	}
	// senha vazia mantém a guardada; esvaziar o endereço apaga tudo
	if err := api.SetScreenSettings("", "turn:relay.exemplo.org:3478", "furito", "", false); err != nil {
		t.Fatal(err)
	}
	if sc = api.GetState().Screen; len(sc.IceServers) != 2 || sc.IceServers[1].Credential != "segredo" || sc.RelayOnly {
		t.Errorf("senha deveria ser mantida: %+v", sc)
	}
	app.Shutdown()
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if sc = again.API().GetState().Screen; !sc.Warned || !sc.HasTurn || sc.IceServers[1].Credential != "segredo" {
		t.Errorf("ao reabrir: %+v", sc)
	}
	again.API().SetScreenSettings("", "", "", "", false)
	if sc = again.API().GetState().Screen; sc.HasTurn || len(sc.IceServers) != 1 {
		t.Errorf("sem TURN: %+v", sc)
	}
}

// Os quadros da captura chegam à interface por um endereço dentro da janela.
func TestScreenFrameHandler(t *testing.T) {
	app, _ := newTestApp(t)
	srv := httptest.NewServer(app.ScreenFrameHandler())
	defer srv.Close()
	get := func(q string) (*http.Response, []byte) {
		res, err := http.Get(srv.URL + ScreenFramePath + q)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, body
	}
	if res, _ := get(""); res.StatusCode != http.StatusNoContent || res.Header.Get("X-Screen-Ended") != "1" {
		t.Errorf("sem captura: %d ended=%q", res.StatusCode, res.Header.Get("X-Screen-Ended"))
	}
	if err := app.API().ScreenCaptureStart(-1, 320, 180, 30); err != nil {
		t.Fatal(err)
	}
	defer app.API().ScreenCaptureStop()
	res, body := get("?after=0")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || len(body) < 200 || body[0] != 0xFF || body[1] != 0xD8 {
		t.Fatalf("primeiro quadro: %d %q %d bytes", res.StatusCode, res.Header.Get("Content-Type"), len(body))
	}
	seq := res.Header.Get("X-Frame-Seq")
	res2, _ := get("?after=" + seq)
	if res2.StatusCode != 200 || res2.Header.Get("X-Frame-Seq") == seq {
		t.Errorf("o pedido seguinte deveria trazer um quadro novo: %d seq %s → %s", res2.StatusCode, seq, res2.Header.Get("X-Frame-Seq"))
	}
	if res, _ := http.Get(srv.URL + "/outro"); res.StatusCode != http.StatusNotFound {
		t.Errorf("outro caminho: %d", res.StatusCode)
	}
	app.API().ScreenCaptureStop()
	if screencap.Running() {
		t.Error("a captura deveria ter parado")
	}
}

// Dois apps no mesmo canal: a sinalização vai e volta pelo servidor de
// verdade, inclusive uma oferta de vídeo grande; quem está bloqueado é ignorado.
func TestLiveScreenSignaling(t *testing.T) {
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
	a, emA := start("TelaA")
	b, emB := start("TelaB")
	stA, _ := emA.lastState()
	stB, _ := emB.lastState()
	signals := func(em *fakeEmitter) []ScreenSignalDTO {
		var out []ScreenSignalDTO
		for _, ev := range em.eventsNamed("screen:signal") {
			if s, ok := ev.(ScreenSignalDTO); ok {
				out = append(out, s)
			}
		}
		return out
	}

	// aviso para o canal
	if err := a.API().ScreenSend(0, `{"t":"sharing","sid":"s1"}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "B receber o aviso de compartilhamento", 3*time.Second, func() bool { return len(signals(emB)) == 1 })
	got := signals(emB)[0]
	if got.From != stA.Self.ClientID || got.FromName != "TelaA" || got.Payload != `{"t":"sharing","sid":"s1"}` {
		t.Errorf("aviso recebido: %+v", got)
	}
	// oferta grande, só para um cliente
	offer := `{"t":"offer","sid":"s1","sdp":"` + strings.ReplaceAll(sampleSDP(), "\r\n", `\r\n`) + `"}`
	if err := a.API().ScreenSend(stB.Self.ClientID, offer); err != nil {
		t.Fatalf("enviar a oferta: %v", err)
	}
	waitFor(t, "B receber a oferta inteira", 3*time.Second, func() bool {
		s := signals(emB)
		return len(s) == 2 && s[1].Payload == offer
	})
	// resposta de volta
	if err := b.API().ScreenSend(stA.Self.ClientID, `{"t":"answer","sid":"s1"}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "A receber a resposta", 3*time.Second, func() bool {
		s := signals(emA)
		return len(s) == 1 && s[0].From == stB.Self.ClientID
	})
	if len(signals(emA)) != 1 {
		t.Errorf("A não deveria receber o eco do próprio aviso ao canal: %+v", signals(emA))
	}
	// bloqueado: a sinalização dele é ignorada
	if err := b.API().SetContact(stA.Self.ClientID, "blocked"); err != nil {
		t.Fatal(err)
	}
	a.API().ScreenSend(0, `{"t":"sharing","sid":"s2"}`)
	time.Sleep(700 * time.Millisecond)
	if n := len(signals(emB)); n != 2 {
		t.Errorf("sinalização de cliente bloqueado deveria ser ignorada (%d mensagens)", n)
	}
	t.Logf("oferta de %d caracteres entregue inteira pelo servidor", len(offer))
}

// A sinalização do compartilhamento de tela vem de outros clientes: qualquer
// texto tem de ser recusado ou devolvido dentro do limite, sem derrubar o app.
func FuzzDecodeSignal(f *testing.F) {
	for _, payload := range []string{`{"t":"offer","sdp":"v=0"}`, strings.Repeat("a", 5000)} {
		if wire, err := encodeSignal(payload); err == nil {
			f.Add(wire)
		}
	}
	f.Add("z")
	f.Add("zAAAA")
	f.Add("j")
	f.Add("z" + strings.Repeat("7MEBAQAAAIAg", 500)) // lixo em base64
	f.Fuzz(func(t *testing.T, wire string) {
		out, ok := decodeSignal(wire)
		if ok && (out == "" || len(out) > maxSignalPlain) {
			t.Fatalf("mensagem aceita fora dos limites: %d bytes", len(out))
		}
	})
}

// Uma mensagem pequena que se expande muito (bomba de descompressão) é recusada.
func TestDecodeSignalRejectsBomb(t *testing.T) {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write(make([]byte, 4<<20)) // 4 MB de zeros cabem em poucos KB
	w.Close()
	wire := "z" + base64.RawStdEncoding.EncodeToString(buf.Bytes())
	if len(wire) > maxSignalWire {
		t.Skipf("a bomba não coube num comando (%d bytes)", len(wire))
	}
	if out, ok := decodeSignal(wire); ok {
		t.Fatalf("bomba de descompressão aceita: %d bytes", len(out))
	}
}
