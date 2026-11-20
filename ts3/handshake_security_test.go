package ts3

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"furito-ts/crypto"
)

// Testes de segurança do handshake e da aceitação de pacotes: o que chega
// antes de haver uma chave combinada vem de qualquer um (o servidor em que o
// usuário digitou o endereço, ou alguém no caminho da rede), e nada disso pode
// derrubar ou travar o app.

// handshakeSession é uma sessão no ponto em que o servidor responde ao
// clientinitiv: já mandamos o alpha e esperamos o initivexpand.
func handshakeSession(t testing.TB) (*Client, *session) {
	c, s := fuzzSession(t)
	s.cryMu.Lock()
	s.cryptoInit = false
	s.cryMu.Unlock()
	s.alphaTmp = make([]byte, 10)
	c.OnDisconnected = func(string, bool) {}
	return c, s
}

// Protocolo antigo: a chave pública do servidor vem numa estrutura DER. Uma
// estrutura cortada ou um ponto fora da curva não podem derrubar o app.
func TestLegacyHandshakeRejectsBadServerKey(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString
	beta := b64(make([]byte, 10))
	bad := map[string][]byte{
		"um byte só":             {0x30},
		"tamanho sem conteúdo":   {0x30, 0x82},
		"inteiro além do fim":    {0x30, 0x06, 0x02, 0x7f, 0x01},
		"sequência além do fim":  {0x30, 0x7f, 0x03, 0x02, 0x07, 0x80},
		"ponto fora da curva":    {0x30, 0x0c, 0x03, 0x02, 0x07, 0x80, 0x02, 0x01, 0x20, 0x02, 0x01, 0x05, 0x02, 0x01, 0x07},
		"ponto no infinito (0)":  {0x30, 0x0c, 0x03, 0x02, 0x07, 0x80, 0x02, 0x01, 0x20, 0x02, 0x01, 0x00, 0x02, 0x01, 0x00},
		"inteiros de 1 MB":       append([]byte{0x30, 0x84, 0x00, 0x10, 0x00, 0x00}, make([]byte, 64)...),
		"comprimento com 0 byte": {0x30, 0x80, 0x02, 0x80},
	}
	for name, omega := range bad {
		t.Run(name, func(t *testing.T) {
			c, s := handshakeSession(t)
			c.handleCommand(s, "initivexpand alpha=AAAA beta="+beta+" omega="+b64(omega))
			if s.cryptoInit {
				t.Error("uma chave de servidor inválida foi aceita")
			}
		})
	}
}

// O desafio do handshake é uma exponenciação modular com números escolhidos
// pelo servidor. Com o módulo zero ela deixa de ser modular e o resultado tem
// bilhões de dígitos: o app travaria consumindo toda a memória.
func TestHandshakePuzzleRejectsZeroModulus(t *testing.T) {
	for name, mod := range map[string]byte{"módulo zero": 0, "módulo um": 1} {
		t.Run(name, func(t *testing.T) {
			_, s := handshakeSession(t)
			s.alphaTmp = nil // o passo 3 só é tratado antes do clientinitiv
			pkt := make([]byte, 233)
			pkt[0] = 3
			pkt[64] = 7                                    // x = 7
			pkt[128] = mod                                 // n
			binary.BigEndian.PutUint32(pkt[129:133], 4000) // 7^(2^4000): impossível sem módulo
			done := make(chan struct{})
			go func() { s.handleInit(pkt); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("o desafio com módulo inválido travou o cliente")
			}
			if s.alphaTmp != nil {
				t.Error("o cliente respondeu a um desafio com módulo inválido")
			}
		})
	}
}

// serverPacket monta um pacote do servidor para o cliente.
func serverPacket(mac []byte, id uint16, typeAndFlags byte, payload []byte) []byte {
	raw := make([]byte, serverHeaderLen+len(payload))
	copy(raw, mac)
	binary.BigEndian.PutUint16(raw[crypto.MacLen:], id)
	raw[crypto.MacLen+2] = typeAndFlags
	copy(raw[serverHeaderLen:], payload)
	return raw
}

