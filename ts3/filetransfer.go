package ts3

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Transferência de arquivos: o pedido vai pelo canal de comandos; o servidor
// responde com uma chave e uma porta TCP, onde o arquivo é entregue depois de
// enviarmos a chave.

const (
	ftTimeout = 8 * time.Second
	ftMaxIcon = 4 << 20   // ícones: nada legítimo passa de alguns MB
	ftMaxFile = 256 << 20 // arquivos de canal (o arquivo é carregado na memória)
)

type ftResult struct {
	key  string
	port int
	size int64
	ip   string
	err  error
}

func (c *Client) ftDeliver(id uint16, r ftResult) {
	c.ftMu.Lock()
	ch := c.ftWait[id]
	delete(c.ftWait, id)
	c.ftMu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// ServerError é uma recusa do servidor a um pedido, com o código dele.
type ServerError struct {
	ID      int
	Message string
	Extra   string
}

func (e *ServerError) Error() string {
	if e.Extra != "" {
		return e.Message + " (" + e.Extra + ")"
	}
	return e.Message
}

// Flood indica que o servidor recusou por excesso de comandos em sequência.
func (e *ServerError) Flood() bool { return e.ID == 524 }

// Transient indica uma recusa passageira (excesso de comandos ou de
// transferências simultâneas): vale tentar de novo depois.
func (e *ServerError) Transient() bool {
	return e.Flood() || strings.Contains(strings.ToLower(e.Message), "limit reached")
}

// IsTransient diz se um erro de transferência pode dar certo numa nova
// tentativa (recusa passageira do servidor, tempo esgotado, queda de rede),
// ao contrário de "arquivo não existe".
func IsTransient(err error) bool {
	var se *ServerError
	if errors.As(err, &se) {
		return se.Transient()
	}
	return err != nil
}

// DownloadFile baixa um arquivo do servidor (canal 0 = arquivos do próprio
// servidor, como os ícones).
//
// As transferências são feitas uma de cada vez e com um intervalo entre os
// pedidos: servidores têm proteção contra excesso de comandos, e dezenas de
// pedidos simultâneos (os ícones de um servidor grande) são recusados. Se
// ainda assim o servidor reclamar, espera e tenta de novo.
func (c *Client) DownloadFile(channelID int, path, channelPassword string) ([]byte, error) {
	return c.download(channelID, path, channelPassword, ftMaxFile)
}

func (c *Client) download(channelID int, path, channelPassword string, maxSize int64) ([]byte, error) {
	c.ftSem <- struct{}{}
	defer func() { <-c.ftSem }()

	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		if wait := time.Until(c.ftLast.Add(c.ftPace)); wait > 0 {
			time.Sleep(wait)
		}
		data, err := c.downloadOnce(channelID, path, channelPassword, maxSize)
		c.ftLast = time.Now()
		if err == nil {
			return data, nil
		}
		lastErr = err
		var se *ServerError
		if !errors.As(err, &se) || !se.Transient() {
			return nil, err
		}
		// O servidor pediu calma: reduz o ritmo pelo resto da sessão e espera.
		c.ftFloods.Add(1)
		if c.ftPace < time.Second {
			c.ftPace *= 2
		}
		time.Sleep(time.Duration(attempt+1) * 1500 * time.Millisecond)
	}
	return nil, lastErr
}

