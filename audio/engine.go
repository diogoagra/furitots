// Package audio é o motor de voz: captura e reprodução nativas, Opus, buffer
// de jitter por cliente, tratamento do microfone (supressão de ruído, ganho
// automático, detecção de voz), vozes modificadas e soundboard.
package audio

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/hraban/opus.v2"
)

const (
	SampleRate = 48000
	FrameSize  = 960 // 20 ms

	minPrebuffer     = 2 * FrameSize          // 40 ms acumulados antes de tocar uma fala
	maxPrebuffer     = 6 * FrameSize          // teto do buffer adaptativo (120 ms)
	maxQueueSamples  = 25 * FrameSize         // 500 ms: acima disso a fila é encurtada
	trimQueueSamples = 4 * FrameSize          // tamanho da fila após encurtar
	maxLoopback      = 10 * FrameSize         // 200 ms de áudio local na fila
	lookahead        = 3                      // quadros (60 ms) que o transmissor "vê à frente"
	preRollFrames    = 8                      // 160 ms guardados: o começo da palavra, antes de a voz ser confirmada
	speechTail       = 6                      // 120 ms após a voz: fim de palavra, consoantes finais
	maxTailFrames    = 30                     // 600 ms: até onde o fim da palavra pode ir além da voz, enquanto houver som
	edgeSustain      = 0.3                    // nas bordas da palavra: abaixo disso o quadro é um ataque ou um estalo, não um som que dura
	tailMaxRise      = 3.0                    // no fim da palavra: o nível subir mais que isso em 20 ms é um estalo, não a palavra continuando
	steadySNR        = 8.0                    // dB acima do ruído para um som sem tom contar como sílaba
	steadyLevel      = 0.4                    // idem: menor/maior nível ao longo de 80 ms
	onsetHissSNR     = 6.0                    // modo neural, consoante que abre a frase: dB acima do ruído nos agudos…
	onsetHissHigh    = 0.6                    // …e fração da energia acima de 3 kHz (ver pick)
	confirmMinPeak   = 0.5                    // para abrir: o quadro com voz mais forte, em relação ao mais forte dos últimos 80 ms
	hangoverFrames   = 15                     // 300 ms sem fala antes de encerrar a transmissão
	levelHangover    = 25                     // modo por volume: 500 ms de cauda após cair do limiar
	farAway          = 1 << 20                // "faz muito tempo" para os contadores de quadros
	localGap         = SampleRate / 8         // 125 ms de pausa depois de cada aviso sonoro
	localHoldFrames  = 18                     // 360 ms: tempo para o som do aviso sumir do microfone
	maxNoticeFrames  = 500                    // 10 s: o máximo de um aviso que entra na comparação com o microfone
	maxLocalQueue    = 4                      // avisos esperando a vez; além disso são descartados
	fadeSamples      = 240                    // 5 ms de rampa ao abrir o gate (ao fechar, o quadro inteiro)
	catchUpPerFrame  = FrameSize / 4          // atraso recuperado por quadro: a fala sai até 25% mais rápida
	catchUpMinLag    = SampleRate / 400       // 2,5 ms: menor período de tom removível
	catchUpMaxLag    = SampleRate * 15 / 1000 // 15 ms: maior período de tom removível
	catchUpWindow    = SampleRate / 100       // 10 ms comparados para achar o período
	catchUpMatch     = 0.9                    // semelhança mínima entre dois períodos para fundi-los
	streamIdle       = 60 * time.Second
	// maxStreams limita quantas vozes são acompanhadas ao mesmo tempo. O número
	// de quem fala vem do servidor, a cada pacote: sem limite, um servidor
	// mal-intencionado criaria dezenas de milhares de decodificadores em
	// segundos. Ao passar do limite sai a voz calada há mais tempo (se ela
	// voltar a falar, recomeça como uma voz nova).
	maxStreams   = 128
	stableFrames = 500 // 10 s sem falha para o buffer adaptativo encolher
	DefaultVAD   = 600.0
	// Sensibilidade da detecção de voz (SetVADFloor): com a fala em andamento
	// (até 600 ms depois da voz) o piso cai à metade, para uma palavra dita mais
	// baixo no meio da frase não ficar de fora.
	floorRecent = 0.5
	// Calibração (CalibrateVoice): o nível da voz é o percentil 75 dos quadros
	// com voz — se a TV falar nas pausas de quem calibra, não puxa a medida para
	// baixo — e o piso fica 12 dB abaixo dele. É preciso 1 s de voz.
	calibPercentile = 0.75
	calibRatio      = 0.25
	calibMinFrames  = 50

	VADAuto  = "auto"  // detecção de voz (ruído e teclado não abrem o microfone)
	VADLevel = "level" // abre quando o volume passa do limiar
	VADPTT   = "ptt"   // apertar para falar: transmite enquanto a tecla está apertada

	pttRelease = 5 // quadros (100 ms) transmitidos depois de soltar a tecla

	// Nivelamento das vozes recebidas (SetNormalizeVoices). Alvo um pouco
	// abaixo do microfone local; sobe no máximo 8× (+18 dB) quem chega baixo e
	// desce até 0,4× (−8 dB) quem chega alto. Só mede quadros que passam do
	// silêncio e ficam 8 dB acima do fundo da própria pessoa: o ruído de quem
	// deixa o microfone aberto calado não é tomado por uma voz baixa.
	rxLevelTarget = 3000
	rxMinGain     = 0.4
	rxMaxGain     = 8
	rxMeasureMin  = 20  // −64 dBFS
	rxOverFloor   = 2.5 // +8 dB
	rxGainFall    = 0.25
	rxWarmup      = 50 // 1 s de voz medida para achar o nível de quem acabou de começar a falar

	// MaxClientGain é o volume máximo por pessoa (SetClientGain): 4× = 400%.
	MaxClientGain = 4.0
)

// voiceKey identifica uma voz recebida. O número do cliente só vale dentro de
// uma conexão: em dois servidores, pessoas diferentes têm o mesmo número.
type voiceKey struct {
	conn uint32
	id   uint16
}

type stream struct {
	dec   *opus.Decoder // usado só pela goroutine de rede da conexão dele
	lev   *leveler      // idem
	floor floorTracker  // idem
	lim   *limiter      // idem: o ganho do nivelamento não estoura a amostra de 16 bits

	// protegidos por Engine.rxMu:
	pcm       []int16
	playing   bool
	ending    bool
	talking   bool
	prebuffer int
	stable    int
	seen      time.Time // quando chegou o último pacote aproveitado (para a limpeza dos fluxos parados)

	// usados só pela goroutine de rede da conexão dele:
	lastSeq    uint16
	hasSeq     bool
	lastPacket time.Time
}

