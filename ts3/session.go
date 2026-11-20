package ts3

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"furito-ts/crypto"
)

// Tipos de pacote e flags do protocolo de transporte.
const (
	ptVoice      = 0
	ptWhisper    = 1
	ptCommand    = 2
	ptCommandLow = 3
	ptPing       = 4
	ptPong       = 5
	ptAck        = 6
	ptAckLow     = 7
	ptInit       = 8

	flagUnencrypted = 0x80
	flagCompressed  = 0x40
	flagNewProtocol = 0x20
	flagFragmented  = 0x10

	clientHeaderLen = crypto.MacLen + 5 // mac + id + clid + tipo
	serverHeaderLen = crypto.MacLen + 3 // mac + id + tipo
	maxPayload      = 500 - clientHeaderLen

	handshakeTimeout = 10 * time.Second
	readTimeout      = 15 * time.Second
	resendInterval   = 800 * time.Millisecond
	resendGiveUp     = 20 * time.Second
	maxFragmentSize  = 4 << 20

	// clientEkID é o número do comando clientek, o último que mandamos com a
	// chave fixa (o clientinitiv, dentro do pacote de init, conta como 0).
	clientEkID = 1
)

type pendingPacket struct {
	raw   []byte
	first time.Time
	last  time.Time
}

type inPacket struct {
	flags byte
	data  []byte
}

// session é uma conexão com um servidor. Cada Connect cria uma sessão nova;
// as goroutines de uma sessão nunca tocam no estado de outra.
type session struct {
	c    *Client
	conn *net.UDPConn
	opts ConnectOptions

	done    chan struct{}
	endOnce sync.Once

	cmdMu    sync.Mutex // um comando (com todos os fragmentos) por vez
	sendMu   sync.Mutex // contadores de saída + escrita no socket
	outID    [9]uint16
	outGen   [9]uint32
	lastInit []byte

	cryMu      sync.RWMutex
	cryptoInit bool
	ivStruct   []byte
	fakeSig    []byte

	clientID    atomic.Uint32
	connected   atomic.Bool
	connectedAt time.Time // definido no initserver (goroutine de leitura)

	ackMu   sync.Mutex
	unacked map[uint32]*pendingPacket

	// Usados apenas pela goroutine de leitura:
	alphaTmp       []byte
	inNext         [9]uint16
	inGen          [9]uint32
	pending        [2]map[uint16]inPacket
	fragBuf        [2][]byte
	fragOn         [2]bool
	fragCompressed [2]bool
}

func newSession(c *Client, conn *net.UDPConn, opts ConnectOptions) *session {
	s := &session{
		c:       c,
		conn:    conn,
		opts:    opts,
		done:    make(chan struct{}),
		fakeSig: make([]byte, crypto.MacLen),
		unacked: make(map[uint32]*pendingPacket),
	}
	s.pending[0] = make(map[uint16]inPacket)
	s.pending[1] = make(map[uint16]inPacket)
	// O clientinitiv viaja dentro do pacote de init e conta como comando 0.
	s.outID[ptCommand] = 1
	return s
}

func (s *session) ended() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------- envio

func (s *session) sendInit(data []byte) {
	raw := make([]byte, clientHeaderLen+len(data))
	copy(raw, crypto.InitMac)
	binary.BigEndian.PutUint16(raw[crypto.MacLen:], 101)
	raw[crypto.MacLen+4] = flagUnencrypted | ptInit
	copy(raw[clientHeaderLen:], data)

	s.sendMu.Lock()
	s.lastInit = raw
	s.conn.Write(raw)
	s.sendMu.Unlock()
}

func (s *session) sendInit0() {
	init0 := make([]byte, 21)
	binary.BigEndian.PutUint32(init0[0:4], crypto.InitVersion)
	init0[4] = 0x00
	binary.BigEndian.PutUint32(init0[5:9], uint32(time.Now().Unix()))
	rand.Read(init0[9:13])
	s.sendInit(init0)
}

func (s *session) resendInit() {
	s.sendMu.Lock()
	if s.lastInit != nil {
		s.conn.Write(s.lastInit)
	}
	s.sendMu.Unlock()
}

