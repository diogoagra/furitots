package audio

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// Avaliação com gravações de fala de verdade (não roda por padrão):
//
//	FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run EvalRealSpeech -v
//
// Mede quanto da fala é transmitida e quanto da digitação vaza, com dois
// tipos de teclado e ruído de ventilador ao fundo, em cada modo de supressão.
func TestEvalRealSpeech(t *testing.T) {
	eachNoiseMode(t, evalRealSpeech)
}

func evalRealSpeech(t *testing.T, mode string) {
	paths := os.Getenv("FURITO_SPEECH")
	if paths == "" {
		t.Skip("defina FURITO_SPEECH com gravações de fala")
	}
	var speech []float64
	speech = append(speech, make([]float64, 2*SampleRate)...)
	for _, p := range strings.Split(paths, ",") {
		pcm, err := DecodeAudioFile(p)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]float64, len(pcm))
		toFloat(buf, pcm)
		speech = append(speech, buf...)
		speech = append(speech, make([]float64, SampleRate)...)
	}
	n := len(speech) / FrameSize * FrameSize
	speech = speech[:n]

	// Atividade de fala pela energia do sinal limpo (blocos de 20 ms).
	nf := n / FrameSize
	energy := make([]float64, nf)
	var peak float64
	for i := range energy {
		energy[i] = rmsFloat(speech[i*FrameSize : (i+1)*FrameSize])
		peak = math.Max(peak, energy[i])
	}
	active := make([]bool, nf)
	for i := range active {
		active[i] = energy[i] > peak*0.03
	}
	// "pausa de verdade": sem fala há pelo menos 250 ms e pelos próximos 100 ms
	pause := make([]bool, nf)
	for i := range pause {
		ok := true
		for j := i - 12; j <= i+5; j++ {
			if j >= 0 && j < nf && active[j] {
				ok = false
			}
		}
		pause[i] = ok
	}

	run := func(in []float64) []float64 {
		c := &capture{}
		e, err := newEngine(c.send)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		e.SetAutoGain(false)
		e.SetNoiseMode(mode)
		return runTx(e, in)
	}
	measure := func(out, ref []float64, sel []bool) float64 {
		var sum float64
		k := 0
		for i := 0; i < nf && (i+1)*FrameSize <= len(out); i++ {
			if sel[i] {
				x := rmsFloat(out[i*FrameSize : (i+1)*FrameSize])
				sum += x * x
				k++
			}
		}
		_ = ref
		return math.Sqrt(sum / float64(k+1))
	}

	clean := run(speech)
	speechRef := measure(speech, nil, active)
	t.Logf("fala limpa: %.1f dB transmitida nos trechos de fala; %.0f de nível nas pausas",
		20*math.Log10(measure(clean, nil, active)/speechRef), measure(clean, nil, pause))

	for kind, name := range []string{"notebook", "mecânico"} {
		rng := rand.New(rand.NewSource(int64(50 + kind)))
		typing := make([]float64, n)
		SynthKeyboard(typing, kind, rng)
		fan := make([]float64, n)
		lp := 0.0
		for i := range fan {
			lp += 0.02 * (rng.NormFloat64() - lp)
			fan[i] = lp * 4000
		}
		in := make([]float64, n)
		for i := range in {
			in[i] = speech[i] + typing[i] + fan[i]
		}
		out := run(in)
		noiseIn := make([]float64, n)
		for i := range noiseIn {
			noiseIn[i] = typing[i] + fan[i]
		}
		t.Logf("teclado %s + ventilador: fala %.1f dB; nas pausas entrou %.0f e saiu %.1f (%.1f dB)",
			name,
			20*math.Log10(measure(out, nil, active)/speechRef),
			measure(noiseIn, nil, pause), measure(out, nil, pause),
			20*math.Log10((measure(out, nil, pause)+1e-9)/measure(noiseIn, nil, pause)))
	}
}

