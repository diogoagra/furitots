package ts3

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscapeRoundTrip(t *testing.T) {
	cases := []string{
		"",
		"simples",
		`C:\temp\novo`,
		`a\sb`,
		`x\\p|y/z`,
		"linha1\nlinha2\ttab",
		"com espaço e | pipe",
		`\`,
	}
	for _, s := range cases {
		if got := Unescape(Escape(s)); got != s {
			t.Errorf("roundtrip %q -> %q", s, got)
		}
	}
}

func TestUnescapeWire(t *testing.T) {
	// formato real enviado pelo servidor TS6 para "ola C:\temp\novo [b]x[/b]"
	got := Unescape(`ola\sC:\\temp\\novo\s[b]x[\/b]`)
	want := `ola C:\temp\novo [b]x[/b]`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseCommandInheritance(t *testing.T) {
	name, items := ParseCommand(`notifyclientmoved ctid=5 reasonid=0 clid=1|clid=2|clid=3 reasonid=1`)
	if name != "notifyclientmoved" || len(items) != 3 {
		t.Fatalf("name=%q items=%d", name, len(items))
	}
	if items[1]["ctid"] != "5" || items[1]["clid"] != "2" || items[1]["reasonid"] != "0" {
		t.Errorf("item 1 = %v", items[1])
	}
	if items[2]["reasonid"] != "1" {
		t.Errorf("item 2 deve manter o próprio valor: %v", items[2])
	}
}

func TestParseCommandBareKeys(t *testing.T) {
	_, items := ParseCommand(`notifycliententerview clid=1 client_away_message=fui client_nickname=A|clid=2 client_away_message client_nickname=B\sC`)
	if v, ok := items[1]["client_away_message"]; !ok || v != "" {
		t.Errorf("chave sem valor deve ser vazia e não herdada: %q", v)
	}
	if items[1]["client_nickname"] != "B C" {
		t.Errorf("nickname = %q", items[1]["client_nickname"])
	}
}

func TestParseCommandNoArgs(t *testing.T) {
	name, items := ParseCommand("channellistfinished")
	if name != "channellistfinished" || len(items) != 1 {
		t.Fatalf("name=%q items=%v", name, items)
	}
}

func TestBuildCommand(t *testing.T) {
	got := BuildCommand("sendtextmessage", Param{"targetmode", "2"}, Param{"msg", "oi | tudo/bem"})
	want := `sendtextmessage targetmode=2 msg=oi\s\p\studo\/bem`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestHashPassword(t *testing.T) {
	if HashPassword("") != "" {
		t.Error("senha vazia deve continuar vazia")
	}
	// sha1("123") = 40bd001563085fc35165329ea1ff5c5ecbdbbeef
	if got := HashPassword("123"); got != "QL0AFWMIX8NRZTKeof9cXsvbvu8=" {
		t.Errorf("got %q", got)
	}
}

func TestOrderChannels(t *testing.T) {
	// channel_order = id do canal anterior no mesmo pai (0 = primeiro)
	chans := map[int]*Channel{
		1: {ID: 1, ParentID: 0, Order: 0, Name: "A"},
		7: {ID: 7, ParentID: 0, Order: 1, Name: "B"},
		3: {ID: 3, ParentID: 0, Order: 7, Name: "C"},
		9: {ID: 9, ParentID: 7, Order: 4, Name: "B2"},
		4: {ID: 4, ParentID: 7, Order: 0, Name: "B1"},
		8: {ID: 8, ParentID: 99, Order: 0, Name: "órfão"},
	}
	var names []string
	var depths []int
	for _, oc := range orderChannels(chans) {
		names = append(names, oc.Channel.Name)
		depths = append(depths, oc.Depth)
	}
	wantNames := []string{"A", "B", "B1", "B2", "C", "órfão"}
	wantDepths := []int{0, 0, 1, 1, 0, 0}
	if !reflect.DeepEqual(names, wantNames) || !reflect.DeepEqual(depths, wantDepths) {
		t.Fatalf("ordem=%v profundidades=%v", names, depths)
	}
}

// Editar um canal manda só o que mudou; a descrição só vai quando pedida,
// para não apagar uma que ainda não foi carregada do servidor.
func TestChannelSpecChanges(t *testing.T) {
	cur := Channel{
		ID: 7, Name: "Sala", Topic: "tópico", Description: "", DescriptionSet: false,
		Codec: 4, CodecQuality: 6, MaxClients: -1, IsPermanent: true, NeededTalkPower: 0,
	}
	same := ChannelSpec{Name: "Sala", Topic: "tópico", Permanent: true, MaxClients: -1, Codec: 4, CodecQuality: 6}
	if p := same.changes(cur); len(p) != 0 {
		t.Errorf("nada mudou, mas seriam enviados: %v", p)
	}

	keys := func(p []Param) string {
		var out []string
		for _, x := range p {
			out = append(out, x.Key+"="+x.Value)
		}
		return strings.Join(out, " ")
	}

	rename := same
	rename.Name = "Sala Nova"
	if got := keys(rename.changes(cur)); got != "channel_name=Sala Nova" {
		t.Errorf("renomear: %q", got)
	}

	// descrição vazia no formulário, sem ChangeDescription: não é enviada
	if got := keys(same.changes(Channel{Name: "Sala", Topic: "tópico", Description: "texto", DescriptionSet: true, Codec: 4, CodecQuality: 6, MaxClients: -1, IsPermanent: true})); got != "" {
		t.Errorf("descrição não tocada foi enviada: %q", got)
	}
	desc := same
	desc.Description, desc.ChangeDescription = "nova", true
	if got := keys(desc.changes(cur)); got != "channel_description=nova" {
		t.Errorf("descrição alterada: %q", got)
	}
	// descrição apagada de propósito
	clear := same
	clear.ChangeDescription = true
	loaded := cur
	loaded.Description, loaded.DescriptionSet = "texto", true
	if got := keys(clear.changes(loaded)); got != "channel_description=" {
		t.Errorf("apagar a descrição: %q", got)
	}

	other := same
	other.Permanent, other.SemiPermanent = false, true
	other.MaxClients = 10
	other.NeededTalkPower = 50
	other.ChangePassword, other.Password = true, "abc"
	want := "channel_flag_permanent=0 channel_flag_semi_permanent=1 channel_needed_talk_power=50 " +
		"channel_flag_maxclients_unlimited=0 channel_maxclients=10 channel_password=" + HashPassword("abc")
	if got := keys(other.changes(cur)); got != want {
		t.Errorf("várias mudanças:\n got %q\nwant %q", got, want)
	}
	unlimited := same
	limited := cur
	limited.MaxClients = 10
	if got := keys(unlimited.changes(limited)); got != "channel_flag_maxclients_unlimited=1" {
		t.Errorf("voltar a ilimitado: %q", got)
	}
}

// Espaços do Unicode que não são o separador do protocolo fazem parte do
// valor: um apelido com um deles não pode acrescentar parâmetros ao comando.
func TestParseCommandUnicodeSpacesAreNotSeparators(t *testing.T) {
	for _, sp := range []string{"\u00a0", "\u0085", "\u1680", "\u2000", "\u200a", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000"} {
		nick := "Leko" + sp + "clid=1" + sp + "client_unique_identifier=UID-DE-OUTRO"
		line := "notifycliententerview cfid=0 ctid=5 clid=7 client_unique_identifier=UID-REAL client_nickname=" + Escape(nick) + " client_servergroups=8"
		name, items := ParseCommand(line)
		if name != "notifycliententerview" || len(items) != 1 {
			t.Fatalf("%U: comando lido errado: %q %v", []rune(sp)[0], name, items)
		}
		it := items[0]
		if it["clid"] != "7" || it["client_unique_identifier"] != "UID-REAL" || it["client_nickname"] != nick || len(it) != 6 {
			t.Errorf("%U: o apelido injetou parâmetros: %v", []rune(sp)[0], it)
		}
	}
}

// Um valor gigante num parâmetro é cortado no limite, sem partir um caractere
// nem uma sequência de escape ao meio.
func TestParseCommandClipsHugeValues(t *testing.T) {
	_, items := ParseCommand("notifytextmessage msg=" + strings.Repeat("a", 3*maxValueLen) + " invokername=Ana")
	if len(items) != 1 || len(items[0]["msg"]) != maxValueLen || items[0]["invokername"] != "Ana" {
		t.Fatalf("valor não cortado: %d bytes, nome %q", len(items[0]["msg"]), items[0]["invokername"])
	}
	// o corte cai no meio de "\s" e no meio de um caractere de vários bytes
	for _, tail := range []string{`\s`, `\\\s`, "é", "日本"} {
		v := strings.Repeat("a", maxValueLen-1) + tail + strings.Repeat("b", 100)
		_, items := ParseCommand("x k=" + v)
		got := items[0]["k"]
		if !utf8.ValidString(got) || strings.HasSuffix(got, `\`) && !strings.HasSuffix(got, `\\`) || len(got) > maxValueLen {
			t.Errorf("corte ruim com %q no limite: termina em %q (%d bytes)", tail, got[len(got)-4:], len(got))
		}
	}
}
