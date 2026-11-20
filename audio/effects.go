package audio

import (
	"math"
	"math/rand"
)

// VoiceEffect identifica uma voz modificada.
type VoiceEffect string

const (
	EffectNormal   VoiceEffect = "normal"
	EffectRobot    VoiceEffect = "robot"
	EffectMonster  VoiceEffect = "monster"
	EffectSquirrel VoiceEffect = "squirrel"
)

// EffectInfo descreve um efeito para a interface.
type EffectInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// stage é um bloco de processamento com estado, aplicado no lugar.
type stage interface{ run(buf []float64) }

type effectDef struct {
	info  EffectInfo
	tail  int // quadros extras de transmissão para a cauda (eco/reverb) soar
	build func() []stage
}

// Quadros de cauda que cobrem o atraso dos blocos de tom e de cor: sem eles
// o fim da fala ficaria preso dentro do efeito.
const (
	pitchTail = psDelay/FrameSize + 1
	voiceTail = voiceDelay/FrameSize + 1
)

// O ganho no fim de cada cadeia deixa o efeito com o volume da voz que entrou,
// na média das vozes de teste; o resto, que depende de cada voz, é corrigido
// pelo nivelamento do EffectProcessor.
var effectDefs = []effectDef{
	{EffectInfo{"normal", "Normal"}, 0, func() []stage { return nil }},
	// Outra pessoa, não uma fita: o tom muda bastante e os formantes, pouco.
	{EffectInfo{"grave", "Voz grave"}, voiceTail, func() []stage { return voice(0.76, 0.90) }},
	{EffectInfo{"agudo", "Voz aguda"}, voiceTail, func() []stage { return append(voice(1.45, 1.12), &gain{1.16}) }},
	// Um bicho grande: trato vocal bem maior que o de gente, voz rasgada.
	{EffectInfo{"monster", "Monstro"}, voiceTail, func() []stage {
		return append(voice(0.62, 0.72), &drive{amount: 1.6}, newLowpass(6500, 0.707), &gain{0.6})
	}},
	// O esquilo é a fita acelerada de propósito: tom e formantes sobem juntos.
	{EffectInfo{"squirrel", "Esquilo"}, pitchTail, func() []stage { return []stage{newPitchShifter(1.65)} }},
	{EffectInfo{"robot", "Robô"}, 2, func() []stage {
		return []stage{newSTFTEffect(stftRobot), newHighpass(140, 0.707), newLowpass(6000, 0.707), &gain{1.4}}
	}},
	{EffectInfo{"metallic", "Metálico"}, 0, func() []stage {
		return []stage{newHighpass(180, 0.707), &ringMod{freq: 52, mix: 0.9}, &drive{amount: 1.8}}
	}},
	{EffectInfo{"radio", "Rádio"}, 0, func() []stage {
		return []stage{newHighpass(520, 0.707), newHighpass(520, 0.707), newLowpass(2900, 0.707), newLowpass(2900, 0.707),
			newPeaking(1700, 1.0, 5), &drive{amount: 3}, &gain{0.9}}
	}},
	{EffectInfo{"megaphone", "Megafone"}, 4, func() []stage {
		return []stage{newHighpass(700, 0.707), newLowpass(3800, 0.707), newPeaking(1900, 1.2, 8),
			&drive{amount: 6}, newEcho(45, 0.15, 0.22), &gain{0.45}}
	}},
	{EffectInfo{"cave", "Caverna"}, 70, func() []stage { return []stage{newReverb(0.88, 0.3, 0.42, 0.75), &gain{0.84}} }},
	{EffectInfo{"echo", "Eco"}, 80, func() []stage { return []stage{newEcho(280, 0.42, 0.4)} }},
	// Duas vozes saindo da mesma boca: uma grave e outra uma oitava abaixo da
	// original, com a garganta de um bicho enorme; por cima, o tremor, a
	// saturação e uma sala escura.
	{EffectInfo{"demon", "Demônio"}, 35 + voiceTail, func() []stage {
		return []stage{
			&layers{chains: [][]stage{voice(0.72, 0.82), voice(0.5, 0.72)}, gains: []float64{0.75, 0.6}},
			&ringMod{freq: 34, mix: 0.2}, &drive{amount: 2.2},
			newReverb(0.62, 0.5, 0.22, 0.9), &gain{0.62}}
	}},
	{EffectInfo{"alien", "Alienígena"}, pitchTail, func() []stage {
		return []stage{newPitchShifter(1.3), &ringMod{freq: 220, mix: 0.45}, newVibrato(7, 1.5), &gain{1.6}}
	}},
	{EffectInfo{"whisper", "Sussurro"}, 2, func() []stage {
		return []stage{newSTFTEffect(stftWhisper), newHighpass(300, 0.707), &gain{2.3}}
	}},
	{EffectInfo{"underwater", "Debaixo d'água"}, 0, func() []stage {
		return []stage{newLowpass(520, 0.707), newLowpass(520, 0.707), newVibrato(4, 3), &gain{1.18}}
	}},
}