// dummyPacket cifra um pacote com a chave fixa do protocolo, que é pública:
// qualquer um consegue montar um destes.
func dummyPacket(id uint16, pType byte, payload []byte) []byte {
	header := make([]byte, serverHeaderLen-crypto.MacLen)
	binary.BigEndian.PutUint16(header, id)
	header[2] = pType
	ct, mac := crypto.EaxEncrypt(crypto.DummyKey, crypto.DummyNonce, header, payload, crypto.MacLen)
	return serverPacket(mac, id, pType, ct)
}

// Depois que a chave da sessão foi combinada, um pacote cifrado com a chave
// fixa (pública) ou sem cifra nenhuma não pode ser aceito como comando do
// servidor: senão qualquer um que consiga mandar pacotes em nome do servidor
// injetaria mensagens, cutucadas e mudanças de estado na sessão.
func TestForgedCommandsAreRejectedAfterHandshake(t *testing.T) {
	c, s := fuzzSession(t)
	s.installCrypto(make([]byte, 64))
	var got []string
	c.OnTextMessage = func(m TextMessage) { got = append(got, m.Text) }
	forged := []byte("notifytextmessage targetmode=3 msg=FORJADA invokerid=5 invokername=Ana invokeruid=uidAna")

	for _, pType := range []byte{ptCommand, ptCommandLow} {
		next := s.inNext[pType]
		s.handlePacket(dummyPacket(next, pType, forged))
		s.handlePacket(serverPacket(s.fakeSig, next, pType|flagUnencrypted, forged))
		s.handlePacket(serverPacket(make([]byte, crypto.MacLen), next, pType|flagUnencrypted, forged))
		if s.inNext[pType] != next {
			t.Errorf("tipo %d: um pacote forjado avançou a sequência de comandos", pType)
		}
	}
	if len(got) != 0 {
		t.Fatalf("comando forjado foi aceito: %q", got)
	}

	// o mesmo comando, cifrado com a chave da sessão, continua valendo
	next := s.inNext[ptCommand]
	header := make([]byte, serverHeaderLen-crypto.MacLen)
	binary.BigEndian.PutUint16(header, next)
	header[2] = ptCommand
	key, nonce := crypto.DeriveKeyNonce(true, next, 0, ptCommand, s.ivStruct)
	ct, mac := crypto.EaxEncrypt(key, nonce, header, forged, crypto.MacLen)
	s.handlePacket(serverPacket(mac, next, ptCommand, ct))
	if len(got) != 1 {
		t.Fatalf("um comando legítimo deveria ser aceito; recebidos: %q", got)
	}
}

// A única coisa que ainda chega com a chave fixa depois do handshake é a
// confirmação do clientek (o nosso comando 1), que o servidor manda antes de
// trocar de chave: ela vale, e nenhuma outra confirmação.
func TestOnlyClientEkAckMayUseDummyKeyAfterHandshake(t *testing.T) {
	_, s := fuzzSession(t)
	s.installCrypto(make([]byte, 64))
	pend := func(id uint16) uint32 { return uint32(ptCommand)<<16 | uint32(id) }
	s.ackMu.Lock()
	s.unacked[pend(1)] = &pendingPacket{}
	s.unacked[pend(7)] = &pendingPacket{}
	s.ackMu.Unlock()

	ack := func(id uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, id); return b }
	s.handlePacket(dummyPacket(0, ptAck, ack(7)))
	s.handlePacket(dummyPacket(1, ptAck, ack(1)))
	s.ackMu.Lock()
	_, ek := s.unacked[pend(1)]
	_, other := s.unacked[pend(7)]
	s.ackMu.Unlock()
	if ek {
		t.Error("a confirmação do clientek com a chave fixa deveria valer")
	}
	if !other {
		t.Error("uma confirmação forjada com a chave fixa retirou um comando da fila de reenvio")
	}
}

// Voz sem cifra só vale com a assinatura combinada na sessão.
func TestUnencryptedVoiceNeedsSessionSignature(t *testing.T) {
	c, s := fuzzSession(t)
	s.installCrypto(make([]byte, 64))
	voices := 0
	c.OnVoice = func(uint16, uint16, byte, []byte) { voices++ }
	voice := []byte{0, 1, 0, 5, 4, 0xfc, 0xff, 0xfe}
	s.handlePacket(serverPacket(make([]byte, crypto.MacLen), 0, ptVoice|flagUnencrypted, voice))
	if voices != 0 {
		t.Fatal("voz sem a assinatura da sessão foi aceita")
	}
	s.handlePacket(serverPacket(s.fakeSig, 0, ptVoice|flagUnencrypted, voice))
	if voices != 1 {
		t.Fatal("voz com a assinatura da sessão deveria ser aceita")
	}
}

