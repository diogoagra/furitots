package audio

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// ------------------------------------------------------------ medidas

// vowel gera uma vogal sintética: os harmônicos de um tom que oscila ±8%
// (para o espectro médio desenhar o envelope inteiro, e não só os pontos de um
// pente parado), com a intensidade ditada por três formantes.
func vowel(n int, f0 float64, formants [3]float64, amp float64) []float64 {
	out := make([]float64, n)
	phase := 0.0
	for i := range out {
		sec := float64(i) / SampleRate
		f := f0 * (1 + 0.08*math.Sin(2*math.Pi*1.3*sec))
		phase += 2 * math.Pi * f / SampleRate
		var s float64
		for h := 1; float64(h)*f < 7000; h++ {
			fh := float64(h) * f
			a := 0.04 // um pouco de energia entre os formantes
			for _, fc := range formants {
				d := (fh - fc) / (0.09 * fc)
				a += 1 / (1 + d*d)
			}
			s += a * math.Cos(float64(h)*phase)
		}
		out[i] = amp * s / 3
	}
	return out
}

// applyEffect passa um sinal inteiro por um efeito, quadro a quadro.
func applyEffect(in []float64, effect VoiceEffect) []float64 {
	p := NewEffectProcessor()
	out := make([]float64, 0, len(in))
	for s := 0; s+FrameSize <= len(in); s += FrameSize {
		fr := append([]float64(nil), in[s:s+FrameSize]...)
		p.Process(fr, effect)
		out = append(out, fr...)
	}
	return out
}

// applyStages faz o mesmo com uma cadeia de blocos, sem o nivelamento.
func applyStages(in []float64, stages []stage) []float64 {
	out := make([]float64, 0, len(in))
	for s := 0; s+FrameSize <= len(in); s += FrameSize {
		fr := append([]float64(nil), in[s:s+FrameSize]...)
		for _, st := range stages {
			st.run(fr)
		}
		out = append(out, fr...)
	}
	return out
}

// pitchTrack devolve, a cada 10 ms, o tom (Hz; 0 = sem tom) e a periodicidade
// (0 a 1) de x, medidos em 40 ms abaixo de 1 kHz.
func pitchTrack(x []float64) (f0, per []float64) {
	const win, hop = 1920, 480
	lp := newLowpass(1000, 0.707)
	y := make([]float64, len(x))
	for i, v := range x {
		y[i] = lp.step(v)
	}
	minLag, maxLag := SampleRate/500, SampleRate/60
	corr := make([]float64, maxLag+2)
	for s := 0; s+win+maxLag < len(y); s += hop {
		a := y[s : s+win]
		var ea float64
		for i := 0; i < win; i += 2 {
			ea += a[i] * a[i]
		}
		if ea/(win/2) < 100*100 {
			f0, per = append(f0, 0), append(per, 0)
			continue
		}
		best, bestLag := 0.0, 0
		for lag := minLag; lag <= maxLag; lag++ {
			b := y[s+lag : s+lag+win]
			var ab, eb float64
			for i := 0; i < win; i += 2 {
				ab += a[i] * b[i]
				eb += b[i] * b[i]
			}
			corr[lag] = ab / math.Sqrt(ea*eb+1)
			if corr[lag] > best {
				best, bestLag = corr[lag], lag
			}
		}
		// o menor período com correlação quase tão boa (evita a oitava de baixo)
		for lag := minLag + 1; lag < bestLag; lag++ {
			if corr[lag] >= 0.92*best && corr[lag] >= corr[lag-1] && corr[lag] >= corr[lag+1] {
				bestLag = lag
				break
			}
		}
		if best < 0.5 {
			f0, per = append(f0, 0), append(per, best)
			continue
		}
		f0, per = append(f0, SampleRate/float64(bestLag)), append(per, best)
	}
	return
}

