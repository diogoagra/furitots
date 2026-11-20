package audio

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// fanNoise é um quadro de ruído de ventilador: metade chiado, metade grave.
func fanNoise(rng *rand.Rand, lp *float64, level float64) []float64 {
	out := make([]float64, FrameSize)
	for i := range out {
		w := rng.NormFloat64()
		*lp += 0.02 * (w - *lp)
		out[i] = level * (0.7*w + 0.7**lp*10)
	}
	return out
}

// Supressão neural: tira o ruído de ventilador quase todo (muito além dos
// 24 dB da clássica) e deixa a voz passar com o mesmo nível.
func TestNeuralSuppression(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	lp := 0.0

	p := NewVoiceProcessor()
	var inE, outE float64
	for f := 0; f < 250; f++ { // 5 s de ventilador
		fr := fanNoise(rng, &lp, 300)
		in := rmsFloat(fr)
		feat := p.Process(fr, NoiseNeural, false)
		if f >= 50 {
			inE += in * in
			outE += rmsFloat(fr) * rmsFloat(fr)
			if feat.Speech {
				t.Fatalf("ventilador classificado como voz no quadro %d", f)
			}
		}
	}
	att := 10 * math.Log10(outE/inE)
	t.Logf("ventilador atenuado em %.1f dB", att)
	if att > -35 {
		t.Errorf("supressão neural fraca: %.1f dB", att)
	}

	// Voz sobre o ventilador: passa com o mesmo nível e é reconhecida.
	v := &voiceSynth{f0: 140, amp: 5000}
	voice := v.next(150 * FrameSize)
	out := make([]float64, 0, len(voice))
	speech := 0
	for f := 0; f < 150; f++ {
		fr := fanNoise(rng, &lp, 300)
		for i := range fr {
			fr[i] += voice[f*FrameSize+i]
		}
		feat := p.Process(fr, NoiseNeural, false)
		out = append(out, fr...)
		if f >= 20 && feat.Speech {
			speech++
		}
	}
	a, b := 20*FrameSize, 140*FrameSize
	change := 20 * math.Log10(rmsFloat(out[a+NoiseDelay(NoiseNeural):b+NoiseDelay(NoiseNeural)])/rmsFloat(voice[a:b]))
	t.Logf("voz sobre o ventilador: variação de nível %.2f dB, reconhecida em %d de 130 quadros", change, speech)
	if math.Abs(change) > 1.5 {
		t.Errorf("a voz foi alterada em %.1f dB", change)
	}
	if speech < 110 {
		t.Errorf("voz reconhecida em só %d de 130 quadros", speech)
	}

}

// O áudio do modo neural sai atrasado exatamente o que NoiseDelay diz. O sinal
// do teste é uma voz que muda de tom o tempo todo: com o tom parado, a saída
// casaria igualmente bem um período antes ou depois.
func TestNeuralDelay(t *testing.T) {
	voice := make([]float64, 150*FrameSize)
	phase := 0.0
	for i := range voice {
		sec := float64(i) / SampleRate
		phase += 2 * math.Pi * (110 + 40*sec) / SampleRate // de 110 a 230 Hz em 3 s
		for h := 1; h <= 12; h++ {
			voice[i] += math.Sin(float64(h)*phase) / float64(h)
		}
		voice[i] *= 3000 * (0.25 + 0.75*0.5*(1-math.Cos(2*math.Pi*3*sec)))
	}
	p := NewVoiceProcessor()
	out := make([]float64, 0, len(voice))
	for f := 0; f < 150; f++ {
		fr := append([]float64(nil), voice[f*FrameSize:(f+1)*FrameSize]...)
		p.Process(fr, NoiseNeural, false)
		out = append(out, fr...)
	}
	a, b := 20*FrameSize, 140*FrameSize
	best, bestLag := -1.0, 0
	for lag := 0; lag <= 4*nsHop; lag += nsHop / 8 {
		var xy, xx, yy float64
		for i := a; i < b; i++ {
			xy += voice[i] * out[i+lag]
			xx += voice[i] * voice[i]
			yy += out[i+lag] * out[i+lag]
		}
		if r := xy / math.Sqrt(xx*yy); r > best {
			best, bestLag = r, lag
		}
	}
	t.Logf("atraso medido: %d amostras (semelhança %.3f)", bestLag, best)
	if bestLag != NoiseDelay(NoiseNeural) {
		t.Errorf("atraso medido de %d amostras, NoiseDelay diz %d", bestLag, NoiseDelay(NoiseNeural))
	}
	// (os filtros passa-altas giram a fase dos graves: a semelhança não chega a 1)
	if best < 0.5 {
		t.Errorf("a saída não se parece com a entrada (semelhança %.3f): o teste não mediu o atraso", best)
	}
}

