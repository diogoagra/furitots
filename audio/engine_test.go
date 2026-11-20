package audio

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/hraban/opus.v2"
)

func sine(n int, freq, amp float64, phase *float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * math.Sin(*phase))
		*phase += 2 * math.Pi * freq / SampleRate
	}
	return out
}

// voiceSynth gera um sinal parecido com fala: tom com harmônicos, leve
// vibrato e sílabas (o volume sobe e desce ~3 vezes por segundo).
type voiceSynth struct {
	f0, amp float64
	phase   float64
	t       int
	steady  bool // sem sílabas nem vibrato (para medir tom)
}

func (v *voiceSynth) next(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		t := float64(v.t) / SampleRate
		f := v.f0
		env := 1.0
		if !v.steady {
			f *= 1 + 0.02*math.Sin(2*math.Pi*5*t)
			env = 0.25 + 0.75*0.5*(1-math.Cos(2*math.Pi*3*t))
		}
		v.phase += 2 * math.Pi * f / SampleRate
		var s float64
		for h := 1; h <= 12; h++ {
			s += math.Sin(float64(h)*v.phase) / float64(h)
		}
		out[i] = v.amp * env * s / 1.8
		v.t++
	}
	return out
}

func (v *voiceSynth) nextPCM(n int) []int16 {
	out := make([]int16, n)
	toInt16(out, v.next(n))
	return out
}

// ambient devolve um quadro de ruído de fundo bem baixo, como um microfone
// real em silêncio.
func ambient(rng *rand.Rand) []float64 {
	out := make([]float64, FrameSize)
	for i := range out {
		out[i] = rng.NormFloat64() * 40
	}
	return out
}

func ambientPCM(rng *rand.Rand) []int16 {
	out := make([]int16, FrameSize)
	toInt16(out, ambient(rng))
	return out
}

type capture struct {
	mu     sync.Mutex
	frames int
	ends   int
}

func (c *capture) send(b []byte) {
	c.mu.Lock()
	if len(b) == 0 {
		c.ends++
	} else {
		c.frames++
	}
	c.mu.Unlock()
}

func (c *capture) get() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames, c.ends
}

func newTestEngine(t *testing.T) (*Engine, *capture) {
	t.Helper()
	c := &capture{}
	e, err := newEngine(c.send) // sem txLoop: o teste dirige o relógio
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, c
}

// eachNoiseMode roda o teste com a supressão neural (o padrão) e com a
// clássica: o detector de voz tem de se comportar bem nas duas.
func eachNoiseMode(t *testing.T, fn func(t *testing.T, mode string)) {
	t.Helper()
	for _, mode := range []string{NoiseNeural, NoiseClassic} {
		t.Run(mode, func(t *testing.T) { fn(t, mode) })
	}
}

// ------------------------------------------------------------ transmissão

// runTx passa um sinal pelo transmissor com o teste de microfone ligado e
// devolve o que seria transmitido (alinhado com a entrada, descontado o
// atraso de decisão).
func runTx(e *Engine, in []float64) []float64 {
	e.SetMicTest(true)
	delay := lookahead*FrameSize + NoiseDelay(e.NoiseMode())
	out := make([]float64, 0, len(in)+delay)
	pcm := make([]int16, FrameSize)
	play := make([]int16, FrameSize)
	total := len(in)/FrameSize + lookahead + 1
	for f := 0; f < total; f++ {
		for i := range pcm {
			pcm[i] = 0
			if idx := f*FrameSize + i; idx < len(in) {
				pcm[i] = int16(math.Max(-32768, math.Min(32767, in[idx])))
			}
		}
		e.txFrame(pcm)
		e.fillPlayback(play)
		for _, s := range play {
			out = append(out, float64(s))
		}
	}
	return out[delay:]
}

func region(x []float64, fromMs, toMs int) []float64 {
	a, b := fromMs*SampleRate/1000, toMs*SampleRate/1000
	if b > len(x) {
		b = len(x)
	}
	if a >= b {
		return nil
	}
	return x[a:b]
}

// Modo por volume: abre com sinal acima do limiar, inclui os 60 ms anteriores
// e, depois da cauda, envia o marcador de fim de fala.
func TestLevelGateAndEndMarker(t *testing.T) {
	e, c := newTestEngine(t)
	e.SetVADMode(VADLevel)
	phase := 0.0
	silence := make([]int16, FrameSize)

	for i := 0; i < 6; i++ {
		e.txFrame(silence)
	}
	if f, _ := c.get(); f != 0 {
		t.Fatalf("silêncio não deve transmitir, enviou %d", f)
	}
	e.txFrame(sine(FrameSize, 440, 8000, &phase))
	for i := 0; i < levelHangover+lookahead+12; i++ {
		e.txFrame(silence)
	}
	f, ends := c.get()
	// 3 quadros de antecipação + o do tom (que vaza 10 ms para o seguinte por
	// causa da supressão) + a cauda.
	if want := lookahead + 1 + levelHangover; f < want || f > want+2 {
		t.Errorf("enviou %d quadros, esperado entre %d e %d", f, want, want+2)
	}
	if ends != 1 {
		t.Errorf("deve enviar exatamente 1 marcador de fim, enviou %d", ends)
	}
	if e.Transmitting() {
		t.Error("não deveria estar transmitindo")
	}
}

func TestMutedDoesNotTransmit(t *testing.T) {
	e, c := newTestEngine(t)
	e.SetVADMode(VADLevel)
	e.SetMuted(true)
	rng := rand.New(rand.NewSource(2))
	v := &voiceSynth{f0: 140, amp: 8000}
	for i := 0; i < 10; i++ {
		e.txFrame(ambientPCM(rng))
	}
	for i := 0; i < 10; i++ {
		e.txFrame(v.nextPCM(FrameSize))
	}
	if f, _ := c.get(); f != 0 {
		t.Fatalf("mutado enviou %d quadros", f)
	}
	if e.MicLevel() < 1000 {
		t.Errorf("o medidor deve continuar funcionando mutado: %.0f", e.MicLevel())
	}
}

// Modo automático: chiado não abre o microfone; voz abre, e ao parar de
// falar a transmissão termina sozinha.
func TestAutoVADOpensOnlyOnVoice(t *testing.T) {
	eachNoiseMode(t, testAutoVADOpensOnlyOnVoice)
}

func testAutoVADOpensOnlyOnVoice(t *testing.T, mode string) {
	e, c := newTestEngine(t)
	e.SetNoiseMode(mode)
	rng := rand.New(rand.NewSource(3))
	noise := func() []int16 {
		out := make([]int16, FrameSize)
		for i := range out {
			out[i] = int16(rng.NormFloat64() * 400)
		}
		return out
	}
	for i := 0; i < 150; i++ { // 3 s de chiado
		e.txFrame(noise())
	}
	if f, _ := c.get(); f != 0 {
		t.Fatalf("chiado abriu o microfone: %d quadros enviados", f)
	}

	v := &voiceSynth{f0: 130, amp: 6000}
	for i := 0; i < 50; i++ { // 1 s de voz sobre o chiado
		fr := noise()
		voice := v.nextPCM(FrameSize)
		for j := range fr {
			fr[j] += voice[j]
		}
		e.txFrame(fr)
	}
	if e.VoiceProbability() < 0.5 {
		t.Errorf("probabilidade de voz baixa durante a fala: %.2f", e.VoiceProbability())
	}
	for i := 0; i < 60; i++ {
		e.txFrame(noise())
	}
	f, ends := c.get()
	// 1 s de voz + fim de palavra (120 ms) + espera (300 ms)
	if f < 62 || f > 78 {
		t.Errorf("enviou %d quadros para 1 s de voz; esperado ~71", f)
	}
	if ends != 1 {
		t.Errorf("esperado 1 fim de fala, veio %d", ends)
	}
}

