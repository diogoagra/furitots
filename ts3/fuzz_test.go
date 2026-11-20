package ts3

import (
	"net"
	"strings"
	"sync"
	"testing"
)

// O que chega do servidor (e os arquivos que o usuário importa) não é
// confiável: os analisadores não podem derrubar o app, seja qual for a entrada.

func FuzzParseCommand(f *testing.F) {
	f.Add("notifytextmessage targetmode=2 msg=ol\\sá invokerid=3 invokername=Leko")
	f.Add("clid=1 client_nickname=a|clid=2 client_nickname=b\\pc")
	f.Add("error id=0 msg=ok")
	f.Add("a=\\")
	f.Add("||| = == |")
	f.Fuzz(func(t *testing.T, line string) {
		ParseCommand(line)
	})
}

// O que mandamos ao servidor leva textos de terceiros (apelidos, mensagens,
// nomes de arquivo): depois de codificado, um valor nunca pode virar outro
// parâmetro nem outro comando.
func FuzzEscape(f *testing.F) {
	f.Add("texto simples")
	f.Add("a b|c\\d/e\nf\rg\th")
	f.Add("x client_nickname=outro|clid=9")
	f.Add("\x00\x07\x08\x0b\x0c")
	f.Fuzz(func(t *testing.T, value string) {
		wire := Escape(value)
		if strings.ContainsAny(wire, " |\n\r\t\v\f") {
			t.Fatalf("valor codificado ainda tem separador de comando: %q -> %q", value, wire)
		}
		if got := Unescape(wire); got != value {
			t.Fatalf("ida e volta alterou o valor: %q -> %q -> %q", value, wire, got)
		}
		_, items := ParseCommand(BuildCommand("cmd", Param{"k", value}, Param{"fim", "1"}))
		if len(items) != 1 || items[0]["k"] != value || items[0]["fim"] != "1" || len(items[0]) != 2 {
			t.Fatalf("o valor %q virou outros parâmetros: %v", value, items)
		}
	})
}

func FuzzParseLicense(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1})
	block := make([]byte, 47)
	f.Add(append([]byte{1}, block...))
	ts5 := make([]byte, 46)
	ts5[33], ts5[43] = 8, 200 // bloco do TS5 declarando 200 propriedades
	f.Add(append([]byte{1}, ts5...))
	f.Fuzz(func(t *testing.T, data []byte) {
		blocks, err := parseLicense(data)
		if err == nil {
			deriveLicenseKey(blocks)
		}
	})
}

func FuzzParseTS3Identity(f *testing.F) {
	f.Add("[Identity]\nid=Principal\nidentity=\"12VAAAA\"\nnickname=Leko\n")
	f.Add("0V")
	f.Add("18446744073709551615V" + strings.Repeat("A", 200))
	f.Add("3VMIGTAgEAAkEA")
	f.Fuzz(func(t *testing.T, text string) {
		ParseTS3IdentityInfo(text)
		if len(text) > 4096 {
			return
		}
		ParseTS3Identity(text)
	})
}

func FuzzDerIntegers(f *testing.F) {
	f.Add([]byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x02})
	f.Add([]byte{0x30, 0x82, 0xff, 0xff})
	f.Add([]byte{0x30, 0x81})
	f.Fuzz(func(t *testing.T, data []byte) {
		derIntegers(data)
		parseLtcPrivateKey(data)
	})
}

// fuzzIdentity é gerada uma vez só (gerar uma identidade leva um instante).
var fuzzIdentity = sync.OnceValue(func() *Identity {
	ident, err := GenerateIdentity()
	if err != nil {
		panic(err)
	}
	return ident
})