// As regras que juntam a rede com as medidas clássicas (neuralVoice), com
// medidas montadas à mão: não há como sintetizar um som que engane só um dos
// dois detectores.
func TestNeuralVoiceRules(t *testing.T) {
	pitched := func() VoiceFeatures { // um quadro que as medidas clássicas dão como voz
		return VoiceFeatures{Level: 2000, Sustain: 0.8, Pitched: true, Speech: true}
	}

	// A rede concorda: vale desde o primeiro quadro.
	p := NewVoiceProcessor()
	f := pitched()
	f.Neural, f.Kept = 1, 1
	if p.neuralVoice(&f, true); !f.Speech || !f.Sure {
		t.Error("tom firme com a certeza da rede deveria ser voz no primeiro quadro")
	}

	// A rede discorda (é o caso do teclado, que não passa de 2 quadros com
	// tom): não é voz… até 120 ms seguidos, que uma tecla não produz.
	p = NewVoiceProcessor()
	for i := 1; i <= plainAlone; i++ {
		f := pitched()
		f.Neural, f.Kept = 0.3, 0.4
		p.neuralVoice(&f, true)
		if want := i >= plainAlone; f.Speech != want {
			t.Errorf("rede discordando, quadro %d com tom: voz=%v, esperado %v", i, f.Speech, want)
		}
	}

	// A rede tem certeza mas não há tom firme (voz rouca, sussurro): vale
	// depois de 100 ms com o nível firme.
	p = NewVoiceProcessor()
	for i := 1; i <= neuralAlone; i++ {
		f := VoiceFeatures{Level: 2000, Sustain: 0.8, Neural: 1, Kept: 1}
		p.neuralVoice(&f, false)
		if want := i >= neuralAlone; f.Speech != want {
			t.Errorf("só a rede, quadro %d: voz=%v, esperado %v", i, f.Speech, want)
		}
	}

	// …e nunca se o nível não se sustenta ou se a rede abafou o som: é o que
	// acontece no instante em que uma tecla a engana.
	p = NewVoiceProcessor()
	for i := 0; i < 3*neuralAlone; i++ {
		f := VoiceFeatures{Level: 2000, Sustain: 0.2, Neural: 1, Kept: 1}
		if i%2 == 1 {
			f.Sustain, f.Kept = 0.8, 0.5
		}
		if p.neuralVoice(&f, false); f.Speech {
			t.Fatalf("certeza sem nível firme virou voz no quadro %d", i)
		}
	}
}

// Engine.pick: cada trecho de fala começa com o áudio da supressão clássica e
// passa para o do RNNoise quando a rede deixa o som passar.
func TestPickStartsWithClassicAudio(t *testing.T) {
	e, _ := newTestEngine(t)
	fill := func(v float64) []float64 {
		out := make([]float64, FrameSize)
		for i := range out {
			out[i] = v
		}
		return out
	}
	slot := func(sure bool) *txSlot { return &txSlot{voice: fill(10), plain: fill(1000), sure: sure} }

	e.onset = true
	if got := e.pick(slot(false)); got[0] != 1000 || got[FrameSize-1] != 1000 {
		t.Errorf("antes de a rede reconhecer a fala deveria sair o áudio clássico (saiu %.0f)", got[0])
	}
	cross := e.pick(slot(true))
	if cross[0] < 990 || cross[FrameSize-1] > 20 || cross[FrameSize/2] < 400 || cross[FrameSize/2] > 600 {
		t.Errorf("a passagem deveria ir de um áudio ao outro ao longo do quadro: %.0f … %.0f … %.0f", cross[0], cross[FrameSize/2], cross[FrameSize-1])
	}
	for _, sure := range []bool{false, true} { // depois disso, sempre o RNNoise, até o trecho acabar
		if got := e.pick(slot(sure)); got[0] != 10 {
			t.Errorf("depois da passagem deveria sair o áudio do RNNoise (saiu %.0f)", got[0])
		}
	}
	// Fora do modo neural não há segunda versão.
	e.onset = true
	if got := e.pick(&txSlot{voice: fill(10)}); got[0] != 10 {
		t.Errorf("sem a versão clássica deveria sair o único áudio que há (saiu %.0f)", got[0])
	}
}