// Só teclado (dois tipos), sem ninguém falando: o microfone nunca abre.
//
// No modo neural isso vale para qualquer sequência de teclas e qualquer
// posição delas em relação aos quadros: são 16 execuções de 8 s por teclado.
// No modo clássico só vale para as duas sequências deste teste; com outras
// sementes o teclado sintético abre o microfone numa parte das execuções
// (TestEvalKeyboard mostra a conta), e é essa a diferença que o RNNoise faz.
func TestKeyboardNeverOpensMic(t *testing.T) {
	run := func(mode string, kind int, seed int64, shift int) {
		name := fmt.Sprintf("%s/%s/semente%d/deslocado%d", mode, []string{"notebook", "mecânico"}[kind], seed, shift)
		t.Run(name, func(t *testing.T) {
			t.Parallel() // cada execução tem o seu motor
			e, c := newTestEngine(t)
			e.SetNoiseMode(mode)
			rng := rand.New(rand.NewSource(seed))
			in := make([]float64, 8*SampleRate)
			for i := range in {
				in[i] = rng.NormFloat64() * 60
			}
			SynthKeyboard(in[SampleRate+shift:], kind, rng)
			runTx(e, in)
			if f, _ := c.get(); f != 0 {
				t.Errorf("o teclado abriu o microfone (%d quadros transmitidos)", f)
			}
		})
	}
	for kind := 0; kind < 2; kind++ {
		run(NoiseClassic, kind, int64(20+kind), 0)
		for seed := int64(0); seed < 8; seed++ {
			for _, shift := range []int{0, 480} { // tecla no começo ou no meio do quadro
				run(NoiseNeural, kind, 100+7*seed+int64(kind), shift)
			}
		}
	}
}

// Falando e digitando: a digitação nas pausas entre as falas não é
// transmitida, e a fala continua inteira.
func TestTypingBetweenSpeechIsRemoved(t *testing.T) {
	eachNoiseMode(t, testTypingBetweenSpeechIsRemoved)
}

func testTypingBetweenSpeechIsRemoved(t *testing.T, mode string) {
	for kind, name := range []string{"notebook", "mecânico"} {
		e, _ := newTestEngine(t)
		e.SetNoiseMode(mode)
		e.SetAutoGain(false) // compara níveis sem o ganho automático
		rng := rand.New(rand.NewSource(int64(30 + kind)))
		const seconds = 8
		voice := make([]float64, seconds*SampleRate)
		v := &voiceSynth{f0: 135, amp: 6000}
		// fala de 700 ms a cada 1,5 s, começando em 1 s
		for start := SampleRate; start+SampleRate < len(voice); start += 3 * SampleRate / 2 {
			copy(voice[start:], v.next(7*SampleRate/10))
		}
		typing := make([]float64, len(voice))
		SynthKeyboard(typing, kind, rng)
		in := make([]float64, len(voice))
		for i := range in {
			in[i] = voice[i] + typing[i] + rng.NormFloat64()*60
		}
		out := runTx(e, in)

		var speechIn, speechOut, pauseIn, pauseOut float64
		for start := 1000; start+1500 <= seconds*1000; start += 1500 {
			speechIn += rmsFloat(region(voice, start+60, start+640))
			speechOut += rmsFloat(region(out, start+60, start+640))
			// pausa: de 250 ms depois do fim da fala até 100 ms antes da próxima
			pauseIn += rmsFloat(region(typing, start+950, start+1400))
			pauseOut += rmsFloat(region(out, start+950, start+1400))
		}
		leak := 20 * math.Log10((pauseOut+1e-9)/pauseIn)
		keep := 20 * math.Log10(speechOut/speechIn)
		t.Logf("teclado %s: digitação nas pausas %.1f dB; fala %.1f dB em relação à entrada", name, leak, keep)
		if leak > -30 {
			t.Errorf("teclado %s: digitação vazando nas pausas (%.1f dB)", name, leak)
		}
		if keep < -3 {
			t.Errorf("teclado %s: a fala foi cortada (%.1f dB)", name, keep)
		}
	}
}

// Consoantes de sopro ("s") não têm tom: precisam passar tanto antes da
// vogal ("sim") quanto no fim da palavra ("mas").
//
// No modo neural o "s" inicial é também o teste de Engine.pick: sozinho, antes
// de qualquer vogal, o RNNoise o trata como ruído e o abafa em 10 dB.
func TestFricativesSurvive(t *testing.T) {
	eachNoiseMode(t, testFricativesSurvive)
}

func testFricativesSurvive(t *testing.T, mode string) {
	e, _ := newTestEngine(t)
	e.SetNoiseMode(mode)
	e.SetAutoGain(false)
	rng := rand.New(rand.NewSource(40))
	hiss := func(n int) []float64 { // chiado de "s": ruído acima de ~4 kHz
		hp1, hp2 := newHighpass(4500, 0.707), newHighpass(4500, 0.707)
		out := make([]float64, n)
		for i := range out {
			env := math.Min(1, math.Min(float64(i), float64(n-i))/480)
			out[i] = hp2.step(hp1.step(rng.NormFloat64()*5000)) * env
		}
		return out
	}
	in := make([]float64, 4*SampleRate)
	for i := range in {
		in[i] = rng.NormFloat64() * 60
	}
	add := func(atMs int, x []float64) {
		for i, v := range x {
			in[atMs*48+i] += v
		}
	}
	v := &voiceSynth{f0: 140, amp: 6000}
	add(1000, hiss(120*48)) // "s" inicial…
	add(1120, v.next(300*48))
	add(2200, v.next(300*48)) // …e "s" final
	add(2500, hiss(180*48))
	out := runTx(e, in)

	for _, c := range []struct {
		name     string
		from, to int
	}{{"inicial", 1010, 1110}, {"final", 2510, 2670}} {
		ratio := 20 * math.Log10((rmsFloat(region(out, c.from, c.to))+1e-9)/rmsFloat(region(in, c.from, c.to)))
		t.Logf("\"s\" %s: %.1f dB em relação à entrada", c.name, ratio)
		if ratio < -6 {
			t.Errorf("\"s\" %s foi cortado (%.1f dB)", c.name, ratio)
		}
	}
}

// traceTx passa um sinal pelo transmissor e devolve, para cada amostra da
// entrada, quanto dela foi posto na fila de envio (0 a 1), e o maior atraso
// extra da fila (em amostras) a cada quadro. Diferente de runTx, não depende
// de quando o áudio sai: o começo de uma palavra sai atrasado e é recuperado.
// sent é o áudio posto na fila, na posição da entrada a que corresponde (antes
// das rampas de entrada e de saída).
func traceTx(e *Engine, in []float64) (weight []float64, lag []int, sent []float64) {
	weight = make([]float64, len(in))
	sent = make([]float64, len(in))
	last := -2
	e.onQueue = func(seq int, whole bool, audio []float64) {
		// o quadro seq leva a entrada a partir de seq*960 menos o atraso da supressão
		base := seq*FrameSize - NoiseDelay(e.NoiseMode())
		for i := 0; i < FrameSize; i++ {
			if idx := base + i; idx >= 0 && idx < len(in) {
				w := 1.0
				switch {
				case !whole:
					w = 0.5 * (1 + math.Cos(math.Pi*(float64(i)+0.5)/FrameSize))
				case seq != last+1 && i < fadeSamples:
					w = float64(i) / fadeSamples
				}
				weight[idx] = w
				sent[idx] = audio[i]
			}
		}
		last = seq
		if !whole {
			last = -2
		}
	}
	pcm := make([]int16, FrameSize)
	for f := 0; f < len(in)/FrameSize+lookahead+preRollFrames; f++ {
		for i := range pcm {
			pcm[i] = 0
			if idx := f*FrameSize + i; idx < len(in) {
				pcm[i] = int16(math.Max(-32768, math.Min(32767, in[idx])))
			}
		}
		e.txFrame(pcm)
		lag = append(lag, len(e.outq))
	}
	return weight, lag, sent
}