// Engine concentra o processamento de áudio. Os dispositivos ficam em
// devices.go; o núcleo aqui não depende de hardware.
type Engine struct {
	mu              sync.Mutex
	muted           bool
	deafened        bool
	effect          VoiceEffect
	vadMode         string
	vadThreshold    float64
	vadFloor        float64 // detecção de voz: nível mínimo para abrir o microfone (0 = sem piso)
	volume          float64
	noiseMode       string // NoiseOff, NoiseClassic ou NoiseNeural
	autoGain        bool
	normalizeVoices bool
	micTest         bool

	encoder    *opus.Encoder // voz
	sbEncoder  *opus.Encoder // quadros com soundboard: modo música, mais taxa
	soundboard *SoundboardManager
	sendVoice  func(opusData []byte)

	// transmissão (só a goroutine txLoop usa)
	vp         *VoiceProcessor
	fx         *EffectProcessor
	txLimiter  *limiter
	ring       []txSlot  // quadros aguardando decisão (o mais antigo primeiro)
	hist       []txSlot  // últimos quadros não transmitidos: de onde sai o começo da palavra
	outq       []float64 // áudio já decidido, esperando a vez de ser enviado
	outPeak    float64   // nível recente da fala enviada (para reconhecer trechos fracos)
	credit     int       // amostras que a recuperação de atraso ainda pode remover
	seq        int       // número do próximo quadro do microfone
	sinceVoice int       // quadros desde a última voz confirmada
	sinceHiss  int       // quadros desde a última consoante de sopro transmitida
	sinceOut   int       // quadros de silêncio enviados desde o último áudio de verdade
	tailAlive  bool      // o som que veio depois da voz ainda não parou
	fricative  bool      // no meio de um "s"/"f"/"ch"
	prevPass   bool
	floor      float64                                    // o piso de sensibilidade em vigor neste quadro (0 fora da detecção de voz)
	onset      bool                                       // modo neural: o trecho de fala começou e a rede ainda não reconheceu a voz
	plainMix   float64                                    // modo neural: quanto do último quadro enviado era o áudio clássico (0 a 1; -1 = trecho começando)
	onQueue    func(seq int, whole bool, audio []float64) // testes: quadro do microfone posto na fila de envio
	txOn       bool
	opusBuf    []byte
	micCh      chan []int16
	stop       chan struct{}
	stopOnce   sync.Once
	localHold  atomic.Int32 // quadros em que um aviso sonoro ainda pode estar no microfone
	// O microfone ouve os avisos sonoros? Com caixas de som, sim; com fone de
	// ouvido, não — e aí segurar a transmissão durante o aviso só cortaria o
	// começo da nossa fala. Aprendido a cada aviso tocado (learnCoupling).
	coupling  atomic.Int32
	refEnv    []float64     // volume do aviso por quadro de 20 ms, no relógio da saída (lbMu)
	refAcc    float64       // (lbMu)
	refN      int           // (lbMu)
	noticeMic []float64     // volume bruto do microfone por quadro durante o aviso (só txLoop)
	noticeHit int           // quadros do aviso em que o detector de voz reagiu (só txLoop)
	noticeLog atomic.Value  // string: as medidas do último aviso avaliado (diagnóstico)
	ptt       atomic.Bool   // tecla de apertar-para-falar apertada
	micLevel  atomic.Uint64 // float64 em bits
	voiceProb atomic.Uint64 // idem
	txTalking atomic.Bool

	// medida da voz para a sensibilidade (CalibrateVoice)
	calMu sync.Mutex
	cal   *calibration // a medida em andamento, ou nil

	// para qual conexão vai o que transmitimos (SetTxConn)
	txMu   sync.Mutex
	txConn uint32
	txSend bool // false: nada é entregue a sendVoice
	txSent bool // o trecho de fala em andamento já teve um quadro entregue

	// recepção
	rxMu        sync.Mutex
	streams     map[voiceKey]*stream
	clientGain  map[voiceKey]float64 // volume por pessoa (1 = normal)
	clientMuted map[voiceKey]bool    // mutado só para nós
	connMuted   map[uint32]bool      // conexões silenciadas por inteiro (SetConnMuted)
	mixBuf      []float64
	recBuf      []float64 // as vozes da conexão em gravação (só fillPlayback usa)
	rxLimiter   *limiter

	// gravação da conversa (recorder.go)
	recMu      sync.Mutex
	rec        *recorder
	recTx      []float64 // o que transmitimos e ainda não entrou na gravação
	recLimiter *limiter

	lbMu     sync.Mutex
	loopback []int16
	locals   [][]int16 // fila de avisos sonoros (só locais); o primeiro está tocando

	dev devices
}

// NewEngine cria o motor. sendVoice recebe cada quadro Opus de 20 ms; uma
// chamada com slice vazio marca o fim da fala.
func NewEngine(sendVoice func(opusData []byte)) (*Engine, error) {
	e, err := newEngine(sendVoice)
	if err != nil {
		return nil, err
	}
	go e.txLoop()
	return e, nil
}

func newEngine(sendVoice func(opusData []byte)) (*Engine, error) {
	enc, err := opus.NewEncoder(SampleRate, 1, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("falha ao criar o codificador Opus: %w", err)
	}
	// Voz em banda cheia a 64 kbit/s com o esforço máximo do codificador; a
	// correção de erros embutida deixa o receptor reconstruir pacotes perdidos.
	enc.SetBitrate(64000)
	enc.SetComplexity(10)
	enc.SetInBandFEC(true)
	enc.SetPacketLossPerc(8)

	// O modo de voz do Opus estraga música e efeitos sonoros; os quadros que
	// levam soundboard usam um codificador de áudio geral.
	sbEnc, err := opus.NewEncoder(SampleRate, 1, opus.AppAudio)
	if err != nil {
		return nil, fmt.Errorf("falha ao criar o codificador Opus: %w", err)
	}
	sbEnc.SetBitrate(96000)
	sbEnc.SetComplexity(10)

	e := &Engine{
		effect:          EffectNormal,
		vadMode:         VADAuto,
		vadThreshold:    DefaultVAD,
		volume:          1.0,
		noiseMode:       NoiseNeural,
		autoGain:        true,
		normalizeVoices: true,
		encoder:         enc,
		sbEncoder:       sbEnc,
		soundboard:      NewSoundboardManager(),
		sendVoice:       sendVoice,
		vp:              NewVoiceProcessor(),
		fx:              NewEffectProcessor(),
		txLimiter:       newLimiter(30000, 80),
		sinceVoice:      farAway,
		sinceHiss:       farAway,
		sinceOut:        farAway,
		opusBuf:         make([]byte, 1500),
		micCh:           make(chan []int16, 16),
		stop:            make(chan struct{}),
		txSend:          true,
		streams:         make(map[voiceKey]*stream),
		clientGain:      make(map[voiceKey]float64),
		clientMuted:     make(map[voiceKey]bool),
		connMuted:       make(map[uint32]bool),
		rxLimiter:       newLimiter(30000, 120),
	}
	return e, nil
}

// Close encerra o motor e libera os dispositivos.
func (e *Engine) Close() {
	e.stopOnce.Do(func() { close(e.stop) })
	e.StopDevices()
	e.StopRecording() // fecha o arquivo de uma gravação em andamento
}

func (e *Engine) Soundboard() *SoundboardManager { return e.soundboard }

// ---------------------------------------------------------------- ajustes

func (e *Engine) SetEffect(effect VoiceEffect) {
	if findEffect(effect) == nil {
		effect = EffectNormal
	}
	e.mu.Lock()
	e.effect = effect
	e.mu.Unlock()
}

func (e *Engine) Effect() VoiceEffect {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.effect
}

func (e *Engine) SetVADThreshold(v float64) {
	if v < 0 {
		v = 0
	}
	e.mu.Lock()
	e.vadThreshold = v
	e.mu.Unlock()
}

func (e *Engine) VADThreshold() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vadThreshold
}

// SetVADMode escolhe como o microfone abre: VADAuto ou VADLevel.
func (e *Engine) SetVADMode(mode string) {
	if mode != VADLevel && mode != VADPTT {
		mode = VADAuto
	}
	e.mu.Lock()
	e.vadMode = mode
	e.mu.Unlock()
}

func (e *Engine) VADMode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vadMode
}

// SetVADFloor define a sensibilidade da detecção de voz: o nível (na escala do
// medidor do microfone) que a voz precisa atingir para abrir o microfone. O que
// fica abaixo — a TV, alguém conversando no cômodo, um ruído distante — não
// abre, por mais que pareça voz. Zero desliga o piso. Só vale no modo VADAuto;
// depois de aberto, o microfone segue a fala normalmente (sílabas fracas e
// finais de frase passam).
func (e *Engine) SetVADFloor(v float64) {
	switch {
	case !(v > 0): // inclui NaN
		v = 0
	case v < 50:
		v = 50
	case v > 8000:
		v = 8000
	}
	e.mu.Lock()
	e.vadFloor = v
	e.mu.Unlock()
}

