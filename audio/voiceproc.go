package audio

import (
	"math"

	"furito-ts/audio/rnnoise"
)

// Cadeia de tratamento do microfone: passa-altas → supressão de ruído →
// ganho automático, mais as medidas usadas pelo detector de voz.
//
// Há duas supressões de ruído:
//
//   - a neural (NoiseNeural) é o RNNoise: uma rede treinada com fala e ruídos
//     de verdade decide, a cada 10 ms, quanto de cada faixa é voz. Tira também
//     o que muda depressa (digitação, cliques) e no meio da fala;
//   - a clássica (NoiseClassic) é espectral (STFT de 20 ms com 50% de
//     sobreposição): o ruído de cada banda é estimado continuamente pelos
//     mínimos do sinal e o ganho de cada banda vem de um filtro de Wiener com
//     SNR a priori "decision directed", que é o que evita o chiado musical
//     típico de subtração espectral. Só tira ruído constante.
//
// A análise espectral roda nos dois casos: é dela que saem as medidas do
// detector de voz. No modo neural soma-se a elas a probabilidade de voz que o
// RNNoise devolve, que continua certa com ventilador ao fundo e voz baixa,
// onde a relação sinal/ruído sozinha não separa a fala do ambiente.

// Modos de supressão de ruído.
const (
	NoiseOff     = "off"
	NoiseClassic = "classic"
	NoiseNeural  = "neural"
)

// NoiseDelay é o atraso, em amostras, que a supressão de ruído põe no áudio.
func NoiseDelay(mode string) int {
	switch mode {
	case NoiseNeural:
		return rnnoise.Delay
	case NoiseClassic:
		return nsHop
	}
	return 0
}

const (
	nsHop    = FrameSize / 2 // 10 ms
	nsWindow = FrameSize     // 20 ms
	nsFFT    = 1024
	nsBins   = nsFFT/2 + 1

	nsMinWindowHops = 75 // janela de busca do mínimo: 2 × 0,75 s
	nsDDAlpha       = 0.98
	nsSpeechHold    = 15  // hops (150 ms) em que a fala "segura" o estimador
	nsSpeechCap     = 600 // hops (6 s): depois disso aprende mesmo com "fala"

	vadDecim   = 6   // 48 kHz → 8 kHz
	vadHistory = 400 // 50 ms a 8 kHz
	vadWindow  = 240 // 30 ms
	vadMinLag  = 20  // 400 Hz
	vadMaxLag  = 114 // 70 Hz
	vadMinSNR  = 4.0 // dB acima do ruído
	// Vogais nasais ("ão", "um", "não") e "m"/"n" quase só têm graves, abaixo
	// da faixa acima. Valem como voz pelo SNR dos graves, mas só para
	// continuar uma voz que já vinha: sozinho, um grave com tom é uma pancada
	// na mesa ou a ressonância de uma tecla.
	vadMinLowSNR = 8.0
	vadMinPitch  = 0.5 // periodicidade mínima para considerar voz
	// A voz se sustenta; uma batida de tecla dá um pico e desaba. Abaixo
	// disso (nível mínimo/máximo em blocos de 10 ms) o quadro é um estalo.
	vadMinSustain = 0.25
	// "Tem som" (não necessariamente voz): dB acima do ruído em alguma das
	// três faixas. Em silêncio as medidas ficam abaixo de 0,5 / 4,5 / 0 dB.
	soundSNR    = 3.0
	soundLowSNR = 6.0

	// Modo neural: o que o RNNoise diz de cada quadro. Em fala a probabilidade
	// de voz fica em 1,00 e o som passa quase inteiro, com ou sem ventilador ao
	// fundo; em ruído constante a probabilidade fica abaixo de 0,1 (medido com
	// as vozes sintetizadas do sistema). Um teclado engana a rede por
	// instantes, mas nunca por muito tempo com o nível firme.
	neuralHeard = 0.5  // probabilidade a partir da qual há "som de fala" (bordas das palavras)
	neuralSure  = 0.95 // probabilidade a partir da qual a rede tem certeza de que é voz…
	neuralKept  = 0.85 // …desde que tenha deixado passar pelo menos esta fração do nível do quadro
	neuralAlone = 5    // quadros seguidos (100 ms) de certeza com o nível firme que valem como voz mesmo sem tom firme
	neuralFirm  = 0.5  // "nível firme": menor/maior nível nos últimos 40 ms
	// Rede de segurança: tantos quadros seguidos (120 ms) de voz pelas medidas
	// clássicas valem mesmo que a rede discorde. O teclado não passa de 2.
	plainAlone = 6
)