// fuzzSession monta um cliente "conectado" a um endereço local que não
// responde, com alguns canais e clientes já conhecidos.
func fuzzSession(t testing.TB) (*Client, *session) {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9})
	if err != nil {
		t.Skip("sem rede local para o teste:", err)
	}
	c := NewClient(fuzzIdentity())
	s := newSession(c, conn, ConnectOptions{Host: "127.0.0.1", Nickname: "Eu"})
	s.clientID.Store(1)
	s.installCrypto(make([]byte, 64)) // sessão já com a chave combinada
	c.sess = s
	t.Cleanup(func() { c.endSession(s, "fim do teste", false); conn.Close() })
	for _, line := range []string{
		`channellist cid=1 cpid=0 channel_name=Lobby|cid=2 cpid=0 channel_name=Sala|cid=3 cpid=2 channel_name=Sub`,
		`notifyservergrouplist sgid=6 name=Admin type=1 iconid=300 namemode=1|sgid=8 name=Guest type=1`,
		`notifychannelgrouplist cgid=5 name=Dono type=1|cgid=8 name=Visitante type=1`,
		`notifycliententerview cfid=0 ctid=1 reasonid=0 clid=1 client_nickname=Eu client_servergroups=6`,
		`notifycliententerview cfid=0 ctid=1 reasonid=2 clid=5 client_nickname=Ana client_unique_identifier=uidAna client_servergroups=6,8`,
		`notifycliententerview cfid=0 ctid=2 reasonid=2 clid=6 client_nickname=Beto client_unique_identifier=uidBeto`,
	} {
		c.handleCommand(s, line)
	}
	return c, s
}

// Todos os comandos que o cliente trata, com parâmetros arbitrários: nada do
// que o servidor mande pode derrubar o app.
func FuzzHandleCommand(f *testing.F) {
	names := []string{
		"initivexpand2", "initivexpand", "notifystartdownload", "notifystartupload", "notifystatusfiletransfer",
		"error", "initserver", "notifyserveredited", "channellist", "notifychannelcreated", "channellistfinished",
		"notifychanneledited", "notifychanneldescriptionchanged", "notifychannelmoved", "notifychanneldeleted",
		"notifycliententerview", "notifyclientleftview", "notifyclientmoved", "notifyclientupdated",
		"notifyconnectioninfo", "notifyservergrouplist", "notifychannelgrouplist",
		"notifyservergroupclientadded", "notifyservergroupclientdeleted", "notifyclientchannelgroupchanged",
		"notifyplugincmd", "notifyclientpoke", "notifytextmessage", "notifyfilelist", "notifyfilelistfinished",
		"notifyclientneededpermissions", "notifypermissionlist", "notifyservergrouppermlist", "notifychannelgrouppermlist",
		"notifyclientchatclosed", "notifyclientchatcomposing", "notifytalkrequest", "",
	}
	seeds := []string{
		"", "clid=5", "clid=1 ctid=2 reasonid=4 invokerid=5 invokername=Ana reasonmsg=fora",
		"cid=2 channel_name=Novo cpid=1 channel_order=1 channel_icon_id=-1 channel_flag_password=1",
		"clid=99999999999 ctid=-1 cfid=x reasonid=8 client_servergroups=,,6,,x,99999999999999999999",
		"cid=3 cpid=3 order=3", "cid=1|cid=2|cid=3", "sgid=6 clid=5 cldbid=2 invokerid=1",
		"name=furito.screen data=jteste invokerid=5 invokername=Ana invokeruid=uidAna",
		"targetmode=1 msg=oi target=1 invokerid=5 invokername=Ana invokeruid=uidAna",
		"clientftfid=1 serverftfid=2 ftkey=abc port=30033 size=-5 ip=0.0.0.0 proto=1 seekpos=99999999999999",
		"clientftfid=1 status=2050 msg=erro size=18446744073709551615",
		"id=2568 msg=insufficient\\sclient\\spermissions failed_permid=1 return_code=abc",
		"alpha=AAAA beta=AAAA omega=AAAA ot=1 proof=AAAA tvd=AAAA time=1 l=AQ root=AAAA",
		"virtualserver_name=Srv virtualserver_welcomemessage=oi aclid=1 acn=Eu virtualserver_maxclients=x client_talk_power=y",
		"client_input_muted=2 client_away=x client_icon_id=18446744073709551615 client_flag_avatar=../../x clid=5 client_meta_data=furito/9",
		"cgid=5 cgi=1 cid=1 clid=5 invokerid=1", "permsid=b_a permvalue=x permnegated=2 permskip=-1",
	}
	for i := range names {
		for _, s := range seeds {
			f.Add(uint8(i), s)
		}
	}
	f.Fuzz(func(t *testing.T, which uint8, params string) {
		if len(params) > 4096 {
			return
		}
		c, s := fuzzSession(t)
		if which >= 128 { // metade dos casos: no meio do handshake, à espera do initivexpand
			s.cryMu.Lock()
			s.cryptoInit = false
			s.cryMu.Unlock()
			s.alphaTmp = make([]byte, 10)
		}
		c.OnClientEvent = func(ClientEvent) {}
		c.OnTextMessage = func(TextMessage) {}
		c.OnPoke = func(string, string) {}
		c.OnPluginCommand = func(PluginCommand) {}
		c.OnLog = func(string, string) {}
		c.OnNotify = func(string) {}
		c.OnStateChanged = func() {}
		c.OnDisconnected = func(string, bool) {}
		line := names[int(which)%len(names)]
		if params != "" {
			line += " " + params
		}
		c.handleCommand(s, line)
		c.handleCommand(s, line) // duas vezes: estado repetido também não pode quebrar
		c.Snapshot()
	})
}