// kept devolve, em dB, quanto da energia de sig entre fromMs e toMs foi transmitido.
func kept(sig, weight []float64, fromMs, toMs int) float64 {
	var o, s float64
	for i := fromMs * 48; i < toMs*48 && i < len(sig); i++ {
		o += sig[i] * sig[i] * weight[i] * weight[i]
		s += sig[i] * sig[i]
	}
	return 10 * math.Log10((o+1e-9)/(s+1e-9))
}

// O começo e o fim de uma frase chegam inteiros: a consoante antes da vogal
// (a voz só é confirmada depois), a vogal que vai sumindo aos poucos e a
// última sílaba sem tom depois de uma parada ("per-to").
func TestSentenceEdgesAreKept(t *testing.T) {
	eachNoiseMode(t, testSentenceEdgesAreKept)
}

func testSentenceEdgesAreKept(t *testing.T, mode string) {
	e, _ := newTestEngine(t)
	e.SetNoiseMode(mode)
	rng := rand.New(rand.NewSource(60))
	in := make([]float64, 5*SampleRate)
	for i := range in {
		in[i] = rng.NormFloat64() * 60
	}
	sig := make([]float64, len(in))
	add := func(atMs int, x []float64) {
		for i, v := range x {
			sig[atMs*48+i] += v
		}
	}
	// ruído na faixa da voz, sem tom: consoante surda ou sílaba rouca
	breath := func(ms int, amp float64) []float64 {
		lp1, lp2 := newLowpass(2500, 0.707), newHighpass(500, 0.707)
		out := make([]float64, ms*48)
		for i := range out {
			env := math.Min(1, math.Min(float64(i), float64(len(out)-i))/240)
			out[i] = lp2.step(lp1.step(rng.NormFloat64()*amp)) * env
		}
		return out
	}
	v := &voiceSynth{f0: 125, amp: 6000, steady: true}
	// 2000 ms: consoante fraca de 100 ms, vogal de 300 ms, e a vogal sumindo em 250 ms
	add(2000, breath(100, 1500))
	add(2100, v.next(300*48))
	fadeOut := v.next(250 * 48)
	for i := range fadeOut {
		fadeOut[i] *= math.Exp(-float64(i) / (60 * 48)) // -36 dB em 250 ms
	}
	add(2400, fadeOut)
	// 3500 ms: vogal de 300 ms, parada de 110 ms e uma sílaba sem tom de 130 ms
	add(3500, v.next(300*48))
	add(3910, breath(130, 6000))
	for i := range in {
		in[i] += sig[i]
	}
	weight, lag, _ := traceTx(e, in)

	for _, c := range []struct {
		name     string
		from, to int
		min      float64
	}{
		{"consoante inicial", 2000, 2100, -1},
		{"vogal", 2100, 2400, -0.1},
		{"voz sumindo", 2400, 2560, -0.5}, // até -23 dB do nível da vogal
		{"sílaba final sem tom", 3910, 4040, -1},
	} {
		got := kept(sig, weight, c.from, c.to)
		t.Logf("%s: %.2f dB transmitidos", c.name, got)
		if got < c.min {
			t.Errorf("%s cortada: %.2f dB (mínimo %.1f)", c.name, got, c.min)
		}
	}
	// O que entrou a mais no começo é recuperado: 1 s depois a fila está em dia.
	worst := 0
	for f, l := range lag {
		if ms := f * 20; ms >= 2100 && ms < 2300 && l > worst {
			worst = l
		}
	}
	t.Logf("atraso extra no começo da frase: até %d ms", worst/48)
	if worst < 40*48 {
		t.Errorf("a consoante inicial deveria ter deixado atraso na fila (%d ms)", worst/48)
	}
	if l := lag[3000/20]; l > 15*48 {
		t.Errorf("o atraso não foi recuperado: %d ms sobrando 1 s depois", l/48)
	}
}

// A recuperação de atraso tira períodos inteiros da voz: o que sai continua
// sendo a mesma onda, sem emenda audível, e nunca mais de 25% mais rápido.
func TestCatchUpIsSeamless(t *testing.T) {
	for _, f0 := range []float64{95, 125, 210, 290} {
		e, _ := newTestEngine(t)
		v := &voiceSynth{f0: f0, amp: 6000} // com vibrato e sílabas
		src := v.next(SampleRate)
		e.outq = append(e.outq, src[:FrameSize+preRollFrames*FrameSize]...) // 160 ms de atraso
		e.outPeak = 6000
		fed := len(e.outq)
		var out []float64
		frames := 0
		for len(e.outq) >= FrameSize {
			before := len(e.outq)
			e.catchUp()
			if removed := before - len(e.outq); removed > catchUpMaxLag {
				t.Fatalf("f0=%.0f: removeu %d amostras de uma vez", f0, removed)
			}
			out = append(out, e.outq[:FrameSize]...)
			e.outq = e.outq[:copy(e.outq, e.outq[FrameSize:])]
			if fed+FrameSize <= len(src) { // o microfone continua chegando em tempo real
				e.outq = append(e.outq, src[fed:fed+FrameSize]...)
				fed += FrameSize
			}
			frames++
		}
		removed := fed - len(out) - len(e.outq)
		speed := float64(len(out)+removed) / float64(len(out))
		// Uma emenda mal feita é um degrau na onda. Compara o maior salto entre
		// amostras vizinhas com o do sinal original.
		jump := func(x []float64) float64 {
			m := 0.0
			for i := 1; i < len(x); i++ {
				m = math.Max(m, math.Abs(x[i]-x[i-1]))
			}
			return m
		}
		t.Logf("f0=%.0f Hz: %d ms removidos em %d ms (%.0f%% mais rápido), maior salto %.0f (original %.0f)",
			f0, removed/48, len(out)/48, (speed-1)*100, jump(out), jump(src))
		if removed < 140*48 {
			t.Errorf("f0=%.0f: só recuperou %d ms dos 160", f0, removed/48)
		}
		if speed > 1.26 {
			t.Errorf("f0=%.0f: fala %.0f%% mais rápida (máximo 25%%)", f0, (speed-1)*100)
		}
		if jump(out) > 1.15*jump(src) {
			t.Errorf("f0=%.0f: emenda com degrau (%.0f contra %.0f)", f0, jump(out), jump(src))
		}
		// E o tom não muda: mesmo período antes e depois.
		if got, want := estimateF0(out[len(out)/2:len(out)/2+4800]), estimateF0(src[:4800]); math.Abs(got-want) > 0.06*want {
			t.Errorf("f0=%.0f: tom mudou de %.0f para %.0f Hz", f0, want, got)
		}
	}
}

