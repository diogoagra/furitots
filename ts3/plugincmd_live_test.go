package ts3

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Comandos de plugin entre dois clientes num servidor de verdade: para o
// canal, para um cliente, com textos grandes (uma oferta de vídeo tem alguns
// milhares de caracteres) e vários seguidos.
func TestLivePluginCommand(t *testing.T) {
	host := os.Getenv("FURITO_TEST_HOST")
	if host == "" {
		t.Skip("defina FURITO_TEST_HOST")
	}
	type peer struct {
		c   *Client
		mu  sync.Mutex
		got []PluginCommand
	}
	connect := func(nick string) *peer {
		ident, _ := GenerateIdentity()
		p := &peer{c: NewClient(ident)}
		done := make(chan string, 2)
		p.c.OnConnected = func() { done <- "" }
		p.c.OnDisconnected = func(r string, e bool) { done <- r }
		p.c.OnPluginCommand = func(cmd PluginCommand) {
			p.mu.Lock()
			p.got = append(p.got, cmd)
			p.mu.Unlock()
		}
		p.c.Connect(ConnectOptions{Host: host, Nickname: nick})
		select {
		case r := <-done:
			if r != "" {
				t.Fatalf("%s: %s", nick, r)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: tempo esgotado ao conectar", nick)
		}
		t.Cleanup(p.c.Disconnect)
		return p
	}
	a, b := connect("PluginA"), connect("PluginB")
	time.Sleep(500 * time.Millisecond)
	bid := b.c.Snapshot().SelfID
	last := func(p *peer) PluginCommand {
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.got) == 0 {
			return PluginCommand{}
		}
		return p.got[len(p.got)-1]
	}
	count := func(p *peer) int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.got) }
	waitCount := func(p *peer, n int) bool {
		for i := 0; i < 50; i++ {
			if count(p) >= n {
				return true
			}
			time.Sleep(60 * time.Millisecond)
		}
		return false
	}

	// para o canal: o outro recebe, nós não recebemos o próprio eco
	if err := a.c.SendPluginCommand("furito.teste", "olá, canal / com | barras e \\ espaços"); err != nil {
		t.Fatalf("enviar ao canal: %v", err)
	}
	if !waitCount(b, 1) {
		t.Fatal("B não recebeu o comando enviado ao canal")
	}
	got := last(b)
	if got.Name != "furito.teste" || got.Data != "olá, canal / com | barras e \\ espaços" || got.FromName != "PluginA" || got.FromID != a.c.Snapshot().SelfID {
		t.Errorf("comando recebido diferente do enviado: %+v", got)
	}
	time.Sleep(300 * time.Millisecond)
	if n := count(a); n != 0 {
		t.Errorf("A recebeu %d comandos; o eco do próprio envio deveria ser descartado", n)
	}

	// para um cliente, com tamanhos crescentes
	for _, size := range []int{1000, 4000, 8000, 16000} {
		data := strings.Repeat("abcdefghi ", size/10)
		before := count(b)
		err := a.c.SendPluginCommand("furito.teste", data, bid)
		ok := err == nil && waitCount(b, before+1) && last(b).Data == data
		t.Logf("comando de %5d caracteres para um cliente: entregue inteiro=%v erro=%v", size, ok, err)
		if size <= 8000 && !ok {
			t.Errorf("comando de %d caracteres deveria chegar inteiro", size)
		}
	}

	// vários seguidos (a sinalização de uma transmissão manda poucos)
	before := count(b)
	var firstErr error
	sent := 0
	for i := 0; i < 12; i++ {
		if err := a.c.SendPluginCommand("furito.teste", fmt.Sprintf("n%d", i), bid); err != nil {
			firstErr = err
			break
		}
		sent++
	}
	waitCount(b, before+sent)
	t.Logf("12 comandos seguidos: %d aceitos, %d entregues, erro=%v", sent, count(b)-before, firstErr)
	if sent < 6 {
		t.Errorf("o servidor recusou cedo demais (%d de 12): %v", sent, firstErr)
	}
}
