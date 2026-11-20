package backend

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func avatarPNG(t *testing.T, tone uint8) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), tone, uint8(y * 4), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// O avatar de quem tem um chega à interface: a marca vem no estado do cliente
// e GetAvatar devolve a imagem. Contra um servidor em que temos uma chave de
// privilégio (não roda por padrão):
//
//	FURITO_ICON_TEST_HOST=127.0.0.1:19987 FURITO_ICON_TEST_TOKEN=... go test ./backend/ -run LiveAvatar -v
//
// Com FURITO_AVATAR_HOLD=30s o cliente com avatar ("FuritoAvatarDono") fica
// conectado esse tempo no fim, para olhar o avatar dele em outro cliente.
func TestLiveAvatar(t *testing.T) {
	host, token := os.Getenv("FURITO_ICON_TEST_HOST"), os.Getenv("FURITO_ICON_TEST_TOKEN")
	if host == "" || token == "" {
		t.Skip("defina FURITO_ICON_TEST_HOST e FURITO_ICON_TEST_TOKEN")
	}
	start := func(name string) (*App, *fakeEmitter, Options) {
		opts := testOptions(t)
		app, err := NewApp(opts)
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
		return app, em, opts
	}
	clientOf := func(em *fakeEmitter, id int) ClientDTO {
		st, _ := em.lastState()
		for _, cl := range st.Clients {
			if cl.ID == id {
				return cl
			}
		}
		return ClientDTO{}
	}

	owner, ownerEm, _ := start("FuritoAvatarDono")
	if err := owner.API().UsePrivilegeKey(token); err != nil {
		t.Fatalf("usar chave de privilégio: %v", err)
	}
	ownerState, _ := ownerEm.lastState()
	ownerID := ownerState.Self.ClientID
	time.Sleep(1200 * time.Millisecond)
	viewer, viewerEm, viewerOpts := start("FuritoAvatarOlha")
	api := viewer.API()
	viewerState, _ := viewerEm.lastState()

	waitFor(t, "ver o dono", 3*time.Second, func() bool { return clientOf(viewerEm, ownerID).ID != 0 })
	if got := clientOf(viewerEm, ownerID).Avatar; got != "" {
		t.Fatalf("ainda sem avatar: %q", got)
	}
	if _, err := api.GetAvatar(ownerID); err == nil || !strings.Contains(err.Error(), "não tem avatar") {
		t.Errorf("GetAvatar de quem não tem avatar: %v", err)
	}
	if _, err := api.GetAvatar(60000); err == nil {
		t.Error("GetAvatar de cliente inexistente deveria dar erro")
	}

	img, hash := avatarPNG(t, 40)
	if err := owner.client.SetAvatar(img); err != nil {
		t.Fatalf("pôr o avatar: %v", err)
	}
	waitFor(t, "a marca do avatar chegar ao estado", 4*time.Second, func() bool { return clientOf(viewerEm, ownerID).Avatar == hash })
	check := func(what string, want []byte) {
		t.Helper()
		url, err := api.GetAvatar(ownerID)
		if err != nil {
			t.Fatalf("%s: GetAvatar: %v", what, err)
		}
		const prefix = "data:image/png;base64,"
		if !strings.HasPrefix(url, prefix) {
			t.Fatalf("%s: deveria vir uma imagem PNG: %.40s", what, url)
		}
		if got, _ := base64.StdEncoding.DecodeString(url[len(prefix):]); !bytes.Equal(got, want) {
			t.Fatalf("%s: a imagem não é a que foi enviada (%d bytes contra %d)", what, len(got), len(want))
		}
	}
	check("primeiro avatar", img)
	cached := filepath.Join(filepath.Dir(viewerOpts.SettingsPath), "avatars", hash)
	if data, err := os.ReadFile(cached); err != nil || !bytes.Equal(data, img) {
		t.Errorf("o avatar deveria ficar guardado em disco pela marca: %v", err)
	}
	check("de novo (da memória)", img)
	// o próprio dono também vê o seu
	if url, err := owner.API().GetAvatar(ownerID); err != nil || !strings.HasPrefix(url, "data:image/png") {
		t.Errorf("o dono deveria ver o próprio avatar: %v", err)
	}
	// quem não tem continua sem
	if _, err := api.GetAvatar(viewerState.Self.ClientID); err == nil {
		t.Error("o espectador não tem avatar")
	}

	// troca: marca nova, imagem nova
	img2, hash2 := avatarPNG(t, 210)
	if err := owner.client.SetAvatar(img2); err != nil {
		t.Fatalf("trocar o avatar: %v", err)
	}
	waitFor(t, "a marca nova chegar", 4*time.Second, func() bool { return clientOf(viewerEm, ownerID).Avatar == hash2 })
	check("avatar trocado", img2)

	if hold, _ := time.ParseDuration(os.Getenv("FURITO_AVATAR_HOLD")); hold > 0 {
		t.Logf("segurando FuritoAvatarDono (id %d) com avatar por %v", ownerID, hold)
		time.Sleep(hold)
	}

	if err := owner.client.SetAvatar(nil); err != nil {
		t.Fatalf("tirar o avatar: %v", err)
	}
	waitFor(t, "a marca sumir", 4*time.Second, func() bool { return clientOf(viewerEm, ownerID).Avatar == "" })
	if _, err := api.GetAvatar(ownerID); err == nil {
		t.Error("sem avatar, GetAvatar deveria dar erro")
	}
}
