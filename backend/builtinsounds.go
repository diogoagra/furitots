package backend

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"sort"
	"strings"

	"furito-ts/audio"
)

// O pacote de avisos sonoros do próprio app.
//
// Os sons são sintetizados aqui, por código: notas curtas com o timbre de um
// sino macio (o tom fundamental e dois harmônicos que somem mais depressa),
// em sequências que sobem para o que chega ou liga e descem para o que sai ou
// desliga. Não há gravação nenhuma, então não há arquivo de áudio no
// repositório nem direito de terceiros envolvido.
//
// O pacote sai no mesmo formato dos pacotes de sons do TeamSpeak 3 (uma pasta
// com settings.ini e os .wav), que é o que o resto do app já sabe ler: quem
// preferir outros sons importa um pacote nesse formato.

const (
	builtinPackID   = "padrao"
	builtinPackName = "Padrão"
	chimePeak       = 9000.0 // pico de cada aviso (≈ -11 dBFS): presente sem assustar
)

// Notas usadas nos avisos (Hz).
const (
	noteC4  = 261.63
	noteEb4 = 311.13
	noteE4  = 329.63
	noteG4  = 392.00
	noteAb4 = 415.30
	noteC5  = 523.25
	noteD5  = 587.33
	noteE5  = 659.25
	noteG5  = 783.99
	noteA5  = 880.00
	noteD6  = 1174.66
)

// chimeNote é uma nota de um aviso: quando começa (s), o tom (Hz), em quanto
// tempo o som cai a 37% (s) e a intensidade em relação às outras.
type chimeNote struct {
	at, freq, decay, gain float64
}

// chime é um aviso: as notas e os eventos (com os nomes que o protocolo do
// TeamSpeak dá a eles) em que ele toca.
type chime struct {
	file   string
	notes  []chimeNote
	events []string
}

var builtinChimes = []chime{
	// A conexão: três notas, para cima ao entrar e para baixo ao sair.
	{"conectado", []chimeNote{{0, noteC5, 0.14, 1}, {0.095, noteE5, 0.14, 1}, {0.19, noteG5, 0.20, 1}},
		[]string{"CONNECTION_CONNECTED"}},
	{"desconectado", []chimeNote{{0, noteG5, 0.14, 1}, {0.095, noteE5, 0.14, 1}, {0.19, noteC5, 0.20, 1}},
		[]string{"CONNECTION_DISCONNECTED"}},
	// Um trítono descendo, devagar: alguma coisa deu errado.
	{"conexao-perdida", []chimeNote{{0, noteD5, 0.18, 1}, {0.17, noteAb4, 0.20, 1}},
		[]string{"CONNECTION_LOST_CONNECTION"}},

	// Alguém no seu canal: duas notas, subindo para quem chega e descendo para quem sai.
	{"entrou", []chimeNote{{0, noteE5, 0.09, 1}, {0.085, noteA5, 0.12, 1}}, []string{
		"CLIENT_CONNECTION_CONNECTED_CURRENT_CHANNEL",
		"CLIENT_SWITCHED_TO_CURRENT_CHANNEL_APPEARS", "CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS",
		"CLIENT_MOVED_TO_CURRENT_CHANNEL_APPEARS", "CLIENT_MOVED_TO_CURRENT_CHANNEL_STAYS",
		"CLIENT_WAS_KICKED_FROM_CHANNEL_TO_CURRENT_CHANNEL_APPEARS", "CLIENT_WAS_KICKED_FROM_CHANNEL_TO_CURRENT_CHANNEL_STAYS",
	}},
	{"saiu", []chimeNote{{0, noteA5, 0.09, 1}, {0.085, noteE5, 0.12, 1}}, []string{
		"CLIENT_CONNECTION_DISCONNECTED_CURRENT_CHANNEL", "CLIENT_CONNECTION_LOST_CONNECTION_CURRENT_CHANNEL",
		"CLIENT_SWITCHED_FROM_CURRENT_CHANNEL_DISAPPEARS", "CLIENT_SWITCHED_FROM_CURRENT_CHANNEL_STAYS",
		"CLIENT_MOVED_FROM_CURRENT_CHANNEL_DISAPPEARS", "CLIENT_MOVED_FROM_CURRENT_CHANNEL_STAYS",
		"CLIENT_WAS_KICKED_FROM_CHANNEL_FROM_CURRENT_CHANNEL_DISAPPEARS", "CLIENT_WAS_KICKED_FROM_CHANNEL_FROM_CURRENT_CHANNEL_STAYS",
		"CLIENT_WAS_KICKED_FROM_SERVER_CURRENT_CHANNEL", "CLIENT_WAS_BANNED_CURRENT_CHANNEL",
	}},

	// Você mudando de canal: um toque só; dois, se foi outra pessoa que moveu.
	{"troquei-de-canal", []chimeNote{{0, noteD5, 0.11, 1}}, []string{"YOU_SWITCHED_CHANNEL"}},
	{"fui-movido", []chimeNote{{0, noteD5, 0.09, 1}, {0.12, noteD5, 0.13, 1}}, []string{"YOU_WERE_MOVED_TO_DIFFERENT_CHANNEL"}},
	// Expulso ou banido: três notas graves descendo.
	{"expulso", []chimeNote{{0, noteG4, 0.16, 1}, {0.15, noteE4, 0.16, 1}, {0.30, noteC4, 0.20, 1}},
		[]string{"YOU_WERE_KICKED_FROM_CHANNEL", "YOU_WERE_KICKED_FROM_SERVER", "YOU_WERE_BANNED"}},

	// Microfone: um toque curto, grave ao mutar e agudo ao voltar.
	{"microfone-mutado", []chimeNote{{0, noteE4, 0.07, 1}}, []string{"SOUND_CAPTURE_MUTED"}},
	{"microfone-ligado", []chimeNote{{0, noteE5, 0.07, 1}}, []string{"SOUND_CAPTURE_UNMUTED"}},
	// Alto-falantes: dois toques rápidos, descendo ao mutar e subindo ao voltar.
	{"som-mutado", []chimeNote{{0, noteC5, 0.08, 1}, {0.075, noteG4, 0.12, 1}}, []string{"SOUND_PLAYBACK_MUTED"}},
	{"som-ligado", []chimeNote{{0, noteG4, 0.08, 1}, {0.075, noteC5, 0.12, 1}}, []string{"SOUND_PLAYBACK_UNMUTED"}},
	// Ausente e de volta: uma quinta, devagar.
	{"ausente", []chimeNote{{0, noteG5, 0.16, 1}, {0.16, noteC5, 0.18, 1}}, []string{"STATUS_SET_AWAY"}},
	{"presente", []chimeNote{{0, noteC5, 0.16, 1}, {0.16, noteG5, 0.18, 1}}, []string{"STATUS_SET_PRESENT"}},

	// Cutucada: pede atenção, três toques agudos seguidos.
	{"cutucada", []chimeNote{{0, noteA5, 0.06, 1}, {0.11, noteA5, 0.06, 1}, {0.22, noteD6, 0.12, 0.9}},
		[]string{"OTHER_RECEIVED_POKE"}},
	// Erro: duas notas vizinhas juntas, que "batem" uma na outra, duas vezes.
	{"erro", []chimeNote{{0, noteE4, 0.08, 1}, {0, noteEb4, 0.08, 0.8}, {0.14, noteE4, 0.10, 1}, {0.14, noteEb4, 0.10, 0.8}},
		[]string{"SERVER_ERROR", "SERVER_INSUFFICIENT_PERMISSIONS"}},
}

