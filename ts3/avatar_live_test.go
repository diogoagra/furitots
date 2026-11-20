package ts3

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

// testAvatar gera um PNG pequeno; cada tom dá um arquivo diferente.
func testAvatar(t *testing.T, tone uint8) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 5), uint8(y * 5), tone, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// O endereço do avatar sai do uid do cliente. O par abaixo foi observado num
// servidor TeamSpeak 6 de verdade (uid de 32 bytes).
func TestAvatarPath(t *testing.T) {
	got, err := AvatarPath("sp/lNRUSa2YbHVyeMmUx3vePeIaQVePsBXboJegSyIY=")
	if want := "/avatar_lcjpofdfbfbcglggblbnfmjodcgfdbnophiphiigjaffodomafhgoicfoibcmiig"; err != nil || got != want {
		t.Errorf("AvatarPath (TeamSpeak 6):\n got %q %v\nwant %q", got, err, want)
	}
	// uid de 20 bytes, como no TeamSpeak 3: 40 letras
	if got, err := AvatarPath("oksOGPfF3GibhLPM1jQ+dAOwnuY="); err != nil || len(got) != len("/avatar_")+40 || got[:10] != "/avatar_kc" {
		t.Errorf("AvatarPath (TeamSpeak 3): %q %v", got, err)
	}
	for _, bad := range []string{"", "não é base64!", "../../etc"} {
		if got, err := AvatarPath(bad); err == nil {
			t.Errorf("uid inválido %q deveria dar erro, deu %q", bad, got)
		}
	}

	cl := &ClientInfo{}
	applyClientFields(cl, map[string]string{"client_flag_avatar": "ffe55e815ac54ec9e8d360ba51aed2f1"})
	if cl.Avatar != "ffe55e815ac54ec9e8d360ba51aed2f1" {
		t.Errorf("marca do avatar não foi lida: %q", cl.Avatar)
	}
	applyClientFields(cl, map[string]string{"client_flag_avatar": ""})
	if cl.Avatar != "" {
		t.Errorf("avatar removido deveria limpar a marca: %q", cl.Avatar)
	}
}

// Avatares contra um servidor em que temos uma chave de privilégio (não roda
// por padrão):
//
//	FURITO_ICON_TEST_HOST=127.0.0.1:19987 FURITO_ICON_TEST_TOKEN=... go test ./ts3/ -run LiveAvatar -v
//
// Quem já estava conectado fica sabendo quando alguém põe, troca ou tira o
// avatar; quem chega depois já o vê; e a imagem baixada é a que foi enviada.
// Com FURITO_AVATAR_HOLD=30s o cliente com avatar fica conectado esse tempo
// no fim (para olhar o avatar dele em outro cliente).
func TestLiveAvatar(t *testing.T) {
	host, token := os.Getenv("FURITO_ICON_TEST_HOST"), os.Getenv("FURITO_ICON_TEST_TOKEN")
	if host == "" || token == "" {
		t.Skip("defina FURITO_ICON_TEST_HOST e FURITO_ICON_TEST_TOKEN")
	}
	connect := func(name string) *Client {
		t.Helper()
		ident, err := GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		c := NewClient(ident)
		ok := make(chan string, 2)
		c.OnConnected = func() { ok <- "" }
		c.OnDisconnected = func(r string, e bool) { ok <- r }
		if err := c.Connect(ConnectOptions{Host: host, Nickname: name}); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-ok:
			if r != "" {
				t.Fatalf("%s não conectou: %s", name, r)
			}
		case <-time.After(12 * time.Second):
			t.Fatalf("%s: tempo esgotado", name)
		}
		time.Sleep(700 * time.Millisecond)
		return c
	}
	seen := func(c *Client, id uint16) ClientInfo {
		for _, cl := range c.Snapshot().Clients {
			if cl.ID == id {
				return cl
			}
		}
		return ClientInfo{}
	}
	waitFlag := func(what string, c *Client, id uint16, want string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if seen(c, id).Avatar == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: marca do avatar = %q, esperava %q", what, seen(c, id).Avatar, want)
	}

	owner := connect("FuritoAvatar")
	defer owner.Disconnect()
	if err := owner.UsePrivilegeKey(token); err != nil {
		t.Fatalf("usar chave de privilégio: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	ownerID := owner.ClientID()

	early := connect("FuritoCedo") // já estava no servidor quando o avatar apareceu
	defer early.Disconnect()
	if got := seen(early, ownerID).Avatar; got != "" {
		t.Fatalf("antes de enviar não há avatar: %q", got)
	}
	if _, err := early.DownloadAvatar(seen(early, ownerID).UID); err == nil {
		t.Error("baixar um avatar que não existe deveria dar erro")
	}

	first, firstHash := testAvatar(t, 60)
	if err := owner.SetAvatar(first); err != nil {
		t.Fatalf("SetAvatar: %v", err)
	}
	waitFlag("o próprio dono", owner, ownerID, firstHash)
	waitFlag("quem já estava conectado", early, ownerID, firstHash)
	if data, err := early.DownloadAvatar(seen(early, ownerID).UID); err != nil || !bytes.Equal(data, first) {
		t.Fatalf("o avatar baixado deveria ser o enviado: %d bytes, %v", len(data), err)
	}

	late := connect("FuritoTarde") // chegou depois: já vê o avatar ao entrar
	defer late.Disconnect()
	waitFlag("quem chegou depois", late, ownerID, firstHash)
	if data, err := late.DownloadAvatar(seen(late, ownerID).UID); err != nil || !bytes.Equal(data, first) {
		t.Fatalf("quem chegou depois deveria baixar o mesmo avatar: %d bytes, %v", len(data), err)
	}

	// troca de avatar: marca nova, imagem nova
	second, secondHash := testAvatar(t, 200)
	if err := owner.SetAvatar(second); err != nil {
		t.Fatalf("trocar o avatar: %v", err)
	}
	waitFlag("troca de avatar", early, ownerID, secondHash)
	if data, err := early.DownloadAvatar(seen(early, ownerID).UID); err != nil || !bytes.Equal(data, second) {
		t.Fatalf("depois da troca deveria vir a imagem nova: %d bytes, %v", len(data), err)
	}

	if hold, _ := time.ParseDuration(os.Getenv("FURITO_AVATAR_HOLD")); hold > 0 {
		t.Logf("segurando %q (id %d) com avatar por %v", "FuritoAvatar", ownerID, hold)
		time.Sleep(hold)
	}

	// sem avatar de novo
	if err := owner.SetAvatar(nil); err != nil {
		t.Fatalf("tirar o avatar: %v", err)
	}
	waitFlag("avatar removido", early, ownerID, "")
	waitFlag("avatar removido (quem chegou depois)", late, ownerID, "")
}