// Avaliação das bordas das frases (não roda por padrão):
//
//	FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run EvalSpeechEdges -v
//
// Para cada frase da gravação (trechos separados por 300 ms ou mais de
// silêncio) mede o que os outros ouviriam do começo e do fim dela: quanto
// tempo antes/depois da fala o microfone abre/fecha, quanto da energia dos
// primeiros e dos últimos instantes chega, e os buracos no meio da frase.
// "começo"/"fim" contam só o que o detector de voz deixou passar; "áudio" é o
// nível do que foi de fato enviado, já com o que a supressão de ruído tirou.
func TestEvalSpeechEdges(t *testing.T) {
	eachNoiseMode(t, evalSpeechEdges)
}

func evalSpeechEdges(t *testing.T, mode string) {
	paths := os.Getenv("FURITO_SPEECH")
	if paths == "" {
		t.Skip("defina FURITO_SPEECH com gravações de fala")
	}
	for _, cond := range []struct {
		name          string
		speech, noise float64 // nível da fala (RMS nos trechos ativos) e do ruído
	}{
		{"voz normal, sala quieta", 2500, 40},
		{"voz normal, ventilador", 2500, 250},
		{"voz baixa, sala quieta", 700, 40},
		{"voz baixa, ventilador", 700, 250},
	} {
		var all edgeStats
		for _, p := range strings.Split(paths, ",") {
			pcm, err := DecodeAudioFile(p)
			if err != nil {
				t.Fatal(err)
			}
			st := evalEdges(t, pcm, cond.speech, cond.noise, func(e *Engine) { e.SetNoiseMode(mode) })
			all.merge(st)
		}
		t.Logf("%-24s %s", cond.name, all.String())
	}
}

type edgeStats struct {
	sentences int
	openLate  []float64 // ms entre o começo da fala e a abertura (negativo = abriu antes)
	closeLate []float64 // ms entre o fim da fala e o fechamento (negativo = fechou antes)
	headDB    []float64 // dB transmitidos dos primeiros 60 ms
	tailDB    []float64 // dB transmitidos dos últimos 150 ms
	headAudio []float64 // nível do áudio enviado nos primeiros 60 ms, em dB sobre a fala limpa (conta o que a supressão tira)
	tailAudio []float64 // idem, nos últimos 150 ms
	holeMs    []float64 // ms de fala audível sem transmissão, por frase
	bodyDB    []float64 // dB transmitidos da frase inteira
	lagMean   []float64 // ms de atraso extra (fila de envio), média enquanto transmite
	lagMax    []float64 // idem, o maior
}

func (s *edgeStats) merge(o edgeStats) {
	s.sentences += o.sentences
	s.openLate = append(s.openLate, o.openLate...)
	s.closeLate = append(s.closeLate, o.closeLate...)
	s.headDB = append(s.headDB, o.headDB...)
	s.tailDB = append(s.tailDB, o.tailDB...)
	s.headAudio = append(s.headAudio, o.headAudio...)
	s.tailAudio = append(s.tailAudio, o.tailAudio...)
	s.holeMs = append(s.holeMs, o.holeMs...)
	s.bodyDB = append(s.bodyDB, o.bodyDB...)
	s.lagMean = append(s.lagMean, o.lagMean...)
	s.lagMax = append(s.lagMax, o.lagMax...)
}

func meanWorst(x []float64, high bool) (mean, worst float64) {
	if len(x) == 0 {
		return 0, 0
	}
	worst = x[0]
	for _, v := range x {
		mean += v
		if (high && v > worst) || (!high && v < worst) {
			worst = v
		}
	}
	return mean / float64(len(x)), worst
}

func countIf(x []float64, f func(float64) bool) int {
	n := 0
	for _, v := range x {
		if f(v) {
			n++
		}
	}
	return n
}

func (s edgeStats) String() string {
	om, ow := meanWorst(s.openLate, true)
	cm, cw := meanWorst(s.closeLate, false)
	hm, hw := meanWorst(s.headDB, false)
	tm, tw := meanWorst(s.tailDB, false)
	ham, haw := meanWorst(s.headAudio, false)
	tam, taw := meanWorst(s.tailAudio, false)
	lm, lw := meanWorst(s.holeMs, true)
	bm, _ := meanWorst(s.bodyDB, false)
	gm, _ := meanWorst(s.lagMean, true)
	_, gw := meanWorst(s.lagMax, true)
	return fmt.Sprintf("%d frases | abre %+.0f ms (pior %+.0f, %d cortadas) | começo %.1f dB (pior %.1f), áudio %+.1f dB (pior %+.1f) | fecha %+.0f ms (pior %+.0f, %d cortadas) | fim %.1f dB (pior %.1f), áudio %+.1f dB (pior %+.1f) | buracos %.0f ms/frase (pior %.0f) | frase %.1f dB | atraso extra %.0f ms (pico %.0f)",
		s.sentences, om, ow, countIf(s.openLate, func(v float64) bool { return v > 5 }),
		hm, hw, ham, haw, cm, cw, countIf(s.closeLate, func(v float64) bool { return v < -5 }),
		tm, tw, tam, taw, lm, lw, bm, gm, gw)
}