// A gravação junta, num WAV, as vozes recebidas e o que transmitimos; avisos
// sonoros locais ficam de fora.
func TestRecordingMixesBothSides(t *testing.T) {
	e, _ := newTestEngine(t)
	e.SetVADMode(VADLevel)
	e.SetVADThreshold(0)
	e.SetAutoGain(false)
	e.SetNoiseMode(NoiseOff)
	e.SetNormalizeVoices(false)
	path := filepath.Join(t.TempDir(), "conversa.wav")
	if err := e.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	if err := e.StartRecording(path); err == nil {
		t.Error("duas gravações ao mesmo tempo deveriam ser recusadas")
	}
	if e.RecordingSince().IsZero() {
		t.Error("RecordingSince deveria indicar a gravação em andamento")
	}

	other := encodeTone(t, 100, 1000, 5000) // 2 s de outra pessoa em 1 kHz
	var micPhase, noticePhase float64
	play := make([]int16, FrameSize)
	for f := 0; f < 100; f++ {
		e.PushVoice(7, uint16(f), other[f])
		e.txFrame(sine(FrameSize, 300, 5000, &micPhase)) // nós, em 300 Hz
		if f == 20 {
			e.PlayLocal(sine(SampleRate/2, 3000, 8000, &noticePhase)) // aviso local em 3 kHz
		}
		e.fillPlayback(play)
	}
	got, dur, err := e.StopRecording()
	if err != nil || got != path {
		t.Fatalf("StopRecording: %q, %v", got, err)
	}
	if dur != 2*time.Second {
		t.Errorf("duração gravada %v, esperado 2 s", dur)
	}
	if p, _, _ := e.StopRecording(); p != "" {
		t.Error("parar sem gravação em andamento não deveria devolver arquivo")
	}

	pcm, err := DecodeAudioFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 2*SampleRate {
		t.Fatalf("arquivo com %d amostras, esperado %d", len(pcm), 2*SampleRate)
	}
	x := make([]float64, SampleRate)
	toFloat(x, pcm[SampleRate/2:3*SampleRate/2]) // 1 s do meio, com o aviso tocando
	tone := func(freq float64) float64 {         // amplitude da componente em freq
		var re, im float64
		for i, v := range x {
			a := 2 * math.Pi * freq * float64(i) / SampleRate
			re += v * math.Cos(a)
			im += v * math.Sin(a)
		}
		return 2 * math.Hypot(re, im) / float64(len(x))
	}
	theirs, ours, notice := tone(1000), tone(300), tone(3000)
	t.Logf("na gravação: a outra pessoa %.0f, nós %.0f, aviso local %.0f", theirs, ours, notice)
	if theirs < 3500 || ours < 3500 {
		t.Errorf("a gravação deveria ter os dois lados da conversa (%.0f e %.0f)", theirs, ours)
	}
	if notice > 100 {
		t.Errorf("aviso sonoro local entrou na gravação (%.0f)", notice)
	}
}

// encodeTone devolve n quadros Opus de um tom.
func encodeTone(t *testing.T, n int, freq, amp float64) [][]byte {
	t.Helper()
	enc, err := opus.NewEncoder(SampleRate, 1, opus.AppAudio)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(96000)
	var phase float64
	out := make([][]byte, n)
	for i := range out {
		buf := make([]byte, 1500)
		k, err := enc.Encode(sine(FrameSize, freq, amp, &phase), buf)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = buf[:k]
	}
	return out
}

// O soundboard transmite mesmo sem microfone e toca localmente pela mesma
// mixagem das vozes — sem gerar áudio extra no relógio de saída.
func TestSoundboardSingleClock(t *testing.T) {
	e, c := newTestEngine(t)
	phase := 0.0
	e.Soundboard().play(sine(SampleRate/2, 330, 9000, &phase)) // 0,5 s
	out := make([]int16, FrameSize)
	heard, sent := 0, 0
	for i := 0; i < 400 && e.Soundboard().IsPlaying(); i++ {
		e.txFrame(nil)
		e.fillPlayback(out)
		sent++
		if rms(out) > 60 {
			heard++
		}
	}
	for i := 0; i < 10; i++ {
		e.txFrame(nil)
		e.fillPlayback(out)
	}
	f, ends := c.get()
	if f != sent || ends != 1 {
		t.Errorf("transmitiu %d quadros e %d fim; esperado %d e 1", f, ends, sent)
	}
	if heard < sent*6/10 {
		t.Errorf("ouviu %d de %d quadros locais", heard, sent)
	}
	e.lbMu.Lock()
	pending := len(e.loopback)
	e.lbMu.Unlock()
	if pending != 0 {
		t.Errorf("sobrou %d amostras na fila local (latência acumulada)", pending)
	}
}

// Com os alto-falantes mutados nada sai deles: nem os avisos sonoros (os que
// estavam tocando ou na fila são cortados), nem o retorno do soundboard — que
// continua sendo transmitido. A única exceção é o aviso de que o som foi
// mutado. Ao desmutar, tudo volta.
func TestDeafenedSilencesLocalSounds(t *testing.T) {
	e, c := newTestEngine(t)
	out := make([]int16, FrameSize)
	phase := 0.0
	// quantos dos próximos n quadros saem com som nos alto-falantes
	heard := func(n int) int {
		loud := 0
		for i := 0; i < n; i++ {
			e.txFrame(nil)
			e.fillPlayback(out)
			if rms(out) > 60 {
				loud++
			}
		}
		return loud
	}

	e.PlayLocal(sine(SampleRate, 500, 8000, &phase)) // aviso de 1 s
	e.PlayLocal(sine(SampleRate, 800, 8000, &phase)) // e outro na fila
	if n := heard(5); n != 5 {
		t.Fatalf("o aviso deveria estar tocando antes de mutar (%d de 5 quadros)", n)
	}
	e.SetDeafened(true)
	if n := heard(120); n != 0 {
		t.Errorf("mutar os alto-falantes deveria cortar o aviso em andamento e a fila (%d quadros com som)", n)
	}
	e.PlayLocal(sine(SampleRate/2, 500, 8000, &phase))
	if n := heard(40); n != 0 {
		t.Errorf("aviso novo com os alto-falantes mutados não deveria tocar (%d quadros com som)", n)
	}

	e.Soundboard().play(sine(SampleRate/2, 330, 9000, &phase))
	if n := heard(40); n != 0 {
		t.Errorf("o retorno do soundboard não deveria tocar com os alto-falantes mutados (%d quadros com som)", n)
	}
	if f, _ := c.get(); f < 20 {
		t.Errorf("o soundboard deveria continuar sendo transmitido (%d quadros)", f)
	}

	e.PlayLocalDeafened(sine(SampleRate/2, 500, 8000, &phase))
	if n := heard(40); n < 24 {
		t.Errorf("o aviso de som mutado deveria tocar (%d quadros com som)", n)
	}

	e.SetDeafened(false)
	e.PlayLocal(sine(SampleRate/2, 500, 8000, &phase))
	if n := heard(40); n < 24 {
		t.Errorf("ao desmutar, os avisos deveriam voltar (%d quadros com som)", n)
	}
	e.Soundboard().play(sine(SampleRate/2, 330, 9000, &phase))
	if n := heard(40); n < 15 {
		t.Errorf("ao desmutar, o retorno do soundboard deveria voltar (%d quadros com som)", n)
	}
}

// Avisos sonoros tocam só localmente, um de cada vez, e não são transmitidos.
func TestPlayLocalQueuesAndIsNotTransmitted(t *testing.T) {
	e, c := newTestEngine(t)
	phase := 0.0
	e.PlayLocal(sine(FrameSize*3, 500, 8000, &phase))
	e.PlayLocal(sine(FrameSize*2, 800, 8000, &phase))
	for i := 0; i < 10; i++ {
		e.PlayLocal(sine(FrameSize, 300, 8000, &phase)) // a fila tem limite
	}
	out := make([]int16, FrameSize)
	var pattern []bool
	peak := 0.0
	for i := 0; i < 60; i++ {
		e.txFrame(nil)
		e.fillPlayback(out)
		pattern = append(pattern, rms(out) > 1000)
		for _, s := range out {
			peak = math.Max(peak, math.Abs(float64(s)))
		}
	}
	// 3 quadros, pausa, 2 quadros, pausa, e mais dois avisos de 1 quadro
	loud, bursts := 0, 0
	for i, on := range pattern {
		if on {
			loud++
			if i == 0 || !pattern[i-1] {
				bursts++
			}
		}
	}
	// 3 + 2 + 1 + 1 quadros de som; como a pausa entre avisos (125 ms) não é
	// múltipla de 20 ms, cada aviso seguinte pode ocupar um quadro a mais.
	if bursts != maxLocalQueue || loud < 7 || loud > 10 {
		t.Errorf("esperava %d avisos em sequência (7 a 10 quadros), tocou %d avisos em %d quadros", maxLocalQueue, bursts, loud)
	}
	if !pattern[0] || !pattern[2] || pattern[3] {
		t.Errorf("o primeiro aviso deve tocar inteiro antes do segundo: %v", pattern[:12])
	}
	if peak > 8200 {
		t.Errorf("avisos se somaram (pico %.0f): deveriam tocar um de cada vez", peak)
	}
	if f, _ := c.get(); f != 0 {
		t.Errorf("aviso sonoro não pode ser transmitido (%d quadros)", f)
	}
}

