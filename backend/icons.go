package backend

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"furito-ts/ts3"
)

// IconRef é um ícone mostrado ao lado de um cliente ou canal. Ids de 100 a
// 600 são os ícones padrão do TeamSpeak (a interface os desenha); os demais
// são imagens guardadas no servidor, obtidas com GetIcon.
type IconRef struct {
	ID    uint32 `json:"id"`
	Title string `json:"title"`
}

func sniffImage(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return "image/gif"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("BM")):
		return "image/bmp"
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(data, []byte{0, 0, 1, 0}):
		return "image/x-icon"
	}
	// SVG: o "<svg" pode vir depois de um cabeçalho XML e comentários longos.
	head := bytes.TrimSpace(data)
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.Contains(head, []byte("<svg")) {
		return "image/svg+xml"
	}
	return ""
}

// sortedServerGroups devolve os grupos de servidor do cliente na ordem
// definida pelo servidor.
func sortedServerGroups(cl ts3.ClientInfo, snap ts3.Snapshot) []ts3.GroupInfo {
	ids := append([]int(nil), cl.ServerGroups...)
	sort.Slice(ids, func(i, j int) bool {
		a, b := snap.ServerGroups[ids[i]], snap.ServerGroups[ids[j]]
		if a.SortID != b.SortID {
			return a.SortID < b.SortID
		}
		return ids[i] < ids[j]
	})
	groups := make([]ts3.GroupInfo, 0, len(ids))
	for _, id := range ids {
		if g, ok := snap.ServerGroups[id]; ok {
			groups = append(groups, g)
		}
	}
	return groups
}

// clientNameAffixes monta o que o TeamSpeak mostra junto do apelido na
// árvore: grupos com "modo de nome" 1 aparecem antes ("[1850+] Leko") e os
// de modo 2 depois ("Leko [Staff]").
func clientNameAffixes(cl ts3.ClientInfo, snap ts3.Snapshot) (prefix, suffix string) {
	groups := sortedServerGroups(cl, snap)
	if g, ok := snap.ChannelGroups[cl.ChannelGroupID]; ok {
		groups = append(groups, g)
	}
	for _, g := range groups {
		if g.Name == "" {
			continue
		}
		switch g.NameMode {
		case 1:
			prefix += "[" + g.Name + "]"
		case 2:
			suffix += "[" + g.Name + "]"
		}
	}
	if prefix != "" {
		prefix += " "
	}
	if suffix != "" {
		suffix = " " + suffix
	}
	return prefix, suffix
}

// clientIcons monta, na ordem do TeamSpeak, os ícones à direita do apelido:
// grupo do canal, grupos do servidor (pela ordenação do servidor) e o ícone
// próprio do cliente.
func clientIcons(cl ts3.ClientInfo, snap ts3.Snapshot) []IconRef {
	icons := []IconRef{}
	if g, ok := snap.ChannelGroups[cl.ChannelGroupID]; ok && g.IconID != 0 {
		icons = append(icons, IconRef{ID: g.IconID, Title: g.Name})
	}
	for _, g := range sortedServerGroups(cl, snap) {
		if g.IconID != 0 {
			icons = append(icons, IconRef{ID: g.IconID, Title: g.Name})
		}
	}
	if cl.IconID != 0 {
		icons = append(icons, IconRef{ID: cl.IconID, Title: "Ícone do cliente"})
	}
	return icons
}