// Effects lista os efeitos disponíveis, na ordem do menu.
func Effects() []EffectInfo {
	out := make([]EffectInfo, len(effectDefs))
	for i, d := range effectDefs {
		out[i] = d.info
	}
	return out
}

func findEffect(id VoiceEffect) *effectDef {
	for i := range effectDefs {
		if effectDefs[i].info.ID == string(id) {
			return &effectDefs[i]
		}
	}
	return nil
}

// Nivelamento: o efeito sai com o mesmo volume da voz que entrou.
const (
	matchVoice  = 150.0 // nível a partir do qual o quadro conta como voz
	matchWarmup = 25    // quadros de voz (0,5 s) medidos antes de corrigir
	matchRate   = 0.02  // depois disso, média móvel de ~1 s de voz
	matchMax    = 2.8   // correção máxima: ±9 dB
	matchSlew   = 0.05  // a correção anda devagar (~0,4 s de voz): sem degrau de volume
)

// EffectProcessor aplica o efeito escolhido mantendo o estado entre quadros.
//
// Quanto um efeito muda o volume depende da voz: um filtro de rádio tira os
// graves, e uma voz grave perde mais com isso que uma aguda. Por isso o
// processador mede o volume da voz que entra e o do efeito que sai e corrige a
// diferença aos poucos; a medida vale para o efeito escolhido e continua de
// uma fala para a outra (ver Reset).
type EffectProcessor struct {
	current VoiceEffect
	stages  []stage
	tail    int

	inE, outE float64 // energia média da voz que entrou e do efeito que saiu
	measured  int     // quadros de voz medidos
	level     float64 // correção de volume em uso
}

func NewEffectProcessor() *EffectProcessor {
	return &EffectProcessor{current: EffectNormal, level: 1}
}

// Reset recomeça o efeito do zero (sem ecos nem restos da fala anterior),
// mantendo o que já se mediu do volume.
func (p *EffectProcessor) Reset() {
	p.stages = nil
	if def := findEffect(p.current); def != nil {
		p.stages = def.build()
	}
}

// Process aplica o efeito no lugar e devolve quantos quadros de cauda ele pede.
func (p *EffectProcessor) Process(buf []float64, effect VoiceEffect) int {
	if effect != p.current {
		p.current = effect
		p.stages, p.tail = nil, 0
		p.inE, p.outE, p.measured, p.level = 0, 0, 0, 1
		if def := findEffect(effect); def != nil {
			p.stages, p.tail = def.build(), def.tail
		}
	}
	if len(p.stages) == 0 || len(buf) == 0 {
		return p.tail
	}
	in := rmsFloat(buf)
	for _, st := range p.stages {
		st.run(buf)
	}
	// O volume só é medido enquanto há voz entrando: nas pausas, o que sai é
	// a cauda de um eco, e não diz nada sobre o ganho do efeito.
	prev := p.level
	if in >= matchVoice {
		out := rmsFloat(buf)
		rate := matchRate
		if p.measured < matchWarmup {
			rate = 1 / float64(p.measured+1) // média simples no começo
		}
		p.inE += rate * (in*in - p.inE)
		p.outE += rate * (out*out - p.outE)
		if p.measured++; p.measured >= matchWarmup && p.outE > 1 {
			want := math.Max(1/matchMax, math.Min(matchMax, math.Sqrt(p.inE/p.outE)))
			p.level += matchSlew * (want - p.level)
		}
	}
	ramp(buf, prev, p.level)
	return p.tail
}