func (e *Engine) VADFloor() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vadFloor
}

// calibration junta o nível dos quadros com voz enquanto a pessoa fala.
type calibration struct {
	levels []float64
}

// ErrNoVoice: a calibração não ouviu voz suficiente para medir.
var ErrNoVoice = errors.New("não ouvi voz suficiente para calibrar")

// BeginCalibration começa a medir a voz de quem fala ao microfone. Enquanto a
// medida dura nada é transmitido: quem calibra fala sozinho.
func (e *Engine) BeginCalibration() {
	e.calMu.Lock()
	e.cal = &calibration{}
	e.calMu.Unlock()
}

// EndCalibration encerra a medida e devolve o nível da voz e o piso de
// sensibilidade sugerido para ela (SetVADFloor). Sem pelo menos 1 s de voz
// devolve ErrNoVoice.
func (e *Engine) EndCalibration() (voice, floor float64, err error) {
	e.calMu.Lock()
	cal := e.cal
	e.cal = nil
	e.calMu.Unlock()
	if cal == nil || len(cal.levels) < calibMinFrames {
		return 0, 0, ErrNoVoice
	}
	sort.Float64s(cal.levels)
	voice = cal.levels[int(calibPercentile*float64(len(cal.levels)-1))]
	floor = math.Min(8000, math.Max(50, math.Round(voice*calibRatio)))
	return voice, floor, nil
}

// CalibrateVoice mede a voz durante d (BeginCalibration … EndCalibration). A
// captura precisa estar ligada.
func (e *Engine) CalibrateVoice(d time.Duration) (voice, floor float64, err error) {
	e.BeginCalibration()
	select {
	case <-time.After(d):
	case <-e.stop:
	}
	return e.EndCalibration()
}

// SetNoiseMode escolhe a supressão de ruído do microfone: NoiseNeural
// (RNNoise, o padrão), NoiseClassic (espectral) ou NoiseOff.
func (e *Engine) SetNoiseMode(mode string) {
	if mode != NoiseOff && mode != NoiseClassic {
		mode = NoiseNeural
	}
	e.mu.Lock()
	e.noiseMode = mode
	e.mu.Unlock()
}

func (e *Engine) NoiseMode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.noiseMode
}

func (e *Engine) SetAutoGain(on bool) {
	e.mu.Lock()
	e.autoGain = on
	e.mu.Unlock()
}

func (e *Engine) AutoGain() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.autoGain
}

// SetNormalizeVoices liga o nivelamento do volume das vozes recebidas.
func (e *Engine) SetNormalizeVoices(on bool) {
	e.mu.Lock()
	e.normalizeVoices = on
	e.mu.Unlock()
}

func (e *Engine) NormalizeVoices() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.normalizeVoices
}

// SetMicTest faz a própria voz (já tratada e com efeito) tocar localmente.
func (e *Engine) SetMicTest(on bool) {
	e.mu.Lock()
	e.micTest = on
	e.mu.Unlock()
}

func (e *Engine) MicTest() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.micTest
}

// SetVolume ajusta o volume de saída (0 a 100).
func (e *Engine) SetVolume(percent float64) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	e.mu.Lock()
	e.volume = percent / 100
	e.mu.Unlock()
}

func (e *Engine) Volume() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.volume * 100
}

func (e *Engine) SetMuted(m bool) {
	e.mu.Lock()
	e.muted = m
	e.mu.Unlock()
}

func (e *Engine) Muted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.muted
}

// SetDeafened muta os alto-falantes: além das vozes, calam-se os avisos
// sonoros (os que estavam na fila são descartados) e o retorno do soundboard.
func (e *Engine) SetDeafened(d bool) {
	// lbMu junto: um aviso que chega neste instante ou vê o estado novo ou é
	// descartado com a fila (ver queueLocal).
	e.lbMu.Lock()
	e.mu.Lock()
	e.deafened = d
	e.mu.Unlock()
	if d {
		e.locals = nil
	}
	e.lbMu.Unlock()
	if d {
		e.rxMu.Lock()
		e.streams = make(map[voiceKey]*stream)
		e.rxMu.Unlock()
		e.clearLoopback()
	}
}

func (e *Engine) Deafened() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deafened
}

// SetBitrate ajusta a taxa do codificador (bits por segundo).
func (e *Engine) SetBitrate(bps int) {
	e.mu.Lock()
	e.encoder.SetBitrate(bps)
	e.mu.Unlock()
}

// SetPTT informa se a tecla de apertar-para-falar está apertada.
func (e *Engine) SetPTT(down bool) { e.ptt.Store(down) }

// SetClientGain ajusta o volume de uma pessoa só para nós (1 = normal, 2 =
// dobro, até MaxClientGain). conn é a conexão em que ela está.
func (e *Engine) SetClientGain(conn uint32, id uint16, gain float64) {
	if !(gain >= 0) { // negativo ou NaN
		gain = 0
	}
	if gain > MaxClientGain {
		gain = MaxClientGain
	}
	key := voiceKey{conn, id}
	e.rxMu.Lock()
	if gain == 1 {
		delete(e.clientGain, key)
	} else {
		e.clientGain[key] = gain
	}
	e.rxMu.Unlock()
}

// SetClientMuted silencia (ou volta a ouvir) uma pessoa só para nós.
func (e *Engine) SetClientMuted(conn uint32, id uint16, muted bool) {
	key := voiceKey{conn, id}
	e.rxMu.Lock()
	if muted {
		e.clientMuted[key] = true
	} else {
		delete(e.clientMuted, key)
	}
	e.rxMu.Unlock()
}

// SetConnMuted silencia uma conexão inteira ("silenciar esta aba"): as vozes
// dela deixam de tocar e os pacotes que chegam são descartados, como acontece
// em todas com os alto-falantes mutados. Os ajustes por pessoa ficam.
func (e *Engine) SetConnMuted(conn uint32, muted bool) {
	e.rxMu.Lock()
	if muted {
		e.connMuted[conn] = true
		e.dropStreamsLocked(conn)
	} else {
		delete(e.connMuted, conn)
	}
	e.rxMu.Unlock()
}

// ConnMuted diz se a conexão está silenciada (SetConnMuted).
func (e *Engine) ConnMuted(conn uint32) bool {
	e.rxMu.Lock()
	defer e.rxMu.Unlock()
	return e.connMuted[conn]
}

// SetTxConn diz para qual conexão vai o que transmitimos. Com on = false nada
// é entregue a sendVoice (nem o soundboard) e Transmitting fica falso; o
// medidor do microfone, o teste de microfone e a calibração seguem
// funcionando. Não é o microfone mutado: Muted não muda.
//
// Se um trecho de fala estava sendo entregue, o fim dele (sendVoice com slice
// vazio) é entregue aqui dentro, antes da troca: quem roteia o callback pela
// conexão deve mudar o destino só depois de SetTxConn voltar. O callback não
// pode chamar SetTxConn, e quem chama SetTxConn não pode estar segurando uma
// trava que o callback também toma.
func (e *Engine) SetTxConn(conn uint32, on bool) {
	e.txMu.Lock()
	defer e.txMu.Unlock()
	if e.txConn == conn && e.txSend == on {
		return
	}
	if e.txSent {
		e.txSent = false
		e.txTalking.Store(false)
		e.sendVoice(nil) // o destino antigo fica sabendo que paramos de falar
	}
	e.txConn, e.txSend = conn, on
}

// txTarget devolve o destino atual da transmissão.
func (e *Engine) txTarget() (conn uint32, on bool) {
	e.txMu.Lock()
	defer e.txMu.Unlock()
	return e.txConn, e.txSend
}