// O servidor TS6 confirma os comandos em pacotes sem cifra: valem com a
// assinatura da sessão, e só com ela.
func TestUnencryptedAckNeedsSessionSignature(t *testing.T) {
	_, s := fuzzSession(t)
	s.installCrypto(make([]byte, 64))
	key := uint32(ptCommand)<<16 | 7
	s.ackMu.Lock()
	s.unacked[key] = &pendingPacket{}
	s.ackMu.Unlock()
	pending := func() bool {
		s.ackMu.Lock()
		defer s.ackMu.Unlock()
		_, ok := s.unacked[key]
		return ok
	}
	s.handlePacket(serverPacket(make([]byte, crypto.MacLen), 0, ptAck|flagUnencrypted, []byte{0, 7}))
	if !pending() {
		t.Fatal("confirmação sem a assinatura da sessão foi aceita")
	}
	s.handlePacket(serverPacket(s.fakeSig, 0, ptAck|flagUnencrypted, []byte{0, 7}))
	if pending() {
		t.Fatal("confirmação com a assinatura da sessão deveria valer")
	}
}

// Com a sessão estabelecida, os pacotes de início de handshake (que não têm
// autenticação nenhuma) são ignorados: senão um único pacote de qualquer um
// refaria o handshake ou derrubaria a conexão.
func TestInitPacketsIgnoredAfterHandshake(t *testing.T) {
	c, s := fuzzSession(t)
	s.connected.Store(true)
	ended := ""
	c.OnDisconnected = func(reason string, _ bool) { ended = reason }
	step3 := func(n byte, level uint32) []byte {
		p := make([]byte, 233)
		p[0], p[64], p[128] = 3, 7, n // x = 7, módulo n
		binary.BigEndian.PutUint32(p[129:133], level)
		return serverPacket(crypto.InitMac, 0, ptInit|flagUnencrypted, p)
	}
	s.handlePacket(step3(0xfb, 10))
	if s.alphaTmp != nil {
		t.Error("passo 3 tratado depois do handshake: o desafio foi refeito")
	}
	s.sendMu.Lock()
	s.lastInit = nil
	s.sendMu.Unlock()
	s.handlePacket(serverPacket(crypto.InitMac, 0, ptInit|flagUnencrypted, []byte{127}))
	s.sendMu.Lock()
	restarted := s.lastInit != nil
	s.sendMu.Unlock()
	if restarted {
		t.Error("passo 127 tratado depois do handshake: o cliente recomeçou o handshake no meio da sessão")
	}
	s.handlePacket(step3(0, 10))
	s.handlePacket(step3(0xfb, 2_000_000))
	if ended != "" || s.ended() {
		t.Errorf("um pacote de handshake sem autenticação encerrou a sessão: %q", ended)
	}
}

// Durante o handshake, um desafio inválido é ignorado: não encerra a conexão.
func TestInvalidPuzzleDoesNotEndHandshake(t *testing.T) {
	c, s := handshakeSession(t)
	s.alphaTmp = nil
	ended := false
	c.OnDisconnected = func(string, bool) { ended = true }
	p := make([]byte, 233)
	p[0], p[64] = 3, 7 // módulo zero
	s.handleInit(p)
	if ended || s.ended() || s.alphaTmp != nil {
		t.Error("um desafio inválido deveria ser só ignorado")
	}
}