// ---------------------------------------------------------------- blocos simples

type gain struct{ g float64 }

func (g *gain) run(buf []float64) {
	for i := range buf {
		buf[i] *= g.g
	}
}

// drive é uma saturação suave (tanh), normalizada para manter o volume.
type drive struct{ amount float64 }

func (d *drive) run(buf []float64) {
	norm := math.Tanh(d.amount)
	for i, x := range buf {
		buf[i] = 32768 * math.Tanh(d.amount*x/32768) / norm
	}
}

// ringMod multiplica a voz por uma senoide (o som "metálico" clássico).
type ringMod struct {
	freq, mix float64
	phase     float64
}

func (r *ringMod) run(buf []float64) {
	step := r.freq / SampleRate
	for i, x := range buf {
		m := math.Sin(2 * math.Pi * r.phase)
		r.phase += step
		if r.phase >= 1 {
			r.phase--
		}
		buf[i] = x*(1-r.mix) + x*m*r.mix
	}
}

// echo é um atraso com realimentação.
type echo struct {
	buf           []float64
	idx           int
	feedback, mix float64
}

func newEcho(ms, feedback, mix float64) *echo {
	return &echo{buf: make([]float64, int(ms*SampleRate/1000)), feedback: feedback, mix: mix}
}

func (e *echo) run(buf []float64) {
	for i, x := range buf {
		d := e.buf[e.idx]
		e.buf[e.idx] = x + d*e.feedback
		e.idx = (e.idx + 1) % len(e.buf)
		buf[i] = x + d*e.mix
	}
}

// vibrato lê uma linha de atraso com tempo oscilante (ondula o tom).
type vibrato struct {
	buf   []float64
	idx   int
	phase float64
	rate  float64
	depth float64 // amostras
}

func newVibrato(rateHz, depthMs float64) *vibrato {
	depth := depthMs * SampleRate / 1000
	return &vibrato{buf: make([]float64, int(2*depth)+8), rate: rateHz, depth: depth}
}

func (v *vibrato) run(buf []float64) {
	n := len(v.buf)
	for i, x := range buf {
		v.buf[v.idx] = x
		delay := v.depth*(1+math.Sin(2*math.Pi*v.phase)) + 1
		v.phase += v.rate / SampleRate
		if v.phase >= 1 {
			v.phase--
		}
		pos := float64(v.idx) - delay
		for pos < 0 {
			pos += float64(n)
		}
		i0 := int(pos) % n
		frac := pos - math.Floor(pos)
		buf[i] = v.buf[i0]*(1-frac) + v.buf[(i0+1)%n]*frac
		v.idx = (v.idx + 1) % n
	}
}

// reverb é um reverberador de Schroeder/Freeverb mono: 8 filtros pente em
// paralelo (com amortecimento dos agudos) seguidos de 4 passa-tudo.
type reverb struct {
	combs    [8]comb
	allpass  [4]allpass
	wet, dry float64
}

type comb struct {
	buf            []float64
	idx            int
	feedback, damp float64
	store          float64
}

type allpass struct {
	buf []float64
	idx int
}

func newReverb(room, damp, wet, dry float64) *reverb {
	r := &reverb{wet: wet, dry: dry}
	scale := SampleRate / 44100.0
	for i, d := range []float64{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617} {
		r.combs[i] = comb{buf: make([]float64, int(d*scale)), feedback: room, damp: damp}
	}
	for i, d := range []float64{556, 441, 341, 225} {
		r.allpass[i] = allpass{buf: make([]float64, int(d*scale))}
	}
	return r
}

func (r *reverb) run(buf []float64) {
	for i, x := range buf {
		in := x * 0.03
		var acc float64
		for c := range r.combs {
			cb := &r.combs[c]
			out := cb.buf[cb.idx]
			cb.store = out*(1-cb.damp) + cb.store*cb.damp
			cb.buf[cb.idx] = in + cb.store*cb.feedback
			cb.idx = (cb.idx + 1) % len(cb.buf)
			acc += out
		}
		for a := range r.allpass {
			ap := &r.allpass[a]
			bufOut := ap.buf[ap.idx]
			ap.buf[ap.idx] = acc + bufOut*0.5
			ap.idx = (ap.idx + 1) % len(ap.buf)
			acc = bufOut - acc
		}
		buf[i] = x*r.dry + acc*r.wet*3
	}
}