// Pacotes crus, como chegam da rede, em três momentos da sessão: antes da
// troca de chaves, no meio do handshake e com a sessão estabelecida.
func FuzzHandlePacket(f *testing.F) {
	f.Add(uint8(0), []byte{})
	f.Add(uint8(0), append([]byte("TS3INIT1"), 0, 0, 0x88, 1, 2, 3))
	f.Add(uint8(1), append([]byte("TS3INIT1"), append([]byte{0, 0, 0x88, 3}, make([]byte, 232)...)...))
	f.Add(uint8(1), append([]byte("TS3INIT1"), 0, 0, 0x88, 127))
	f.Add(uint8(2), dummyPacket(0, ptCommand, []byte("initserver aclid=5")))
	f.Add(uint8(2), dummyPacket(1, ptAck, []byte{0, 1}))
	f.Add(uint8(2), serverPacket(make([]byte, 8), 7, ptVoice|flagUnencrypted, []byte{0, 1, 0, 5, 4, 0xfc}))
	f.Add(uint8(2), serverPacket(make([]byte, 8), 7, ptCommand|flagUnencrypted|flagFragmented|flagCompressed, []byte("x")))
	f.Add(uint8(2), serverPacket(make([]byte, 8), 9, ptPing|flagUnencrypted, nil))
	f.Fuzz(func(t *testing.T, state uint8, raw []byte) {
		if len(raw) > 2048 {
			return
		}
		c, s := fuzzSession(t)
		c.OnDisconnected = func(string, bool) {}
		c.OnVoice = func(uint16, uint16, byte, []byte) {}
		switch state % 3 {
		case 0: // antes de tudo
			s.cryMu.Lock()
			s.cryptoInit = false
			s.cryMu.Unlock()
		case 1: // à espera do initivexpand
			s.cryMu.Lock()
			s.cryptoInit = false
			s.cryMu.Unlock()
			s.alphaTmp = make([]byte, 10)
		}
		s.handlePacket(raw)
		// o mesmo conteúdo como se viesse sem cifra, com a assinatura certa
		if len(raw) >= serverHeaderLen {
			forged := append([]byte(nil), raw...)
			copy(forged, s.fakeSig)
			forged[10] |= flagUnencrypted
			s.handlePacket(forged)
		}
	})
}