// sendPacket monta, cifra e envia um pacote. Devolve o id usado.
func (s *session) sendPacket(pType byte, flags byte, data []byte) uint16 {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	id := s.outID[pType]
	gen := s.outGen[pType]
	s.outID[pType]++
	if s.outID[pType] == 0 {
		s.outGen[pType]++
	}

	switch pType {
	case ptCommand, ptCommandLow:
		flags |= flagNewProtocol
	case ptPing, ptPong:
		flags |= flagUnencrypted
	case ptVoice, ptWhisper:
		binary.BigEndian.PutUint16(data[0:2], id)
	}

	raw := make([]byte, clientHeaderLen+len(data))
	binary.BigEndian.PutUint16(raw[crypto.MacLen:], id)
	binary.BigEndian.PutUint16(raw[crypto.MacLen+2:], uint16(s.clientID.Load()))
	raw[crypto.MacLen+4] = (flags & 0xF0) | (pType & 0x0F)

	s.cryMu.RLock()
	if flags&flagUnencrypted != 0 {
		copy(raw[:crypto.MacLen], s.fakeSig)
		copy(raw[clientHeaderLen:], data)
	} else {
		key, nonce := crypto.DummyKey, crypto.DummyNonce
		if s.cryptoInit {
			key, nonce = crypto.DeriveKeyNonce(false, id, gen, pType, s.ivStruct)
		}
		ciphertext, mac := crypto.EaxEncrypt(key, nonce, raw[crypto.MacLen:clientHeaderLen], data, crypto.MacLen)
		copy(raw[:crypto.MacLen], mac)
		copy(raw[clientHeaderLen:], ciphertext)
	}
	s.cryMu.RUnlock()

	if pType == ptCommand || pType == ptCommandLow {
		now := time.Now()
		s.ackMu.Lock()
		s.unacked[uint32(pType)<<16|uint32(id)] = &pendingPacket{raw: raw, first: now, last: now}
		s.ackMu.Unlock()
	}

	s.conn.Write(raw)
	return id
}

// sendCommand envia um comando, fragmentando se passar do tamanho máximo de
// pacote. Devolve o id do último pacote.
func (s *session) sendCommand(cmd string) uint16 {
	s.c.debugOut(cmd)
	data := []byte(cmd)

	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	if len(data) <= maxPayload {
		return s.sendPacket(ptCommand, 0, data)
	}
	var id uint16
	for off := 0; off < len(data); off += maxPayload {
		end := off + maxPayload
		if end > len(data) {
			end = len(data)
		}
		var flags byte
		if off == 0 || end == len(data) {
			flags = flagFragmented // só o primeiro e o último fragmento levam a flag
		}
		id = s.sendPacket(ptCommand, flags, data[off:end])
	}
	return id
}

func (s *session) waitAck(pType byte, id uint16, timeout time.Duration) bool {
	key := uint32(pType)<<16 | uint32(id)
	deadline := time.Now().Add(timeout)
	for {
		s.ackMu.Lock()
		_, waiting := s.unacked[key]
		s.ackMu.Unlock()
		if !waiting {
			return true
		}
		if time.Now().After(deadline) || s.ended() {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---------------------------------------------------------------- manutenção

// maintain cuida de: reenvio do init, timeout do handshake, pings e reenvio
// de comandos não confirmados. Termina junto com a sessão.
func (s *session) maintain() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	start := time.Now()
	lastInit := start
	lastPing := start

	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			if !s.connected.Load() {
				if now.Sub(start) > handshakeTimeout {
					s.c.endSession(s, "tempo esgotado: o servidor não respondeu", true)
					return
				}
				s.cryMu.RLock()
				inHandshake := !s.cryptoInit
				s.cryMu.RUnlock()
				if inHandshake && now.Sub(lastInit) >= time.Second {
					s.resendInit()
					lastInit = now
				}
			} else if now.Sub(lastPing) >= time.Second {
				s.sendPacket(ptPing, flagUnencrypted, nil)
				lastPing = now
			}

			var resend [][]byte
			lost := false
			s.ackMu.Lock()
			for _, p := range s.unacked {
				if now.Sub(p.first) > resendGiveUp {
					lost = true
					break
				}
				if now.Sub(p.last) > resendInterval {
					p.last = now
					resend = append(resend, p.raw)
				}
			}
			s.ackMu.Unlock()
			if lost {
				s.c.endSession(s, "conexão perdida: o servidor parou de confirmar os comandos", true)
				return
			}
			if len(resend) > 0 {
				s.c.debugf("reenviando %d comando(s) sem confirmação", len(resend))
				s.sendMu.Lock()
				for _, raw := range resend {
					s.conn.Write(raw)
				}
				s.sendMu.Unlock()
			}
		}
	}
}