// ---------------------------------------------------------------- mudança de tom

const (
	psWindow = 1200 // 25 ms
	psHop    = psWindow / 2
	psSearch = 360 // ±7,5 ms: cobre um período de tom da voz
	// Amostras de silêncio postas na saída antes do primeiro som: o bastante
	// para a saída nunca faltar, qualquer que seja a razão. É o que fixa o
	// atraso do bloco (o primeiro trecho usado começa em psSearch).
	psLead  = 2*psSearch + psWindow + 8
	psDelay = psLead - psSearch // atraso do som, em amostras (≈33 ms)

	// Estalos (a explosão de um "t", "p", "k", o começo seco de uma vogal).
	psBlock     = 96  // 2 ms: resolução do detector
	psHistory   = 12  // blocos (24 ms) de referência antes do estalo
	psRise      = 36  // o bloco tem 36× a energia média dos anteriores (6× o nível)
	psFloor     = 200 // nível mínimo para contar
	psNearEdge  = 150 // amostras: até onde o estalo pode ficar do centro de um trecho
	psSlack     = 240 // quanto a busca pode recuar a mais para centrar um estalo
	psClickSkew = 48  // o detector marca o começo do bloco; o estalo está um pouco adiante
)

var psHann = func() []float64 {
	w := make([]float64, psWindow)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/psWindow))
	}
	return w
}()

// pitchShifter muda o tom sem mudar a duração. Primeiro estica (ou encolhe) o
// tempo com WSOLA — cada novo trecho é escolhido onde a forma de onda melhor
// continua o trecho anterior, o que evita o "tremido" de emendar trechos às
// cegas — e depois reamostra de volta para a duração original.
//
// A reamostragem leva junto a "cor" da voz (os formantes): sozinho, este
// bloco soa como uma fita tocada mais depressa ou mais devagar. Para uma voz
// de outra pessoa, e não uma fita, ele é combinado com o formantShifter (ver
// voice).
//
// Um estalo não tem forma de onda que continue em outro lugar: esticando às
// cegas ele sai duas vezes, e encolhendo some na emenda entre dois trechos
// (com o tom do demônio, um em cada quatro). Por isso os trechos são
// posicionados de modo que cada estalo fique inteiro no centro de um só.
type pitchShifter struct {
	ratio     float64
	in        []float64
	pos       float64 // posição ideal do próximo trecho em in
	prevStart int
	tail      [psHop]float64
	stretched []float64
	readPos   float64
	out       []float64
	lp1, lp2  *biquad // anti-aliasing quando o tom sobe

	scanned int                // até onde a entrada já passou pelo detector de estalos
	recent  [psHistory]float64 // energia dos últimos blocos
	recentN int
	clicks  []int // posições dos estalos em in
}

func newPitchShifter(ratio float64) *pitchShifter {
	p := &pitchShifter{ratio: ratio, prevStart: -1, pos: psSearch, readPos: 1, out: make([]float64, psLead)}
	if ratio > 1 {
		cut := 0.45 * SampleRate / ratio
		p.lp1, p.lp2 = newLowpass(cut, 0.54), newLowpass(cut, 1.31)
	}
	return p
}

// findClicks procura estalos na entrada nova: um bloco de 2 ms com muito mais
// energia que os 24 ms anteriores. Os pulsos de uma voz grave não chegam a
// isso, porque a média anterior já tem os pulsos de antes.
func (p *pitchShifter) findClicks() {
	for ; p.scanned+psBlock <= len(p.in); p.scanned += psBlock {
		var e float64
		for _, v := range p.in[p.scanned : p.scanned+psBlock] {
			e += v * v
		}
		e /= psBlock
		var avg float64
		for _, v := range p.recent {
			avg += v
		}
		avg /= psHistory
		last := -psWindow
		if len(p.clicks) > 0 {
			last = p.clicks[len(p.clicks)-1]
		}
		if p.recentN >= psHistory && e > psFloor*psFloor && e > psRise*avg && p.scanned-last > psHop {
			p.clicks = append(p.clicks, p.scanned+psClickSkew)
		}
		p.recent[p.recentN%psHistory] = e
		p.recentN++
	}
}