// Um aviso sonoro sai pelo alto-falante e volta pelo microfone (aqui, uma voz
// que começa junto com o aviso): não pode ser transmitido. Fala que já estava
// em andamento continua; depois do aviso, o microfone volta a abrir.
func TestNotificationPickedUpByMicIsNotSent(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	feed := func(e *Engine, frames int, v *voiceSynth) {
		out := make([]int16, FrameSize)
		for i := 0; i < frames; i++ {
			fr := ambientPCM(rng)
			if v != nil {
				voice := v.nextPCM(FrameSize)
				for j := range fr {
					fr[j] += voice[j]
				}
			}
			e.txFrame(fr)
			e.fillPlayback(out) // é aqui que o aviso "toca"
		}
	}
	notice := make([]int16, SampleRate) // aviso de 1 s
	echo := func() *voiceSynth { return &voiceSynth{f0: 180, amp: 4000} }

	// 1) aviso tocando e o microfone captando voz o tempo todo
	e, c := newTestEngine(t)
	feed(e, 25, nil)
	e.PlayLocal(notice)
	feed(e, 50+localGap/FrameSize+10, echo())
	if f, _ := c.get(); f != 0 {
		t.Errorf("o som do aviso captado pelo microfone foi transmitido (%d quadros)", f)
	}

	// 2) depois que o aviso acaba, a fala abre o microfone normalmente
	feed(e, localHoldFrames+5, nil)
	feed(e, 40, &voiceSynth{f0: 130, amp: 6000})
	if f, _ := c.get(); f < 30 {
		t.Errorf("depois do aviso a fala deveria transmitir, enviou %d quadros", f)
	}

	// 3) quem já estava falando quando o aviso tocou não é cortado
	e2, c2 := newTestEngine(t)
	me := &voiceSynth{f0: 130, amp: 6000}
	feed(e2, 25, nil)
	feed(e2, 20, me)
	before, _ := c2.get()
	e2.PlayLocal(notice)
	feed(e2, 40, me)
	after, _ := c2.get()
	if after-before < 36 {
		t.Errorf("fala em andamento foi cortada pelo aviso: só %d de 40 quadros", after-before)
	}
}

// Os avisos do TeamSpeak são frases faladas ("sound resumed", "user joined
// your channel") cheias de "s". O chiado de um "s" abre o microfone por um
// caminho próprio (consoantes de sopro), que também precisa respeitar o aviso.
func TestNotificationSibilantIsNotSent(t *testing.T) {
	rng := rand.New(rand.NewSource(81))
	hp1, hp2 := newHighpass(4500, 0.707), newHighpass(4500, 0.707)
	hiss := func() []int16 { // chiado de "s": ruído acima de ~4 kHz
		fr := ambientPCM(rng)
		for i := range fr {
			fr[i] += int16(hp2.step(hp1.step(rng.NormFloat64() * 5000)))
		}
		return fr
	}
	out := make([]int16, FrameSize)
	step := func(e *Engine, fr []int16) {
		e.txFrame(fr)
		e.fillPlayback(out)
	}

	// Sem aviso tocando, o mesmo chiado abre o microfone: garante que o
	// sinal do teste é mesmo um "s" para o detector.
	ref, cr := newTestEngine(t)
	for i := 0; i < 25; i++ {
		step(ref, ambientPCM(rng))
	}
	for i := 0; i < 10; i++ {
		step(ref, hiss())
	}
	if f, _ := cr.get(); f == 0 {
		t.Fatal("o chiado do teste não abre o microfone nem sem aviso; o teste não prova nada")
	}

	// "s…ound re-s-umed": chiado, voz, chiado, voz — tudo vindo do aviso.
	e, c := newTestEngine(t)
	for i := 0; i < 25; i++ {
		step(e, ambientPCM(rng))
	}
	e.PlayLocal(make([]int16, SampleRate))
	v := &voiceSynth{f0: 180, amp: 4000}
	for i := 0; i < 50+localGap/FrameSize; i++ {
		switch {
		case i < 8 || (i >= 25 && i < 32):
			step(e, hiss())
		default:
			fr := ambientPCM(rng)
			voice := v.nextPCM(FrameSize)
			for j := range fr {
				fr[j] += voice[j]
			}
			step(e, fr)
		}
	}
	for i := 0; i < localHoldFrames+10; i++ {
		step(e, ambientPCM(rng))
	}
	if f, _ := c.get(); f != 0 {
		t.Errorf("o chiado do aviso captado pelo microfone abriu a transmissão (%d quadros)", f)
	}

	// Um "s" nosso que já estava saindo quando o aviso começou não é cortado.
	e2, c2 := newTestEngine(t)
	for i := 0; i < 25; i++ {
		step(e2, ambientPCM(rng))
	}
	for i := 0; i < 6; i++ {
		step(e2, hiss())
	}
	before, _ := c2.get()
	if before == 0 {
		t.Fatal("o \"s\" inicial deveria ter aberto o microfone antes do aviso")
	}
	e2.PlayLocal(make([]int16, SampleRate))
	me := &voiceSynth{f0: 130, amp: 6000}
	for i := 0; i < 6; i++ {
		step(e2, hiss())
	}
	for i := 0; i < 30; i++ {
		step(e2, me.nextPCM(FrameSize))
	}
	after, _ := c2.get()
	if after-before < 32 {
		t.Errorf("palavra que começou com \"s\" antes do aviso foi cortada: %d de 36 quadros", after-before)
	}
}

// noticeClip imita um aviso do TeamSpeak: uma voz dizendo três "sílabas".
func noticeClip() []int16 {
	v := &voiceSynth{f0: 180, amp: 9000}
	clip := make([]int16, 0, SampleRate)
	for _, part := range []struct{ on, off int }{{220, 90}, {160, 70}, {260, 0}} {
		clip = append(clip, v.nextPCM(part.on*48)...)
		clip = append(clip, make([]int16, part.off*48)...)
	}
	return clip
}

// noticeRig alimenta o motor quadro a quadro como os dispositivos fariam: o
// que sai do alto-falante volta (ou não) para o microfone com atraso.
type noticeRig struct {
	e     *Engine
	c     *capture
	rng   *rand.Rand
	delay [][]int16 // saída dos últimos quadros, a caminho do microfone
	echo  float64   // quanto do alto-falante chega ao microfone (0 = fone de ouvido)
}

func newNoticeRig(t *testing.T, echo float64) *noticeRig {
	e, c := newTestEngine(t)
	return &noticeRig{e: e, c: c, rng: rand.New(rand.NewSource(77)), echo: echo, delay: make([][]int16, 3)}
}

// run avança n quadros; me, se não for nil, é a nossa voz no microfone.
func (r *noticeRig) run(n int, me *voiceSynth) {
	for i := 0; i < n; i++ {
		fr := ambientPCM(r.rng)
		if heard := r.delay[0]; heard != nil && r.echo > 0 {
			for j := range fr {
				fr[j] += int16(float64(heard[j]) * r.echo)
			}
		}
		if me != nil {
			voice := me.nextPCM(FrameSize)
			for j := range fr {
				fr[j] += voice[j]
			}
		}
		r.e.txFrame(fr)
		out := make([]int16, FrameSize)
		r.e.fillPlayback(out)
		r.delay = append(r.delay[1:], out)
	}
}

func (r *noticeRig) sent() int { f, _ := r.c.get(); return f }