func median(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// colorDB devolve a "cor" média de x: o envelope espectral (dB) dos quadros
// marcados em use (um a cada 10 ms), em faixas de um semitom de 150 Hz a 8 kHz.
func colorDB(x []float64, use []bool) []float64 {
	var sp spectrum
	sp.fft = newFFT(stftSize)
	var centers []float64
	for fc := 150.0; fc < 8000; fc *= math.Pow(2, 1.0/12) {
		centers = append(centers, fc)
	}
	acc := make([]float64, len(centers))
	n := 0
	for fr := 0; fr*480+stftSize < len(x) && fr < len(use); fr++ {
		if !use[fr] {
			continue
		}
		for i := 0; i < stftSize; i++ {
			sp.re[i] = x[fr*480+i] * 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/stftSize))
		}
		sp.analyze()
		for b, fc := range centers {
			pos := fc * stftSize / SampleRate
			k := int(pos)
			acc[b] += sp.env[k]*(1-(pos-float64(k))) + sp.env[k+1]*(pos-float64(k))
		}
		n++
	}
	for b := range acc {
		acc[b] = acc[b] / float64(n+1) * 20 / math.Ln10
	}
	return acc
}

// colorScale diz por quanto é preciso multiplicar as frequências da cor a
// para ela coincidir com a cor b (em passos de 1/4 de semitom): é o quanto os
// formantes subiram (>1) ou desceram (<1).
func colorScale(a, b []float64) float64 {
	at := func(x []float64, pos float64) (float64, bool) {
		i := int(math.Floor(pos))
		if i < 0 || i+1 >= len(x) {
			return 0, false
		}
		t := pos - float64(i)
		return x[i]*(1-t) + x[i+1]*t, true
	}
	best, bestShift := math.Inf(-1), 0.0
	for shift := -12.0; shift <= 12; shift += 0.25 {
		var av, bv []float64
		for i := 8; i < len(a)-8; i++ {
			if v, ok := at(b, float64(i)+shift); ok {
				av, bv = append(av, a[i]), append(bv, v)
			}
		}
		if len(av) < len(a)/2 {
			continue
		}
		var ma, mb float64
		for i := range av {
			ma += av[i]
			mb += bv[i]
		}
		ma /= float64(len(av))
		mb /= float64(len(av))
		var xy, xx, yy float64
		for i := range av {
			xy += (av[i] - ma) * (bv[i] - mb)
			xx += (av[i] - ma) * (av[i] - ma)
			yy += (bv[i] - mb) * (bv[i] - mb)
		}
		if r := xy / math.Sqrt(xx*yy+1e-9); r > best {
			best, bestShift = r, shift
		}
	}
	return math.Pow(2, bestShift/12)
}

// effectLag estima o atraso de out em relação a in (ms), pelo volume a cada 5 ms.
func effectLag(in, out []float64) int {
	const blk = 240
	env := func(x []float64) []float64 {
		e := make([]float64, len(x)/blk)
		for i := range e {
			e[i] = rmsFloat(x[i*blk : (i+1)*blk])
		}
		return e
	}
	a, b := env(in), env(out)
	best, bestLag := math.Inf(-1), 0
	for lag := 0; lag <= 60; lag++ {
		var xy, xx, yy float64
		for i := 0; i+lag < len(b) && i < len(a); i++ {
			xy += a[i] * b[i+lag]
			xx += a[i] * a[i]
			yy += b[i+lag] * b[i+lag]
		}
		if r := xy / math.Sqrt(xx*yy+1); r > best {
			best, bestLag = r, lag
		}
	}
	return bestLag * 5
}

// effectStats mede o que um efeito fez com uma fala: atraso (ms), volume (dB
// sobre a entrada, nos trechos com voz), razão de tom, razão dos formantes e
// a periodicidade mediana nos quadros em que a entrada tinha tom.
type effectStats struct {
	lagMs                     int
	levelDB, peak             float64
	pitch, color, periodicity float64
	pitchedShare              float64 // fração dos quadros vozeados da entrada em que a saída tem tom
	inPitch, inPeriodicity    float64
}

