package ts3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

// Ações sobre o servidor. Todas esperam a confirmação do servidor e devolvem
// o erro dele (ex.: permissão insuficiente).

func itoa(n int) string { return strconv.Itoa(n) }

func (c *Client) do(cmd string, params ...Param) error {
	_, err := c.request(BuildCommand(cmd, params...))
	return err
}

// SetNickname troca o apelido na conexão atual.
func (c *Client) SetNickname(name string) error {
	return c.do("clientupdate", Param{"client_nickname", NormalizeNickname(name)})
}

// UsePrivilegeKey usa uma chave de privilégio (token) para ganhar um grupo.
func (c *Client) UsePrivilegeKey(key string) error {
	return c.do("tokenuse", Param{"token", key})
}

// MoveClient move um cliente para um canal.
func (c *Client) MoveClient(clientID uint16, channelID int, password string) error {
	params := []Param{{"clid", itoa(int(clientID))}, {"cid", itoa(channelID)}}
	if password != "" {
		params = append(params, Param{"cpw", HashPassword(password)})
	}
	return c.do("clientmove", params...)
}

// KickClient expulsa um cliente do canal (volta para o canal padrão) ou do servidor.
func (c *Client) KickClient(clientID uint16, fromServer bool, reason string) error {
	reasonID := "4"
	if fromServer {
		reasonID = "5"
	}
	return c.do("clientkick", Param{"clid", itoa(int(clientID))}, Param{"reasonid", reasonID}, Param{"reasonmsg", reason})
}

// BanClient bane um cliente. seconds 0 = permanente.
func (c *Client) BanClient(clientID uint16, seconds int, reason string) error {
	return c.do("banclient", Param{"clid", itoa(int(clientID))}, Param{"time", itoa(seconds)}, Param{"banreason", reason})
}

// PokeClient cutuca um cliente.
func (c *Client) PokeClient(clientID uint16, message string) error {
	return c.do("clientpoke", Param{"clid", itoa(int(clientID))}, Param{"msg", message})
}

// SetClientTalker dá ou tira a palavra de um cliente num canal moderado.
func (c *Client) SetClientTalker(clientID uint16, on bool) error {
	return c.do("clientedit", Param{"clid", itoa(int(clientID))}, Param{"client_is_talker", boolParam(on)})
}

// RequestTalk pede (ou cancela o pedido de) a palavra.
func (c *Client) RequestTalk(on bool, message string) error {
	return c.do("clientupdate", Param{"client_talk_request", boolParam(on)}, Param{"client_talk_request_msg", message})
}

// ChannelSpec são as propriedades de um canal a criar ou editar.
type ChannelSpec struct {
	Name        string
	ParentID    int
	Topic       string
	Description string
	// ChangeDescription: na edição, a descrição só é enviada quando true. A
	// descrição de um canal vem do servidor sob demanda; quem edita sem ela
	// carregada não pode apagá-la por acidente.
	ChangeDescription bool
	Password          string
	ChangePassword    bool // na edição: false mantém a senha atual
	Permanent         bool
	SemiPermanent     bool // nenhum dos dois = temporário
	MaxClients        int  // -1 = ilimitado
	Codec             int  // 4 = voz, 5 = música
	CodecQuality      int  // 0..10
	NeededTalkPower   int
	IsDefault         bool
}

// params são as propriedades enviadas na criação de um canal.
func (spec ChannelSpec) params() []Param {
	p := []Param{
		{"channel_name", spec.Name},
		{"channel_topic", spec.Topic},
		{"channel_description", spec.Description},
		{"channel_flag_permanent", boolParam(spec.Permanent)},
		{"channel_flag_semi_permanent", boolParam(spec.SemiPermanent && !spec.Permanent)},
		{"channel_codec", itoa(spec.Codec)},
		{"channel_codec_quality", itoa(spec.CodecQuality)},
		{"channel_needed_talk_power", itoa(spec.NeededTalkPower)},
	}
	if spec.MaxClients < 0 {
		p = append(p, Param{"channel_flag_maxclients_unlimited", "1"})
	} else {
		p = append(p, Param{"channel_flag_maxclients_unlimited", "0"}, Param{"channel_maxclients", itoa(spec.MaxClients)})
	}
	p = append(p, Param{"channel_password", HashPassword(spec.Password)})
	if spec.IsDefault {
		p = append(p, Param{"channel_flag_default", "1"})
	}
	return p
}

