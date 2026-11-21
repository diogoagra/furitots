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

	// A rede discorda: não é voz, por mais que dure. O que as medidas clássicas
	// tomam por voz e a rede não é ruído com tom (porta rangendo, aspirador).
	p = NewVoiceProcessor()
	for i := 1; i <= 50; i++ {
		f := pitched()
		f.Neural, f.Kept = 0.3, 0.4
		if p.neuralVoice(&f, true); f.Speech {
			t.Fatalf("rede discordando, quadro %d com tom: virou voz", i)
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

// A rede não olha o volume: uma voz de longe (a TV, alguém conversando no
// cômodo) é fala para ela. Do silêncio, a certeza da rede só abre o microfone
// se o som tiver corpo; com a fala em andamento, sílabas fracas continuam
// valendo.
func TestNeuralFarVoiceDoesNotOpenFromSilence(t *testing.T) {
	far := func() VoiceFeatures { // a rede tem certeza, mas o som mal passa do ambiente
		return VoiceFeatures{Level: 80, SNR: 2, Sustain: 0.8, Neural: 1, Kept: 1}
	}
	p := NewVoiceProcessor()
	p.idle = farAway
	for i := 0; i < 50; i++ {
		f := far()
		if p.neuralVoice(&f, true); f.Speech || f.Sure {
			t.Fatalf("voz de longe abriu o microfone no quadro %d", i)
		}
	}
	// Com corpo, abre: pelo nível…
	f := far()
	f.Level = neuralFloor
	if p.neuralVoice(&f, true); !f.Speech {
		t.Error("voz com nível deveria abrir do silêncio")
	}
	// …ou por estar bem acima do ruído (microfone de ganho baixo em sala quieta).
	p = NewVoiceProcessor()
	p.idle = farAway
	f = far()
	f.SNR = vadMinSNR
	if p.neuralVoice(&f, true); !f.Speech {
		t.Error("voz baixa mas bem acima do ruído deveria abrir do silêncio")
	}
	// Com a fala em andamento a sílaba fraca vale: até 600 ms depois da voz.
	for gap := 0; gap < neuralIdle; gap += 5 {
		p = NewVoiceProcessor()
		strong := VoiceFeatures{Level: 2000, SNR: 20, Sustain: 0.8, Neural: 1, Kept: 1}
		p.neuralVoice(&strong, true)
		for i := 0; i < gap; i++ {
			quiet := VoiceFeatures{Level: 10, Sustain: 0.8, Kept: 1}
			p.neuralVoice(&quiet, false)
		}
		f = far()
		if p.neuralVoice(&f, true); !f.Speech {
			t.Errorf("sílaba fraca %d ms depois da voz deveria valer", gap*20)
		}
	}
}

// Engine.pick: no começo de um trecho de fala, até a rede reconhecer a voz, só
// os quadros que são um chiado agudo que dura (a consoante de sopro que abre a
// frase) saem com o áudio da supressão clássica. O ambiente, as teclas e os
// cliques saem pelo RNNoise, como todo o resto.
func TestPickUsesClassicAudioOnlyForOnsetConsonant(t *testing.T) {
	e, _ := newTestEngine(t)
	fill := func(v float64) []float64 {
		out := make([]float64, FrameSize)
		for i := range out {
			out[i] = v
		}
		return out
	}
	slot := func(kind string) *txSlot {
		s := &txSlot{voice: fill(10), plain: fill(1000), sound: true, level: 500, sustain: 0.8}
		switch kind {
		case "ambiente":
			s.sound, s.level = false, 40
		case "tecla": // pancada grave que ressoa: som que dura, mas não é chiado agudo
			s.snr, s.highSNR, s.high = 20, 12, 0.2
		case "clique": // agudo, mas desaba em milissegundos
			s.snr, s.highSNR, s.high, s.sustain = 20, 20, 0.8, 0.1
		case "ataque": // o "s" começando: agudo, com o nível ainda subindo
			s.highSNR, s.high, s.sustain, s.level = 10, 0.8, 0.1, 200
		case "s":
			s.highSNR, s.high = 15, 0.8
		case "vogal":
			s.snr, s.sure = 25, true
		}
		return s
	}
	flat := func(name string, got []float64, want float64) {
		t.Helper()
		if got[0] != want || got[FrameSize-1] != want {
			t.Errorf("%s: saiu %.0f … %.0f, esperado %.0f", name, got[0], got[FrameSize-1], want)
		}
	}
	ramp := func(name string, got []float64, from, to float64) {
		t.Helper()
		mid := (from + to) / 2
		if math.Abs(got[0]-from) > 5 || math.Abs(got[FrameSize-1]-to) > 5 || math.Abs(got[FrameSize/2]-mid) > 5 {
			t.Errorf("%s: saiu %.0f … %.0f … %.0f, esperado ir de %.0f a %.0f ao longo do quadro", name, got[0], got[FrameSize/2], got[FrameSize-1], from, to)
		}
	}

	e.onset, e.plainMix = true, -1
	flat("ambiente que entra com o começo da frase", e.pick(slot("ambiente"), slot("tecla")), 10)
	flat("tecla", e.pick(slot("tecla"), slot("clique")), 10)
	flat("clique", e.pick(slot("clique"), slot("ataque")), 10)
	ramp("ataque do \"s\": entra o áudio clássico", e.pick(slot("ataque"), slot("s")), 10, 1000)
	flat("\"s\"", e.pick(slot("s"), slot("s")), 1000)
	flat("\"s\" que continua", e.pick(slot("s"), slot("vogal")), 1000)
	ramp("a rede reconheceu a voz: volta ao RNNoise", e.pick(slot("vogal"), slot("vogal")), 1000, 10)
	// Depois disso, sempre o RNNoise, até o trecho acabar.
	flat("\"s\" no meio da fala", e.pick(slot("s"), slot("vogal")), 10)
	flat("vogal", e.pick(slot("vogal")), 10)

	// Um clique logo antes do "s" não é o ataque dele: é mais forte que o que vem depois.
	e.onset, e.plainMix = true, -1
	click := slot("clique")
	click.level = 3000
	flat("clique forte antes do \"s\"", e.pick(click, slot("s")), 10)
	// Um trecho que já começa na consoante começa direto no áudio clássico.
	e.onset, e.plainMix = true, -1
	flat("trecho que começa no \"s\"", e.pick(slot("s"), slot("s")), 1000)
	// Fora do modo neural não há segunda versão.
	e.onset, e.plainMix = true, -1
	s := slot("s")
	s.plain = nil
	flat("modo clássico", e.pick(s), 10)
}

// O ambiente que entra junto com o começo da frase (o quadro guardado de antes
// da voz) sai pelo RNNoise, tão limpo quanto o resto. A supressão clássica tira
// no máximo 24 dB do ventilador: se esse quadro saísse com o áudio dela, o
// ruído apareceria a cada frase.
func TestNeuralFrameStartIsDenoised(t *testing.T) {
	rng := rand.New(rand.NewSource(21))
	lp := 0.0
	const start, frames = 150, 250 // a voz começa aos 3 s
	in := make([]float64, 0, frames*FrameSize)
	noise := make([]float64, 0, frames*FrameSize)
	v := &voiceSynth{f0: 140, amp: 5000}
	for f := 0; f < frames; f++ {
		fr := fanNoise(rng, &lp, 300)
		noise = append(noise, fr...)
		if f >= start {
			for i, s := range v.next(FrameSize) {
				fr[i] += s
			}
		}
		in = append(in, fr...)
	}
	e, _ := newTestEngine(t)
	e.SetAutoGain(false)
	weight, _, sent := traceTx(e, in)

	// O que saiu antes da voz, só o que foi transmitido. Os últimos 10 ms ficam
	// de fora: a janela que já pega a voz deixa o ruído passar junto com ela.
	var out, ref float64
	n := 0
	for i := (start - 10) * FrameSize; i < start*FrameSize-nsHop; i++ {
		if weight[i] > 0 {
			out += sent[i] * sent[i]
			ref += noise[i] * noise[i]
			n++
		}
	}
	if n < nsHop/2 {
		t.Fatalf("só %d amostras de antes da voz foram transmitidas: o teste não mediu o começo da frase", n)
	}
	att := 10 * math.Log10((out+1e-9)/ref)
	t.Logf("ventilador no começo da frase (%d ms transmitidos antes da voz): %.1f dB", n/48, att)
	if att > -30 {
		t.Errorf("o ventilador saiu só %.1f dB abaixo no começo da frase: não é o áudio do RNNoise", att)
	}
	// E a voz chega inteira.
	if k := kept(in, weight, start*20+100, start*20+1500); k < -0.5 {
		t.Errorf("a voz foi cortada (%.1f dB)", k)
	}
}

// Um chiado que dura (palmas, passos, um gole) abre o microfone pelo caminho
// das consoantes de sopro, que não pede voz. No modo neural só conta o chiado
// em que a rede ouve fala.
func TestNeuralHissNeedsTheNetwork(t *testing.T) {
	hiss := func(unheard bool) txSlot {
		return txSlot{voice: make([]float64, FrameSize), sound: true, level: 800, snr: 15, high: 0.8, sustain: 0.8, rise: 1, unheard: unheard}
	}
	for _, unheard := range []bool{true, false} {
		e, _ := newTestEngine(t)
		e.ring = []txSlot{hiss(unheard), hiss(unheard), hiss(unheard)}
		cur := hiss(unheard)
		if got, want := e.decide(&cur, VADAuto), !unheard; got != want {
			t.Errorf("chiado que dura, rede sem ouvir fala=%v: transmitiu=%v, esperado %v", unheard, got, want)
		}
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