// ---------------------------------------------------------------- recepção

func (s *session) readLoop() {
	buf := make([]byte, 4096)
	for {
		s.conn.SetReadDeadline(time.Now().Add(readTimeout))
		n, err := s.conn.Read(buf)
		if err != nil {
			if s.ended() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				s.c.endSession(s, "conexão perdida: o servidor parou de responder", true)
			} else if errors.Is(err, syscall.ECONNREFUSED) && !s.connected.Load() {
				s.c.endSession(s, "não há servidor TeamSpeak respondendo neste endereço e porta", true)
			} else {
				s.c.endSession(s, "conexão perdida: "+err.Error(), true)
			}
			return
		}
		if !s.handleSafely(buf[:n]) || s.ended() {
			return
		}
	}
}

// handleSafely trata um pacote recebido. Um erro de programação no caminho
// (os bytes vêm da rede) encerra esta sessão com uma mensagem, em vez de
// derrubar o app inteiro com a conversa, a gravação e os ajustes abertos.
func (s *session) handleSafely(raw []byte) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("erro interno ao tratar um pacote do servidor: %v\n%s", r, debug.Stack())
			s.c.endSession(s, "erro interno ao tratar os dados do servidor (o detalhe está no registro do app)", true)
			ok = false
		}
	}()
	s.handlePacket(raw)
	return true
}

// genFor estima a geração (contador de voltas do id de 16 bits) de um pacote.
func (s *session) genFor(pType byte, id uint16) uint32 {
	next, gen := s.inNext[pType], s.inGen[pType]
	if id < next && next-id > 0x8000 {
		return gen + 1 // o id já deu a volta
	}
	if id > next && id-next > 0x8000 && gen > 0 {
		return gen - 1 // pacote atrasado, de antes da volta
	}
	return gen
}

// track avança a janela de recepção de tipos sem ordenação (voz, ping).
func (s *session) track(pType byte, id uint16) {
	full := uint64(s.genFor(pType, id))<<16 | uint64(id)
	cur := uint64(s.inGen[pType])<<16 | uint64(s.inNext[pType])
	if full >= cur {
		cur = full + 1
		s.inGen[pType] = uint32(cur >> 16)
		s.inNext[pType] = uint16(cur)
	}
}