// deliver entrega um quadro codificado ao destino da transmissão; opusData
// vazio encerra o trecho de fala. Só a goroutine txLoop chama.
func (e *Engine) deliver(opusData []byte) {
	e.txMu.Lock()
	defer e.txMu.Unlock()
	if len(opusData) == 0 {
		e.txTalking.Store(false)
		if e.txSent {
			e.txSent = false
			e.sendVoice(nil) // avisa os outros que paramos de falar
		}
		return
	}
	if !e.txSend {
		return
	}
	e.txSent = true
	e.txTalking.Store(true)
	e.sendVoice(opusData)
}

// MicLevel é o RMS do último quadro do microfone, já sem ruído (mesma escala
// do limiar do modo por volume).
func (e *Engine) MicLevel() float64 { return math.Float64frombits(e.micLevel.Load()) }

// VoiceProbability vai de 0 a 1: o quanto o detector está vendo voz agora.
func (e *Engine) VoiceProbability() float64 { return math.Float64frombits(e.voiceProb.Load()) }

// Transmitting indica se estamos enviando voz agora.
func (e *Engine) Transmitting() bool { return e.txTalking.Load() }

// ---------------------------------------------------------------- recepção

// PushVoice recebe um pacote de voz do cliente from da conexão conn. Cada
// conexão deve chamar sempre da mesma goroutine (a de rede dela); conexões
// diferentes podem chamar ao mesmo tempo. opusData vazio = fim da fala.
func (e *Engine) PushVoice(conn uint32, from uint16, seq uint16, opusData []byte) {
	e.mu.Lock()
	deafened, normalize := e.deafened, e.normalizeVoices
	e.mu.Unlock()
	if deafened {
		return
	}
	now := time.Now()
	key := voiceKey{conn, from}

	e.rxMu.Lock()
	if e.connMuted[conn] {
		e.rxMu.Unlock()
		return
	}
	st := e.streams[key]
	if st == nil {
		dec, err := opus.NewDecoder(SampleRate, 1)
		if err != nil {
			e.rxMu.Unlock()
			return
		}
		lev := newLeveler(rxLevelTarget, rxMinGain, rxMaxGain)
		lev.fall, lev.warmup = rxGainFall, rxWarmup
		st = &stream{dec: dec, lev: lev, lim: newLimiter(30000, 80), prebuffer: minPrebuffer}
		e.streams[key] = st
		// Aproveita para limpar quem sumiu, de qualquer conexão. Só se olha o
		// que a trava protege (seen): os outros fluxos podem estar sendo
		// alimentados agora mesmo pela goroutine de outra conexão.
		var oldest voiceKey
		found := false
		for k, other := range e.streams {
			if k == key {
				continue
			}
			if now.Sub(other.seen) > streamIdle {
				delete(e.streams, k)
			} else if !found || other.seen.Before(e.streams[oldest].seen) {
				oldest, found = k, true
			}
		}
		if found && len(e.streams) > maxStreams {
			delete(e.streams, oldest)
		}
	}
	e.rxMu.Unlock()

	if len(opusData) == 0 {
		st.hasSeq = false
		e.rxMu.Lock()
		st.ending = true
		e.rxMu.Unlock()
		return
	}

	var decoded []int16
	if st.hasSeq {
		gap := int16(seq - st.lastSeq)
		if gap <= 0 && now.Sub(st.lastPacket) < time.Second {
			return // repetido ou atrasado demais
		}
		if gap > 1 && gap <= 5 {
			// Pacotes perdidos: os mais antigos o decodificador disfarça
			// (PLC); o imediatamente anterior é reconstruído a partir da
			// cópia de baixa taxa que vem dentro deste pacote (FEC).
			for i := int16(2); i < gap; i++ {
				plc := make([]int16, FrameSize)
				if st.dec.DecodePLC(plc) == nil {
					decoded = append(decoded, plc...)
				}
			}
			fec := make([]int16, FrameSize)
			if st.dec.DecodeFEC(opusData, fec) == nil {
				decoded = append(decoded, fec...)
			}
		}
	}

	pcm := make([]int16, FrameSize*6)
	n, err := st.dec.Decode(opusData, pcm)
	if err != nil || n == 0 {
		return
	}
	decoded = append(decoded, pcm[:n]...)
	st.lastSeq, st.hasSeq, st.lastPacket = seq, true, now

	if normalize {
		buf := make([]float64, len(decoded))
		toFloat(buf, decoded)
		lvl := rmsFloat(buf)
		floor := st.floor.update(lvl)
		st.lev.run(buf, lvl > rxMeasureMin && lvl > rxOverFloor*floor)
		st.lim.run(buf)
		toInt16(decoded, buf)
	}

	e.rxMu.Lock()
	st.ending = false
	st.seen = now
	st.pcm = append(st.pcm, decoded...)
	if len(st.pcm) > maxQueueSamples {
		// Atrasou demais (travada local ou rajada): descarta o mais antigo
		// para a latência não ficar acumulada.
		drop := len(st.pcm) - trimQueueSamples
		copy(st.pcm, st.pcm[drop:])
		st.pcm = st.pcm[:trimQueueSamples]
	}
	if st.stable++; st.stable >= stableFrames && st.prebuffer > minPrebuffer {
		st.prebuffer -= FrameSize // rede estável: volta a reduzir a latência
		st.stable = 0
	}
	e.rxMu.Unlock()
}

// ResetStreams esquece o que veio da conexão conn: o áudio recebido pendente
// e os ajustes por pessoa (volume e mudo), já que os números de cliente dela
// deixam de valer. As outras conexões não são tocadas.
func (e *Engine) ResetStreams(conn uint32) {
	e.rxMu.Lock()
	e.dropStreamsLocked(conn)
	for k := range e.clientGain {
		if k.conn == conn {
			delete(e.clientGain, k)
		}
	}
	for k := range e.clientMuted {
		if k.conn == conn {
			delete(e.clientMuted, k)
		}
	}
	e.rxMu.Unlock()
	e.clearLoopback()
}

// dropStreamsLocked descarta o áudio recebido pendente de uma conexão.
// Chamar com rxMu travado.
func (e *Engine) dropStreamsLocked(conn uint32) {
	for k := range e.streams {
		if k.conn == conn {
			delete(e.streams, k)
		}
	}
}

// clearLoopback descarta o retorno local pendente (teste de microfone e
// soundboard).
func (e *Engine) clearLoopback() {
	e.lbMu.Lock()
	e.loopback = e.loopback[:0]
	e.lbMu.Unlock()
}

// TalkingClients devolve os clientes da conexão conn cujo áudio está tocando
// agora.
func (e *Engine) TalkingClients(conn uint32) []uint16 {
	e.rxMu.Lock()
	defer e.rxMu.Unlock()
	var out []uint16
	for k, st := range e.streams {
		if k.conn == conn && st.talking {
			out = append(out, k.id)
		}
	}
	return out
}

// TalkingConns devolve, em ordem crescente, as conexões em que o áudio de
// alguém está tocando agora.
func (e *Engine) TalkingConns() []uint32 {
	e.rxMu.Lock()
	defer e.rxMu.Unlock()
	var out []uint32
	for k, st := range e.streams {
		if st.talking && !slices.Contains(out, k.conn) {
			out = append(out, k.conn)
		}
	}
	slices.Sort(out)
	return out
}

