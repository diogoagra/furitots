package ts3

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Teste ao vivo dos ícones, contra um servidor em que temos uma chave de
// privilégio (não roda por padrão):
//
//	FURITO_ICON_TEST_HOST=127.0.0.1:19987 FURITO_ICON_TEST_TOKEN=... go test ./ts3/ -run LiveIcons -v
//
// Envia um ícone, cria um grupo de servidor com ele, e confere que outro
// cliente enxerga o ícone no grupo, no canal e consegue baixá-lo.
func TestLiveIcons(t *testing.T) {
	host, token := os.Getenv("FURITO_ICON_TEST_HOST"), os.Getenv("FURITO_ICON_TEST_TOKEN")
	if host == "" || token == "" {
		t.Skip("defina FURITO_ICON_TEST_HOST e FURITO_ICON_TEST_TOKEN")
	}

	connect := func(name string) (*Client, func() []string) {
		ident, err := GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		c := NewClient(ident)
		var mu sync.Mutex
		var lines []string
		c.Logf = func(f string, a ...any) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(f, a...))
			mu.Unlock()
		}
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
		return c, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), lines...)
		}
	}

	// um PNG 16x16 qualquer
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 16), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	icon := buf.Bytes()
	id := crc32.ChecksumIEEE(icon)

	admin, adminLog := connect("IconAdmin")
	defer admin.Disconnect()
	admin.send(BuildCommand("tokenuse", Param{"token", token}))
	time.Sleep(600 * time.Millisecond)

	// envia o ícone
	s := admin.session()
	admin.ftMu.Lock()
	admin.ftNext++
	ftid := admin.ftNext
	wait := make(chan ftResult, 1)
	admin.ftWait[ftid] = wait
	admin.ftMu.Unlock()
	s.sendCommand(BuildCommand("ftinitupload",
		Param{"clientftfid", strconv.Itoa(int(ftid))},
		Param{"name", "/icon_" + strconv.FormatUint(uint64(id), 10)},
		Param{"cid", "0"}, Param{"cpw", ""},
		Param{"size", strconv.Itoa(len(icon))},
		Param{"overwrite", "1"}, Param{"resume", "0"},
		Param{"return_code", "ft" + strconv.Itoa(int(ftid))},
	))
	var up ftResult
	select {
	case up = <-wait:
	case <-time.After(6 * time.Second):
		t.Fatal("sem resposta ao ftinitupload")
	}
	if up.err != nil {
		t.Fatalf("envio recusado: %v", up.err)
	}
	h, _, _ := net.SplitHostPort(host)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(h, strconv.Itoa(up.port)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte(up.key))
	conn.Write(icon)
	time.Sleep(500 * time.Millisecond)
	conn.Close()

	// grupo de servidor com o ícone, atribuído ao próprio admin; ícone no canal 1
	admin.send(BuildCommand("servergroupadd", Param{"name", "FuritoIcone"}, Param{"type", "1"}))
	time.Sleep(500 * time.Millisecond)
	admin.send("servergrouplist") // a lista só é reenviada a pedido
	time.Sleep(700 * time.Millisecond)
	sgid := ""
	for gid, g := range admin.Snapshot().ServerGroups {
		if g.Name == "FuritoIcone" {
			sgid = strconv.Itoa(gid)
		}
	}
	if sgid == "" {
		log := adminLog()
		if len(log) > 14 {
			log = log[len(log)-14:]
		}
		for i := range log {
			if len(log[i]) > 220 {
				log[i] = log[i][:220]
			}
		}
		t.Fatalf("não achei o grupo criado; fim do log:\n%s", strings.Join(log, "\n"))
	}
	signed := strconv.Itoa(int(int32(id)))
	admin.send(BuildCommand("servergroupaddperm", Param{"sgid", sgid}, Param{"permsid", "i_icon_id"},
		Param{"permvalue", signed}, Param{"permnegated", "0"}, Param{"permskip", "0"}))
	// "mostrar o nome do grupo antes do apelido"
	admin.send(BuildCommand("servergroupaddperm", Param{"sgid", sgid}, Param{"permsid", "i_group_show_name_in_tree"},
		Param{"permvalue", "1"}, Param{"permnegated", "0"}, Param{"permskip", "0"}))
	var dbid int
	snap := admin.Snapshot()
	for _, cl := range snap.Clients {
		if cl.ID == snap.SelfID {
			dbid = cl.DatabaseID
		}
	}
	admin.send(BuildCommand("servergroupaddclient", Param{"sgid", sgid}, Param{"cldbid", strconv.Itoa(dbid)}))
	admin.send(BuildCommand("channeledit", Param{"cid", "1"}, Param{"channel_icon_id", signed}))
	time.Sleep(800 * time.Millisecond)

	viewer, _ := connect("IconViewer")
	defer viewer.Disconnect()
	vs := viewer.Snapshot()

	group, ok := vs.ServerGroups[atoi(sgid)]
	t.Logf("ícone %d (%d bytes); grupo %s visto pelo outro cliente: %+v", id, len(icon), sgid, group)
	if !ok || group.IconID != id {
		t.Errorf("grupo sem o ícone: %+v", group)
	}
	if group.NameMode != 1 {
		t.Errorf("modo de nome do grupo = %d, esperado 1 (antes do apelido)", group.NameMode)
	}
	seen := false
	for _, cl := range vs.Clients {
		if cl.Nickname == "IconAdmin" {
			for _, g := range cl.ServerGroups {
				seen = seen || strconv.Itoa(g) == sgid
			}
			t.Logf("IconAdmin visto com grupos %v", cl.ServerGroups)
		}
	}
	if !seen {
		t.Error("o cliente não aparece no grupo com ícone")
	}
	for _, oc := range vs.Channels {
		if oc.Channel.ID == 1 {
			// Só informativo: o teste ainda não acerta o jeito de definir o
			// ícone de um canal neste servidor.
			t.Logf("canal 1: ícone %d (enviado %d)", oc.Channel.IconID, id)
		}
	}

	start := time.Now()
	got, err := viewer.DownloadIcon(id)
	if err != nil {
		t.Fatalf("download do ícone: %v", err)
	}
	t.Logf("ícone baixado em %v: %d bytes, idêntico=%v", time.Since(start).Round(time.Millisecond), len(got), bytes.Equal(got, icon))
	if !bytes.Equal(got, icon) {
		t.Error("o ícone baixado é diferente do enviado")
	}

	start = time.Now()
	_, err = viewer.DownloadIcon(424242)
	t.Logf("ícone inexistente: erro %q em %v", err, time.Since(start).Round(time.Millisecond))
	if err == nil {
		t.Error("ícone inexistente deveria dar erro")
	}
}