// candidates devolve a faixa em que o próximo trecho pode começar, e center
// quando ele deve ser posto exatamente ali (centrado num estalo).
//
// Um trecho entra e sai devagar (janela de Hann): o que está no centro dele
// passa inteiro, e o que está nas pontas quase some. Então, para cada estalo:
// ou o trecho termina antes dele, ou o estalo fica perto do centro; e, depois
// que um estalo passou, nenhum trecho novo pode começar antes dele.
func (p *pitchShifter) candidates(start int) (lo, hi int, center bool) {
	lo, hi = start-psSearch, start+psSearch
	for _, c := range p.clicks {
		switch {
		case c < p.prevStart+psHop+psNearEdge:
			// Já saiu (estava no centro do trecho anterior ou antes).
			if lo < c-psNearEdge/3 {
				lo = c - psNearEdge/3
			}
		case hi > c-psWindow:
			// Está ao alcance: ou fica para depois, ou vai para o centro. Fica
			// para depois se daqui não dá para centrar, ou se o próximo trecho
			// vai cair mais perto dele (o estalo sai mais perto da hora certa).
			later := float64(start)+psHop/p.ratio/2 < float64(c-psHop) && c-psWindow >= lo
			if later || c-psHop-psNearEdge > hi || c-psHop+psNearEdge < lo-psSlack {
				hi = c - psWindow
				continue
			}
			at := c - psHop
			if at > hi {
				at = hi
			}
			if at < lo-psSlack {
				at = lo - psSlack
			}
			return at, at, true
		}
	}
	return lo, hi, false
}

func (p *pitchShifter) run(buf []float64) {
	n := len(buf)
	p.in = append(p.in, buf...)
	p.findClicks()

	for {
		start := int(p.pos + 0.5)
		if start+psSearch+psWindow > len(p.in) {
			break
		}
		best := start
		if p.prevStart >= 0 {
			lo, hi, center := p.candidates(start)
			natural := p.prevStart + psHop
			if center {
				best = lo
			} else if lo > hi {
				// Nenhuma posição serve: segue de onde o trecho anterior parou,
				// se ainda couber na entrada que já chegou.
				best = natural
				if best+psWindow > len(p.in) || best < 0 {
					best = start
				}
			}
			tpl := p.in[p.prevStart+psHop : p.prevStart+2*psHop]
			bestScore := math.Inf(-1)
			for cand := lo; cand <= hi && !center; cand += 2 {
				seg := p.in[cand : cand+psHop]
				var xy, yy float64
				for i := 0; i < psHop; i += 2 {
					xy += tpl[i] * seg[i]
					yy += seg[i] * seg[i]
				}
				if score := xy / math.Sqrt(yy+1); score > bestScore {
					bestScore, best = score, cand
				}
			}
		}

		seg := p.in[best : best+psWindow]
		for i := 0; i < psHop; i++ {
			v := p.tail[i] + seg[i]*psHann[i]
			if p.lp1 != nil {
				v = p.lp2.step(p.lp1.step(v))
			}
			p.stretched = append(p.stretched, v)
			p.tail[i] = seg[psHop+i] * psHann[psHop+i]
		}
		p.prevStart = best
		p.pos += psHop / p.ratio

		// Descarta a entrada que nenhum trecho futuro vai usar.
		drop := int(p.pos) - psSearch - psSlack
		if p.prevStart < drop {
			drop = p.prevStart
		}
		if drop > 0 {
			p.in = append(p.in[:0], p.in[drop:]...)
			p.pos -= float64(drop)
			p.prevStart -= drop
			p.scanned -= drop
			keep := p.clicks[:0]
			for _, c := range p.clicks {
				if c-drop >= 0 {
					keep = append(keep, c-drop)
				}
			}
			p.clicks = keep
		}
	}

	// Reamostra o sinal esticado (interpolação cúbica de 4 pontos).
	for int(p.readPos)+2 < len(p.stretched) {
		i := int(p.readPos)
		t := p.readPos - float64(i)
		y0, y1, y2, y3 := p.stretched[i-1], p.stretched[i], p.stretched[i+1], p.stretched[i+2]
		c1 := 0.5 * (y2 - y0)
		c2 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
		c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
		p.out = append(p.out, ((c3*t+c2)*t+c1)*t+y1)
		p.readPos += p.ratio
	}
	if k := int(p.readPos) - 1; k > 0 {
		p.stretched = append(p.stretched[:0], p.stretched[k:]...)
		p.readPos -= float64(k)
	}
	p.out = drain(buf, p.out, n)
}