// nsBandCenters são os centros das bandas (Hz). Até ~650 Hz há uma banda por
// bin da FFT, para isolar zumbido de rede elétrica sem levar junto os
// harmônicos graves da voz; acima disso as bandas alargam 12% a cada passo.
var nsBandCenters = func() []float64 {
	var c []float64
	binHz := float64(SampleRate) / nsFFT
	for k := 0; k < 14; k++ {
		c = append(c, float64(k)*binHz)
	}
	for f := 700.0; f < 21000; f *= 1.12 {
		c = append(c, f)
	}
	return append(c, 24000)
}()

// VoiceFeatures são as medidas de um quadro de 20 ms.
type VoiceFeatures struct {
	Level       float64 // RMS depois da supressão, antes do ganho automático
	SNR         float64 // dB acima do ruído estimado, na faixa da voz (300 Hz–4 kHz)
	LowSNR      float64 // idem, nos graves (até 300 Hz): vogais nasais, "m", "n", "u"
	HighSNR     float64 // idem, nos agudos (4–10 kHz): "s", "f", "ch"
	Periodicity float64 // 0..1: quão periódico (vozeado) é o quadro
	HighRatio   float64 // 0..1: fração da energia acima de 3 kHz (alto em "s", "f", "ch")
	Sustain     float64 // 0..1: menor/maior nível entre os blocos de 10 ms dos últimos 40 ms
	Rise        float64 // quantas vezes o nível subiu dos 20 ms anteriores para estes (ataque, estalo)
	Neural      float64 // 0..1: probabilidade de voz segundo o RNNoise (0 fora do modo neural)
	Kept        float64 // 0..1: fração do nível do quadro que o RNNoise deixou passar (1 fora do modo neural)
	Sure        bool    // modo neural: a rede tem certeza de que é voz e está deixando o som passar
	Sound       bool    // há som acima do ruído de fundo (voz ou não)
	Pitched     bool    // tem tom de voz, mas o nível ainda não se sustentou (ataque de sílaba ou estalo)
	Speech      bool    // voz detectada
}

// VoiceProcessor trata um fluxo mono de 48 kHz em quadros de 20 ms.
type VoiceProcessor struct {
	hpf *biquad

	fft     *fft
	window  [nsWindow]float64
	prevIn  [nsHop]float64
	overlap [nsHop]float64
	re, im  [nsFFT]float64

	binBand [nsBins]int     // banda inferior de cada bin
	binFrac [nsBins]float64 // peso da banda inferior
	nb      int
	energy  []float64
	smooth  []float64
	smin    []float64
	stmp    []float64
	noise   []float64
	bias    []float64
	gain    []float64
	post    []float64
	hops    int
	primed  bool

	speechHold int // hops restantes em que a última fala ainda vale
	speechRun  int // hops seguidos com fala (ou dentro do hold)

	// Ajustes do estimador (calibrados com fala + ruído reais).
	NoiseBias float64 // compensa o viés de estimar o ruído pelo mínimo
	NoiseOver float64 // superestimação do ruído no cálculo do ganho

	maxReduction float64 // ganho mínimo linear (piso da atenuação)

	// Supressão neural (só existe enquanto o modo neural está em uso).
	rn     *rnnoise.Denoiser
	rnHeld [nsHop]float64     // os 10 ms que a análise espectral espera para andar junto com o RNNoise
	rnProb [2]float64         // probabilidade de voz das duas últimas janelas do quadro anterior
	plain  [FrameSize]float64 // o mesmo quadro tratado pela supressão clássica (ver Plain)

	vadLP1, vadLP2 *biquad
	vadBuf         [vadHistory]float64
	vadPhase       int
	prevLag        int        // período de tom do quadro anterior (0 = sem tom)
	prevSpeech     bool       // o quadro anterior era voz
	plainRun       int        // modo neural: quadros seguidos de voz pelas medidas clássicas
	sureRun        int        // modo neural: quadros seguidos em que a rede tem certeza e o nível está firme
	hopLevels      [4]float64 // nível dos últimos 4 blocos de 10 ms (sinal limpo)

	agc *leveler
}

