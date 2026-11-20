package ts3

import (
	"os"
	"testing"
	"time"
)

// Confere ao vivo como o servidor responde a comandos com return_code:
//
//	FURITO_TEST_HOST=127.0.0.1:9987 go test ./ts3/ -run LiveRequest -v
func TestLiveRequest(t *testing.T) {
	host := os.Getenv("FURITO_TEST_HOST")
	if host == "" {
		t.Skip("defina FURITO_TEST_HOST")
	}
	ident, _ := GenerateIdentity()
	c := NewClient(ident)
	ok := make(chan string, 2)
	c.OnConnected = func() { ok <- "" }
	c.OnDisconnected = func(r string, e bool) { ok <- r }
	c.Connect(ConnectOptions{Host: host, Nickname: "FuritoReq"})
	select {
	case r := <-ok:
		if r != "" {
			t.Fatal(r)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("tempo esgotado")
	}
	defer c.Disconnect()
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	_, err := c.request(BuildCommand("clientupdate", Param{"client_away", "0"}))
	t.Logf("comando simples que dá certo: err=%v em %v", err, time.Since(start).Round(time.Millisecond))
	if err != nil {
		t.Errorf("comando válido deveria confirmar sem erro: %v", err)
	}

	start = time.Now()
	items, err := c.request(BuildCommand("ftgetfilelist", Param{"cid", "1"}, Param{"cpw", ""}, Param{"path", "/"}), "notifyfilelist", "notifyfilelistfinished")
	t.Logf("lista de arquivos do canal 1: %d itens, err=%v em %v", len(items), err, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	_, err = c.request(BuildCommand("clientkick", Param{"clid", "9999"}, Param{"reasonid", "5"}, Param{"reasonmsg", "x"}))
	t.Logf("expulsar cliente inexistente: err=%v em %v", err, time.Since(start).Round(time.Millisecond))
	if err == nil {
		t.Error("deveria dar erro")
	}

	start = time.Now()
	items, err = c.request(BuildCommand("servergrouppermlist", Param{"sgid", "8"})+" -permsid", "notifyservergrouppermlist")
	t.Logf("permissões do grupo Guest: %d itens, err=%v em %v", len(items), err, time.Since(start).Round(time.Millisecond))
	if len(items) > 0 {
		t.Logf("exemplo: %v", items[0])
	}
}