// drain copia para buf as n primeiras amostras da fila e devolve o resto. Se
// faltar (não deveria: as filas começam com folga), completa com silêncio.
func drain(buf, queue []float64, n int) []float64 {
	k := copy(buf[:n], queue)
	for i := k; i < n; i++ {
		buf[i] = 0
	}
	return append(queue[:0], queue[k:]...)
}

// ---------------------------------------------------------------- envelope espectral

const (
	stftSize = 1024 // 21 ms
	stftBins = stftSize/2 + 1
	// O envelope é o espectro sem o "pente" dos harmônicos: guarda só o que
	// varia devagar ao longo da frequência. No cepstro isso é cortar tudo o
	// que está acima de ~1,7 ms — abaixo do período de qualquer voz (até
	// 500 Hz), então os harmônicos somem e os formantes ficam.
	envCut = 80
)

// spectrum faz a análise de um quadro e calcula o envelope espectral dele.
type spectrum struct {
	fft    *fft
	re, im [stftSize]float64 // espectro do quadro (preenchido por analyze)
	cr, ci [stftSize]float64
	env    [stftBins]float64 // logaritmo natural do envelope
}

// analyze recebe o quadro já multiplicado pela janela em s.re.
func (s *spectrum) analyze() {
	for i := range s.im {
		s.im[i] = 0
	}
	s.fft.transform(s.re[:], s.im[:], false)
	for k := 0; k < stftBins; k++ {
		l := math.Log(math.Hypot(s.re[k], s.im[k]) + 1)
		s.cr[k], s.ci[k] = l, 0
		if k > 0 && k < stftSize/2 {
			s.cr[stftSize-k], s.ci[stftSize-k] = l, 0
		}
	}
	s.fft.transform(s.cr[:], s.ci[:], true) // cepstro (real e simétrico)
	for q := 1; q < stftSize; q++ {
		d := q
		if d > stftSize/2 {
			d = stftSize - d
		}
		switch {
		case d >= envCut:
			s.cr[q] = 0
		case d > envCut/2: // borda suave: sem ondulação no envelope
			s.cr[q] *= 0.5 * (1 + math.Cos(math.Pi*float64(d-envCut/2)/float64(envCut-envCut/2)))
		}
		s.ci[q] = 0
	}
	s.ci[0] = 0
	s.fft.transform(s.cr[:], s.ci[:], false)
	copy(s.env[:], s.cr[:stftBins])
}

// ---------------------------------------------------------------- cor da voz

const (
	fsHop      = stftSize / 4
	fsMaxGain  = 2.76 // ±24 dB (em logaritmo natural)
	fsSmoothMs = 10.0 // o envelope é alisado no tempo: os formantes mudam devagar
)

var fsWindow = func() []float64 { // raiz de Hann: usada na análise e na síntese
	w := make([]float64, stftSize)
	for i := range w {
		w[i] = math.Sqrt(0.5 * (1 - math.Cos(2*math.Pi*float64(i)/stftSize)))
	}
	return w
}()

// formantShifter desloca a "cor" da voz (os formantes) sem mexer no tom. Para
// cada quadro calcula o envelope espectral e multiplica cada faixa por
// envelope deslocado ÷ envelope: os harmônicos ficam onde estavam e passam a
// ter a intensidade que o envelope teria na nova posição. Só os módulos
// mudam, e de forma suave ao longo da frequência, então não há a "fase
// borrada" dos vocoders de fase. scale > 1 sobe os formantes (trato vocal
// menor); < 1 desce (trato vocal maior). Atrasa o som stftSize amostras.
type formantShifter struct {
	scale  float64
	sp     spectrum
	in     []float64
	ola    [stftSize]float64
	out    []float64
	env    [stftBins]float64
	primed bool
}

