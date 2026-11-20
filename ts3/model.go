package ts3

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Channel é um canal do servidor.
type Channel struct {
	ID              int
	ParentID        int
	Order           int // id do canal anterior no mesmo pai (0 = primeiro)
	Name            string
	Topic           string
	Description     string
	DescriptionSet  bool // a descrição já foi pedida ao servidor
	Codec           int
	CodecQuality    int
	MaxClients      int // -1 = ilimitado
	IsDefault       bool
	HasPassword     bool
	IsPermanent     bool
	IsSemiPermanent bool
	NeededTalkPower int
	IconID          uint32 // 0 = sem ícone
}

// GroupInfo é um grupo de servidor ou de canal.
type GroupInfo struct {
	Name   string
	IconID uint32 // 0 = sem ícone; 100–600 = ícones padrão do TeamSpeak
	SortID int
	Type   int // 0 = modelo, 1 = normal (atribuível), 2 = ServerQuery
	// NameMode diz se o nome do grupo aparece junto do apelido na árvore:
	// 0 = não, 1 = antes ("[Grupo] Apelido"), 2 = depois ("Apelido [Grupo]").
	NameMode int
}

// ClientInfo é um cliente visível no servidor.
type ClientInfo struct {
	ID             uint16
	ChannelID      int
	Nickname       string
	UID            string
	DatabaseID     int
	Type           int // 0 = cliente normal, 1 = ServerQuery
	InputMuted     bool
	OutputMuted    bool
	Away           bool
	AwayMessage    string
	TalkPower      int
	IsRecording    bool
	Country        string
	ServerGroups   []int
	ChannelGroupID int
	Version        string
	Platform       string
	MetaData       string // texto livre que o cliente informa ao entrar (client_meta_data)
	Description    string
	ConnectedSince time.Time // zero = desconhecido

	IconID             uint32
	Avatar             string // marca do avatar (o MD5 do arquivo); "" = sem avatar
	InputHardware      bool   // microfone presente/ativado
	OutputHardware     bool   // saída de som presente/ativada
	IsChannelCommander bool
	IsPrioritySpeaker  bool
	TalkRequest        bool
	TalkRequestMessage string
	IsTalker           bool // recebeu a palavra num canal moderado
}

// ServerInfo são os dados do servidor virtual.
type ServerInfo struct {
	Name           string
	WelcomeMessage string
	Version        string
	Platform       string
	MaxClients     int
	UID            string
	BannerURL      string // imagem do banner
	BannerLink     string // endereço aberto ao clicar no banner
}

// OrderedChannel é um canal já posicionado na árvore.
type OrderedChannel struct {
	Channel Channel
	Depth   int
}

// Snapshot é uma cópia consistente do estado do servidor.
type Snapshot struct {
	Connected     bool
	Server        ServerInfo
	SelfID        uint16
	SelfChannelID int
	Channels      []OrderedChannel // em ordem de exibição (profundidade primeiro)
	Clients       []ClientInfo     // ordenados por apelido
	ServerGroups  map[int]GroupInfo
	ChannelGroups map[int]GroupInfo
}

// TextMessage é uma mensagem de texto recebida.
type TextMessage struct {
	TargetMode  int // 1 = privada, 2 = canal, 3 = servidor
	Target      int
	InvokerID   int
	InvokerName string
	InvokerUID  string
	Text        string
}

// ClientEvent conta uma movimentação de cliente de forma estruturada (é ao que
// as automações do usuário reagem): alguém conectou ou saiu do servidor,
// entrou ou saiu do nosso canal, ou nós mesmos mudamos de canal.
type ClientEvent struct {
	Kind    string     // ClientConnected, ClientDisconnected, ClientJoined, ClientLeft ou SelfMoved
	Client  ClientInfo // quem (como estava no momento)
	From    int        // canal de origem (0 = acabou de conectar)
	To      int        // canal de destino (0 = saiu do servidor)
	Reason  string     // connected, switched, moved, kicked, disconnected, lost, server-kick ou banned
	Invoker string     // quem moveu, expulsou ou baniu, quando houver
}

const (
	ClientConnected    = "connected"    // conectou ao servidor (num canal que enxergamos)
	ClientDisconnected = "disconnected" // saiu do servidor
	ClientJoined       = "joined"       // entrou no nosso canal
	ClientLeft         = "left"         // saiu do nosso canal
	SelfMoved          = "self"         // nós mudamos de canal
)

// moveReason traduz o reasonid de uma troca de canal.
func moveReason(id string) string {
	switch id {
	case "1":
		return "moved"
	case "4":
		return "kicked"
	}
	return "switched"
}

// Limites do que o cliente guarda de um servidor. Servidores de verdade têm
// centenas de canais e dezenas de grupos; um mal-intencionado mandaria
// milhões, até a memória acabar (e a árvore de canais, que é percorrida em
// profundidade, estourar a pilha).
const (
	maxChannels = 20000
	maxGroups   = 5000
)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// iconID lê um id de ícone. É um CRC32 sem sinal, mas alguns servidores o
// enviam como inteiro com sinal.
func iconID(s string) uint32 {
	n, _ := strconv.ParseInt(s, 10, 64)
	if n == -1 || n == 0xFFFFFFFF {
		return 0 // alguns servidores usam -1 para "sem ícone"
	}
	return uint32(n)
}

