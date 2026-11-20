package backend

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"furito-ts/screencap"
	"furito-ts/ts3"
)

// Compartilhamento de tela entre clientes Furito.
//
// O servidor TeamSpeak não transporta vídeo. O que ele transporta são
// "comandos de plugin": textos curtos que repassa de um cliente a outros sem
// interpretar. Por eles os clientes combinam a transmissão (quem está
// compartilhando, quem quer assistir, a oferta e a resposta do WebRTC); o
// vídeo em si vai direto de um computador ao outro (ou por um servidor de
// retransmissão TURN, se configurado), codificado pelo motor de vídeo do
// navegador embutido. Clientes oficiais ignoram esses comandos.
//
// O backend só leva e traz as mensagens; o protocolo e o vídeo ficam na
// interface (frontend/screen.js), que é onde o WebRTC existe.

const (
	screenPluginName = "furito.screen"
	// O servidor entrega comandos de até ~8 mil caracteres (medido no TS6);
	// acima disso ele nem responde.
	maxSignalWire = 7000
	// Tamanho máximo de uma mensagem depois de descomprimida.
	maxSignalPlain = 64 * 1024

	defaultStunURL = "stun:stun.l.google.com:19302"
)

// ScreenSettings são os ajustes do compartilhamento de tela.
type ScreenSettings struct {
	// StunURL descobre o endereço público de cada lado para a conexão direta.
	// "" = o padrão; "none" = nenhum (só funciona na mesma rede).
	StunURL string `json:"stunUrl"`
	// Servidor de retransmissão (TURN), opcional: o vídeo passa por ele quando
	// a conexão direta não é possível — ou sempre, com RelayOnly.
	TurnURL  string `json:"turnUrl"`
	TurnUser string `json:"turnUser"`
	TurnPass string `json:"turnPass"`
	// RelayOnly manda todo o vídeo pelo TURN: os participantes não descobrem
	// o endereço IP uns dos outros.
	RelayOnly bool `json:"relayOnly"`
	// Warned: o usuário já leu o aviso de que a conexão direta revela o IP.
	Warned bool `json:"warned"`
}

// IceServerDTO segue o formato de RTCIceServer do WebRTC.
type IceServerDTO struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// ScreenDTO é o que a interface precisa saber para compartilhar e assistir.
type ScreenDTO struct {
	IceServers []IceServerDTO `json:"iceServers"`
	RelayOnly  bool           `json:"relayOnly"`
	Warned     bool           `json:"warned"`
	// NativeCapture: a captura é feita pelo app (macOS). Sem ela, a interface
	// usa a captura do navegador embutido (Windows).
	NativeCapture bool `json:"nativeCapture"`

	StunURL  string `json:"stunUrl"` // como está nos ajustes, para o formulário
	TurnURL  string `json:"turnUrl"`
	TurnUser string `json:"turnUser"`
	HasTurn  bool   `json:"hasTurn"`
}

// ScreenSignalDTO é uma mensagem de sinalização recebida de outro cliente.
type ScreenSignalDTO struct {
	From     int    `json:"from"`
	FromName string `json:"fromName"`
	Payload  string `json:"payload"`
}

func (s ScreenSettings) dto() ScreenDTO {
	d := ScreenDTO{
		IceServers: []IceServerDTO{}, RelayOnly: s.RelayOnly && s.TurnURL != "", Warned: s.Warned,
		NativeCapture: screencap.Available(),
		StunURL:       s.StunURL, TurnURL: s.TurnURL, TurnUser: s.TurnUser, HasTurn: s.TurnURL != "",
	}
	stun := s.StunURL
	if stun == "" {
		stun = defaultStunURL
	}
	if stun != "none" {
		d.IceServers = append(d.IceServers, IceServerDTO{URLs: []string{stun}})
	}
	if s.TurnURL != "" {
		d.IceServers = append(d.IceServers, IceServerDTO{URLs: []string{s.TurnURL}, Username: s.TurnUser, Credential: s.TurnPass})
	}
	return d
}

func checkIceURL(url string, schemes ...string) error {
	for _, s := range schemes {
		if rest, ok := strings.CutPrefix(url, s+":"); ok && rest != "" && !strings.ContainsAny(rest, " \t\"'<>") {
			return nil
		}
	}
	return errors.New("endereço inválido: use o formato " + schemes[0] + ":servidor:porta")
}

// SetScreenSettings grava os servidores usados pelo compartilhamento de tela.
// turnPass vazio mantém a senha já guardada (a interface nunca a recebe de
// volta); para apagar o TURN, basta esvaziar turnURL.
func (api *API) SetScreenSettings(stunURL, turnURL, turnUser, turnPass string, relayOnly bool) error {
	stunURL, turnURL, turnUser = strings.TrimSpace(stunURL), strings.TrimSpace(turnURL), strings.TrimSpace(turnUser)
	if stunURL != "" && stunURL != "none" {
		if err := checkIceURL(stunURL, "stun", "stuns"); err != nil {
			return errors.New("servidor STUN: " + err.Error())
		}
	}
	if turnURL != "" {
		if err := checkIceURL(turnURL, "turn", "turns"); err != nil {
			return errors.New("servidor de retransmissão: " + err.Error())
		}
	}
	if relayOnly && turnURL == "" {
		return errors.New("para esconder o IP é preciso informar um servidor de retransmissão (TURN)")
	}
	api.app.updateSettings(func(s *Settings) {
		s.Screen.StunURL, s.Screen.TurnURL, s.Screen.TurnUser, s.Screen.RelayOnly = stunURL, turnURL, turnUser, relayOnly
		switch {
		case turnURL == "":
			s.Screen.TurnPass = ""
		case turnPass != "":
			s.Screen.TurnPass = turnPass
		}
	})
	return nil
}

