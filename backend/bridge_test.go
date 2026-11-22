package backend

import (
	"encoding/json"
	"strings"
	"testing"
)

// raw monta os argumentos como a janela os manda: cada um em JSON.
func raw(t *testing.T, args ...any) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, len(args))
	for i, a := range args {
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = data
	}
	return out
}

// A janela chega ao backend por um método só, que diz de que aba é o pedido e
// chama a API pelo nome.
func TestBridgeCallsTheAPIByName(t *testing.T) {
	app, _ := newTestApp(t)
	b := app.Bridge()
	tab := app.current().id

	// sem argumentos, devolvendo um valor
	res, err := b.Call(tab, "GetState", nil)
	st, ok := res.(State)
	if err != nil || !ok || st.Tab != tab || st.Status != "disconnected" {
		t.Fatalf("GetState: %+v, %v", res, err)
	}
	// a aba 0 é a que está à vista
	if res, err := b.Call(0, "GetState", nil); err != nil || res.(State).Tab != tab {
		t.Fatalf("aba 0 deveria ser a que está à vista: %+v, %v", res, err)
	}

	// argumentos convertidos de JSON, sem valor de volta
	if res, err := b.Call(tab, "SetOutputVolume", raw(t, 42)); err != nil || res != nil {
		t.Fatalf("SetOutputVolume: %v, %v", res, err)
	}
	if v := app.engine.Volume(); v != 42 {
		t.Errorf("volume %v, esperado 42", v)
	}

	// um objeto como argumento e (valor, erro) de volta
	res, err = b.Call(tab, "SaveBookmark", raw(t, Bookmark{Name: "Casa", Host: "exemplo.local"}))
	if id, _ := res.(string); err != nil || id == "" {
		t.Fatalf("SaveBookmark: %v, %v", res, err)
	}

	// o erro do método chega como erro
	if _, err := b.Call(tab, "SendText", raw(t, 2, 0, "oi")); err == nil || !strings.Contains(err.Error(), "não conectado") {
		t.Errorf("SendText desconectado deveria falhar com \"não conectado\": %v", err)
	}
	if _, err := b.Call(tab, "SendText", raw(t, 9, 0, "oi")); err == nil {
		t.Error("destino de mensagem inválido deveria ser recusado")
	}
}

func TestBridgeRefusesWhatItCannotCall(t *testing.T) {
	app, _ := newTestApp(t)
	b := app.Bridge()
	tab := app.current().id

	cases := []struct {
		name   string
		tab    int
		method string
		args   []json.RawMessage
		want   string
	}{
		{"aba que não existe", tab + 100, "GetState", nil, "aba foi fechada"},
		{"método que não existe", tab, "ApagarTudo", nil, "método desconhecido"},
		{"método não exportado", tab, "connect", nil, "método desconhecido"},
		{"argumentos demais", tab, "SetOutputVolume", raw(t, 10, 20), "argumentos"},
		{"tipo errado", tab, "SetOutputVolume", raw(t, "alto"), "inválido"},
	}
	for _, c := range cases {
		if _, err := b.Call(c.tab, c.method, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: erro %v, esperado conter %q", c.name, err, c.want)
		}
	}

	// argumento que falta vale o zero do tipo, como uma chamada com undefined
	if _, err := b.Call(tab, "SetOutputVolume", nil); err != nil {
		t.Errorf("argumento ausente: %v", err)
	}
	if v := app.engine.Volume(); v != 0 {
		t.Errorf("volume %v, esperado 0", v)
	}
}

// Depois de uma queda, a reconexão se apresenta igual à primeira conexão:
// também como cliente Furito (antes só a primeira mandava a marca).
func TestReconnectAnnouncesFuritoToo(t *testing.T) {
	opts := connectOptions(ConnectRequest{Host: " exemplo.local ", Nickname: "Furico", ServerPassword: "s", DefaultChannel: "Sala", ChannelPassword: "c"})
	if opts.MetaData != furitoMeta() || opts.MetaData == "" {
		t.Errorf("a conexão deveria levar a marca de cliente Furito: %q", opts.MetaData)
	}
	if opts.Host != "exemplo.local" || opts.Nickname != "Furico" || opts.ServerPassword != "s" || opts.DefaultChannel != "Sala" || opts.ChannelPassword != "c" {
		t.Errorf("pedido de conexão traduzido errado: %+v", opts)
	}
}
