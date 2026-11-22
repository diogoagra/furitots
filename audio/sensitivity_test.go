package audio

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// txScript monta um sinal de microfone a partir de trechos de silêncio (só o
// ambiente) e de voz, e lembra onde cada trecho ficou.
type txScript struct {
	rng *rand.Rand
	sig []float64
}

func (s *txScript) silence(ms int) {
	for i := 0; i < ms*SampleRate/1000; i++ {
		s.sig = append(s.sig, s.rng.NormFloat64()*40)
	}
}

// voice soma ms de voz sintética e devolve onde ela ficou (em ms).
func (s *txScript) voice(ms int, amp float64) (from, to int) {
	from = len(s.sig) * 1000 / SampleRate
	v := &voiceSynth{f0: 150, amp: amp}
	for _, x := range v.next(ms * SampleRate / 1000) {
		s.sig = append(s.sig, x+s.rng.NormFloat64()*40)
	}
	return from, len(s.sig) * 1000 / SampleRate
}

// frames devolve o sinal em quadros inteiros.
func (s *txScript) frames() []float64 { return s.sig[:len(s.sig)/FrameSize*FrameSize] }

// sentShare diz que fração do trecho entre fromMs e toMs foi transmitida.
func sentShare(weight []float64, fromMs, toMs int) float64 {
	n, sent := 0, 0
	for i := fromMs * 48; i < toMs*48 && i < len(weight); i++ {
		n++
		if weight[i] > 0 {
			sent++
		}
	}
	return float64(sent) / float64(n)
}

const (
	loudVoice  = 6000.0
	quietVoice = loudVoice * 0.15 // 16 dB abaixo: abaixo do piso calibrado, acima da metade dele
)

// calibratedFloor mede a voz forte como a calibração do app faria.
func calibratedFloor(t *testing.T, mode string) (voice, floor float64) {
	t.Helper()
	e, _ := newTestEngine(t)
	e.SetNoiseMode(mode)
	s := &txScript{rng: rand.New(rand.NewSource(1))}
	s.silence(1000)
	s.voice(3000, loudVoice)
	e.BeginCalibration()
	traceTx(e, s.frames())
	voice, floor, err := e.EndCalibration()
	if err != nil {
		t.Fatal(err)
	}
	return voice, floor
}

// A sensibilidade da detecção de voz: do silêncio, só abre o microfone a voz
// que chega ao piso; a que fica abaixo (a TV, alguém no cômodo) não abre, por
// mais que seja voz. Com a fala em andamento o piso cai à metade.
func TestVADFloorOpensOnlyOnVoiceWithBody(t *testing.T) {
	eachNoiseMode(t, func(t *testing.T, mode string) {
		voice, floor := calibratedFloor(t, mode)
		t.Logf("voz medida: %.0f; piso: %.0f", voice, floor)
		run := func(floor float64, build func(s *txScript) (from, to int)) float64 {
			e, _ := newTestEngine(t)
			e.SetNoiseMode(mode)
			e.SetAutoGain(false)
			e.SetVADFloor(floor)
			s := &txScript{rng: rand.New(rand.NewSource(2))}
			s.silence(1500)
			from, to := build(s)
			s.silence(1000)
			weight, _, _ := traceTx(e, s.frames())
			return sentShare(weight, from+100, to-100)
		}
		quiet := func(s *txScript) (int, int) { return s.voice(1500, quietVoice) }
		loud := func(s *txScript) (int, int) { return s.voice(1500, loudVoice) }
		after := func(gapMs int) func(s *txScript) (int, int) {
			return func(s *txScript) (int, int) {
				s.voice(1000, loudVoice)
				s.silence(gapMs)
				return s.voice(1000, quietVoice)
			}
		}

		// Sem piso a voz baixa abre o microfone: é o piso que a segura, não o detector.
		if got := run(0, quiet); got < 0.6 {
			t.Fatalf("sem piso, a voz baixa deveria ser transmitida (%.0f%%): o teste não prova nada", 100*got)
		}
		if got := run(floor, quiet); got != 0 {
			t.Errorf("voz abaixo do piso abriu o microfone (%.0f%% transmitido)", 100*got)
		}
		if got := run(floor, loud); got < 0.9 {
			t.Errorf("voz no nível calibrado: só %.0f%% transmitido", 100*got)
		}
		if got := run(floor, after(300)); got < 0.6 {
			t.Errorf("palavra dita mais baixo 300 ms depois da voz: só %.0f%% transmitido", 100*got)
		}
		if got := run(floor, after(1500)); got != 0 {
			t.Errorf("voz baixa 1,5 s depois da fala abriu o microfone (%.0f%% transmitido)", 100*got)
		}
	})
}

