package ts3

import (
	"os"
	"testing"
	"time"
)

// Confere ao vivo que o servidor aceita o aviso de gravação e o repassa aos
// outros clientes:
//
//	FURITO_TEST_HOST=127.0.0.1:9987 go test ./ts3/ -run LiveRecordingFlag -v
func TestLiveRecordingFlag(t *testing.T) {
	host := os.Getenv("FURITO_TEST_HOST")
	if host == "" {
		t.Skip("defina FURITO_TEST_HOST")
	}
	connect := func(nick string) *Client {
		ident, _ := GenerateIdentity()
		c := NewClient(ident)
		ok := make(chan string, 2)
		c.OnConnected = func() { ok <- "" }
		c.OnDisconnected = func(r string, e bool) { ok <- r }
		c.Connect(ConnectOptions{Host: host, Nickname: nick})
		select {
		case r := <-ok:
			if r != "" {
				t.Fatal(r)
			}
		case <-time.After(12 * time.Second):
			t.Fatal("tempo esgotado")
		}
		t.Cleanup(c.Disconnect)
		return c
	}
	me := connect("FuritoRec")
	other := connect("FuritoOuvinte")
	time.Sleep(500 * time.Millisecond)
	myID := me.Snapshot().SelfID

	seen := func(want bool) bool { // o outro cliente vê o nosso indicador?
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			for _, cl := range other.Snapshot().Clients {
				if cl.ID == myID && cl.IsRecording == want {
					return true
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}
	if !seen(false) {
		t.Fatal("o outro cliente não nos vê no servidor")
	}
	me.SetRecording(true)
	if !seen(true) {
		t.Error("o outro cliente não recebeu o indicador de gravação")
	}
	me.SetRecording(false)
	if !seen(false) {
		t.Error("o indicador de gravação não foi apagado ao parar")
	}
}