func applyChannelFields(ch *Channel, it map[string]string) {
	for k, v := range it {
		switch k {
		case "cpid", "pid":
			ch.ParentID = atoi(v)
		case "channel_order":
			ch.Order = atoi(v)
		case "channel_name":
			ch.Name = v
		case "channel_topic":
			ch.Topic = v
		case "channel_description":
			ch.Description = v
			ch.DescriptionSet = true
		case "channel_codec":
			ch.Codec = atoi(v)
		case "channel_codec_quality":
			ch.CodecQuality = atoi(v)
		case "channel_flag_default":
			ch.IsDefault = v == "1"
		case "channel_flag_password":
			ch.HasPassword = v == "1"
		case "channel_flag_permanent":
			ch.IsPermanent = v == "1"
		case "channel_flag_semi_permanent":
			ch.IsSemiPermanent = v == "1"
		case "channel_needed_talk_power":
			ch.NeededTalkPower = atoi(v)
		case "channel_icon_id":
			ch.IconID = iconID(v)
		}
	}
	// maxclients depende de dois campos; só recalcula quando algum veio.
	_, hasMax := it["channel_maxclients"]
	unl, hasUnl := it["channel_flag_maxclients_unlimited"]
	if hasMax {
		ch.MaxClients = atoi(it["channel_maxclients"])
	}
	if hasUnl && unl == "1" {
		ch.MaxClients = -1
	}
}

func applyClientFields(cl *ClientInfo, it map[string]string) {
	for k, v := range it {
		switch k {
		case "client_nickname":
			cl.Nickname = v
		case "client_unique_identifier":
			cl.UID = v
		case "client_database_id":
			cl.DatabaseID = atoi(v)
		case "client_type":
			cl.Type = atoi(v)
		case "client_input_muted":
			cl.InputMuted = v == "1"
		case "client_output_muted":
			cl.OutputMuted = v == "1"
		case "client_away":
			cl.Away = v == "1"
		case "client_away_message":
			cl.AwayMessage = v
		case "client_talk_power":
			cl.TalkPower = atoi(v)
		case "client_is_recording":
			cl.IsRecording = v == "1"
		case "client_country":
			cl.Country = v
		case "client_servergroups":
			cl.ServerGroups = cl.ServerGroups[:0]
			for _, g := range strings.Split(v, ",") {
				if g != "" {
					cl.ServerGroups = append(cl.ServerGroups, atoi(g))
				}
			}
		case "client_channel_group_id":
			cl.ChannelGroupID = atoi(v)
		case "client_version":
			cl.Version = v
		case "client_platform":
			cl.Platform = v
		case "client_meta_data":
			cl.MetaData = v
		case "client_description":
			cl.Description = v
		case "client_icon_id":
			cl.IconID = iconID(v)
		case "client_flag_avatar":
			cl.Avatar = v
		case "client_input_hardware":
			cl.InputHardware = v == "1"
		case "client_output_hardware":
			cl.OutputHardware = v == "1"
		case "client_is_channel_commander":
			cl.IsChannelCommander = v == "1"
		case "client_is_priority_speaker":
			cl.IsPrioritySpeaker = v == "1"
		case "client_talk_request":
			cl.TalkRequest = v != "" && v != "0"
		case "client_talk_request_msg":
			cl.TalkRequestMessage = v
		case "client_is_talker":
			cl.IsTalker = v == "1"
		}
	}
}

// orderChannels monta a árvore na ordem de exibição. Em cada pai os canais
// formam uma lista encadeada por channel_order (id do irmão anterior).
func orderChannels(chans map[int]*Channel) []OrderedChannel {
	children := make(map[int][]*Channel)
	for _, ch := range chans {
		parent := ch.ParentID
		if _, ok := chans[parent]; !ok {
			parent = 0 // pai desconhecido: mostra na raiz em vez de sumir
		}
		children[parent] = append(children[parent], ch)
	}

	sortSiblings := func(list []*Channel) []*Channel {
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		byPrev := make(map[int][]*Channel, len(list))
		ids := make(map[int]bool, len(list))
		for _, ch := range list {
			ids[ch.ID] = true
		}
		for _, ch := range list {
			prev := ch.Order
			if !ids[prev] {
				prev = 0
			}
			byPrev[prev] = append(byPrev[prev], ch)
		}
		out := make([]*Channel, 0, len(list))
		seen := make(map[int]bool, len(list))
		var walk func(prev int)
		walk = func(prev int) {
			for _, ch := range byPrev[prev] {
				if seen[ch.ID] {
					continue
				}
				seen[ch.ID] = true
				out = append(out, ch)
				walk(ch.ID)
			}
		}
		walk(0)
		for _, ch := range list { // ciclos/cadeias quebradas
			if !seen[ch.ID] {
				out = append(out, ch)
			}
		}
		return out
	}

	var out []OrderedChannel
	visited := make(map[int]bool, len(chans))
	var dfs func(parent, depth int)
	dfs = func(parent, depth int) {
		for _, ch := range sortSiblings(children[parent]) {
			if visited[ch.ID] {
				continue
			}
			visited[ch.ID] = true
			out = append(out, OrderedChannel{Channel: *ch, Depth: depth})
			dfs(ch.ID, depth+1)
		}
	}
	dfs(0, 0)
	return out
}