func measureEffect(in, out []float64) effectStats {
	var st effectStats
	f0in, perIn := pitchTrack(in)
	voiced := make([]bool, len(f0in))
	var f0v, pv []float64
	for i := range f0in {
		if voiced[i] = f0in[i] > 0 && perIn[i] > 0.8; voiced[i] {
			f0v, pv = append(f0v, f0in[i]), append(pv, perIn[i])
		}
	}
	st.inPitch, st.inPeriodicity = median(f0v), median(pv)
	st.lagMs = effectLag(in, out)
	shifted := out[st.lagMs*48:]
	f0o, perO := pitchTrack(shifted)
	var ratio, po []float64
	for i := range f0o {
		if i < len(voiced) && voiced[i] {
			po = append(po, perO[i])
			if f0o[i] > 0 {
				ratio = append(ratio, f0o[i]/f0in[i])
			}
		}
	}
	st.pitch, st.periodicity = median(ratio), median(po)
	st.pitchedShare = float64(len(ratio)) / float64(len(po)+1)
	var sIn, sOut float64
	for s := 0; s+480 <= len(shifted) && s+480 <= len(in); s += 480 {
		if l := rmsFloat(in[s : s+480]); l > 300 {
			o := rmsFloat(shifted[s : s+480])
			sIn += l * l
			sOut += o * o
		}
	}
	st.levelDB = 10 * math.Log10((sOut+1e-9)/(sIn+1e-9))
	for _, v := range out {
		st.peak = math.Max(st.peak, math.Abs(v))
	}
	st.color = colorScale(colorDB(in, voiced), colorDB(shifted, voiced))
	return st
}

// ------------------------------------------------------------ testes

// O que separa "outra pessoa" de "fita acelerada": o tom muda numa razão e os
// formantes em outra, cada um no valor pedido.
func TestVoiceChangesPitchAndFormantsSeparately(t *testing.T) {
	in := vowel(3*SampleRate, 125, [3]float64{620, 1450, 2600}, 2500)
	for _, c := range []struct{ pitch, formant float64 }{{1.45, 1.12}, {0.76, 0.90}, {0.62, 0.72}, {0.5, 0.72}} {
		t.Run(fmt.Sprintf("tom%.2f-formantes%.2f", c.pitch, c.formant), func(t *testing.T) {
			t.Parallel()
			st := measureEffect(in, applyStages(in, voice(c.pitch, c.formant)))
			t.Logf("saiu tom ×%.3f, formantes ×%.3f, periodicidade %.2f (entrada %.2f)", st.pitch, st.color, st.periodicity, st.inPeriodicity)
			if math.Abs(st.pitch/c.pitch-1) > 0.03 {
				t.Errorf("tom ×%.2f saiu ×%.3f", c.pitch, st.pitch)
			}
			if math.Abs(st.color/c.formant-1) > 0.05 {
				t.Errorf("formantes ×%.2f saíram ×%.3f", c.formant, st.color)
			}
			// (a uma oitava abaixo o tom sai em 62 Hz, no limite do que a medida alcança)
			if c.pitch > 0.6 && st.periodicity < st.inPeriodicity-0.1 {
				t.Errorf("a voz ficou áspera: periodicidade %.2f (entrada %.2f)", st.periodicity, st.inPeriodicity)
			}
		})
	}
	// Só o bloco de tom, sem o de cor: os formantes vão junto (é o esquilo).
	t.Run("só o tom", func(t *testing.T) {
		t.Parallel()
		st := measureEffect(in, applyStages(in, []stage{newPitchShifter(1.45)}))
		t.Logf("só o tom ×1.45: formantes ×%.3f", st.color)
		if st.color < 1.35 {
			t.Errorf("sem o bloco de cor os formantes deveriam subir com o tom; saíram ×%.3f", st.color)
		}
	})
}

// harmonicContrast compara a energia nos harmônicos de f0 com a energia no
// meio do caminho entre eles (dB): alto numa voz com tom, perto de zero num
// sussurro.
func harmonicContrast(x []float64, f0 float64) float64 {
	power := func(f float64) float64 {
		var re, im float64
		for i, v := range x {
			a := 2 * math.Pi * f * float64(i) / SampleRate
			re += v * math.Cos(a)
			im += v * math.Sin(a)
		}
		return re*re + im*im
	}
	var on, off float64
	for h := 2; h <= 16; h++ {
		on += power(float64(h) * f0)
		off += power((float64(h) + 0.5) * f0)
	}
	return 10 * math.Log10(on/off)
}