// Com fone de ouvido o microfone não ouve os avisos; segurar a transmissão
// enquanto eles tocam só cortaria o começo da nossa fala. O motor aprende
// isso no primeiro aviso e deixa de segurar; com caixas de som, continua.
func TestNotificationHoldLearnsHeadphones(t *testing.T) {
	noticeFrames := (len(noticeClip()) + localGap) / FrameSize
	whole := noticeFrames + localHoldFrames + 5

	// ---- fone de ouvido
	r := newNoticeRig(t, 0)
	r.run(25, nil)
	r.e.PlayLocal(noticeClip())
	r.run(whole, nil)
	if got := r.e.coupling.Load(); got != couplingSilent {
		t.Fatalf("aviso tocou e o microfone ficou em silêncio: deveria concluir que não ouve os avisos (estado %d)", got)
	}
	// agora falamos durante um aviso: a fala passa
	r.e.PlayLocal(noticeClip())
	r.run(5, nil)
	before := r.sent()
	r.run(30, &voiceSynth{f0: 130, amp: 6000})
	if got := r.sent() - before; got < 26 {
		t.Errorf("com fone, a fala durante um aviso deveria ser transmitida: %d de 30 quadros", got)
	}
	r.run(whole, nil)
	// falar por cima do aviso não parece eco: continua sabendo que é fone
	if got := r.e.coupling.Load(); got != couplingSilent {
		t.Errorf("falar durante o aviso foi confundido com o microfone ouvindo o aviso (estado %d)", got)
	}

	// ---- caixas de som: o aviso volta pelo microfone
	s := newNoticeRig(t, 0.5)
	s.run(25, nil)
	for i := 0; i < 2; i++ {
		s.e.PlayLocal(noticeClip())
		s.run(whole, nil)
		if got := s.sent(); got != 0 {
			t.Fatalf("aviso %d: o som captado pelo microfone foi transmitido (%d quadros)", i+1, got)
		}
		if got := s.e.coupling.Load(); got != couplingHeard {
			t.Fatalf("aviso %d: deveria concluir que o microfone ouve os avisos (estado %d)", i+1, got)
		}
	}

	// ---- tirou o fone no meio da sessão: o primeiro aviso ensina, o segundo já é segurado
	r.echo = 0.5
	r.e.PlayLocal(noticeClip())
	r.run(whole, nil)
	if got := r.e.coupling.Load(); got != couplingHeard {
		t.Fatalf("depois de trocar para caixas de som deveria reaprender (estado %d)", got)
	}
	r.run(10, nil) // o primeiro aviso foi transmitido: deixa a transmissão dele acabar (cauda + espera)
	before = r.sent()
	r.e.PlayLocal(noticeClip())
	r.run(whole, nil)
	if got := r.sent() - before; got != 0 {
		t.Errorf("segundo aviso nas caixas de som foi transmitido (%d quadros)", got)
	}

	// ---- voltou para o fone: um aviso em silêncio libera de novo
	r.echo = 0
	r.e.PlayLocal(noticeClip())
	r.run(whole, nil)
	if got := r.e.coupling.Load(); got != couplingSilent {
		t.Errorf("de volta ao fone deveria liberar a transmissão durante avisos (estado %d)", got)
	}

	// ---- trocar de dispositivo esquece o que foi aprendido
	r.e.coupling.Store(couplingSilent)
	if err := r.e.SetOutputDevice("outro"); err != nil {
		t.Logf("SetOutputDevice: %v", err)
	}
	if got := r.e.coupling.Load(); got != couplingUnknown {
		t.Errorf("trocar a saída de som deveria zerar o aprendizado (estado %d)", got)
	}
}

// Apertar para falar: só transmite com a tecla apertada (com 60 ms de
// antecipação e 100 ms depois de soltar), qualquer que seja o som.
func TestPushToTalk(t *testing.T) {
	e, c := newTestEngine(t)
	e.SetVADMode(VADPTT)
	rng := rand.New(rand.NewSource(9))
	v := &voiceSynth{f0: 140, amp: 6000}
	for i := 0; i < 30; i++ { // falando sem apertar: nada
		e.txFrame(v.nextPCM(FrameSize))
	}
	if f, _ := c.get(); f != 0 {
		t.Fatalf("sem a tecla apertada nada deve ser enviado, enviou %d", f)
	}
	e.SetPTT(true)
	for i := 0; i < 20; i++ { // apertada, mesmo em silêncio: transmite
		e.txFrame(ambientPCM(rng))
	}
	e.SetPTT(false)
	for i := 0; i < 30; i++ {
		e.txFrame(v.nextPCM(FrameSize))
	}
	f, ends := c.get()
	if want := lookahead + 20 + pttRelease; f < want-1 || f > want+1 {
		t.Errorf("enviou %d quadros; esperado ~%d (antecipação + 20 + cauda)", f, want)
	}
	if ends != 1 {
		t.Errorf("esperado 1 fim de fala, veio %d", ends)
	}
}

// Volume por pessoa e "mutar só para mim".
func TestPerClientVolumeAndMute(t *testing.T) {
	e, _ := newTestEngine(t)
	e.SetNormalizeVoices(false)
	frames := encodeFrames(t, 10, false)
	level := func() float64 {
		e.ResetStreams()
		for i, f := range frames {
			e.PushVoice(6, uint16(i), f)
		}
		out := make([]int16, FrameSize)
		var max float64
		for i := 0; i < 10; i++ {
			e.fillPlayback(out)
			max = math.Max(max, rms(out))
		}
		return max
	}
	normal := level()
	e.SetClientGain(6, 0.5)
	half := level()
	e.SetClientGain(6, 1)
	e.SetClientMuted(6, true)
	muted := level()
	e.SetClientMuted(6, false)
	back := level()
	if half < normal*0.4 || half > normal*0.6 || muted != 0 || math.Abs(back-normal) > normal*0.05 {
		t.Errorf("normal=%.0f metade=%.0f mutado=%.0f de volta=%.0f", normal, half, muted, back)
	}
}

// Teste de microfone: a própria voz tratada volta pela saída local.
func TestMicTestLoopback(t *testing.T) {
	e, _ := newTestEngine(t)
	rng := rand.New(rand.NewSource(4))
	in := make([]float64, 2*SampleRate)
	for i := range in {
		in[i] = rng.NormFloat64() * 40
	}
	v := &voiceSynth{f0: 140, amp: 5000}
	copy(in[SampleRate/2:], v.next(SampleRate))
	out := runTx(e, in)
	if got := rmsFloat(region(out, 600, 1400)); got < 800 {
		t.Errorf("a própria voz deveria voltar no teste (nível %.0f)", got)
	}

	e.SetMicTest(false)
	e.ResetStreams()
	play := make([]int16, FrameSize)
	for i := 0; i < 10; i++ {
		e.txFrame(v.nextPCM(FrameSize))
		e.fillPlayback(play)
	}
	if rms(play) != 0 {
		t.Error("com o teste desligado a voz não deve voltar")
	}
}

// txLoop: sem microfone, o relógio reserva mantém o soundboard andando.
func TestTxLoopFallbackClock(t *testing.T) {
	c := &capture{}
	e, err := NewEngine(c.send)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	phase := 0.0
	e.Soundboard().play(sine(SampleRate/2, 330, 9000, &phase))
	deadline := time.Now().Add(6 * time.Second)
	for e.Soundboard().IsPlaying() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	f, ends := c.get()
	if f < 10 || ends != 1 {
		t.Errorf("relógio reserva enviou %d quadros e %d fim", f, ends)
	}
}

// ------------------------------------------------------------ recepção

func encodeFrames(t *testing.T, n int, fec bool) [][]byte {
	t.Helper()
	enc, err := opus.NewEncoder(SampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	if fec {
		enc.SetInBandFEC(true)
		enc.SetPacketLossPerc(20)
	}
	phase := 0.0
	var out [][]byte
	for i := 0; i < n; i++ {
		buf := make([]byte, 1000)
		k, err := enc.Encode(sine(FrameSize, 300, 9000, &phase), buf)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, buf[:k])
	}
	return out
}