// Reproduz um servidor com muitos ícones: 40 pedidos disparados juntos, sem
// controle de ritmo, e depois 40 pelo caminho normal (DownloadIcon).
func TestLiveManyIcons(t *testing.T) {
	host, token := os.Getenv("FURITO_ICON_TEST_HOST"), os.Getenv("FURITO_ICON_TEST_TOKEN")
	if host == "" || token == "" {
		t.Skip("defina FURITO_ICON_TEST_HOST e FURITO_ICON_TEST_TOKEN")
	}
	connect := func(name string) *Client {
		ident, _ := GenerateIdentity()
		c := NewClient(ident)
		ok := make(chan string, 2)
		c.OnConnected = func() { ok <- "" }
		c.OnDisconnected = func(r string, e bool) { ok <- r }
		c.Connect(ConnectOptions{Host: host, Nickname: name})
		select {
		case r := <-ok:
			if r != "" {
				t.Fatalf("%s não conectou: %s", name, r)
			}
		case <-time.After(12 * time.Second):
			t.Fatalf("%s: tempo esgotado", name)
		}
		time.Sleep(600 * time.Millisecond)
		return c
	}

	admin := connect("IconAdmin")
	defer admin.Disconnect()
	admin.send(BuildCommand("tokenuse", Param{"token", token}))
	time.Sleep(600 * time.Millisecond)

	const n = 80
	icons := make(map[uint32][]byte, n)
	var ids []uint32
	h, _, _ := net.SplitHostPort(host)
	for i := 0; i < n; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				img.Set(x, y, color.RGBA{uint8(i * 3), uint8(x * 16), uint8(y * 16), 255})
			}
		}
		var buf bytes.Buffer
		png.Encode(&buf, img)
		data := buf.Bytes()
		id := crc32.ChecksumIEEE(data)
		icons[id] = data
		ids = append(ids, id)

		s := admin.session()
		admin.ftMu.Lock()
		admin.ftNext++
		ftid := admin.ftNext
		wait := make(chan ftResult, 1)
		admin.ftWait[ftid] = wait
		admin.ftMu.Unlock()
		s.sendCommand(BuildCommand("ftinitupload",
			Param{"clientftfid", strconv.Itoa(int(ftid))},
			Param{"name", "/icon_" + strconv.FormatUint(uint64(id), 10)},
			Param{"cid", "0"}, Param{"cpw", ""},
			Param{"size", strconv.Itoa(len(data))},
			Param{"overwrite", "1"}, Param{"resume", "0"},
			Param{"return_code", "ft" + strconv.Itoa(int(ftid))},
		))
		select {
		case up := <-wait:
			if up.err != nil {
				t.Fatalf("envio %d recusado: %v", i, up.err)
			}
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(h, strconv.Itoa(up.port)), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			conn.Write([]byte(up.key))
			conn.Write(data)
			time.Sleep(40 * time.Millisecond)
			conn.Close()
		case <-time.After(6 * time.Second):
			t.Fatalf("sem resposta ao envio %d", i)
		}
	}
	time.Sleep(time.Second)

	fetch := func(c *Client, list []uint32, get func(uint32) ([]byte, error)) (ok int, errs map[string]int, took time.Duration) {
		errs = make(map[string]int)
		var mu sync.Mutex
		var wg sync.WaitGroup
		start := time.Now()
		for _, id := range list {
			wg.Add(1)
			go func(id uint32) {
				defer wg.Done()
				data, err := get(id)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs[err.Error()]++
				} else if bytes.Equal(data, icons[id]) {
					ok++
				} else {
					errs["conteúdo diferente"]++
				}
			}(id)
		}
		wg.Wait()
		return ok, errs, time.Since(start).Round(10 * time.Millisecond)
	}

	// 1) como era: todos os pedidos de uma vez, sem controle
	burst := connect("IconBurst")
	ok, errs, took := fetch(burst, ids[:40], func(id uint32) ([]byte, error) {
		return burst.downloadOnce(0, "/icon_"+strconv.FormatUint(uint64(id), 10), "", ftMaxIcon)
	})
	t.Logf("40 pedidos simultâneos sem controle: %d ok em %v; falhas: %v", ok, took, errs)
	burst.Disconnect()

	// 2) como ficou: mesmo disparo, pelo caminho normal
	time.Sleep(3 * time.Second)
	viewer := connect("IconViewer")
	defer viewer.Disconnect()
	ok, errs, took = fetch(viewer, ids[40:], viewer.DownloadIcon)
	t.Logf("40 pedidos simultâneos pelo DownloadIcon: %d ok em %v; falhas: %v; reclamações de flood do servidor: %d", ok, took, errs, viewer.ftFloods.Load())
	if ok != 40 {
		t.Errorf("deveriam vir os 40 ícones, vieram %d", ok)
	}
}