func NewVoiceProcessor() *VoiceProcessor {
	p := &VoiceProcessor{
		hpf:    newHighpass(85, 0.707),
		fft:    newFFT(nsFFT),
		nb:     len(nsBandCenters),
		vadLP1: newLowpass(900, 0.707),
		vadLP2: newLowpass(900, 0.707),
		// alvo ≈ -19 dBFS; pode reduzir até -6 dB e amplificar até +15 dB
		agc: newLeveler(3600, 0.5, 5.6),
	}
	p.SetReduction(24)
	for i := range p.window {
		p.window[i] = math.Sin(math.Pi * (float64(i) + 0.5) / nsWindow)
	}
	for k := 0; k < nsBins; k++ {
		f := float64(k) * SampleRate / nsFFT
		b := 0
		for b < p.nb-2 && f >= nsBandCenters[b+1] {
			b++
		}
		p.binBand[k] = b
		frac := (nsBandCenters[b+1] - f) / (nsBandCenters[b+1] - nsBandCenters[b])
		p.binFrac[k] = math.Min(1, math.Max(0, frac))
	}
	for _, s := range []*[]float64{&p.energy, &p.smooth, &p.smin, &p.stmp, &p.noise, &p.bias, &p.gain, &p.post} {
		*s = make([]float64, p.nb)
	}
	p.NoiseBias, p.NoiseOver = 1.0, 1.0
	// Bandas com poucos bins flutuam mais, então o mínimo fica mais abaixo
	// da média: o fator de correção é maior nelas.
	count := make([]float64, p.nb)
	for k := 0; k < nsBins; k++ {
		count[p.binBand[k]] += p.binFrac[k]
		count[p.binBand[k]+1] += 1 - p.binFrac[k]
	}
	for b := range p.bias {
		p.bias[b] = 1.5 + 1.5/math.Sqrt(math.Max(count[b], 1))
	}
	for i := range p.gain {
		p.gain[i] = 1
	}
	return p
}

// SetReduction define a atenuação máxima do ruído, em dB (padrão 24).
func (p *VoiceProcessor) SetReduction(db float64) {
	if db < 0 {
		db = 0
	}
	if db > 40 {
		db = 40
	}
	p.maxReduction = math.Pow(10, -db/20)
}