// O sussurro não tem tom: sobra a cor da voz feita de ruído. (Antes desta
// versão os harmônicos da voz continuavam lá, em metade dos quadros.)
func TestWhisperHasNoPitch(t *testing.T) {
	const f0 = 125.0
	v := &voiceSynth{f0: f0, amp: 5000, steady: true}
	in := v.next(3 * SampleRate)
	out := applyEffect(in, "whisper")
	a, b := SampleRate, 2*SampleRate // 1 s: os harmônicos de 125 Hz caem em frequências exatas
	cin, cout := harmonicContrast(in[a:b], f0), harmonicContrast(out[a:b], f0)
	t.Logf("harmônicos sobre o resto: entrada %.1f dB, sussurro %.1f dB", cin, cout)
	if cin < 20 {
		t.Fatalf("a voz do teste deveria ter tom claro (%.1f dB)", cin)
	}
	if cout > 4 {
		t.Errorf("o sussurro ainda tem tom: harmônicos %.1f dB acima do resto", cout)
	}
	// …e a cor continua a da voz.
	vw := vowel(4*SampleRate, 125, [3]float64{620, 1450, 2600}, 2500)
	use := make([]bool, len(vw)/480)
	for i := range use {
		use[i] = i > 50
	}
	if s := colorScale(colorDB(vw, use), colorDB(applyStages(vw, []stage{newSTFTEffect(stftWhisper)})[stftSize:], use)); math.Abs(s-1) > 0.05 {
		t.Errorf("o sussurro mudou a cor da voz: formantes ×%.3f", s)
	}
}

// O timbre do robô não depende do tom de quem fala: com o envelope no lugar do
// espectro inteiro, o volume dele fica parado enquanto a voz sobe e desce.
func TestRobotIgnoresIntonation(t *testing.T) {
	in := make([]float64, 4*SampleRate)
	phase := 0.0
	for i := range in {
		sec := float64(i) / SampleRate
		phase += 2 * math.Pi * (110 + 25*sec) / SampleRate // de 110 a 210 Hz
		for h := 1; h <= 20; h++ {
			in[i] += 2500 * math.Sin(float64(h)*phase) / float64(h)
		}
	}
	out := applyStages(in, []stage{newSTFTEffect(stftRobot)})
	lo, hi := math.Inf(1), 0.0
	for s := SampleRate / 2; s+4800 <= len(out); s += 4800 { // blocos de 100 ms
		l := rmsFloat(out[s:s+4800]) / rmsFloat(in[s-stftSize:s-stftSize+4800])
		lo, hi = math.Min(lo, l), math.Max(hi, l)
	}
	t.Logf("volume do robô ao longo da subida de tom: de ×%.2f a ×%.2f", lo, hi)
	if hi/lo > 1.25 {
		t.Errorf("o volume do robô oscila com a entonação (×%.2f a ×%.2f)", lo, hi)
	}
}

// Um estalo (a explosão de um "t", "p", "k") passa pela mudança de tom uma vez
// só, inteiro: nem some na emenda de dois trechos, nem sai repetido.
func TestPitchShifterKeepsClicks(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	in := make([]float64, 12*SampleRate)
	for i := range in {
		in[i] = rng.NormFloat64() * 20
	}
	clicks := 0
	for pos := SampleRate; pos < len(in)-SampleRate; pos += 9000 + rng.Intn(9000) {
		for j := 0; j < 300; j++ {
			in[pos+j] += rng.NormFloat64() * 6000 * math.Exp(-float64(j)/60)
		}
		clicks++
	}
	count := func(x []float64) int { // trechos de som separados por mais de 10 ms de silêncio
		n, last := 0, -100000
		for s := 0; s+96 <= len(x); s += 48 {
			if rmsFloat(x[s:s+96]) > 600 {
				if s-last > 480 {
					n++
				}
				last = s
			}
		}
		return n
	}
	if got := count(in); got != clicks {
		t.Fatalf("o contador vê %d estalos na entrada, há %d", got, clicks)
	}
	for _, ratio := range []float64{0.5, 0.62, 0.76, 1.3, 1.45, 1.65} {
		t.Run(fmt.Sprintf("tom%.2f", ratio), func(t *testing.T) {
			t.Parallel()
			if got := count(applyStages(in, []stage{newPitchShifter(ratio)})); got != clicks {
				t.Errorf("entraram %d estalos, saíram %d", clicks, got)
			}
		})
	}
}