// icon devolve a imagem de um ícone do servidor como data URL. Ícones são
// identificados pelo CRC32 do conteúdo, então nunca mudam: ficam guardados em
// disco e não são baixados de novo.
func (a *App) icon(id uint32) (string, error) {
	if id == 0 {
		return "", errors.New("sem ícone")
	}
	a.iconMu.Lock()
	if url, ok := a.icons[id]; ok {
		a.iconMu.Unlock()
		if url == "" {
			return "", errors.New("ícone indisponível")
		}
		return url, nil
	}
	if wait, busy := a.iconWait[id]; busy {
		a.iconMu.Unlock()
		<-wait // outro pedido já está baixando este ícone
		a.iconMu.Lock()
		url := a.icons[id]
		a.iconMu.Unlock()
		if url == "" {
			return "", errors.New("ícone indisponível")
		}
		return url, nil
	}
	wait := make(chan struct{})
	a.iconWait[id] = wait
	a.iconMu.Unlock()

	file := filepath.Join(a.iconDir, strconv.FormatUint(uint64(id), 10))
	data, err := os.ReadFile(file)
	fromDisk := err == nil
	if !fromDisk {
		data, err = a.client.DownloadIcon(id)
	}
	url := ""
	if err == nil {
		if mime := sniffImage(data); mime != "" {
			url = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			if !fromDisk && os.MkdirAll(a.iconDir, 0o700) == nil {
				os.WriteFile(file, data, 0o600)
			}
		} else {
			err = errors.New("o arquivo do ícone não é uma imagem")
		}
	}

	a.iconMu.Lock()
	// Só a falha definitiva (o ícone não existe ou não é imagem) fica
	// lembrada, e só até a próxima conexão; falha passageira é tentada de
	// novo no próximo pedido.
	if url != "" || !ts3.IsTransient(err) {
		a.icons[id] = url
	}
	delete(a.iconWait, id)
	a.iconMu.Unlock()
	close(wait)
	return url, err
}

// forgetFailedIcons permite tentar de novo os ícones e avatares que falharam.
func (a *App) forgetFailedIcons() {
	a.iconMu.Lock()
	for id, url := range a.icons {
		if url == "" {
			delete(a.icons, id)
		}
	}
	for key, url := range a.avatars {
		if url == "" {
			delete(a.avatars, key)
		}
	}
	a.iconMu.Unlock()
}

var avatarFlag = regexp.MustCompile(`^[0-9a-f]{32}$`)

// avatar devolve o avatar de um cliente como data URL. O servidor avisa que
// um cliente tem avatar por uma marca — o MD5 do arquivo —, que muda quando
// o avatar muda: por ela a imagem fica guardada em disco (avatars/) e não é
// baixada de novo.
func (a *App) avatar(clientID int) (string, error) {
	var cl ts3.ClientInfo
	for _, c := range a.client.Snapshot().Clients {
		if int(c.ID) == clientID {
			cl = c
		}
	}
	if cl.ID == 0 {
		return "", errors.New("cliente não está mais online")
	}
	if cl.Avatar == "" {
		return "", errors.New("este cliente não tem avatar")
	}
	key := cl.UID + "\x00" + cl.Avatar
	a.iconMu.Lock()
	url, known := a.avatars[key]
	a.iconMu.Unlock()
	if known {
		if url == "" {
			return "", errors.New("avatar indisponível")
		}
		return url, nil
	}

	// só uma marca que é mesmo um MD5 vira nome de arquivo
	file := ""
	if avatarFlag.MatchString(cl.Avatar) {
		file = filepath.Join(a.avatarDir, cl.Avatar)
	}
	var data []byte
	var err error = os.ErrNotExist
	if file != "" {
		data, err = os.ReadFile(file)
	}
	fromDisk := err == nil
	if !fromDisk {
		data, err = a.client.DownloadAvatar(cl.UID)
	}
	url = ""
	if err == nil {
		if mime := sniffImage(data); mime != "" {
			url = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			// guarda em disco só o que confere com a marca (senão o nome mentiria sobre o conteúdo)
			if sum := md5.Sum(data); !fromDisk && file != "" && hex.EncodeToString(sum[:]) == cl.Avatar && os.MkdirAll(a.avatarDir, 0o700) == nil {
				os.WriteFile(file, data, 0o600)
			}
		} else {
			err = errors.New("o arquivo do avatar não é uma imagem")
		}
	}
	a.iconMu.Lock()
	// como nos ícones: só a falha definitiva fica lembrada, e só até a próxima conexão
	if url != "" || !ts3.IsTransient(err) {
		a.avatars[key] = url
	}
	a.iconMu.Unlock()
	return url, err
}