// Process trata um quadro de 20 ms no lugar e devolve as medidas dele.
// noise escolhe a supressão de ruído (NoiseOff, NoiseClassic ou NoiseNeural) e
// autoGain liga o ganho automático; as medidas são sempre calculadas sobre o
// sinal limpo. O áudio sai atrasado de NoiseDelay(noise) amostras.
func (p *VoiceProcessor) Process(frame []float64, noise string, autoGain bool) VoiceFeatures {
	p.hpf.run(frame)

	var feat VoiceFeatures
	neural := noise == NoiseNeural
	analysis := frame
	var held [FrameSize]float64
	if neural {
		if p.rn == nil {
			// Estado novo a cada vez que o modo é ligado: o antigo guardaria
			// 20 ms de um áudio que já passou.
			p.rn = rnnoise.New()
			p.rnProb, p.plainRun, p.sureRun = [2]float64{}, 0, 0
			// A espera começa com o próprio começo do quadro, não com zeros:
			// um trecho de silêncio absoluto faria o estimador de ruído partir
			// do zero e tomar o ambiente por som durante meio segundo.
			copy(p.rnHeld[:], frame[:nsHop])
		}
		// O RNNoise atrasa o áudio 20 ms e a análise espectral, 10: ela recebe
		// a entrada 10 ms atrasada para as medidas falarem do mesmo trecho.
		copy(held[:nsHop], p.rnHeld[:])
		copy(held[nsHop:], frame[:nsHop])
		copy(p.rnHeld[:], frame[nsHop:])
		analysis = held[:]
		// Cada probabilidade é a da janela de 20 ms que termina no trecho que
		// acabou de entrar. O áudio que sai agora é o de 20 ms atrás: é coberto
		// pelas duas janelas do quadro anterior e pela primeira deste.
		p0 := p.rn.Process(frame[:nsHop])
		p1 := p.rn.Process(frame[nsHop:])
		feat.Neural = math.Max(p0, math.Max(p.rnProb[0], p.rnProb[1]))
		p.rnProb = [2]float64{p0, p1}
	} else {
		p.rn = nil
	}

	var clean [FrameSize]float64
	var highSum float64
	var snrSum [3]float64
	for h := 0; h < 2; h++ {
		in := analysis[h*nsHop : (h+1)*nsHop]
		snr, high := p.hop(clean[h*nsHop:(h+1)*nsHop], in)
		for i := range snrSum {
			snrSum[i] += snr[i]
		}
		highSum += high
	}
	feat.LowSNR, feat.SNR, feat.HighSNR = snrSum[0]/2, snrSum[1]/2, snrSum[2]/2
	feat.HighRatio = highSum / 2

	p.hopLevels[0], p.hopLevels[1] = p.hopLevels[2], p.hopLevels[3]
	p.hopLevels[2], p.hopLevels[3] = rmsFloat(clean[:nsHop]), rmsFloat(clean[nsHop:])
	lo, hi := p.hopLevels[0], p.hopLevels[0]
	for _, l := range p.hopLevels[1:] {
		lo, hi = math.Min(lo, l), math.Max(hi, l)
	}
	if hi > 0 {
		feat.Sustain = lo / hi
	}
	feat.Rise = math.Max(p.hopLevels[2], p.hopLevels[3]) / (math.Min(p.hopLevels[0], p.hopLevels[1]) + 1)
	feat.Level = rmsFloat(clean[:])
	feat.Kept = 1
	if neural {
		// A análise espectral e o RNNoise trataram o mesmo trecho: a razão
		// entre os dois níveis diz quanto do som a rede considerou voz.
		feat.Kept = math.Min(1, rmsFloat(frame)/(feat.Level+1))
	}
	// A rede reconhece fala que mal passa do ruído (voz baixa, ventilador), onde
	// a relação sinal/ruído não basta nem para dizer que há som.
	heard := neural && feat.Neural >= neuralHeard
	feat.Sound = feat.SNR >= soundSNR || feat.HighSNR >= soundSNR || feat.LowSNR >= soundLowSNR || heard
	var lag int
	feat.Periodicity, lag = p.periodicity(clean[:])
	if feat.Periodicity < vadMinPitch {
		lag = 0
	}
	// Voz = acima do ruído, periódica, e com o tom parecido com o do quadro
	// anterior. O tom da fala muda devagar; estalos de teclado até parecem
	// periódicos num quadro isolado, mas não repetem o mesmo período.
	steady := lag > 0 && p.prevLag > 0 && math.Abs(float64(lag-p.prevLag)) <= 0.2*float64(lag)
	p.prevLag = lag
	loud := feat.SNR >= vadMinSNR || (p.prevSpeech && feat.LowSNR >= vadMinLowSNR && feat.Sustain >= 0.5)
	feat.Pitched = steady && loud && feat.Level > 20
	feat.Speech = feat.Pitched && feat.Sustain >= vadMinSustain
	if neural {
		p.neuralVoice(&feat, steady)
	}
	p.prevSpeech = feat.Speech
	if feat.Speech {
		p.speechHold = nsSpeechHold
	}

	if noise == NoiseClassic {
		copy(frame, clean[:])
	}
	p.plain = clean
	if autoGain {
		before := p.agc.gain
		p.agc.run(frame, feat.Speech)
		ramp(p.plain[:], before, p.agc.gain) // o mesmo ganho nas duas versões
	}
	return feat
}