// CreateChannel cria um canal. Um canal temporário existe enquanto houver
// alguém nele; o servidor move quem o criou para dentro.
func (c *Client) CreateChannel(spec ChannelSpec) error {
	params := spec.params()
	if spec.ParentID > 0 {
		params = append(params, Param{"cpid", itoa(spec.ParentID)})
	}
	return c.do("channelcreate", params...)
}

// EditChannel altera um canal enviando só o que mudou em relação ao que o
// servidor informou, como o cliente oficial: o servidor confere uma permissão
// por propriedade enviada, então quem só pode renomear não deve mandar o resto.
func (c *Client) EditChannel(channelID int, spec ChannelSpec) error {
	c.mu.RLock()
	ch, ok := c.channels[channelID]
	var cur Channel
	if ok {
		cur = *ch
	}
	c.mu.RUnlock()
	if !ok {
		return errors.New("canal inexistente")
	}
	params := spec.changes(cur)
	if len(params) == 0 {
		return nil
	}
	return c.do("channeledit", append([]Param{{"cid", itoa(channelID)}}, params...)...)
}

// changes devolve as propriedades de spec que diferem do canal atual.
func (spec ChannelSpec) changes(cur Channel) []Param {
	var p []Param
	if spec.Name != cur.Name {
		p = append(p, Param{"channel_name", spec.Name})
	}
	if spec.Topic != cur.Topic {
		p = append(p, Param{"channel_topic", spec.Topic})
	}
	if spec.ChangeDescription && !(cur.DescriptionSet && spec.Description == cur.Description) {
		p = append(p, Param{"channel_description", spec.Description})
	}
	semi := spec.SemiPermanent && !spec.Permanent
	if spec.Permanent != cur.IsPermanent || semi != cur.IsSemiPermanent {
		p = append(p, Param{"channel_flag_permanent", boolParam(spec.Permanent)}, Param{"channel_flag_semi_permanent", boolParam(semi)})
	}
	if spec.Codec != cur.Codec {
		p = append(p, Param{"channel_codec", itoa(spec.Codec)})
	}
	if spec.CodecQuality != cur.CodecQuality {
		p = append(p, Param{"channel_codec_quality", itoa(spec.CodecQuality)})
	}
	if spec.NeededTalkPower != cur.NeededTalkPower {
		p = append(p, Param{"channel_needed_talk_power", itoa(spec.NeededTalkPower)})
	}
	switch {
	case spec.MaxClients < 0 && cur.MaxClients >= 0:
		p = append(p, Param{"channel_flag_maxclients_unlimited", "1"})
	case spec.MaxClients >= 0 && spec.MaxClients != cur.MaxClients:
		p = append(p, Param{"channel_flag_maxclients_unlimited", "0"}, Param{"channel_maxclients", itoa(spec.MaxClients)})
	}
	if spec.ChangePassword {
		p = append(p, Param{"channel_password", HashPassword(spec.Password)})
	}
	if spec.IsDefault && !cur.IsDefault {
		p = append(p, Param{"channel_flag_default", "1"})
	}
	return p
}

// DeleteChannel apaga um canal, mesmo com clientes dentro.
func (c *Client) DeleteChannel(channelID int) error {
	return c.do("channeldelete", Param{"cid", itoa(channelID)}, Param{"force", "1"})
}

func (c *Client) AddServerGroup(databaseID, groupID int) error {
	return c.do("servergroupaddclient", Param{"sgid", itoa(groupID)}, Param{"cldbid", itoa(databaseID)})
}

func (c *Client) RemoveServerGroup(databaseID, groupID int) error {
	return c.do("servergroupdelclient", Param{"sgid", itoa(groupID)}, Param{"cldbid", itoa(databaseID)})
}