func newFormantShifter(scale float64) *formantShifter {
	return &formantShifter{scale: scale, sp: spectrum{fft: newFFT(stftSize)}, out: make([]float64, stftSize)}
}

func (f *formantShifter) run(buf []float64) {
	n := len(buf)
	f.in = append(f.in, buf...)
	smooth := 1 - math.Exp(-fsHop*1000.0/SampleRate/fsSmoothMs)
	for len(f.in) >= stftSize {
		sp := &f.sp
		for i := 0; i < stftSize; i++ {
			sp.re[i] = f.in[i] * fsWindow[i]
		}
		sp.analyze()
		if !f.primed {
			f.primed = true
			f.env = sp.env
		}
		for k := range f.env {
			f.env[k] += smooth * (sp.env[k] - f.env[k])
		}
		for k := 0; k < stftBins; k++ {
			// a faixa k recebe a intensidade que o envelope tinha em k/scale
			src := float64(k) / f.scale
			g := -fsMaxGain
			if i := int(src); i < stftBins-1 {
				t := src - float64(i)
				g = f.env[i]*(1-t) + f.env[i+1]*t - f.env[k]
				g = math.Max(-fsMaxGain, math.Min(fsMaxGain, g))
			}
			m := math.Exp(g)
			sp.re[k] *= m
			sp.im[k] *= m
			if k > 0 && k < stftSize/2 {
				sp.re[stftSize-k] *= m
				sp.im[stftSize-k] *= m
			}
		}
		sp.fft.transform(sp.re[:], sp.im[:], true)
		for i := 0; i < stftSize; i++ {
			f.ola[i] += sp.re[i] * fsWindow[i] * 0.5 // 4 janelas de Hann sobrepostas somam 2
		}
		f.out = append(f.out, f.ola[:fsHop]...)
		copy(f.ola[:], f.ola[fsHop:])
		for i := stftSize - fsHop; i < stftSize; i++ {
			f.ola[i] = 0
		}
		f.in = append(f.in[:0], f.in[fsHop:]...)
	}
	f.out = drain(buf, f.out, n)
}

// voice monta uma voz com o tom multiplicado por pitch e os formantes por
// formant. Entre um homem e uma mulher o tom quase dobra, mas os formantes
// mudam só uns 15%: é essa diferença entre os dois números que faz o
// resultado soar como outra pessoa.
//
// O envelope é sempre medido do lado em que o tom é mais grave, onde os
// harmônicos estão mais juntos e o desenham melhor: antes de subir o tom,
// depois de descer.
func voice(pitch, formant float64) []stage {
	ps, fs := newPitchShifter(pitch), newFormantShifter(formant/pitch)
	if pitch > 1 {
		return []stage{fs, ps}
	}
	return []stage{ps, fs}
}

// voiceDelay é o atraso, em amostras, de uma voz montada por voice.
const voiceDelay = psDelay + stftSize

// layers soma vozes paralelas (cada uma com o seu peso) feitas do mesmo som.
// As camadas montadas por voice têm o mesmo atraso, então somam alinhadas.
type layers struct {
	chains   [][]stage
	gains    []float64
	tmp, sum []float64
}

func (l *layers) run(buf []float64) {
	if cap(l.tmp) < len(buf) {
		l.tmp, l.sum = make([]float64, len(buf)), make([]float64, len(buf))
	}
	tmp, sum := l.tmp[:len(buf)], l.sum[:len(buf)]
	for i := range sum {
		sum[i] = 0
	}
	for c, chain := range l.chains {
		copy(tmp, buf)
		for _, st := range chain {
			st.run(tmp)
		}
		for i, v := range tmp {
			sum[i] += v * l.gains[c]
		}
	}
	copy(buf, sum)
}

// ---------------------------------------------------------------- efeitos espectrais

const (
	stftRobot = iota
	stftWhisper
)