// evalEdges passa a gravação (com ruído de sala somado) pelo transmissor e
// compara, frase a frase, o que saiu com a fala limpa.
func evalEdges(t *testing.T, pcm []int16, speechLevel, noiseLevel float64, configure func(*Engine)) edgeStats {
	t.Helper()
	const blk = SampleRate / 200 // 5 ms
	lead := 3 * SampleRate
	speech := make([]float64, lead+len(pcm)+SampleRate)
	for i, s := range pcm {
		speech[lead+i] = float64(s)
	}
	n := len(speech) / FrameSize * FrameSize
	speech = speech[:n]

	nb := n / blk
	lvl := make([]float64, nb)
	var peak float64
	for i := range lvl {
		lvl[i] = rmsFloat(speech[i*blk : (i+1)*blk])
		peak = math.Max(peak, lvl[i])
	}
	// nível médio dos trechos com fala → escala para o nível pedido
	var sum float64
	k := 0
	for _, l := range lvl {
		if l > peak*0.03 {
			sum += l * l
			k++
		}
	}
	scale := speechLevel / math.Sqrt(sum/float64(k))
	for i := range speech {
		speech[i] *= scale
	}
	for i := range lvl {
		lvl[i] *= scale
	}
	peak *= scale
	// Audível: acima de -36 dB do pico e do próprio ruído da sala.
	floor := math.Max(peak*0.016, noiseLevel*0.7)
	active := make([]bool, nb)
	for i := range active {
		active[i] = lvl[i] > floor
	}

	rng := rand.New(rand.NewSource(7))
	in := make([]float64, n)
	lp := 0.0
	for i := range in {
		w := rng.NormFloat64()
		lp += 0.02 * (w - lp)
		// metade do ruído é chiado, metade é grave (ventilador)
		in[i] = speech[i] + noiseLevel*(0.7*w+0.7*lp*10)
	}

	c := &capture{}
	e, err := newEngine(c.send)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.SetAutoGain(false)
	if configure != nil {
		configure(e)
	}
	weight, lags, queued := traceTx(e, in)
	var lagSum, lagMax float64
	for _, l := range lags {
		lagSum += float64(l) / 48
		lagMax = math.Max(lagMax, float64(l)/48)
	}
	lagN := len(lags) - 1
	sent := make([]bool, nb) // este bloco da entrada foi transmitido?
	for i := range sent {
		var w float64
		for _, v := range weight[i*blk : (i+1)*blk] {
			w += v
		}
		sent[i] = w/blk > 0.1
	}

	ratio := func(a, b int) float64 { // dB transmitidos entre os blocos a e b
		if b > nb {
			b = nb
		}
		if a < 0 {
			a = 0
		}
		var o, s float64
		for i := a * blk; i < b*blk; i++ {
			o += speech[i] * speech[i] * weight[i] * weight[i]
			s += speech[i] * speech[i]
		}
		return math.Max(-40, 10*math.Log10((o+1e-9)/(s+1e-9))) // -40 = nada chegou
	}
	level := func(a, b int) float64 { // nível do áudio enviado entre os blocos a e b, em dB sobre a fala limpa
		if b > nb {
			b = nb
		}
		if a < 0 {
			a = 0
		}
		var o, s float64
		for i := a * blk; i < b*blk; i++ {
			o += queued[i] * queued[i]
			s += speech[i] * speech[i]
		}
		return math.Max(-40, 10*math.Log10((o+1e-9)/(s+1e-9)))
	}

	var st edgeStats
	st.lagMean = []float64{lagSum / float64(lagN+1)}
	st.lagMax = []float64{lagMax}
	const gap = 60 // 300 ms sem fala separam duas frases
	for i := 0; i < nb; {
		if !active[i] {
			i++
			continue
		}
		start, end := i, i
		for j := i; j < nb && j-end <= gap; j++ {
			if active[j] {
				end = j
			}
		}
		i = end + 1
		if end-start < 20 { // menos de 100 ms: não é uma frase
			continue
		}
		st.sentences++

		open := start + 80 // nunca abriu = 400 ms
		for j := start - 80; j < start+80 && j < nb; j++ {
			if j >= 0 && sent[j] {
				open = j
				break
			}
		}
		st.openLate = append(st.openLate, float64(open-start)*5)
		closed := end - 80
		for j := end + 160; j > end-80; j-- {
			if j < nb && sent[j] {
				closed = j
				break
			}
		}
		st.closeLate = append(st.closeLate, float64(closed-end)*5)
		st.headDB = append(st.headDB, ratio(start, start+12))
		st.tailDB = append(st.tailDB, ratio(end-29, end+1))
		st.headAudio = append(st.headAudio, level(start, start+12))
		st.tailAudio = append(st.tailAudio, level(end-29, end+1))
		st.bodyDB = append(st.bodyDB, ratio(start, end+1))
		holes := 0
		for j := start; j <= end; j++ {
			if active[j] && !sent[j] {
				holes++
			}
		}
		st.holeMs = append(st.holeMs, float64(holes)*5)
		if os.Getenv("FURITO_VERBOSE") != "" {
			t.Logf("  frase %2d em %5d ms (%4d ms): abre %+4.0f ms, começo %6.1f dB (áudio %+6.1f) | fecha %+4.0f ms, fim %6.1f dB (áudio %+6.1f) | buracos %3d ms",
				st.sentences, (start*blk-lead)/48, (end-start)*5, float64(open-start)*5, ratio(start, start+12), level(start, start+12),
				float64(closed-end)*5, ratio(end-29, end+1), level(end-29, end+1), holes*5)
		}
	}
	return st
}