// Antes da troca de chaves tudo vem com a chave fixa (pública): nessa fase só
// vale a resposta do handshake. Um "initserver" forjado não abre sessão, e não
// há voz.
func TestNoSessionBeforeKeyExchange(t *testing.T) {
	c, s := handshakeSession(t)
	connected := false
	c.OnConnected = func() { connected = true }
	voices := 0
	c.OnVoice = func(uint16, uint16, byte, []byte) { voices++ }
	s.handlePacket(dummyPacket(0, ptCommand, []byte("initserver aclid=5 virtualserver_name=Falso")))
	s.handlePacket(dummyPacket(0, ptVoice, []byte{0, 0, 0, 9, 5, 1, 2}))
	s.handlePacket(serverPacket(make([]byte, crypto.MacLen), 0, ptVoice|flagUnencrypted, []byte{0, 0, 0, 9, 5, 1, 2}))
	if connected || s.connected.Load() {
		t.Error("um initserver com a chave fixa abriu a sessão sem troca de chaves")
	}
	if voices != 0 {
		t.Error("voz aceita antes da troca de chaves")
	}
}

// O initivexpand2 repetido depois do handshake (a nossa confirmação se perdeu)
// é confirmado de novo, sem ser tratado outra vez.
func TestRepeatedInitIvExpandIsAckedNotProcessed(t *testing.T) {
	_, s := fuzzSession(t)
	next := s.inNext[ptCommand]
	acks := s.outID[ptAck]
	s.handlePacket(dummyPacket(0, ptCommand, []byte("initivexpand2 l=AQ== beta=AAAA omega=AAAA ot=1 proof=AAAA tvd= root= time=1")))
	if s.outID[ptAck] != acks+1 {
		t.Error("o initivexpand2 repetido deveria ser confirmado de novo")
	}
	if s.inNext[ptCommand] != next {
		t.Error("o initivexpand2 repetido não deveria avançar a sequência de comandos")
	}
}

// Uma linha de comando pequena não pode fazer o analisador alocar memória sem
// proporção: cada item herda do primeiro as chaves que não traz.
func TestParseCommandMemoryIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("x")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, " %x", i)
	}
	b.WriteString(strings.Repeat("|", 8000))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, items := ParseCommand(b.String())
	runtime.ReadMemStats(&after)
	if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 64 {
		t.Errorf("uma linha de %d bytes alocou %d MB", b.Len(), mb)
	}
	if len(items) == 0 || len(items[0]) != 2000 {
		t.Errorf("o primeiro item deveria ter sido lido inteiro: %d itens", len(items))
	}
	// e uma linha só de separadores não vira milhões de itens
	if _, items := ParseCommand("x a=1" + strings.Repeat("|", 1<<20)); len(items) > maxCommandItems {
		t.Errorf("%d itens aceitos num comando", len(items))
	}
	// listas normais continuam herdando as chaves do primeiro item
	_, items = ParseCommand("notifyclientmoved ctid=5 reasonid=0 clid=1|clid=2|clid=3")
	if len(items) != 3 || items[2]["ctid"] != "5" || items[2]["clid"] != "3" {
		t.Errorf("herança de chaves quebrada: %v", items)
	}
}