// SetChannelGroup define o grupo de canal de um cliente num canal.
func (c *Client) SetChannelGroup(databaseID, channelID, groupID int) error {
	return c.do("setclientchannelgroup", Param{"cgid", itoa(groupID)}, Param{"cid", itoa(channelID)}, Param{"cldbid", itoa(databaseID)})
}

// Permission é uma permissão atribuída a um grupo.
type Permission struct {
	Name    string
	Value   int
	Negated bool
	Skip    bool
}

func groupKey(server bool) (list, notify, add, del, id string) {
	if server {
		return "servergrouppermlist", "notifyservergrouppermlist", "servergroupaddperm", "servergroupdelperm", "sgid"
	}
	return "channelgrouppermlist", "notifychannelgrouppermlist", "channelgroupaddperm", "channelgroupdelperm", "cgid"
}

// GroupPermissions lista as permissões de um grupo de servidor ou de canal.
func (c *Client) GroupPermissions(server bool, groupID int) ([]Permission, error) {
	list, notify, _, _, id := groupKey(server)
	items, err := c.request(BuildCommand(list, Param{id, itoa(groupID)})+" -permsid", notify)
	var se *ServerError
	if errors.As(err, &se) && se.ID == 1281 { // "database empty result set": grupo sem permissões
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	perms := make([]Permission, 0, len(items))
	for _, it := range items {
		if it["permsid"] == "" {
			continue
		}
		perms = append(perms, Permission{Name: it["permsid"], Value: atoi(it["permvalue"]), Negated: it["permnegated"] == "1", Skip: it["permskip"] == "1"})
	}
	return perms, nil
}

func (c *Client) SetGroupPermission(server bool, groupID int, perm Permission) error {
	_, _, add, _, id := groupKey(server)
	params := []Param{{id, itoa(groupID)}, {"permsid", perm.Name}, {"permvalue", itoa(perm.Value)}}
	if server {
		params = append(params, Param{"permnegated", boolParam(perm.Negated)}, Param{"permskip", boolParam(perm.Skip)})
	}
	return c.do(add, params...)
}

func (c *Client) RemoveGroupPermission(server bool, groupID int, name string) error {
	_, _, _, del, id := groupKey(server)
	return c.do(del, Param{id, itoa(groupID)}, Param{"permsid", name})
}

// FileEntry é um arquivo ou pasta de um canal.
type FileEntry struct {
	Name     string
	Size     int64
	Modified time.Time
	IsDir    bool
}

// ListFiles lista os arquivos de uma pasta de um canal.
func (c *Client) ListFiles(channelID int, path, password string) ([]FileEntry, error) {
	items, err := c.request(BuildCommand("ftgetfilelist", Param{"cid", itoa(channelID)}, Param{"cpw", HashPassword(password)}, Param{"path", path}),
		"notifyfilelist", "notifyfilelistfinished")
	var se *ServerError
	if errors.As(err, &se) && se.ID == 1281 { // pasta vazia
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []FileEntry
	for _, it := range items {
		if it["name"] == "" {
			continue
		}
		size, _ := strconv.ParseInt(it["size"], 10, 64)
		ts, _ := strconv.ParseInt(it["datetime"], 10, 64)
		if ts > 1e11 {
			ts /= 1000 // o TS6 informa a data em milissegundos; o TS3, em segundos
		}
		files = append(files, FileEntry{Name: it["name"], Size: size, Modified: time.Unix(ts, 0), IsDir: it["type"] == "0"})
	}
	return files, nil
}

func (c *Client) DeleteFile(channelID int, path, password string) error {
	return c.do("ftdeletefile", Param{"cid", itoa(channelID)}, Param{"cpw", HashPassword(password)}, Param{"name", path})
}

func (c *Client) CreateDirectory(channelID int, path, password string) error {
	return c.do("ftcreatedir", Param{"cid", itoa(channelID)}, Param{"cpw", HashPassword(password)}, Param{"dirname", path})
}

// UploadFile envia um arquivo para um canal (canal 0 = arquivos do servidor).
func (c *Client) UploadFile(channelID int, path, password string, data []byte) error {
	s := c.session()
	if s == nil || !s.connected.Load() {
		return errors.New("não conectado")
	}
	c.ftSem <- struct{}{}
	defer func() { <-c.ftSem }()

	c.ftMu.Lock()
	c.ftNext++
	if c.ftNext == 0 {
		c.ftNext = 1
	}
	id := c.ftNext
	wait := make(chan ftResult, 1)
	c.ftWait[id] = wait
	c.ftMu.Unlock()

	s.sendCommand(BuildCommand("ftinitupload",
		Param{"clientftfid", itoa(int(id))},
		Param{"name", path},
		Param{"cid", itoa(channelID)},
		Param{"cpw", HashPassword(password)},
		Param{"size", itoa(len(data))},
		Param{"overwrite", "1"},
		Param{"resume", "0"},
		Param{"return_code", "ft" + itoa(int(id))},
	))
	var res ftResult
	select {
	case res = <-wait:
	case <-s.done:
		return errors.New("conexão encerrada")
	case <-time.After(ftTimeout):
		c.ftMu.Lock()
		delete(c.ftWait, id)
		c.ftMu.Unlock()
		return errors.New("o servidor não respondeu ao pedido de envio")
	}
	if res.err != nil {
		return res.err
	}

	target, err := transferTarget(s.conn.RemoteAddr(), res)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", target, ftTimeout)
	if err != nil {
		return fmt.Errorf("não foi possível abrir a transferência: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := conn.Write([]byte(res.key)); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("envio interrompido: %w", err)
	}
	// O servidor fecha a conexão quando termina de gravar; esperamos por isso
	// para não devolver antes de o arquivo existir.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	conn.Read(make([]byte, 1))
	return nil
}

// SendWhisper envia um quadro de voz só para os clientes e canais indicados
// (sussurro), em vez do canal atual. opus vazio sinaliza fim da fala.
func (c *Client) SendWhisper(opus []byte, codec byte, clients []uint16, channels []int) {
	s := c.session()
	if s == nil || !s.connected.Load() || len(clients)+len(channels) == 0 || len(clients) > 255 || len(channels) > 255 {
		return
	}
	// [id(2)] [codec] [nº canais] [nº clientes] [canais (8 bytes cada)] [clientes (2 bytes cada)] [opus]
	buf := make([]byte, 5, 5+8*len(channels)+2*len(clients)+len(opus))
	buf[2] = codec
	buf[3] = byte(len(channels))
	buf[4] = byte(len(clients))
	for _, ch := range channels {
		buf = binary.BigEndian.AppendUint64(buf, uint64(ch))
	}
	for _, cl := range clients {
		buf = binary.BigEndian.AppendUint16(buf, cl)
	}
	buf = append(buf, opus...)
	s.sendPacket(ptWhisper, 0, buf)
}

// SetIdentity troca a identidade usada nas próximas conexões.
func (c *Client) SetIdentity(ident *Identity) {
	c.Disconnect()
	c.mu.Lock()
	c.identity = ident
	c.mu.Unlock()
}

// PluginCommand é um comando de plugin: um texto livre que o servidor repassa
// de um cliente a outros, sem interpretar. É o canal que os plugins do
// TeamSpeak usam para conversar entre si.
type PluginCommand struct {
	Name     string // quem fala: cada aplicação usa um nome e ignora os demais
	Data     string
	FromID   uint16
	FromName string
	FromUID  string
}

// SendPluginCommand envia um comando de plugin. Sem alvos, vai para todos os
// clientes do nosso canal; com alvos, só para esses clientes.
func (c *Client) SendPluginCommand(name, data string, targets ...uint16) error {
	params := []Param{{"name", name}, {"data", data}}
	if len(targets) == 0 {
		params = append(params, Param{"targetmode", "0"})
		return c.do("plugincmd", params...)
	}
	// alvos: targetmode=2 e uma lista target=1|target=2…
	cmd := BuildCommand("plugincmd", append(params, Param{"targetmode", "2"})...)
	for i, t := range targets {
		sep := " "
		if i > 0 {
			sep = "|"
		}
		cmd += sep + "target=" + itoa(int(t))
	}
	_, err := c.request(cmd)
	return err
}