// Avaliação da supressão de ruído em si, sem o detector de voz (não roda por
// padrão):
//
//	FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run EvalNoiseSuppression -v
//
// Para cada modo e cada ambiente, compara o que sai do tratamento com a fala
// limpa: quanto da fala e das consoantes de sopro ("s", "f", "ch") é mantido,
// quanto ruído sobra nas pausas e a relação sinal/distorção durante a fala:
// tudo o que não é a fala original conta como distorção, seja ruído que
// sobrou, seja fala que foi mexida. Ela é medida no espectro (só os módulos,
// janelas de 20 ms), porque os filtros passa-altas do caminho giram a fase dos
// graves sem que isso se ouça, e uma comparação amostra a amostra acusaria.
func TestEvalNoiseSuppression(t *testing.T) {
	paths := os.Getenv("FURITO_SPEECH")
	if paths == "" {
		t.Skip("defina FURITO_SPEECH com gravações de fala")
	}
	var speech []float64
	speech = append(speech, make([]float64, 3*SampleRate)...)
	for _, p := range strings.Split(paths, ",") {
		pcm, err := DecodeAudioFile(p)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]float64, len(pcm))
		toFloat(buf, pcm)
		speech = append(speech, buf...)
		speech = append(speech, make([]float64, SampleRate)...)
	}
	n := len(speech) / FrameSize * FrameSize
	speech = speech[:n]
	nf := n / FrameSize

	// fala a 2500 de nível médio nos trechos ativos, como em evalEdges
	energy := make([]float64, nf)
	var peak float64
	for i := range energy {
		energy[i] = rmsFloat(speech[i*FrameSize : (i+1)*FrameSize])
		peak = math.Max(peak, energy[i])
	}
	var sum float64
	k := 0
	for _, e := range energy {
		if e > peak*0.03 {
			sum += e * e
			k++
		}
	}
	scale := 2500 / math.Sqrt(sum/float64(k))
	for i := range speech {
		speech[i] *= scale
	}
	peak *= scale
	highOf := func(x []float64) []float64 { // só o que está acima de ~4 kHz
		h1, h2 := newHighpass(4000, 0.707), newHighpass(4000, 0.707)
		out := make([]float64, len(x))
		for i, v := range x {
			out[i] = h2.step(h1.step(v))
		}
		return out
	}
	speechHigh := highOf(speech)
	active := make([]bool, nf)
	pause := make([]bool, nf)
	hiss := make([]bool, nf) // consoante de sopro: a maior parte da energia está nos agudos
	for i := range active {
		lvl := rmsFloat(speech[i*FrameSize : (i+1)*FrameSize])
		active[i] = lvl > peak*0.03
		hiss[i] = lvl > peak*0.01 && rmsFloat(speechHigh[i*FrameSize:(i+1)*FrameSize]) > 0.7*lvl
	}
	for i := range pause {
		pause[i] = true
		for j := i - 12; j <= i+5; j++ {
			if j >= 0 && j < nf && energy[j]*scale > peak*0.03 {
				pause[i] = false
			}
		}
	}

	type env struct {
		name  string
		noise []float64
	}
	mk := func(seed int64, fan float64, keyboard int) []float64 {
		rng := rand.New(rand.NewSource(seed))
		out := make([]float64, n)
		lp := 0.0
		for i := range out {
			w := rng.NormFloat64()
			lp += 0.02 * (w - lp)
			out[i] = fan * (0.7*w + 0.7*lp*10)
		}
		if keyboard >= 0 {
			SynthKeyboard(out, keyboard, rng)
		}
		return out
	}
	envs := []env{
		{"sala quieta", mk(1, 40, -1)},
		{"ventilador", mk(2, 250, -1)},
		{"ventilador forte", mk(3, 700, -1)},
		{"digitando (notebook)", mk(4, 40, 0)},
		{"digitando (mecânico)", mk(5, 40, 1)},
	}
	for _, ev := range envs {
		for _, mode := range []string{NoiseClassic, NoiseNeural} {
			p := NewVoiceProcessor()
			delay := NoiseDelay(mode)
			out := make([]float64, n+FrameSize)
			frame := make([]float64, FrameSize)
			for f := 0; f < nf; f++ {
				for i := range frame {
					frame[i] = math.Round(speech[f*FrameSize+i] + ev.noise[f*FrameSize+i])
				}
				p.Process(frame, mode, false)
				copy(out[f*FrameSize:], frame)
			}
			out = out[delay:] // alinhado com a entrada
			outHigh := highOf(out)

			var sOut, sRef, hOut, hRef, pOut, pIn float64
			var sdr []float64
			for f := 0; f < nf-1; f++ {
				a, b := f*FrameSize, (f+1)*FrameSize
				switch {
				case active[f]:
					for i := a; i < b; i++ {
						sOut += out[i] * out[i]
						sRef += speech[i] * speech[i]
					}
					ref, got := magnitudes(speech[a:b]), magnitudes(out[a:b])
					var sig, dist float64
					for k := range ref {
						sig += ref[k] * ref[k]
						dist += (got[k] - ref[k]) * (got[k] - ref[k])
					}
					sdr = append(sdr, 10*math.Log10((sig+1e-9)/(dist+1e-9)))
				case pause[f]:
					for i := a; i < b; i++ {
						pOut += out[i] * out[i]
						pIn += ev.noise[i] * ev.noise[i]
					}
				}
				if hiss[f] {
					for i := a; i < b; i++ {
						hOut += outHigh[i] * outHigh[i]
						hRef += speechHigh[i] * speechHigh[i]
					}
				}
			}
			var sdrMean float64
			for _, v := range sdr {
				sdrMean += math.Max(-10, math.Min(40, v))
			}
			sdrMean /= float64(len(sdr))
			t.Logf("%-22s %-8s fala %+5.1f dB | sopro (\"s\") %+5.1f dB | ruído nas pausas %6.1f dB | sinal/distorção na fala %5.1f dB",
				ev.name, mode, 10*math.Log10(sOut/sRef), 10*math.Log10(hOut/hRef), 10*math.Log10((pOut+1e-9)/pIn), sdrMean)
		}
	}
}