// O atraso dos blocos de tom e de cor é o que as constantes dizem, qualquer
// que seja a razão: as camadas de uma voz composta somam alinhadas e a cauda
// cobre o atraso. Um estalo é posto no centro de um trecho, então a hora em
// que ele sai varia: até meio passo de análise (psHop/razão/2) para cada lado,
// mais uns 5 ms quando o trecho não pôde esperar pela posição ideal.
func TestVoiceDelayIsFixed(t *testing.T) {
	in := make([]float64, SampleRate)
	const at = 12000
	for j := 0; j < 200; j++ {
		in[at+j] = 8000 * math.Exp(-float64(j)/40) * math.Cos(float64(j)*0.9)
	}
	where := func(x []float64) int {
		best, pos := 0.0, 0
		for s := 0; s+96 <= len(x); s += 8 {
			if l := rmsFloat(x[s : s+96]); l > best {
				best, pos = l, s
			}
		}
		return pos
	}
	ref := where(in)
	for _, ratio := range []float64{0.5, 0.76, 1.45, 1.65} {
		got := where(applyStages(in, []stage{newPitchShifter(ratio)})) - ref
		t.Logf("tom ×%.2f: atraso de %d amostras (psDelay = %d)", ratio, got, psDelay)
		if math.Abs(float64(got-psDelay)) > psHop/ratio/2+350 {
			t.Errorf("tom ×%.2f: atraso de %d amostras, esperado %d", ratio, got, psDelay)
		}
	}
	for _, c := range []struct{ pitch, formant float64 }{{0.72, 0.82}, {0.5, 0.72}, {1.45, 1.12}} {
		got := where(applyStages(in, voice(c.pitch, c.formant))) - ref
		t.Logf("voz %.2f/%.2f: atraso de %d amostras (voiceDelay = %d)", c.pitch, c.formant, got, voiceDelay)
		if math.Abs(float64(got-voiceDelay)) > psHop/c.pitch/2+350 {
			t.Errorf("voz %.2f/%.2f: atraso de %d amostras, esperado %d", c.pitch, c.formant, got, voiceDelay)
		}
	}
	if voiceTail*FrameSize < voiceDelay || pitchTail*FrameSize < psDelay {
		t.Errorf("a cauda não cobre o atraso: %d e %d quadros para %d e %d amostras", voiceTail, pitchTail, voiceDelay, psDelay)
	}
}

// Todo efeito sai com o volume da voz que entrou, e a medida que o processador
// faz disso continua de uma fala para a outra.
func TestEffectsMatchVoiceLoudness(t *testing.T) {
	v := &voiceSynth{f0: 140, amp: 5000}
	in := v.next(4 * SampleRate)
	for _, info := range Effects() {
		t.Run(info.ID, func(t *testing.T) {
			t.Parallel()
			p := NewEffectProcessor()
			var sIn, sOut float64
			for f := 0; (f+1)*FrameSize <= len(in); f++ {
				fr := append([]float64(nil), in[f*FrameSize:(f+1)*FrameSize]...)
				l := rmsFloat(fr)
				p.Process(fr, VoiceEffect(info.ID))
				if f >= 100 { // os 2 s finais: a medida já assentou
					sIn += l * l
					sOut += rmsFloat(fr) * rmsFloat(fr)
				}
			}
			db := 10 * math.Log10(sOut/sIn)
			t.Logf("%+5.1f dB (correção em uso ×%.2f)", db, p.level)
			if math.Abs(db) > 1.5 {
				t.Errorf("sai %+.1f dB em relação à voz", db)
			}
			before := p.level
			if p.Reset(); p.level != before {
				t.Errorf("Reset perdeu a correção de volume (%.2f → %.2f)", before, p.level)
			}
		})
	}
}

// Custo de tratar 20 ms de voz com cada efeito (o tempo real pede 50 por segundo).
func BenchmarkEffects(b *testing.B) {
	v := &voiceSynth{f0: 140, amp: 5000}
	src := v.next(50 * FrameSize)
	for _, id := range []string{"agudo", "demon", "robot", "whisper", "squirrel"} {
		b.Run(id, func(b *testing.B) {
			p := NewEffectProcessor()
			fr := make([]float64, FrameSize)
			for i := 0; i < b.N; i++ {
				copy(fr, src[(i%50)*FrameSize:])
				p.Process(fr, VoiceEffect(id))
			}
		})
	}
}