// O piso é da detecção de voz: "por volume" e "apertar para falar" têm as
// próprias regras.
func TestVADFloorOnlyInAutoMode(t *testing.T) {
	for _, mode := range []string{VADLevel, VADPTT} {
		e, _ := newTestEngine(t)
		e.SetAutoGain(false)
		e.SetVADMode(mode)
		e.SetVADThreshold(300)
		e.SetPTT(true)
		e.SetVADFloor(8000)
		s := &txScript{rng: rand.New(rand.NewSource(3))}
		s.silence(1000)
		from, to := s.voice(1500, loudVoice)
		s.silence(500)
		weight, _, _ := traceTx(e, s.frames())
		if got := sentShare(weight, from+100, to-100); got < 0.6 {
			t.Errorf("modo %s: o piso da detecção de voz segurou a transmissão (%.0f%% transmitido)", mode, 100*got)
		}
	}
}

func TestVADFloorLimits(t *testing.T) {
	e, _ := newTestEngine(t)
	if e.VADFloor() != 0 {
		t.Errorf("o padrão é sem piso; veio %.0f", e.VADFloor())
	}
	for in, want := range map[float64]float64{0: 0, -5: 0, 10: 50, 700: 700, 9000: 8000} {
		e.SetVADFloor(in)
		if got := e.VADFloor(); got != want {
			t.Errorf("SetVADFloor(%.0f) → %.0f, esperado %.0f", in, got, want)
		}
	}
	e.SetVADFloor(700)
	e.SetVADFloor(math.NaN())
	if got := e.VADFloor(); got != 0 {
		t.Errorf("SetVADFloor(NaN) → %.0f, esperado 0", got)
	}
}

// A calibração mede a voz de quem fala e sugere o piso 12 dB abaixo dela.
// Enquanto dura, nada é transmitido; sem voz, não inventa um número.
func TestVoiceCalibration(t *testing.T) {
	eachNoiseMode(t, func(t *testing.T, mode string) {
		e, c := newTestEngine(t)
		e.SetNoiseMode(mode)
		e.SetVADFloor(8000) // um piso antigo alto demais não atrapalha a medida
		s := &txScript{rng: rand.New(rand.NewSource(4))}
		s.silence(1000)
		s.voice(3000, loudVoice)
		e.BeginCalibration()
		traceTx(e, s.frames())
		if frames, _ := c.get(); frames != 0 {
			t.Errorf("%d quadros transmitidos durante a calibração", frames)
		}
		voice, floor, err := e.EndCalibration()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("voz %.0f, piso %.0f", voice, floor)
		// A voz sintética tem amplitude 6000 com o volume subindo e descendo: os
		// quadros fortes ficam entre 2000 e 6000.
		if voice < 2000 || voice > 6000 {
			t.Errorf("nível da voz medido: %.0f", voice)
		}
		if floor != math.Round(voice*calibRatio) {
			t.Errorf("piso %.0f, esperado %.0f (%.2f × a voz)", floor, math.Round(voice*calibRatio), calibRatio)
		}

		// Terminada a medida, a transmissão volta.
		e.SetVADFloor(floor)
		s = &txScript{rng: rand.New(rand.NewSource(5))}
		s.silence(500)
		s.voice(1000, loudVoice)
		s.silence(500)
		traceTx(e, s.frames())
		if frames, _ := c.get(); frames < 30 {
			t.Errorf("depois da calibração só %d quadros foram transmitidos", frames)
		}

		// Sem voz: erro, e nada de piso.
		e.BeginCalibration()
		s = &txScript{rng: rand.New(rand.NewSource(6))}
		s.silence(3000)
		traceTx(e, s.frames())
		if _, _, err := e.EndCalibration(); !errors.Is(err, ErrNoVoice) {
			t.Errorf("calibração só com o ambiente: erro %v, esperado ErrNoVoice", err)
		}
		if _, _, err := e.EndCalibration(); !errors.Is(err, ErrNoVoice) {
			t.Errorf("EndCalibration sem medida em andamento: erro %v, esperado ErrNoVoice", err)
		}
	})
}