// magnitudes devolve os módulos do espectro de um quadro de 20 ms (janela de Hann).
func magnitudes(x []float64) []float64 {
	f := newFFT(nsFFT)
	re, im := make([]float64, nsFFT), make([]float64, nsFFT)
	for i, v := range x {
		re[i] = v * 0.5 * (1 - math.Cos(2*math.Pi*(float64(i)+0.5)/float64(len(x))))
	}
	f.transform(re, im, false)
	out := make([]float64, nsBins)
	for k := range out {
		out[k] = math.Hypot(re[k], im[k])
	}
	return out
}

// Avaliação do detector de voz contra o teclado (não roda por padrão):
//
//	FURITO_EVAL=1 go test ./audio/ -run EvalKeyboard -v
//
// 48 execuções de 10 s por teclado e por modo: 12 sequências de teclas, cada
// uma em 4 posições em relação aos quadros de 20 ms. Conta em quantas o
// microfone abriu sem ninguém falar.
func TestEvalKeyboard(t *testing.T) {
	if os.Getenv("FURITO_EVAL") == "" {
		t.Skip("defina FURITO_EVAL=1")
	}
	for _, mode := range []string{NoiseClassic, NoiseNeural} {
		for kind, name := range []string{"notebook", "mecânico"} {
			opened, total, frames := 0, 0, 0
			for seed := int64(0); seed < 12; seed++ {
				for _, shift := range []int{0, 240, 480, 720} {
					c := &capture{}
					e, err := newEngine(c.send)
					if err != nil {
						t.Fatal(err)
					}
					e.SetNoiseMode(mode)
					rng := rand.New(rand.NewSource(100 + seed*7 + int64(kind)))
					in := make([]float64, 10*SampleRate)
					for i := range in {
						in[i] = rng.NormFloat64() * 60
					}
					SynthKeyboard(in[SampleRate+shift:], kind, rng)
					runTx(e, in)
					e.Close()
					f, _ := c.get()
					total++
					frames += f
					if f > 0 {
						opened++
					}
				}
			}
			t.Logf("%-8s teclado %-9s abriu o microfone em %2d de %d execuções (%d quadros transmitidos em %d s de digitação)",
				mode, name, opened, total, frames, 9*total)
		}
	}
}