// fillPlayback preenche out com a mixagem das vozes + áudio local.
// É chamado pelo dispositivo de saída, no relógio da placa de som.
func (e *Engine) fillPlayback(out []int16) {
	n := len(out)
	if cap(e.mixBuf) < n {
		e.mixBuf = make([]float64, n)
	}
	mix := e.mixBuf[:n]
	for i := range mix {
		mix[i] = 0
	}

	e.mu.Lock()
	volume, deafened := e.volume, e.deafened
	e.mu.Unlock()

	// Gravação em andamento: as vozes da conexão gravada são somadas à parte.
	var recMix []float64
	recConn, recording := e.recordingConn()
	if recording {
		if cap(e.recBuf) < n {
			e.recBuf = make([]float64, n)
		}
		recMix = e.recBuf[:n]
		for i := range recMix {
			recMix[i] = 0
		}
	}

	e.rxMu.Lock()
	for key, st := range e.streams {
		if !st.playing {
			if len(st.pcm) >= st.prebuffer || (st.ending && len(st.pcm) > 0) {
				st.playing = true
			} else {
				st.talking = false
				continue
			}
		}
		k := n
		if len(st.pcm) < k {
			k = len(st.pcm)
		}
		if !deafened && !e.clientMuted[key] {
			gain := 1.0
			if g, ok := e.clientGain[key]; ok {
				gain = g
			}
			for i := 0; i < k; i++ {
				mix[i] += float64(st.pcm[i]) * gain
			}
			if recording && key.conn == recConn {
				for i := 0; i < k; i++ {
					recMix[i] += float64(st.pcm[i]) * gain
				}
			}
		}
		copy(st.pcm, st.pcm[k:])
		st.pcm = st.pcm[:len(st.pcm)-k]
		st.talking = k > 0
		if len(st.pcm) == 0 {
			if !st.ending && st.prebuffer < maxPrebuffer {
				// A fila secou no meio da fala: a rede está oscilando mais
				// que o buffer aguenta. Acumula um pouco mais da próxima vez.
				st.prebuffer += FrameSize
				st.stable = 0
			}
			st.playing = false
			st.ending = false
		}
	}
	e.rxMu.Unlock()

	if recording {
		e.recordPlayback(recMix, recConn) // só as vozes: avisos e teste de microfone não entram
	}

	e.lbMu.Lock()
	k := n
	if len(e.loopback) < k {
		k = len(e.loopback)
	}
	for i := 0; i < k; i++ {
		mix[i] += float64(e.loopback[i])
	}
	copy(e.loopback, e.loopback[k:])
	e.loopback = e.loopback[:len(e.loopback)-k]
	// Avisos sonoros: um de cada vez, na ordem em que chegaram. Enquanto um
	// toca (e um pouco depois), o transmissor é avisado: o que sai do
	// alto-falante volta pelo microfone.
	if len(e.locals) > 0 {
		e.localHold.Store(localHoldFrames)
	}
	for pos := 0; pos < n && len(e.locals) > 0; {
		clip := e.locals[0]
		m := n - pos
		if len(clip) < m {
			m = len(clip)
		}
		for i := 0; i < m; i++ {
			v := float64(clip[i])
			mix[pos+i] += v
			e.refAcc += v * v
			if e.refN++; e.refN == FrameSize {
				if len(e.refEnv) < maxNoticeFrames {
					e.refEnv = append(e.refEnv, math.Sqrt(e.refAcc/FrameSize))
				}
				e.refAcc, e.refN = 0, 0
			}
		}
		pos += m
		if m == len(clip) {
			e.locals = e.locals[1:]
		} else {
			e.locals[0] = clip[m:]
		}
	}
	e.lbMu.Unlock()

	// Várias pessoas falando juntas somam acima do limite: o limitador baixa
	// o volume no pico em vez de distorcer.
	e.rxLimiter.run(mix)
	for i := range mix {
		mix[i] *= volume
	}
	toInt16(out, mix)
}

// PlayLocal toca um áudio só nos alto-falantes locais (avisos sonoros); não é
// transmitido. Os avisos entram numa fila e tocam um de cada vez, com uma
// pequena pausa entre eles. Com a fila cheia, ou com os alto-falantes mutados,
// o novo aviso é descartado.
func (e *Engine) PlayLocal(pcm []int16) { e.queueLocal(pcm, false) }

// PlayLocalDeafened é o PlayLocal que toca mesmo com os alto-falantes
// mutados: serve para o aviso de que o som acabou de ser mutado.
func (e *Engine) PlayLocalDeafened(pcm []int16) { e.queueLocal(pcm, true) }

func (e *Engine) queueLocal(pcm []int16, evenDeafened bool) {
	if len(pcm) == 0 {
		return
	}
	clip := make([]int16, len(pcm)+localGap)
	copy(clip, pcm)
	e.lbMu.Lock()
	defer e.lbMu.Unlock()
	if !evenDeafened && e.Deafened() {
		return
	}
	if len(e.locals) < maxLocalQueue {
		e.locals = append(e.locals, clip)
	}
}

func (e *Engine) pushLoopback(samples []int16) {
	e.lbMu.Lock()
	if len(e.loopback) < maxLoopback {
		e.loopback = append(e.loopback, samples...)
	}
	e.lbMu.Unlock()
}

// ---------------------------------------------------------------- transmissão

// pushMic entrega um quadro de 20 ms do microfone (chamado pelo dispositivo
// de captura). Nunca bloqueia: se a transmissão atrasar, o quadro é perdido.
func (e *Engine) pushMic(frame []int16) {
	select {
	case e.micCh <- frame:
	default:
	}
}

func (e *Engine) txLoop() {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var lastMic time.Time
	for {
		select {
		case <-e.stop:
			return
		case frame := <-e.micCh:
			lastMic = time.Now()
			e.txFrame(frame)
		case <-ticker.C:
			// Sem microfone ativo o soundboard ainda precisa de um relógio.
			if time.Since(lastMic) > 60*time.Millisecond {
				e.txFrame(nil)
			}
		}
	}
}

func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// txSlot é um quadro já tratado, esperando a decisão de transmitir ou não.
type txSlot struct {
	seq       int
	voice     []float64
	plain     []float64 // modo neural: o mesmo quadro pela supressão clássica (ver pick)
	sure      bool      // modo neural: a rede tem certeza de que é voz e deixa o som passar
	unheard   bool      // modo neural: a rede não ouve fala neste quadro (um chiado assim é ruído, não consoante)
	voiced    bool      // o detector viu voz neste quadro
	sound     bool      // há som acima do ruído (voz ou não)
	pitched   bool      // tem tom de voz, sem o nível ter se sustentado ainda
	confirmed bool      // faz parte de um trecho de voz sustentado (não é um estalo)
	level     float64
	snr       float64
	highSNR   float64 // dB acima do ruído nos agudos (4–10 kHz)
	high      float64
	sustain   float64
	rise      float64
}