func (s *session) handlePacket(raw []byte) {
	if len(raw) < serverHeaderLen {
		return
	}
	mac := raw[:crypto.MacLen]
	id := binary.BigEndian.Uint16(raw[crypto.MacLen:])
	pType := raw[crypto.MacLen+2] & 0x0F
	flags := raw[crypto.MacLen+2] & 0xF0
	payload := raw[serverHeaderLen:]
	if pType > ptInit {
		return
	}

	var data []byte
	switch {
	case pType == ptInit:
		// Os pacotes de início de handshake não têm autenticação nenhuma (o
		// "MAC" deles é um texto fixo): só valem enquanto o handshake acontece.
		// Com a sessão estabelecida, tratá-los deixaria qualquer um refazer o
		// handshake — ou encerrar a sessão — com um único pacote.
		if s.cryptoInit || !bytes.Equal(mac, crypto.InitMac) {
			return
		}
		s.handleInit(payload)
		return
	case flags&flagUnencrypted != 0:
		// Sem cifra viajam a voz, os pings e as confirmações (o servidor TS6
		// confirma os comandos assim). Comandos, nunca: a "assinatura" dos
		// pacotes sem cifra é um valor fixo da sessão, que passa às claras em
		// cada um deles — quem escuta a rede a conhece, e com ela forjaria
		// comandos do servidor (mensagens, cutucadas, mudanças de estado).
		switch pType {
		case ptCommand, ptCommandLow:
			s.c.debugf("pacote recusado: comando (tipo %d) sem cifra", pType)
			return
		}
		if s.cryptoInit && !bytes.Equal(mac, s.fakeSig) {
			s.c.debugf("pacote recusado: tipo %d sem cifra e sem a assinatura da sessão", pType)
			return
		}
		data = payload
	default:
		header := raw[crypto.MacLen:serverHeaderLen]
		ok := false
		if s.cryptoInit {
			key, nonce := crypto.DeriveKeyNonce(true, id, s.genFor(pType, id), pType, s.ivStruct)
			data, ok = crypto.EaxDecrypt(key, nonce, header, payload, mac, crypto.MacLen)
		}
		// A chave fixa do protocolo é pública: qualquer um cifra com ela. Ela
		// só vale enquanto não há chave de sessão (o handshake) e, depois, para
		// uma única coisa: a confirmação do clientek, que o servidor manda antes
		// de trocar de chave. Aceitar qualquer outro pacote com ela deixaria
		// quem consegue mandar pacotes em nome do servidor injetar comandos.
		if !ok && (!s.cryptoInit || pType == ptAck) {
			data, ok = crypto.EaxDecrypt(crypto.DummyKey, crypto.DummyNonce, header, payload, mac, crypto.MacLen)
			if ok && s.cryptoInit && (len(data) < 2 || binary.BigEndian.Uint16(data) > clientEkID) {
				s.c.debugf("pacote recusado: confirmação com a chave fixa depois do handshake")
				return
			}
		}
		// O initivexpand2 (comando 0, com a chave fixa) repetido depois do
		// handshake: o servidor não recebeu a nossa confirmação. Confirma-se de
		// novo, sem tratar o comando outra vez.
		if !ok && s.cryptoInit && pType == ptCommand && id == 0 {
			if _, again := crypto.EaxDecrypt(crypto.DummyKey, crypto.DummyNonce, header, payload, mac, crypto.MacLen); again {
				s.sendPacket(ptAck, 0, []byte{0, 0})
			}
			return
		}
		if !ok {
			if s.cryptoInit {
				s.c.debugf("pacote recusado: tipo %d id %d não confere com a chave da sessão", pType, id)
			}
			return
		}
	}

	switch pType {
	case ptCommand, ptCommandLow:
		s.handleCommandPacket(pType, id, flags, data)
	case ptAck, ptAckLow:
		if len(data) >= 2 {
			acked := binary.BigEndian.Uint16(data)
			t := uint32(ptCommand)
			if pType == ptAckLow {
				t = ptCommandLow
			}
			s.ackMu.Lock()
			delete(s.unacked, t<<16|uint32(acked))
			s.ackMu.Unlock()
		}
	case ptPing:
		s.track(pType, id)
		pong := make([]byte, 2)
		binary.BigEndian.PutUint16(pong, id)
		s.sendPacket(ptPong, flagUnencrypted, pong)
	case ptPong:
		s.track(pType, id)
	case ptVoice, ptWhisper:
		if !s.cryptoInit {
			return // não há voz antes de a sessão existir
		}
		s.track(pType, id)
		// [id voz(2)] [clid origem(2)] [codec(1)] [opus...]; opus vazio = fim da fala
		if len(data) < 5 || s.c.OnVoice == nil {
			return
		}
		seq := binary.BigEndian.Uint16(data[0:2])
		from := binary.BigEndian.Uint16(data[2:4])
		opus := make([]byte, len(data)-5)
		copy(opus, data[5:])
		s.c.OnVoice(from, seq, data[4], opus)
	}
}

func (s *session) handleCommandPacket(pType byte, id uint16, flags byte, data []byte) {
	idx := int(pType - ptCommand)

	// Sempre confirma, inclusive duplicados (o ack anterior pode ter se perdido).
	ack := make([]byte, 2)
	binary.BigEndian.PutUint16(ack, id)
	s.sendPacket(pType+4, flags&flagUnencrypted, ack)

	next := s.inNext[pType]
	switch {
	case id == next:
		s.processCommandPacket(idx, flags, data)
		s.bump(pType)
		for !s.ended() {
			p, ok := s.pending[idx][s.inNext[pType]]
			if !ok {
				break
			}
			delete(s.pending[idx], s.inNext[pType])
			s.processCommandPacket(idx, p.flags, p.data)
			s.bump(pType)
		}
	case id-next < 0x8000:
		// Chegou fora de ordem: guarda até os anteriores chegarem.
		if len(s.pending[idx]) < 1024 {
			cp := make([]byte, len(data))
			copy(cp, data)
			s.pending[idx][id] = inPacket{flags: flags, data: cp}
		}
	default:
		// Duplicado de um pacote já processado.
	}
}

func (s *session) bump(pType byte) {
	s.inNext[pType]++
	if s.inNext[pType] == 0 {
		s.inGen[pType]++
	}
}