// Trocar de modo no meio da conversa não solta áudio velho: o RNNoise guarda
// 20 ms do que tratou por último.
func TestNoiseModeSwitchDropsOldAudio(t *testing.T) {
	p := NewVoiceProcessor()
	rng := rand.New(rand.NewSource(12))
	v := &voiceSynth{f0: 150, amp: 8000, steady: true}
	for f := 0; f < 50; f++ {
		p.Process(v.next(FrameSize), NoiseNeural, false)
	}
	for f := 0; f < 50; f++ {
		p.Process(ambient(rng), NoiseClassic, false)
	}
	for f := 0; f < 5; f++ {
		fr := ambient(rng)
		p.Process(fr, NoiseNeural, false)
		if lvl := rmsFloat(fr); lvl > 100 {
			t.Fatalf("ao voltar para o modo neural saiu áudio de antes da troca (nível %.0f no quadro %d)", lvl, f)
		}
	}
}

// O motor aceita os três modos e cai no neural para qualquer outro valor.
func TestNoiseModeSetting(t *testing.T) {
	e, _ := newTestEngine(t)
	if e.NoiseMode() != NoiseNeural {
		t.Errorf("o padrão deveria ser a supressão neural, é %q", e.NoiseMode())
	}
	for in, want := range map[string]string{NoiseOff: NoiseOff, NoiseClassic: NoiseClassic, NoiseNeural: NoiseNeural, "": NoiseNeural, "xyz": NoiseNeural} {
		e.SetNoiseMode(in)
		if got := e.NoiseMode(); got != want {
			t.Errorf("SetNoiseMode(%q) → %q, esperado %q", in, got, want)
		}
	}
}

// O caminho de verdade, como no app: o dispositivo entrega o microfone em
// pedaços de tamanho qualquer, a goroutine do transmissor trata com o RNNoise
// e a troca de modo chega de outra goroutine no meio da fala.
func TestNeuralThroughRealLoop(t *testing.T) {
	c := &capture{}
	e, err := NewEngine(c.send)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	rng := rand.New(rand.NewSource(13))
	v := &voiceSynth{f0: 220, amp: 6000}
	feed := func(frames int, voice bool) {
		for i := 0; i < frames; i++ {
			fr := ambientPCM(rng)
			if voice {
				for j, s := range v.nextPCM(FrameSize) {
					fr[j] += s
				}
			}
			// em dois pedaços desiguais, como um dispositivo com outro tamanho de bloco
			e.FeedCapture(fr[:300])
			e.FeedCapture(fr[300:])
			time.Sleep(4 * time.Millisecond)
		}
	}
	feed(25, false)
	feed(40, true)
	e.SetNoiseMode(NoiseClassic) // no meio da fala
	feed(20, true)
	e.SetNoiseMode(NoiseNeural)
	feed(20, true)
	feed(40, false)
	time.Sleep(100 * time.Millisecond)
	f, ends := c.get()
	t.Logf("%d quadros enviados, %d fim de fala", f, ends)
	if f < 70 {
		t.Errorf("1,6 s de voz deveriam ter sido transmitidos; foram %d quadros", f)
	}
	if ends < 1 {
		t.Error("a transmissão deveria ter terminado quando a voz parou")
	}
}