// txFrame processa 20 ms de transmissão. mic pode ser nil (sem captura).
//
// A decisão sobre cada quadro é tomada 60 ms depois de ele chegar, quando já
// se sabe o que veio em seguida. É isso que permite distinguir fala de
// teclado: uma batida de tecla até parece voz por um ou dois quadros, mas
// some logo; a fala se sustenta. Fora da fala nada é transmitido, nem durante
// as pausas entre palavras.
//
// A voz só é confirmada depois de a palavra já ter começado (a consoante
// inicial não tem tom), então os últimos quadros não transmitidos ficam
// guardados: quando a fala é confirmada, a transmissão começa no ponto em que
// o som começou de verdade. Esse trecho a mais entra numa fila e o atraso é
// recuperado em seguida (catchUp), sem ninguém perceber.
func (e *Engine) txFrame(mic []int16) {
	e.mu.Lock()
	muted, effect, mode, threshold := e.muted, e.effect, e.vadMode, e.vadThreshold
	noise, autoGain, micTest, deafened := e.noiseMode, e.autoGain, e.micTest, e.deafened
	e.floor = 0
	if mode == VADAuto {
		e.floor = e.vadFloor
	}
	e.mu.Unlock()

	sb, sbLocal := e.soundboard.GetNextChunk(FrameSize)
	if deafened {
		sbLocal = false // alto-falantes mutados: o soundboard segue para os outros, sem o retorno local
	}

	// O tratamento roda em todo quadro, mesmo sem transmitir: é assim que o
	// estimador de ruído aprende o ambiente.
	var cur *txSlot
	if mic != nil {
		slot := txSlot{seq: e.seq, voice: make([]float64, FrameSize)}
		e.seq++
		toFloat(slot.voice, mic)
		feat := e.vp.Process(slot.voice, noise, autoGain)
		slot.voiced, slot.sound, slot.level, slot.snr, slot.high, slot.sustain = feat.Speech, feat.Sound, feat.Level, feat.SNR, feat.HighRatio, feat.Sustain
		slot.rise, slot.pitched, slot.highSNR = feat.Rise, feat.Pitched, feat.HighSNR
		if noise == NoiseNeural {
			slot.plain, slot.sure = append([]float64(nil), e.vp.Plain()...), feat.Sure
			slot.unheard = feat.Neural < neuralHeard
		}

		// Calibração em andamento: guarda o nível dos quadros com voz e segura a
		// transmissão (o detector segue rodando, é dele que vem a medida).
		e.calMu.Lock()
		if e.cal != nil {
			muted = true
			if feat.Speech {
				e.cal.levels = append(e.cal.levels, feat.Level)
			}
		}
		e.calMu.Unlock()

		e.micLevel.Store(math.Float64bits(feat.Level))
		prob := 0.6 * e.VoiceProbability()
		if feat.Speech {
			prob += 0.4
		}
		e.voiceProb.Store(math.Float64bits(prob))

		// Um aviso sonoro está saindo pelo alto-falante: o microfone o capta,
		// e como os avisos do TeamSpeak são uma voz falando, pareceria fala
		// nossa. Enquanto ele toca, nada começa a ser transmitido; só continua
		// o que já estava sendo falado antes. Todas as medidas do quadro são
		// zeradas, não só a de voz: o "s" de "sound resumed" abre o microfone
		// pelo caminho das consoantes de sopro, que olha chiado e não tom.
		if e.localHold.Load() > 0 {
			left := e.localHold.Add(-1)
			if len(e.noticeMic) < maxNoticeFrames+localHoldFrames {
				e.noticeMic = append(e.noticeMic, rms(mic))
			}
			if feat.Speech || (feat.SNR >= 6 && feat.HighRatio >= 0.45 && feat.Sustain >= 0.3) {
				e.noticeHit++
			}
			if e.coupling.Load() != couplingSilent && mode != VADPTT && !e.alreadySpeaking() {
				slot.voiced, slot.sound, slot.pitched = false, false, false
				slot.level, slot.snr, slot.highSNR, slot.high, slot.sustain = 0, 0, 0, 0, 0
			}
			if left <= 0 {
				e.learnCoupling()
			}
		}

		if !muted {
			e.ring = append(e.ring, slot)
			switch mode {
			case VADLevel:
				e.ring[len(e.ring)-1].confirmed = slot.level >= threshold
			case VADPTT:
				e.ring[len(e.ring)-1].confirmed = e.ptt.Load()
			default:
				e.confirmSpeech()
			}
			if len(e.ring) > lookahead {
				first := e.ring[0]
				copy(e.ring, e.ring[1:])
				e.ring = e.ring[:len(e.ring)-1]
				cur = &first
			}
		}
	} else {
		e.micLevel.Store(0)
		e.voiceProb.Store(0)
	}
	if mic == nil || muted {
		e.ring, e.hist, e.outq = e.ring[:0], e.hist[:0], e.outq[:0]
		e.sinceVoice, e.sinceOut, e.sinceHiss = farAway, farAway, farAway
		e.fricative, e.tailAlive, e.prevPass, e.onset = false, false, false, false
	}

	pass := false
	if cur != nil {
		pass = e.decide(cur, mode)
		e.enqueue(cur, pass, mode)
	}

	hang := hangoverFrames
	if mode == VADLevel || mode == VADPTT {
		hang = 0 // nesses modos a cauda já está na decisão
	}
	if def := findEffect(effect); def != nil {
		hang += def.tail // deixa o eco/reverb terminar de soar
	}

	var frame []float64
	switch {
	case len(e.outq) >= FrameSize || (len(e.outq) > 0 && !pass):
		e.catchUp()
		frame = make([]float64, FrameSize)
		n := copy(frame, e.outq)
		e.outq = e.outq[:copy(e.outq, e.outq[n:])]
		e.outPeak = math.Max(0.98*e.outPeak, rmsFloat(frame))
		e.sinceOut = 0
	case cur != nil && e.sinceOut < hang:
		// Pausa entre palavras: a transmissão continua aberta, em silêncio.
		e.sinceOut++
		frame = make([]float64, FrameSize)
	}

	if frame == nil && sb == nil {
		if e.txOn {
			e.txOn = false
			e.deliver(nil) // avisa os outros que paramos de falar
		}
		return
	}
	if frame != nil && !e.txOn {
		e.fx.Reset() // começa o efeito do zero, sem restos da fala anterior
	}
	e.emit(frame, sb, effect, sbLocal, micTest)
}

// enqueue põe na fila de envio o quadro que acabou de ser decidido. No começo
// de um trecho de fala entram antes dele os quadros guardados em que o som já
// tinha começado; no fim, o primeiro quadro fora da fala entra sumindo aos
// poucos, para o som não ser cortado seco.
func (e *Engine) enqueue(cur *txSlot, pass bool, mode string) {
	add := func(s *txSlot, whole bool, next ...*txSlot) []float64 {
		from := len(e.outq)
		e.outq = append(e.outq, e.pick(s, next...)...)
		if e.onQueue != nil {
			e.onQueue(s.seq, whole, e.outq[from:])
		}
		return e.outq[from:]
	}
	switch {
	case pass:
		from := len(e.outq)
		if !e.prevPass {
			e.onset, e.plainMix = true, -1
			n := 0
			if mode != VADPTT { // apertar para falar: nada de antes da tecla
				n = e.preRoll()
			}
			for i := len(e.hist) - n; i < len(e.hist); i++ {
				next := cur
				if i+1 < len(e.hist) {
					next = &e.hist[i+1]
				}
				add(&e.hist[i], true, next)
			}
			e.hist = e.hist[:0]
		}
		if len(e.ring) > 0 {
			add(cur, true, &e.ring[0])
		} else {
			add(cur, true)
		}
		if !e.prevPass {
			fade(e.outq[from:], true, false)
		}
	case e.prevPass:
		tail := add(cur, false)
		for i := range tail { // meia onda de cosseno: desce suave até o zero
			tail[i] *= 0.5 * (1 + math.Cos(math.Pi*(float64(i)+0.5)/float64(len(tail))))
		}
	default:
		if len(e.hist) == preRollFrames {
			copy(e.hist, e.hist[1:])
			e.hist = e.hist[:preRollFrames-1]
		}
		e.hist = append(e.hist, *cur)
	}
	e.prevPass = pass
}