// Avaliação dos efeitos de voz com gravações de fala (não roda por padrão):
//
//	FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run EvalEffects -v
//
// Para cada voz e cada efeito: o atraso, o volume em relação à voz original, o
// quanto o tom e os formantes (a "cor" da voz) mudaram, e a periodicidade — a
// da entrada fica perto de 1; quanto menor a da saída, mais áspera ou sem tom
// ficou a voz (de propósito no demônio e no sussurro, defeito na voz aguda).
func TestEvalEffects(t *testing.T) {
	paths := os.Getenv("FURITO_SPEECH")
	if paths == "" {
		t.Skip("defina FURITO_SPEECH com gravações de fala")
	}
	for _, p := range strings.Split(paths, ",") {
		pcm, err := DecodeAudioFile(p)
		if err != nil {
			t.Fatal(err)
		}
		in := make([]float64, SampleRate/2+len(pcm)+SampleRate)
		for i, s := range pcm {
			in[SampleRate/2+i] = float64(s)
		}
		in = in[:len(in)/FrameSize*FrameSize]
		// fala a 3000 de nível médio nos trechos com voz, perto do alvo do ganho automático
		var sum float64
		k := 0
		for s := 0; s+480 <= len(in); s += 480 {
			if l := rmsFloat(in[s : s+480]); l > 300 {
				sum += l * l
				k++
			}
		}
		g := 3000 / math.Sqrt(sum/float64(k))
		for i := range in {
			in[i] *= g
		}
		for i, info := range Effects() {
			st := measureEffect(in, applyEffect(in, VoiceEffect(info.ID)))
			if i == 0 {
				t.Logf("== %s: tom mediano %.0f Hz, periodicidade %.2f", p[strings.LastIndex(p, "/")+1:], st.inPitch, st.inPeriodicity)
			}
			t.Logf("   %-11s atraso %3d ms | volume %+5.1f dB, pico %5.0f | tom ×%.2f | formantes ×%.2f | periodicidade %.2f, com tom em %3.0f%% dos quadros vozeados",
				info.ID, st.lagMs, st.levelDB, st.peak, st.pitch, st.color, st.periodicity, 100*st.pitchedShare)
		}
	}
}