// neuralVoice refaz a decisão de voz do quadro no modo neural. Quem diz se há
// voz acima do ruído passa a ser a rede, no lugar da relação sinal/ruído; o
// tom firme e a sustentação continuam sendo a defesa contra o teclado. Cada
// detector só decide sozinho com uma evidência longa demais para uma tecla
// produzir: 100 ms de certeza da rede com o nível firme (voz rouca, palavra
// curta sem tom firme), ou 120 ms de voz pelas medidas clássicas (uma voz que
// a rede não reconheça).
//
// feat chega com Pitched e Speech calculados pelas medidas clássicas.
func (p *VoiceProcessor) neuralVoice(feat *VoiceFeatures, steady bool) {
	feat.Sure = feat.Neural >= neuralSure && feat.Kept >= neuralKept
	if feat.Speech {
		p.plainRun++
	} else {
		p.plainRun = 0
	}
	if feat.Sure && feat.Sustain >= neuralFirm {
		p.sureRun++
	} else {
		p.sureRun = 0
	}
	alone := p.sureRun >= neuralAlone
	feat.Pitched = alone || (steady && feat.Level > 20 && (feat.Sure || p.plainRun >= plainAlone))
	feat.Speech = alone || (feat.Pitched && feat.Sustain >= vadMinSustain)
}

// Plain devolve o último quadro tratado pela supressão clássica, alinhado e
// com o mesmo ganho do que Process deixou no quadro. Serve ao modo neural: o
// RNNoise só reconhece a fala quando a vogal chega e, com ruído ao fundo,
// abafa a consoante que abre a frase ("s", "f", "ch"); a supressão clássica
// não tem esse defeito. O transmissor usa esta versão no começo de cada trecho
// de fala, até a rede deixar o som passar (ver Engine.pick). O conteúdo vale
// até a próxima chamada de Process.
func (p *VoiceProcessor) Plain() []float64 { return p.plain[:] }

// hop processa 10 ms: devolve em out o trecho limpo e retorna o SNR (dB) nos
// graves, na faixa da voz e nos agudos, e a fração da energia limpa que está
// acima de 3 kHz.
func (p *VoiceProcessor) hop(out, in []float64) (snr [3]float64, high float64) {
	for i := 0; i < nsHop; i++ {
		p.re[i] = p.prevIn[i] * p.window[i]
		p.re[nsHop+i] = in[i] * p.window[nsHop+i]
	}
	for i := nsWindow; i < nsFFT; i++ {
		p.re[i] = 0
	}
	for i := range p.im {
		p.im[i] = 0
	}
	copy(p.prevIn[:], in)

	p.fft.transform(p.re[:], p.im[:], false)

	for b := range p.energy {
		p.energy[b] = 0
	}
	for k := 0; k < nsBins; k++ {
		pw := p.re[k]*p.re[k] + p.im[k]*p.im[k]
		b := p.binBand[k]
		p.energy[b] += pw * p.binFrac[k]
		p.energy[b+1] += pw * (1 - p.binFrac[k])
	}

	if !p.primed {
		p.primed = true
		// O ruído começa em zero e sobe até o piso medido: se o microfone
		// abrir com alguém já falando, a fala não é tomada por ruído.
		for b := 0; b < p.nb; b++ {
			p.smooth[b], p.smin[b], p.stmp[b], p.noise[b] = p.energy[b], p.energy[b], p.energy[b], 0
		}
	}

	// A fala recente "segura" o estimador: enquanto há voz o piso de ruído só
	// pode descer. O limite de 6 s evita travar num ruído periódico novo.
	held := false
	if p.speechHold > 0 {
		p.speechHold--
		p.speechRun++
		held = p.speechRun < nsSpeechCap
	} else {
		p.speechRun = 0
	}
	rotate := false
	if !held {
		p.hops++
		window := nsMinWindowHops
		if p.hops < nsMinWindowHops {
			window = 15 // nos primeiros instantes aprende o ambiente mais rápido
		}
		rotate = p.hops%window == 0
	}

	var sigSum, noiseSum [3]float64
	var lowE, highE float64
	for b := 0; b < p.nb; b++ {
		e := p.energy[b]

		// Estimativa de ruído por estatística de mínimos: o ruído de fundo é
		// o menor nível (suavizado) que a banda atingiu no último ~1,5 s,
		// corrigido pelo viés. A fala sempre deixa buracos nesse intervalo.
		p.smooth[b] = 0.8*p.smooth[b] + 0.2*e
		if rotate {
			p.smin[b] = math.Min(p.stmp[b], p.smooth[b])
			p.stmp[b] = p.smooth[b]
		} else {
			p.smin[b] = math.Min(p.smin[b], p.smooth[b])
			p.stmp[b] = math.Min(p.stmp[b], p.smooth[b])
		}
		target := p.NoiseBias * p.bias[b] * p.smin[b]
		if target < p.noise[b] {
			p.noise[b] += 0.3 * (target - p.noise[b])
		} else {
			p.noise[b] += 0.1 * (target - p.noise[b])
		}

		// Ganho de Wiener com SNR a priori "decision directed".
		n := p.NoiseOver*p.noise[b] + 1e-3
		post := e / n
		prio := nsDDAlpha*p.gain[b]*p.gain[b]*p.post[b] + (1-nsDDAlpha)*math.Max(post-1, 0)
		g := prio / (1 + prio)
		if g < p.maxReduction {
			g = p.maxReduction
		}
		p.gain[b], p.post[b] = g, post

		f := nsBandCenters[b]
		if r := snrRegion(f); r >= 0 {
			sigSum[r] += e
			noiseSum[r] += p.noise[b]
		}
		if f < 1000 {
			lowE += e * g * g
		} else if f >= 3000 {
			highE += e * g * g
		}
	}

	for k := 0; k < nsBins; k++ {
		b := p.binBand[k]
		g := p.gain[b]*p.binFrac[k] + p.gain[b+1]*(1-p.binFrac[k])
		p.re[k] *= g
		p.im[k] *= g
		if k > 0 && k < nsFFT/2 {
			p.re[nsFFT-k] *= g
			p.im[nsFFT-k] *= g
		}
	}
	p.fft.transform(p.re[:], p.im[:], true)

	for i := 0; i < nsHop; i++ {
		out[i] = p.overlap[i] + p.re[i]*p.window[i]
		p.overlap[i] = p.re[nsHop+i] * p.window[nsHop+i]
	}

	for r := range snr {
		snr[r] = 10 * math.Log10((sigSum[r]+1e-3)/(noiseSum[r]+1e-3))
	}
	return snr, highE / (highE + lowE + 1e-3)
}