// pick devolve o áudio de um quadro que vai ser transmitido; next é o quadro
// que vem depois dele, se já se sabe qual é. No modo neural o áudio é o do
// RNNoise, com uma exceção: a rede só reconhece a fala quando a vogal chega e,
// até lá, trata a consoante de sopro que abre a frase ("s", "ch", "f") como
// ruído e a apaga ("Chega" vira "ega"). Por isso, do começo de cada trecho até
// a rede reconhecer a voz, os quadros que são um chiado agudo que dura — e o
// ataque dele, com o nível ainda subindo — saem com o áudio da supressão
// clássica. Só eles: o ambiente, as teclas e os cliques que entram junto com o
// começo da frase saem pelo RNNoise, como todo o resto. A troca entre as duas
// versões é feita ao longo de um quadro, sem degrau.
func (e *Engine) pick(s *txSlot, next ...*txSlot) []float64 {
	mix := 0.0
	if e.onset && s.plain != nil {
		hissy := func(s *txSlot) bool { return s.highSNR >= onsetHissSNR && s.high >= onsetHissHigh }
		lasting := func(s *txSlot) bool { return hissy(s) && s.sustain >= edgeSustain }
		switch {
		case s.sure:
			e.onset = false
		case lasting(s):
			mix = 1
		case hissy(s) && len(next) > 0 && !next[0].sure && lasting(next[0]) && next[0].level >= s.level:
			mix = 1 // o ataque do chiado; um clique antes dele é mais forte que o que vem depois
		}
	}
	from := e.plainMix
	if from < 0 {
		from = mix
	}
	e.plainMix = mix
	if from == 0 && mix == 0 {
		return s.voice
	}
	out := make([]float64, len(s.voice))
	for i := range out {
		m := from + (mix-from)*(float64(i)+0.5)/float64(len(out))
		out[i] = s.voice[i] + m*(s.plain[i]-s.voice[i])
	}
	return out
}

// preRoll diz quantos dos quadros guardados pertencem à palavra que acabou de
// ser confirmada: os que têm som sem interrupção até ela, mais um de ambiente
// para a entrada ser suave.
//
// Um som de fala se sustenta ("s", "f", "m", a vogal crescendo); só o ataque
// dele, um ou dois quadros, é um nível que muda de repente. Uma sequência de
// teclas é ataque atrás de ataque: no terceiro quadro assim a busca para, e a
// digitação de antes da palavra fica de fora.
func (e *Engine) preRoll() int {
	n, attacks := 0, 0
	for i := len(e.hist) - 1; i >= 0 && e.hist[i].sound; i-- {
		if e.hist[i].sustain < edgeSustain {
			if attacks++; attacks > 2 {
				break
			}
		}
		n++
	}
	if n < len(e.hist) {
		n++
	}
	return n
}

// catchUp recupera o atraso que o começo da palavra deixou na fila: remove um
// período de tom da voz de cada vez, fundindo dois períodos vizinhos e quase
// iguais em um só (a técnica que os buffers de jitter usam para acelerar a
// reprodução). A fala sai no máximo 25% mais rápida, sem mudar de tom, até a
// fila voltar ao tempo real; as pausas entre palavras também esvaziam a fila.
func (e *Engine) catchUp() {
	if e.credit += catchUpPerFrame; e.credit > catchUpMaxLag {
		e.credit = catchUpMaxLag
	}
	q := e.outq
	maxLag := len(q) - FrameSize // o atraso atual
	for _, limit := range []int{e.credit, catchUpMaxLag, len(q) - catchUpWindow, len(q) / 2} {
		if limit < maxLag {
			maxLag = limit
		}
	}
	if maxLag < catchUpMinLag {
		return
	}
	a := q[:catchUpWindow]
	var ea float64
	for _, v := range a {
		ea += v * v
	}
	best, bestLag := -1.0, 0
	for lag := catchUpMinLag; lag <= maxLag; lag++ {
		b := q[lag : lag+catchUpWindow]
		var ab, eb float64
		for i, v := range a {
			ab += v * b[i]
			eb += b[i] * b[i]
		}
		if r := ab / math.Sqrt(ea*eb+1e-9); r > best {
			best, bestLag = r, lag
		}
	}
	// Trecho fraco (ambiente, respiração, consoante de sopro): é ruído, e
	// fundir ruído com ruído não deixa marca. Trecho forte: só se os dois
	// períodos forem quase iguais, isto é, uma vogal sustentada.
	quiet := rmsFloat(q[:bestLag+catchUpWindow]) < 0.05*e.outPeak
	if best < catchUpMatch && !quiet {
		return
	}
	for i := 0; i < bestLag; i++ {
		t := (float64(i) + 0.5) / float64(bestLag)
		q[i] = q[i]*(1-t) + q[bestLag+i]*t
	}
	e.outq = q[:bestLag+copy(q[bestLag:], q[2*bestLag:])]
	e.credit -= bestLag
}

// Estados de Engine.coupling.
const (
	couplingUnknown int32 = iota // ainda não deu para saber: segura a transmissão durante avisos
	couplingHeard                // o microfone ouve os avisos (caixas de som): segura
	couplingSilent               // o microfone não ouve os avisos (fone de ouvido): não segura
)

// NoticeCoupling diz o que o motor concluiu sobre o microfone ouvir os avisos
// sonoros: "unknown", "heard" (caixas de som) ou "silent" (fone de ouvido).
func (e *Engine) NoticeCoupling() string {
	switch e.coupling.Load() {
	case couplingHeard:
		return "heard"
	case couplingSilent:
		return "silent"
	}
	return "unknown"
}

// NoticeMeasurements descreve as medidas do último aviso avaliado (diagnóstico).
func (e *Engine) NoticeMeasurements() string {
	s, _ := e.noticeLog.Load().(string)
	return s
}

// learnCoupling roda quando um aviso sonoro termina e conclui, pelo que o
// microfone captou enquanto ele tocava, se o microfone ouve os avisos:
//
//   - o detector de voz não reagiu a nada: não ouve (fone de ouvido);
//   - reagiu e o volume do microfone subiu e desceu junto com o do aviso: ouve;
//   - reagiu, mas sem acompanhar o aviso: era alguém falando por cima; não dá
//     para concluir, fica como estava.
func (e *Engine) learnCoupling() {
	e.lbMu.Lock()
	ref := e.refEnv
	e.refEnv, e.refAcc, e.refN = nil, 0, 0
	e.lbMu.Unlock()
	mic, hits := e.noticeMic, e.noticeHit
	e.noticeMic, e.noticeHit = nil, 0

	audible := 0
	for _, v := range ref {
		if v > 100 { // -50 dBFS
			audible++
		}
	}
	match := -1.0 // -1 = não chegou a ser medida
	defer func() {
		e.noticeLog.Store(fmt.Sprintf("aviso=%d quadros (%d com som), microfone=%d quadros, reações do detector=%d, semelhança=%.2f",
			len(ref), audible, len(mic), hits, match))
	}()
	if audible < 15 {
		return // menos de 300 ms de som: curto demais para concluir
	}
	if len(mic) < len(ref) {
		return // o microfone não estava ligado durante o aviso inteiro
	}
	if hits <= 2 {
		e.coupling.Store(couplingSilent)
		return
	}
	if match = envelopeMatch(ref, mic); match >= 0.7 {
		e.coupling.Store(couplingHeard)
	}
}

// envelopeMatch mede o quanto o volume captado pelo microfone acompanha o do
// aviso tocado: a maior correlação entre as duas curvas, testando atrasos de
// 0 a 400 ms (latência da saída + caminho pelo ar + latência da entrada).
func envelopeMatch(ref, mic []float64) float64 {
	best := 0.0
	for lag := 0; lag <= 20; lag++ {
		n := len(mic) - lag
		if n > len(ref) {
			n = len(ref)
		}
		if n < 15 {
			break
		}
		var sa, sb float64
		for i := 0; i < n; i++ {
			sa += ref[i]
			sb += mic[i+lag]
		}
		ma, mb := sa/float64(n), sb/float64(n)
		var cov, va, vb float64
		for i := 0; i < n; i++ {
			a, b := ref[i]-ma, mic[i+lag]-mb
			cov += a * b
			va += a * a
			vb += b * b
		}
		if va > 0 && vb > 0 {
			best = math.Max(best, cov/math.Sqrt(va*vb))
		}
	}
	return best
}

// alreadySpeaking indica se há fala nossa confirmada em andamento.
func (e *Engine) alreadySpeaking() bool {
	// Uma palavra que começa em "s" ainda não tem voz confirmada: o chiado
	// recém-transmitido também conta como fala em andamento.
	if e.sinceVoice <= speechTail || e.sinceHiss <= speechTail {
		return true
	}
	for i := range e.ring {
		if e.ring[i].confirmed {
			return true
		}
	}
	return false
}