func (c *Client) downloadOnce(channelID int, path, channelPassword string, maxSize int64) ([]byte, error) {
	s := c.session()
	if s == nil || !s.connected.Load() {
		return nil, errors.New("não conectado")
	}

	c.ftMu.Lock()
	c.ftNext++
	if c.ftNext == 0 {
		c.ftNext = 1
	}
	id := c.ftNext
	wait := make(chan ftResult, 1)
	c.ftWait[id] = wait
	c.ftMu.Unlock()

	s.sendCommand(BuildCommand("ftinitdownload",
		Param{"clientftfid", strconv.Itoa(int(id))},
		Param{"name", path},
		Param{"cid", strconv.Itoa(channelID)},
		Param{"cpw", HashPassword(channelPassword)},
		Param{"seekpos", "0"},
		Param{"return_code", "ft" + strconv.Itoa(int(id))},
	))

	var res ftResult
	select {
	case res = <-wait:
	case <-s.done:
		return nil, errors.New("conexão encerrada")
	case <-time.After(ftTimeout):
		c.ftMu.Lock()
		delete(c.ftWait, id)
		c.ftMu.Unlock()
		return nil, errors.New("o servidor não respondeu ao pedido do arquivo")
	}
	if res.err != nil {
		return nil, res.err
	}
	if res.size < 0 || res.size > maxSize {
		return nil, fmt.Errorf("arquivo grande demais (%d bytes)", res.size)
	}

	target, err := transferTarget(s.conn.RemoteAddr(), res)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", target, ftTimeout)
	if err != nil {
		return nil, fmt.Errorf("não foi possível abrir a transferência: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * ftTimeout))

	if _, err := conn.Write([]byte(res.key)); err != nil {
		return nil, err
	}
	// A memória cresce com o que chega de verdade, não com o tamanho que o
	// servidor declarou (que pode ser 256 MB sem que ele mande um byte).
	data, err := io.ReadAll(io.LimitReader(conn, res.size))
	if err != nil || int64(len(data)) != res.size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("transferência interrompida: %w", err)
	}
	return data, nil
}

// transferTarget confere a resposta do servidor a um pedido de transferência
// e devolve o endereço TCP em que ela acontece. O endereço, a porta e a chave
// vêm do servidor, e o cliente abre uma conexão e escreve a chave nela sem
// mais ninguém conferir — por isso:
//
//   - a chave tem de ter a cara de uma chave (letras e números): com bytes
//     livres, o servidor escreveria o que quisesse (um pedido HTTP, um comando
//     de outro protocolo) em qualquer serviço;
//   - o endereço é o do próprio servidor de voz, a não ser que ele indique
//     outro — e um servidor da internet não pode apontar para dentro da rede
//     do usuário (o roteador, outro computador, o próprio computador).
func transferTarget(voice net.Addr, res ftResult) (string, error) {
	if res.port < 1 || res.port > 65535 {
		return "", errors.New("o servidor indicou uma porta de transferência inválida")
	}
	if !validTransferKey(res.key) {
		return "", errors.New("o servidor mandou uma chave de transferência inválida")
	}
	var server net.IP
	if addr, ok := voice.(*net.UDPAddr); ok {
		server = addr.IP
	}
	if server == nil {
		return "", errors.New("endereço do servidor desconhecido")
	}
	host := server
	if ip := net.ParseIP(res.ip); ip != nil && !ip.IsUnspecified() && !ip.Equal(server) {
		if ipReach(ip) < ipReach(server) {
			return "", errors.New("o servidor indicou um endereço de transferência dentro da sua rede; a transferência foi recusada")
		}
		host = ip
	}
	return net.JoinHostPort(host.String(), strconv.Itoa(res.port)), nil
}

// validTransferKey: as chaves de transferência do TeamSpeak são 32 letras e
// números; aceita-se uma folga, nunca espaços, quebras de linha ou controles.
func validTransferKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '+' || c == '/' || c == '=' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ipReach classifica um endereço por até onde ele alcança: 0 = o próprio
// computador, 1 = a rede local (e faixas reservadas), 2 = a internet.
func ipReach(ip net.IP) int {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		switch {
		case v4[0] == 0, v4[0] >= 240: // "esta rede" e faixas reservadas
			return 1
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64.0.0/10, da operadora
			return 1
		case v4[0] == 198 && v4[1]&0xfe == 18, v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // testes e uso especial
			return 1
		}
	}
	switch {
	case ip.IsLoopback():
		return 0
	case ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsUnspecified():
		return 1
	}
	return 2
}

// AvatarPath devolve onde o servidor guarda o avatar do cliente com esse uid:
// "/avatar_" seguido de cada byte do uid (que circula em base64) escrito com
// duas letras de 'a' a 'p'. Vale para os uids de 20 bytes do TeamSpeak 3 e
// para os de 32 bytes do TeamSpeak 6.
func AvatarPath(uid string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(uid)
	if err != nil || len(raw) == 0 {
		return "", errors.New("identificador de cliente inválido")
	}
	name := make([]byte, 0, len(raw)*2)
	for _, b := range raw {
		name = append(name, 'a'+b>>4, 'a'+b&0x0f)
	}
	return "/avatar_" + string(name), nil
}

// DownloadAvatar baixa o avatar do cliente com esse uid.
func (c *Client) DownloadAvatar(uid string) ([]byte, error) {
	path, err := AvatarPath(uid)
	if err != nil {
		return nil, err
	}
	return c.download(0, path, "", ftMaxIcon)
}

// SetAvatar define o nosso avatar (uma imagem) ou, com data vazio, tira o que
// houver. O arquivo vai para o servidor e os outros clientes ficam sabendo
// pela marca client_flag_avatar, que é o MD5 dele.
func (c *Client) SetAvatar(data []byte) error {
	if len(data) == 0 {
		if err := c.do("clientupdate", Param{"client_flag_avatar", ""}); err != nil {
			return err
		}
		c.do("ftdeletefile", Param{"cid", "0"}, Param{"cpw", ""}, Param{"name", "/avatar"}) // se já não existir, tanto faz
		return nil
	}
	if err := c.UploadFile(0, "/avatar", "", data); err != nil {
		return err
	}
	sum := md5.Sum(data)
	return c.do("clientupdate", Param{"client_flag_avatar", hex.EncodeToString(sum[:])})
}

// DownloadIcon baixa um ícone do servidor pelo id.
func (c *Client) DownloadIcon(id uint32) ([]byte, error) {
	return c.download(0, "/icon_"+strconv.FormatUint(uint64(id), 10), "", ftMaxIcon)
}