// A transferência de arquivos acontece no endereço, na porta e com a chave que
// o servidor indica. Um servidor da internet não pode mandar o cliente
// conectar dentro da rede do usuário, nem escrever o que quiser na conexão.
func TestTransferTarget(t *testing.T) {
	public := &net.UDPAddr{IP: net.ParseIP("203.0.113.10"), Port: 9987}
	lan := &net.UDPAddr{IP: net.ParseIP("192.168.0.20"), Port: 9987}
	local := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9987}
	key := "abcdefghijklmnopqrstuvwxyz012345"
	cases := []struct {
		name   string
		voice  net.Addr
		res    ftResult
		target string // "" = recusado
	}{
		{"sem endereço: o do servidor de voz", public, ftResult{key: key, port: 30033}, "203.0.113.10:30033"},
		{"0.0.0.0: o do servidor de voz", public, ftResult{key: key, port: 30033, ip: "0.0.0.0"}, "203.0.113.10:30033"},
		{"lista de endereços: o do servidor de voz", public, ftResult{key: key, port: 30033, ip: "0.0.0.0,::"}, "203.0.113.10:30033"},
		{"outro endereço público", public, ftResult{key: key, port: 30033, ip: "198.51.100.7"}, "198.51.100.7:30033"},
		{"servidor público apontando para o próprio computador", public, ftResult{key: key, port: 80, ip: "127.0.0.1"}, ""},
		{"idem, IPv6", public, ftResult{key: key, port: 80, ip: "::1"}, ""},
		{"idem, IPv4 dentro de IPv6", public, ftResult{key: key, port: 80, ip: "::ffff:127.0.0.1"}, ""},
		{"servidor público apontando para o roteador", public, ftResult{key: key, port: 80, ip: "192.168.0.1"}, ""},
		{"idem, 10.x", public, ftResult{key: key, port: 80, ip: "10.0.0.1"}, ""},
		{"idem, link-local (metadados de nuvem)", public, ftResult{key: key, port: 80, ip: "169.254.169.254"}, ""},
		{"idem, faixa da operadora", public, ftResult{key: key, port: 80, ip: "100.64.0.1"}, ""},
		{"idem, IPv6 local", public, ftResult{key: key, port: 80, ip: "fd00::1"}, ""},
		{"servidor da rede local apontando para outro da rede", lan, ftResult{key: key, port: 30033, ip: "192.168.0.30"}, "192.168.0.30:30033"},
		{"servidor da rede local apontando para o próprio computador", lan, ftResult{key: key, port: 80, ip: "127.0.0.1"}, ""},
		{"servidor local de teste", local, ftResult{key: key, port: 30033, ip: "127.0.0.1"}, "127.0.0.1:30033"},
		{"porta zero", public, ftResult{key: key, port: 0}, ""},
		{"porta negativa", public, ftResult{key: key, port: -1}, ""},
		{"porta acima de 65535", public, ftResult{key: key, port: 70000}, ""},
		{"chave vazia", public, ftResult{key: "", port: 30033}, ""},
		{"chave com um pedido HTTP", public, ftResult{key: "POST /admin HTTP/1.0\r\n\r\n", port: 30033}, ""},
		{"chave com quebra de linha", public, ftResult{key: "abc\ndef", port: 30033}, ""},
		{"chave enorme", public, ftResult{key: strings.Repeat("a", 4096), port: 30033}, ""},
	}
	for _, tc := range cases {
		got, err := transferTarget(tc.voice, tc.res)
		if (err != nil) != (tc.target == "") || got != tc.target {
			t.Errorf("%s: destino %q, erro %v (esperado %q)", tc.name, got, err, tc.target)
		}
	}
}

// Um servidor com centenas de milhares de canais aninhados não estoura a
// pilha ao montar a árvore: o cliente guarda um número limitado de canais.
func TestChannelCountIsBounded(t *testing.T) {
	c, s := fuzzSession(t)
	var b strings.Builder
	for start := 10; start < maxChannels+5000; start += 500 {
		b.Reset()
		b.WriteString("channellist")
		for id := start; id < start+500; id++ {
			if id > start {
				b.WriteByte('|')
			} else {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "cid=%d cpid=%d channel_name=c", id, id-1)
		}
		c.handleCommand(s, b.String())
	}
	if n := len(c.Snapshot().Channels); n > maxChannels {
		t.Errorf("%d canais guardados; o limite é %d", n, maxChannels)
	}
}

// Um erro de programação ao tratar um pacote encerra a sessão; não derruba o
// app nem deixa a trava do cliente presa.
func TestPanicWhileHandlingPacketEndsSessionOnly(t *testing.T) {
	c, s := fuzzSession(t)
	reason := ""
	c.OnDisconnected = func(r string, _ bool) { reason = r }
	c.OnTextMessage = func(TextMessage) { panic("falha de teste") }
	msg := []byte("notifytextmessage targetmode=3 msg=oi invokerid=5 invokername=Ana invokeruid=uidAna")
	id := s.inNext[ptCommand]
	header := []byte{byte(id >> 8), byte(id), ptCommand}
	key, nonce := crypto.DeriveKeyNonce(true, id, 0, ptCommand, s.ivStruct)
	ct, mac := crypto.EaxEncrypt(key, nonce, header, msg, crypto.MacLen)
	if s.handleSafely(serverPacket(mac, id, ptCommand, ct)) {
		t.Fatal("a pane deveria ter sido percebida")
	}
	if !s.ended() || !strings.Contains(reason, "erro interno") {
		t.Errorf("a sessão deveria ter sido encerrada com aviso: %q", reason)
	}
	done := make(chan struct{})
	go func() { c.Snapshot(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a trava do cliente ficou presa depois da pane")
	}
}