// confirmSpeech marca como voz confirmada os quadros de um trecho vozeado que
// se sustenta: pelo menos 3 dos últimos 4 quadros com voz, sem a energia
// despencar. Uma batida de tecla ressoa como voz por 1–2 quadros e morre.
//
// Uma sílaba curta ("por", "ti", "é") só dá 2 quadros de voz sustentada: o
// primeiro é o ataque, com tom mas com o nível ainda subindo. Ela também vale
// quando os 2 quadros sustentados vêm logo depois de um ataque com tom — a
// ordem inversa da tecla, que ataca e desaba.
func (e *Engine) confirmSpeech() {
	n := len(e.ring)
	last := &e.ring[n-1]
	if !last.voiced {
		return
	}
	// Continuação de um trecho já confirmado.
	if n >= 2 && e.ring[n-2].confirmed {
		last.confirmed = true
		return
	}
	if n < 2 && e.sinceVoice == 0 {
		last.confirmed = true
		return
	}
	from := n - 4
	if from < 0 {
		from = 0
	}
	voiced, peak, top := 0, 0.0, 0.0
	for i := from; i < n; i++ {
		top = math.Max(top, e.ring[i].level)
		if e.ring[i].voiced {
			voiced++
			peak = math.Max(peak, e.ring[i].level)
		}
	}
	short := n >= 3 && e.ring[n-2].voiced && e.ring[n-3].pitched
	// A voz que abre o microfone é a parte forte da sílaba. Se os quadros com
	// voz são só o resto fraco de uma pancada que veio antes deles, é a
	// ressonância de uma tecla morrendo, não uma vogal começando.
	if (voiced >= 3 || short) && last.level >= 0.35*peak && peak >= confirmMinPeak*top && peak >= e.openFloor() {
		for i := from; i < n; i++ {
			if e.ring[i].voiced {
				e.ring[i].confirmed = true
			}
		}
	}
}

// openFloor é o nível que a voz precisa atingir para abrir o microfone agora:
// o piso de sensibilidade (SetVADFloor), ou a metade dele com a fala em
// andamento.
func (e *Engine) openFloor() float64 {
	if e.sinceVoice <= maxTailFrames {
		return e.floor * floorRecent
	}
	return e.floor
}

// decide diz se o quadro mais antigo da janela deve ser transmitido.
func (e *Engine) decide(cur *txSlot, mode string) bool {
	if cur.confirmed {
		e.sinceVoice = 0
		e.fricative = false
		e.tailAlive = true
		return true
	}
	if e.sinceVoice < farAway {
		e.sinceVoice++
	}
	if e.sinceHiss < farAway {
		e.sinceHiss++
	}
	// Voz confirmada logo à frente: este quadro é o começo da palavra.
	for i := range e.ring {
		if e.ring[i].confirmed {
			return true
		}
	}
	if mode == VADLevel {
		return e.sinceVoice <= levelHangover
	}
	if mode == VADPTT {
		return e.sinceVoice <= pttRelease
	}
	// Logo depois da voz: final de palavra.
	if e.sinceVoice <= speechTail {
		return true
	}
	// A palavra ainda não acabou: sílaba final sem tom, voz que vai sumindo
	// aos poucos. Continua enquanto o som não parar. Um fim de palavra só
	// diminui; um nível que salta de repente é outra coisa (uma tecla) e
	// encerra.
	if e.tailAlive && e.sinceVoice <= maxTailFrames && cur.sound && cur.rise < tailMaxRise {
		return true
	}
	e.tailAlive = false
	// Última sílaba sem tom ou com a voz rouca de fim de frase, depois da
	// parada de um "t"/"p"/"c" ("per-to", "a-cho"): o som volta quando a cauda
	// já acabou. Vale se vier logo depois da voz e durar com o nível firme —
	// 80 ms em que uma tecla já teria desabado.
	if e.sinceVoice <= maxTailFrames && e.steadySound(cur) {
		e.tailAlive = true
		return true
	}
	// Consoantes de sopro ("s", "f", "ch", "x") não têm tom, mas são um chiado
	// agudo que dura; um estalo de teclado também é agudo, só que desaba em
	// poucos milissegundos. Palmas, passos e um gole de bebida também são
	// chiados que duram: no modo neural só conta o chiado em que a rede ouve
	// fala. (O "s" que abre uma palavra não depende disto: entra com os quadros
	// guardados quando a vogal é confirmada.)
	hiss := func(s *txSlot, snr, high float64) bool {
		return !s.unheard && s.snr >= snr && s.high >= high && s.sustain >= 0.3
	}
	if e.fricative {
		e.fricative = hiss(cur, 6, 0.45)
		if e.fricative {
			e.sinceHiss = 0
		}
		return e.fricative
	}
	if len(e.ring) >= 2 && hiss(cur, 8, 0.55) && hiss(&e.ring[0], 8, 0.45) && hiss(&e.ring[1], 8, 0.45) {
		a, b, c := cur.level, e.ring[0].level, e.ring[1].level
		if lo, hi := math.Min(a, math.Min(b, c)), math.Max(a, math.Max(b, c)); lo > 0 && lo >= 0.3*hi && hi >= e.openFloor() {
			e.fricative = true
			e.sinceHiss = 0
			return true
		}
	}
	return false
}

// steadySound diz se o quadro e os três seguintes têm som forte e de nível
// parecido: um som que dura, não um estalo.
func (e *Engine) steadySound(cur *txSlot) bool {
	if len(e.ring) < lookahead || cur.snr < steadySNR {
		return false
	}
	lo, hi := cur.level, cur.level
	for i := 0; i < lookahead; i++ {
		s := &e.ring[i]
		if s.snr < steadySNR {
			return false
		}
		lo, hi = math.Min(lo, s.level), math.Max(hi, s.level)
	}
	return lo >= steadyLevel*hi
}

func fade(buf []float64, in, out bool) {
	n := fadeSamples
	if n > len(buf) {
		n = len(buf)
	}
	if in {
		for i := 0; i < n; i++ {
			buf[i] *= float64(i) / float64(n)
		}
	}
	if out {
		last := len(buf) - 1
		for i := 0; i < n; i++ {
			buf[last-i] *= float64(i) / float64(n)
		}
	}
}

// emit aplica o efeito na voz, soma o soundboard, limita e envia um quadro.
// voice pode ser nil (só soundboard).
func (e *Engine) emit(voice []float64, sb []int16, effect VoiceEffect, sbLocal, micTest bool) {
	out := make([]float64, FrameSize)
	if voice != nil {
		copy(out, voice)
		e.fx.Process(out, effect)
	}
	if sb != nil {
		for i := range out {
			out[i] += float64(sb[i])
		}
	}
	e.txLimiter.run(out)
	conn, send := e.txTarget()
	if send {
		e.recordTx(out, conn)
	}

	pcm := make([]int16, FrameSize)
	toInt16(pcm, out)
	switch {
	case micTest && voice != nil:
		e.pushLoopback(pcm) // teste de microfone: ouve exatamente o que é enviado
	case sb != nil && sbLocal:
		e.pushLoopback(sb)
	}

	if !send {
		// Sem destino: o trecho segue (o efeito não recomeça a cada quadro e o
		// teste de microfone toca), só não é codificado nem entregue.
		e.txOn = true
		return
	}

	e.mu.Lock()
	enc := e.encoder
	if sb != nil {
		enc = e.sbEncoder
	}
	n, err := enc.Encode(pcm, e.opusBuf)
	e.mu.Unlock()
	if err != nil || n == 0 {
		return
	}
	e.txOn = true
	e.deliver(e.opusBuf[:n])
}