// stftEffect refaz a voz a partir só da "cor" dela (o envelope espectral),
// trocando o que a faz vibrar:
//   - robô: fase zero em todo quadro, com quadros a cada 10 ms → um trem de
//     pulsos de tom fixo (100 Hz) com as vogais originais;
//   - sussurro: fase aleatória → ruído com as vogais originais, sem tom.
//
// Usar o envelope, e não o espectro inteiro, é o que tira o tom original do
// resultado: com o espectro inteiro os harmônicos da voz continuam lá — o
// sussurro sai com tom, e o timbre do robô oscila conforme a entonação.
type stftEffect struct {
	mode int
	hop  int
	sp   spectrum
	win  [stftSize]float64
	in   []float64
	ola  [stftSize]float64
	out  []float64
	rng  *rand.Rand
}

func newSTFTEffect(mode int) *stftEffect {
	e := &stftEffect{mode: mode, sp: spectrum{fft: newFFT(stftSize)}, rng: rand.New(rand.NewSource(1))}
	e.hop = 480
	if mode == stftWhisper {
		e.hop = stftSize / 4
	}
	for i := range e.win {
		e.win[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/stftSize))
	}
	e.out = make([]float64, stftSize)
	return e
}

func (e *stftEffect) run(buf []float64) {
	n := len(buf)
	e.in = append(e.in, buf...)
	sp := &e.sp
	for len(e.in) >= stftSize {
		var inE float64
		for i := 0; i < stftSize; i++ {
			sp.re[i] = e.in[i] * e.win[i]
			inE += sp.re[i] * sp.re[i]
		}
		sp.analyze()

		for k := 0; k < stftBins; k++ {
			mag := math.Exp(sp.env[k]) - 1
			if mag < 0 {
				mag = 0
			}
			re, im := mag, 0.0
			if e.mode == stftWhisper && k > 0 && k < stftSize/2 {
				ph := 2 * math.Pi * e.rng.Float64()
				re, im = mag*math.Cos(ph), mag*math.Sin(ph)
			}
			sp.re[k], sp.im[k] = re, im
			if k > 0 && k < stftSize/2 {
				sp.re[stftSize-k], sp.im[stftSize-k] = re, -im
			}
		}
		sp.fft.transform(sp.re[:], sp.im[:], true)

		// Fase zero concentra a energia em torno de n=0: gira para o centro.
		var frame [stftSize]float64
		var outE float64
		for i := 0; i < stftSize; i++ {
			src := i
			if e.mode == stftRobot {
				src = (i + stftSize/2) % stftSize
			}
			frame[i] = sp.re[src] * e.win[i]
			outE += frame[i] * frame[i]
		}
		// Mantém o volume do quadro original.
		scale := 0.0
		if outE > 1e-9 {
			scale = math.Sqrt(inE/outE) * float64(e.hop) / (stftSize / 2)
		}
		for i := 0; i < stftSize; i++ {
			e.ola[i] += frame[i] * scale
		}
		e.out = append(e.out, e.ola[:e.hop]...)
		copy(e.ola[:], e.ola[e.hop:])
		for i := stftSize - e.hop; i < stftSize; i++ {
			e.ola[i] = 0
		}
		e.in = append(e.in[:0], e.in[e.hop:]...)
	}
	e.out = drain(buf, e.out, n)
}

// RenderEffect aplica um efeito a um áudio inteiro (48 kHz mono), fora do
// tempo real — para pré-escuta e demonstrações.
func RenderEffect(pcm []int16, effect VoiceEffect) []int16 {
	fx := NewEffectProcessor()
	lim := newLimiter(30000, 80)
	out := make([]int16, 0, len(pcm)+2*SampleRate)
	frame := make([]float64, FrameSize)
	tail := 0
	for pos := 0; pos < len(pcm) || tail > 0; pos += FrameSize {
		for i := range frame {
			frame[i] = 0
			if pos+i < len(pcm) {
				frame[i] = float64(pcm[pos+i])
			}
		}
		if pos >= len(pcm) {
			tail--
		} else if pos+FrameSize >= len(pcm) {
			tail = fx.Process(make([]float64, 0), effect) + 5 // deixa a cauda soar
		}
		fx.Process(frame, effect)
		lim.run(frame)
		block := make([]int16, FrameSize)
		toInt16(block, frame)
		out = append(out, block...)
	}
	return out
}