func (s *session) processCommandPacket(idx int, flags byte, data []byte) {
	fragmented := flags&flagFragmented != 0

	if !s.fragOn[idx] {
		if !fragmented {
			s.deliver(data, flags&flagCompressed != 0)
			return
		}
		s.fragOn[idx] = true
		s.fragCompressed[idx] = flags&flagCompressed != 0
		s.fragBuf[idx] = append(s.fragBuf[idx][:0], data...)
		return
	}

	s.fragBuf[idx] = append(s.fragBuf[idx], data...)
	if len(s.fragBuf[idx]) > maxFragmentSize {
		s.fragOn[idx] = false
		s.fragBuf[idx] = nil
		return
	}
	if fragmented { // último fragmento
		full := s.fragBuf[idx]
		compressed := s.fragCompressed[idx]
		s.fragOn[idx] = false
		s.fragBuf[idx] = nil
		s.deliver(full, compressed)
	}
}

func (s *session) deliver(payload []byte, compressed bool) {
	if compressed {
		out, err := crypto.QlzDecompress(payload)
		if err != nil {
			s.c.debugf("descompressão QuickLZ falhou (%d bytes): %v", len(payload), err)
			return
		}
		payload = out
	}
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.Trim(line, wireSpace)
		if line != "" && !s.ended() {
			s.c.handleCommand(s, line)
		}
	}
}

// ---------------------------------------------------------------- handshake

func (s *session) handleInit(data []byte) {
	if len(data) < 1 {
		return
	}
	switch data[0] {
	case 1:
		if len(data) < 21 {
			return
		}
		init2 := make([]byte, 25)
		binary.BigEndian.PutUint32(init2[0:4], crypto.InitVersion)
		init2[4] = 0x02
		copy(init2[5:25], data[1:21])
		s.sendInit(init2)

	case 3:
		if len(data) < 233 {
			return
		}
		if s.alphaTmp != nil {
			// O servidor repetiu o passo 3: nosso passo 4 se perdeu.
			s.resendInit()
			return
		}
		xBytes := data[1:65]
		nBytes := data[65:129]
		level := binary.BigEndian.Uint32(data[129:133])
		serverData := data[133:233]
		// Desafio RSA: y = x^(2^level) mod n
		x := new(big.Int).SetBytes(xBytes)
		n := new(big.Int).SetBytes(nBytes)
		// Com o módulo zero a conta deixaria de ser modular: o resultado teria
		// bilhões de dígitos e o app travaria consumindo toda a memória.
		// O pacote não tem autenticação: um desafio inválido é ignorado (se o
		// servidor de verdade não mandar um válido, o handshake expira), em vez
		// de encerrar a conexão a pedido de qualquer um.
		if level > 1_000_000 || n.Cmp(big.NewInt(1)) <= 0 {
			s.c.debugf("desafio de handshake inválido ignorado (nível %d)", level)
			return
		}
		exp := new(big.Int).Lsh(big.NewInt(1), uint(level))
		yBytes := new(big.Int).Exp(x, exp, n).Bytes()
		y := make([]byte, 64)
		if len(yBytes) <= 64 {
			copy(y[64-len(yBytes):], yBytes)
		}

		s.alphaTmp = make([]byte, 10)
		rand.Read(s.alphaTmp)
		cmd := fmt.Sprintf("clientinitiv alpha=%s omega=%s ot=1 ip=",
			base64.StdEncoding.EncodeToString(s.alphaTmp), s.c.identity.OmegaStr)

		init4 := make([]byte, 0, 4+1+64+64+4+100+64+len(cmd))
		init4 = binary.BigEndian.AppendUint32(init4, crypto.InitVersion)
		init4 = append(init4, 0x04)
		init4 = append(init4, xBytes...)
		init4 = append(init4, nBytes...)
		init4 = binary.BigEndian.AppendUint32(init4, level)
		init4 = append(init4, serverData...)
		init4 = append(init4, y...)
		init4 = append(init4, cmd...)
		s.sendInit(init4)

	case 127:
		// O servidor pediu para recomeçar o handshake.
		s.alphaTmp = nil
		s.sendInit0()
	}
}

func (s *session) installCrypto(iv []byte) {
	sig := crypto.Sha1(iv)
	s.cryMu.Lock()
	s.ivStruct = iv
	copy(s.fakeSig, sig[:crypto.MacLen])
	s.cryptoInit = true
	s.cryMu.Unlock()
	s.alphaTmp = nil
}