// Uma travada (rajada de pacotes atrasados) não pode virar atraso permanente.
func TestJitterBufferBoundsLatency(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeFrames(t, 100, false)
	for i, f := range frames { // 2 s de áudio chegando de uma vez
		e.PushVoice(7, uint16(i), f)
	}
	e.rxMu.Lock()
	queued := len(e.streams[7].pcm)
	e.rxMu.Unlock()
	if queued > maxQueueSamples {
		t.Fatalf("fila com %d amostras (%.0f ms); limite %d", queued, float64(queued)/48, maxQueueSamples)
	}
}

func TestPlaybackPrebufferAndTalking(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeFrames(t, 12, false)
	out := make([]int16, FrameSize)

	e.PushVoice(3, 0, frames[0])
	e.fillPlayback(out)
	if len(e.TalkingClients()) != 0 {
		t.Error("não deve tocar antes de acumular o pré-buffer")
	}
	for i := 1; i < 12; i++ {
		e.PushVoice(3, uint16(i), frames[i])
	}
	loud := 0
	for i := 0; i < 12; i++ {
		e.fillPlayback(out)
		if rms(out) > 500 {
			loud++
		}
		if i == 3 {
			if ids := e.TalkingClients(); len(ids) != 1 || ids[0] != 3 {
				t.Errorf("talking = %v", ids)
			}
		}
	}
	if loud < 10 {
		t.Errorf("tocou só %d quadros de 12", loud)
	}
	e.PushVoice(3, 12, nil) // fim da fala
	e.fillPlayback(out)
	if len(e.TalkingClients()) != 0 {
		t.Error("indicador deve apagar ao fim da fala")
	}
}

// Um pacote perdido é reconstruído (FEC/PLC): a fila não fica com buraco.
func TestLostPacketIsConcealed(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeFrames(t, 6, true)
	for _, i := range []int{0, 1, 2, 4, 5} { // o pacote 3 se perdeu
		e.PushVoice(8, uint16(i), frames[i])
	}
	e.rxMu.Lock()
	queued := len(e.streams[8].pcm)
	e.rxMu.Unlock()
	if queued != 6*FrameSize {
		t.Errorf("fila com %d amostras; esperado %d (5 pacotes + 1 reconstruído)", queued, 6*FrameSize)
	}
}

// Fila secando no meio da fala faz o buffer crescer; no fim da fala, não.
func TestAdaptivePrebuffer(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeFrames(t, 8, false)
	out := make([]int16, FrameSize)
	prebuffer := func() int {
		e.rxMu.Lock()
		defer e.rxMu.Unlock()
		return e.streams[4].prebuffer
	}

	for i := 0; i < 3; i++ {
		e.PushVoice(4, uint16(i), frames[i])
	}
	for i := 0; i < 4; i++ { // consome tudo sem ter chegado o fim da fala
		e.fillPlayback(out)
	}
	if got := prebuffer(); got != minPrebuffer+FrameSize {
		t.Errorf("após falha no meio da fala: prebuffer %d, esperado %d", got, minPrebuffer+FrameSize)
	}

	for i := 3; i < 7; i++ {
		e.PushVoice(4, uint16(i), frames[i])
	}
	e.PushVoice(4, 7, nil)
	for i := 0; i < 6; i++ {
		e.fillPlayback(out)
	}
	if got := prebuffer(); got != minPrebuffer+FrameSize {
		t.Errorf("fim de fala normal não deve mexer no buffer: %d", got)
	}
}

func TestDeafenedDropsVoice(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeFrames(t, 6, false)
	e.SetDeafened(true)
	for i, f := range frames {
		e.PushVoice(9, uint16(i), f)
	}
	out := make([]int16, FrameSize)
	e.fillPlayback(out)
	if rms(out) != 0 || len(e.TalkingClients()) != 0 {
		t.Error("com alto-falantes mutados nada deve tocar")
	}
}

func TestOutputVolume(t *testing.T) {
	e, _ := newTestEngine(t)
	e.SetNormalizeVoices(false)
	frames := encodeFrames(t, 8, false)
	level := func(vol float64) float64 {
		e.ResetStreams()
		e.SetVolume(vol)
		for i, f := range frames {
			e.PushVoice(5, uint16(i), f)
		}
		out := make([]int16, FrameSize)
		var max float64
		for i := 0; i < 8; i++ {
			e.fillPlayback(out)
			max = math.Max(max, rms(out))
		}
		return max
	}
	full, half, zero := level(100), level(50), level(0)
	if zero != 0 || half < full*0.4 || half > full*0.6 {
		t.Errorf("volumes: 100%%=%.0f 50%%=%.0f 0%%=%.0f", full, half, zero)
	}
}

// Quem fala baixo é trazido para perto do volume de quem fala normal.
func TestNormalizeVoices(t *testing.T) {
	enc, _ := opus.NewEncoder(SampleRate, 1, opus.AppVoIP)
	encode := func(amp float64, n int) [][]byte {
		v := &voiceSynth{f0: 150, amp: amp}
		var out [][]byte
		for i := 0; i < n; i++ {
			buf := make([]byte, 1000)
			k, _ := enc.Encode(v.nextPCM(FrameSize), buf)
			out = append(out, buf[:k])
		}
		return out
	}
	play := func(normalize bool, amp float64) float64 {
		e, _ := newTestEngine(t)
		e.SetNormalizeVoices(normalize)
		out := make([]int16, FrameSize)
		var sum float64
		n := 0
		for i, f := range encode(amp, 200) { // 4 s
			e.PushVoice(2, uint16(i), f)
			e.fillPlayback(out)
			if i >= 150 {
				sum += rms(out)
				n++
			}
		}
		return sum / float64(n)
	}
	quietRaw, quietNorm, loudNorm := play(false, 1200), play(true, 1200), play(true, 9000)
	t.Logf("voz baixa: sem nivelar %.0f, nivelada %.0f; voz alta nivelada %.0f", quietRaw, quietNorm, loudNorm)
	if quietNorm < quietRaw*2 {
		t.Errorf("voz baixa deveria subir bem: %.0f → %.0f", quietRaw, quietNorm)
	}
	if loudNorm > quietNorm*2.5 {
		t.Errorf("vozes niveladas ainda muito diferentes: %.0f vs %.0f", quietNorm, loudNorm)
	}
}

// Várias vozes altas somadas não podem estourar nem distorcer por corte.
func TestMixLimiter(t *testing.T) {
	e, _ := newTestEngine(t)
	e.SetNormalizeVoices(false)
	enc := func() [][]byte {
		en, _ := opus.NewEncoder(SampleRate, 1, opus.AppVoIP)
		phase := 0.0
		var out [][]byte
		for i := 0; i < 20; i++ {
			buf := make([]byte, 1000)
			k, _ := en.Encode(sine(FrameSize, 250, 20000, &phase), buf)
			out = append(out, buf[:k])
		}
		return out
	}()
	for i, f := range enc {
		for id := uint16(1); id <= 3; id++ {
			e.PushVoice(id, uint16(i), f)
		}
	}
	out := make([]int16, FrameSize)
	clipped := 0
	for i := 0; i < 18; i++ {
		e.fillPlayback(out)
		for _, s := range out {
			if s == math.MaxInt16 || s == math.MinInt16 {
				clipped++
			}
		}
	}
	if clipped > 0 {
		t.Errorf("%d amostras estouraram na soma de 3 vozes", clipped)
	}
}

// ------------------------------------------------------------ tratamento do microfone