// AcceptScreenWarning registra que o usuário leu o aviso sobre o endereço IP.
func (api *API) AcceptScreenWarning() {
	api.app.updateSettings(func(s *Settings) { s.Screen.Warned = true })
}

// encodeSignal prepara uma mensagem para caber num comando de plugin:
// comprimida quando isso a deixa menor ("z" + base64), senão como está ("j").
func encodeSignal(payload string) (string, error) {
	if payload == "" || len(payload) > maxSignalPlain {
		return "", errors.New("mensagem de sinalização vazia ou grande demais")
	}
	wire := "j" + payload
	var buf bytes.Buffer
	if w, err := flate.NewWriter(&buf, flate.BestCompression); err == nil {
		w.Write([]byte(payload))
		w.Close()
		if z := "z" + base64.RawStdEncoding.EncodeToString(buf.Bytes()); len(z) < len(wire) {
			wire = z
		}
	}
	if len(wire) > maxSignalWire {
		return "", errors.New("mensagem de sinalização grande demais para o servidor")
	}
	return wire, nil
}

func decodeSignal(wire string) (string, bool) {
	if len(wire) < 2 || len(wire) > maxSignalWire+1024 {
		return "", false
	}
	switch wire[0] {
	case 'j':
		return wire[1:], true
	case 'z':
		raw, err := base64.RawStdEncoding.DecodeString(wire[1:])
		if err != nil {
			return "", false
		}
		// limite na descompressão: quem manda é outro cliente, não confiável
		out, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(raw)), maxSignalPlain+1))
		if err != nil || len(out) == 0 || len(out) > maxSignalPlain {
			return "", false
		}
		return string(out), true
	}
	return "", false
}

// ScreenSend envia uma mensagem de sinalização a um cliente, ou a todos do
// canal quando toClientID é 0.
func (api *API) ScreenSend(toClientID int, payload string) error {
	wire, err := encodeSignal(payload)
	if err != nil {
		return err
	}
	if toClientID == 0 {
		return api.app.client.SendPluginCommand(screenPluginName, wire)
	}
	if toClientID < 0 || toClientID > 0xFFFF {
		return errors.New("cliente inválido")
	}
	return api.app.client.SendPluginCommand(screenPluginName, wire, uint16(toClientID))
}

func (a *App) onPluginCommand(cmd ts3.PluginCommand) {
	if cmd.Name != screenPluginName {
		return
	}
	a.mu.Lock()
	blocked := a.settings.Contacts[cmd.FromUID] == "blocked"
	a.mu.Unlock()
	if blocked {
		return
	}
	payload, ok := decodeSignal(cmd.Data)
	if !ok {
		return
	}
	a.emit("screen:signal", ScreenSignalDTO{From: int(cmd.FromID), FromName: cmd.FromName, Payload: payload})
}

// ---------------------------------------------------------------- captura nativa

// ScreenDisplays lista as telas que a captura nativa pode compartilhar.
func (api *API) ScreenDisplays() ([]screencap.Display, error) {
	return screencap.Displays()
}

// ScreenCaptureStart começa a capturar uma tela (0 = a principal), reduzida
// para caber em maxWidth×maxHeight. Os quadros saem por ScreenFrameHandler.
func (api *API) ScreenCaptureStart(displayID, maxWidth, maxHeight, fps int) error {
	if displayID < 0 {
		// Padrão de teste: quadros gerados, sem tocar na tela.
		if maxWidth <= 0 || maxHeight <= 0 {
			maxWidth, maxHeight = 1280, 720
		}
		return screencap.StartTestPattern(maxWidth, maxHeight, fps)
	}
	return screencap.Start(uint32(displayID), maxWidth, maxHeight, fps)
}

// ScreenCaptureStop encerra a captura nativa.
func (api *API) ScreenCaptureStop() { screencap.Stop() }

// ScreenFramePath é o endereço, dentro da janela, de onde a interface busca
// os quadros da captura nativa.
const ScreenFramePath = "/__screen/frame.jpg"

// ScreenFrameHandler serve os quadros capturados: GET ?after=N devolve o
// quadro mais recente com número maior que N (esperando um pouco por ele), ou
// 204 se não chegou nenhum.
func (a *App) ScreenFrameHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ScreenFramePath {
			http.NotFound(w, r)
			return
		}
		after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		w.Header().Set("Cache-Control", "no-store")
		fr, ok := screencap.Next(after, 500*time.Millisecond)
		if !ok {
			if !screencap.Running() {
				w.Header().Set("X-Screen-Ended", "1")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-Frame-Seq", strconv.FormatUint(fr.Seq, 10))
		w.Header().Set("Content-Length", strconv.Itoa(len(fr.JPEG)))
		w.Write(fr.JPEG)
	})
}