// setupCrypto2 trata o initivexpand2 (protocolo novo, servidores 3.1+ e TS6).
func (s *session) setupCrypto2(it map[string]string) {
	if s.cryptoInit || s.alphaTmp == nil {
		return // repetição de um pacote já tratado
	}
	fail := func(what string, err error) {
		s.c.endSession(s, fmt.Sprintf("falha no handshake (%s): %v", what, err), true)
	}

	beta, err := base64.StdEncoding.DecodeString(it["beta"])
	if err != nil {
		fail("beta", err)
		return
	}
	license, err := base64.StdEncoding.DecodeString(it["l"])
	if err != nil {
		fail("licença", err)
		return
	}
	blocks, err := parseLicense(license)
	if err != nil {
		fail("licença", err)
		return
	}
	serverKey, err := deriveLicenseKey(blocks)
	if err != nil {
		fail("chave do servidor", err)
		return
	}
	tempPub, tempPriv, err := generateTemporaryKey()
	if err != nil {
		fail("chave temporária", err)
		return
	}
	clientEk, err := generateClientEk(tempPub, tempPriv, beta, s.c.identity)
	if err != nil {
		fail("clientek", err)
		return
	}
	shared, err := getSharedSecret2(serverKey, tempPriv)
	if err != nil {
		fail("segredo compartilhado", err)
		return
	}

	// O clientek ainda vai com a chave fixa; tudo depois dele usa a nova.
	s.sendCommand(clientEk)

	iv := make([]byte, len(shared))
	copy(iv, shared)
	crypto.XorInto(iv, s.alphaTmp, 10)
	if len(iv) > 10 {
		crypto.XorInto(iv[10:], beta, len(beta))
	}
	s.installCrypto(iv)
	s.sendClientInit()
}

// setupCrypto trata o initivexpand (protocolo antigo, servidores < 3.1).
func (s *session) setupCrypto(it map[string]string) {
	if s.cryptoInit || s.alphaTmp == nil {
		return
	}
	beta, err1 := base64.StdEncoding.DecodeString(it["beta"])
	omega, err2 := base64.StdEncoding.DecodeString(it["omega"])
	if err1 != nil || err2 != nil || len(beta) != 10 || len(omega) == 0 {
		s.c.endSession(s, "falha no handshake: resposta inválida do servidor", true)
		return
	}
	pubX, pubY, err := ParseDerSequence(omega)
	if err != nil {
		s.c.endSession(s, "falha no handshake: chave pública do servidor inválida", true)
		return
	}
	priv, err := s.c.identity.PrivateKey.ECDH()
	if err != nil {
		s.c.endSession(s, "falha no handshake: a identidade não serve para o protocolo antigo", true)
		return
	}
	shared, err := crypto.GetSharedSecret(priv, pubX, pubY) // 20 bytes
	if err != nil {
		s.c.endSession(s, "falha no handshake: chave pública do servidor inválida", true)
		return
	}

	iv := make([]byte, 20)
	copy(iv, shared)
	crypto.XorInto(iv, s.alphaTmp, 10)
	crypto.XorInto(iv[10:], beta, 10)
	s.installCrypto(iv)
	s.sendClientInit()
}

func (s *session) sendClientInit() {
	s.c.mu.RLock()
	nickname := s.c.nickname
	s.c.mu.RUnlock()

	version, platform, sign := s.opts.Version, s.opts.Platform, s.opts.VersionSign
	if version == "" {
		version, platform, sign = DefaultVersion, DefaultPlatform, DefaultVersionSign
	}
	s.sendCommand(BuildCommand("clientinit",
		Param{"client_nickname", nickname},
		Param{"client_version", version},
		Param{"client_platform", platform},
		Param{"client_input_hardware", "1"},
		Param{"client_output_hardware", "1"},
		Param{"client_default_channel", s.opts.DefaultChannel},
		Param{"client_default_channel_password", HashPassword(s.opts.ChannelPassword)},
		Param{"client_server_password", HashPassword(s.opts.ServerPassword)},
		Param{"client_meta_data", s.opts.MetaData},
		Param{"client_version_sign", sign},
		Param{"client_key_offset", fmt.Sprintf("%d", s.c.identity.KeyOffset)},
		Param{"client_nickname_phonetic", ""},
		Param{"client_default_token", ""},
		Param{"hwid", ""},
	))
}