func TestNoiseSuppressionAndTransparency(t *testing.T) {
	rng := rand.New(rand.NewSource(5))

	// Chiado puro: depois de adaptar, sai bem mais baixo.
	p := NewVoiceProcessor()
	var inE, outE float64
	for f := 0; f < 200; f++ {
		fr := make([]float64, FrameSize)
		for i := range fr {
			fr[i] = rng.NormFloat64() * 1200
		}
		in := rmsFloat(fr)
		feat := p.Process(fr, NoiseClassic, false)
		if f >= 100 {
			inE += in
			outE += rmsFloat(fr)
			if feat.Speech {
				t.Fatalf("chiado classificado como voz no quadro %d", f)
			}
		}
	}
	att := 20 * math.Log10(outE/inE)
	t.Logf("chiado atenuado em %.1f dB", att)
	if att > -12 {
		t.Errorf("supressão fraca: %.1f dB", att)
	}

	// Voz limpa: passa praticamente igual e é reconhecida como voz.
	p = NewVoiceProcessor()
	for f := 0; f < 25; f++ {
		p.Process(ambient(rng), NoiseClassic, false)
	}
	v := &voiceSynth{f0: 140, amp: 5000}
	inE, outE = 0, 0
	speech := 0
	for f := 0; f < 150; f++ {
		fr := v.next(FrameSize)
		in := rmsFloat(fr)
		feat := p.Process(fr, NoiseClassic, false)
		if f >= 20 {
			inE += in
			outE += rmsFloat(fr)
			if feat.Speech {
				speech++
			}
		}
	}
	change := 20 * math.Log10(outE/inE)
	t.Logf("voz limpa: variação de nível %.2f dB, reconhecida em %d de 130 quadros", change, speech)
	if math.Abs(change) > 1.5 {
		t.Errorf("voz limpa foi alterada em %.1f dB", change)
	}
	if speech < 110 {
		t.Errorf("voz reconhecida em só %d de 130 quadros", speech)
	}
}

func TestAutoGainAndLimiter(t *testing.T) {
	level := func(amp float64) (rmsOut, peak float64) {
		p := NewVoiceProcessor()
		lim := newLimiter(30000, 80)
		rng := rand.New(rand.NewSource(6))
		for f := 0; f < 25; f++ {
			p.Process(ambient(rng), NoiseClassic, true)
		}
		v := &voiceSynth{f0: 150, amp: amp}
		n := 0
		for f := 0; f < 300; f++ { // 6 s
			fr := v.next(FrameSize)
			p.Process(fr, NoiseClassic, true)
			lim.run(fr)
			if f >= 200 {
				rmsOut += rmsFloat(fr)
				n++
				for _, x := range fr {
					peak = math.Max(peak, math.Abs(x))
				}
			}
		}
		return rmsOut / float64(n), peak
	}
	quiet, _ := level(900)
	normal, _ := level(5000)
	loud, peak := level(30000)
	t.Logf("nível de saída — voz baixa %.0f, normal %.0f, muito alta %.0f (pico %.0f)", quiet, normal, loud, peak)
	// amp 900 dá ~-30 dB: o ganho máximo (+15 dB) ainda deixa abaixo do alvo,
	// mas tem de subir bastante em relação à entrada.
	if quiet < 1000 {
		t.Errorf("voz baixa não foi amplificada o bastante: %.0f", quiet)
	}
	if loud > normal*2.2 {
		t.Errorf("voz alta não foi contida: %.0f vs %.0f", loud, normal)
	}
	if peak > 30001 {
		t.Errorf("limitador deixou passar pico de %.0f", peak)
	}
}

// ------------------------------------------------------------ efeitos

func estimateF0(x []float64) float64 {
	best, bestLag := 0.0, 0
	for lag := SampleRate / 500; lag <= SampleRate/50; lag++ {
		var xy, xx, yy float64
		for i := 0; i+lag < len(x); i++ {
			xy += x[i] * x[i+lag]
			xx += x[i] * x[i]
			yy += x[i+lag] * x[i+lag]
		}
		if r := xy / math.Sqrt(xx*yy+1); r > best+0.02 { // prefere o menor período
			best, bestLag = r, lag
		}
	}
	if bestLag == 0 {
		return 0
	}
	return SampleRate / float64(bestLag)
}

func TestEveryEffectIsSane(t *testing.T) {
	for _, info := range Effects() {
		t.Run(info.ID, func(t *testing.T) {
			t.Parallel()
			p := NewEffectProcessor()
			v := &voiceSynth{f0: 140, amp: 6000}
			var inE, outE, maxInside, maxEdge float64
			var prevLast float64
			for f := 0; f < 150; f++ { // 3 s
				fr := v.next(FrameSize)
				in := rmsFloat(fr)
				p.Process(fr, VoiceEffect(info.ID))
				for i, x := range fr {
					if math.IsNaN(x) || math.IsInf(x, 0) {
						t.Fatal("amostra inválida")
					}
					if f >= 25 && i > 0 {
						maxInside = math.Max(maxInside, math.Abs(x-fr[i-1]))
					}
				}
				if f >= 25 {
					inE += in
					outE += rmsFloat(fr)
					maxEdge = math.Max(maxEdge, math.Abs(fr[0]-prevLast))
				}
				prevLast = fr[len(fr)-1]
			}
			ratio := outE / inE
			t.Logf("volume ×%.2f, maior salto dentro do quadro %.0f / na emenda %.0f", ratio, maxInside, maxEdge)
			if ratio < 0.7 || ratio > 1.4 { // o volume fino é conferido em TestEffectsMatchVoiceLoudness
				t.Errorf("volume fora do razoável (×%.2f)", ratio)
			}
			if maxEdge > maxInside*1.5+200 {
				t.Errorf("estalo na emenda dos quadros (%.0f vs %.0f)", maxEdge, maxInside)
			}
		})
	}
}

func TestPitchShiftAccuracy(t *testing.T) {
	for _, ratio := range []float64{0.66, 0.84, 1.22, 1.65} {
		ps := newPitchShifter(ratio)
		v := &voiceSynth{f0: 150, amp: 6000, steady: true}
		var tail []float64
		for f := 0; f < 60; f++ {
			fr := v.next(FrameSize)
			ps.run(fr)
			if f >= 40 {
				tail = append(tail, fr...)
			}
		}
		got := estimateF0(tail[:9600])
		want := 150 * ratio
		t.Logf("tom ×%.2f: esperado %.1f Hz, medido %.1f Hz", ratio, want, got)
		if math.Abs(got-want) > want*0.05 {
			t.Errorf("tom ×%.2f fora: %.1f Hz (esperado %.1f)", ratio, got, want)
		}
	}
}

// O robô deve sair com tom fixo (100 Hz), qualquer que seja o tom da voz.
func TestRobotIsMonotone(t *testing.T) {
	for _, f0 := range []float64{110, 190} {
		fx := newSTFTEffect(stftRobot)
		v := &voiceSynth{f0: f0, amp: 6000, steady: true}
		var tail []float64
		for f := 0; f < 40; f++ {
			fr := v.next(FrameSize)
			fx.run(fr)
			if f >= 20 {
				tail = append(tail, fr...)
			}
		}
		got := estimateF0(tail[:9600])
		t.Logf("voz a %.0f Hz → robô a %.1f Hz", f0, got)
		if math.Abs(got-100) > 4 {
			t.Errorf("robô deveria ficar em 100 Hz, ficou em %.1f", got)
		}
	}
}

// O número de quem fala vem do servidor, a cada pacote: um servidor
// mal-intencionado não pode criar vozes (e decodificadores) sem limite.
func TestVoiceStreamsAreBounded(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeTone(t, 1, 440, 8000)
	for from := 1; from <= 5000; from++ {
		e.PushVoice(uint16(from), 0, frames[0])
	}
	e.rxMu.Lock()
	n, newest := len(e.streams), e.streams[5000]
	e.rxMu.Unlock()
	if n > maxStreams {
		t.Errorf("%d vozes acompanhadas ao mesmo tempo; o limite é %d", n, maxStreams)
	}
	if newest == nil {
		t.Error("a voz mais recente deveria estar entre as acompanhadas")
	}
}