// render sintetiza o aviso em 48 kHz mono.
func (c *chime) render() []int16 {
	const (
		rate    = audio.SampleRate
		attack  = 0.006 // s: entra sem estalo
		release = 0.04  // s: o fim do arquivo chega ao zero
	)
	var length float64
	for _, n := range c.notes {
		// Até a nota cair a -30 dB; o resto é cauda que ninguém ouve e que só
		// atrasaria o próximo aviso (e a volta da transmissão: o motor de áudio
		// não abre o microfone enquanto um aviso toca).
		length = math.Max(length, n.at+3.5*n.decay)
	}
	buf := make([]float64, int(length*rate))
	for _, n := range c.notes {
		start := int(n.at * rate)
		for i := start; i < len(buf); i++ {
			t := float64(i-start) / rate
			env := math.Exp(-t / n.decay)
			if t < attack {
				env *= 0.5 * (1 - math.Cos(math.Pi*t/attack))
			}
			w := 2 * math.Pi * n.freq * t
			// O fundamental e dois harmônicos que somem mais depressa: o
			// começo da nota tem brilho e o resto fica redondo.
			v := math.Sin(w) +
				0.30*math.Sin(2*w)*math.Exp(-t/(0.6*n.decay)) +
				0.10*math.Sin(3*w)*math.Exp(-t/(0.35*n.decay))
			buf[i] += n.gain * env * v
		}
	}
	// Um reflexo fraco e curto, para o som não sair "seco" demais.
	const echoAt, echoGain = 0.043, 0.16
	for i := int(echoAt * rate); i < len(buf); i++ {
		buf[i] += echoGain * buf[i-int(echoAt*rate)]
	}
	var peak float64
	for _, v := range buf {
		peak = math.Max(peak, math.Abs(v))
	}
	out := make([]int16, len(buf))
	tail := int(release * rate)
	for i, v := range buf {
		if left := len(buf) - i; left < tail {
			v *= float64(left) / float64(tail)
		}
		out[i] = int16(math.Round(v / peak * chimePeak))
	}
	return out
}

// wavBytes monta um arquivo WAV (mono, 16 bits, 48 kHz).
func wavBytes(pcm []int16) []byte {
	b := make([]byte, 44+len(pcm)*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], audio.SampleRate)
	binary.LittleEndian.PutUint32(b[28:], audio.SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)*2))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(s))
	}
	return b
}

// BuiltinSoundPacks devolve o pacote de avisos do próprio app, num zip no
// formato que Options.SoundPacksZip espera: soundpacks/padrao/ com o
// settings.ini e um .wav por aviso.
func BuiltinSoundPacks() []byte {
	var ini strings.Builder
	ini.WriteString("[soundpack]\nname = " + builtinPackName + "\n\n[soundfiles]\n")
	files := map[string][]byte{}
	for i := range builtinChimes {
		c := &builtinChimes[i]
		files[c.file+".wav"] = wavBytes(c.render())
		for _, ev := range c.events {
			ini.WriteString(ev + " = play(\"" + c.file + ".wav\")\n")
		}
	}
	files["settings.ini"] = []byte(ini.String())

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names) // sempre na mesma ordem: o zip sai idêntico a cada vez

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(bundledPacksDir + "/" + builtinPackID + "/" + name)
		if err != nil {
			return nil
		}
		w.Write(files[name])
	}
	if zw.Close() != nil {
		return nil
	}
	return buf.Bytes()
}