// snrRegion diz em qual das três faixas de medida do SNR cai a frequência:
// 0 = graves, 1 = voz, 2 = agudos, -1 = fora.
func snrRegion(f float64) int {
	switch {
	case f < 90 || f > 10000:
		return -1
	case f < 300:
		return 0
	case f <= 4000:
		return 1
	}
	return 2
}

// periodicity mede o quanto o quadro é periódico na faixa de tom da voz
// (70–400 Hz): a maior correlação normalizada entre o sinal e ele mesmo
// atrasado. Vogais ficam perto de 1; teclado, cliques e chiado ficam baixos.
func (p *VoiceProcessor) periodicity(clean []float64) (float64, int) {
	for _, x := range clean {
		y := p.vadLP2.step(p.vadLP1.step(x))
		p.vadPhase++
		if p.vadPhase == vadDecim {
			p.vadPhase = 0
			copy(p.vadBuf[:], p.vadBuf[1:])
			p.vadBuf[vadHistory-1] = y
		}
	}

	x := p.vadBuf[vadHistory-vadWindow:]
	var ex float64
	for _, v := range x {
		ex += v * v
	}
	if ex < 1 {
		return 0, 0
	}
	var corr [vadMaxLag + 1]float64
	best := 0.0
	for lag := vadMinLag; lag <= vadMaxLag; lag++ {
		y := p.vadBuf[vadHistory-vadWindow-lag : vadHistory-lag]
		var xy, ey float64
		for i, v := range x {
			xy += v * y[i]
			ey += y[i] * y[i]
		}
		if ey < 1 || xy <= 0 {
			continue
		}
		corr[lag] = xy / math.Sqrt(ex*ey)
		best = math.Max(best, corr[lag])
	}
	if best == 0 {
		return 0, 0
	}
	// Um sinal periódico se repete também a cada 2, 3… períodos, com quase a
	// mesma correlação; o período de verdade é o menor deles. Sem isso a
	// medida pula de oitava de um quadro para o outro no meio de uma vogal.
	lag := vadMinLag
	for corr[lag] < 0.9*best {
		lag++
	}
	for lag < vadMaxLag && corr[lag+1] >= corr[lag] {
		lag++
	}
	return best, lag
}
