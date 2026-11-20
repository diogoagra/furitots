package audio

import (
	"math"
	"math/rand"
)

// SynthKeyboard soma batidas de tecla sintéticas a um sinal (48 kHz), para
// testar e demonstrar o detector de voz. kind 0 imita o teclado de um
// notebook captado pelo microfone embutido (pancada grave que ressoa na
// carcaça + estalo agudo); kind 1 imita um teclado mecânico (clique seco +
// "clack"). Cada tecla tem a descida e, ~90 ms depois, a subida.
func SynthKeyboard(buf []float64, kind int, rng *rand.Rand) {
	hit := func(pos int, amp float64) {
		type mode struct{ freq, tau, gain float64 }
		modes := []mode{{190, 18, 1.0}, {410, 12, 0.7}, {900, 6, 0.5}}
		click := 3.0
		if kind == 1 {
			modes = []mode{{150, 25, 0.6}, {1200, 8, 0.9}, {2600, 4, 0.6}}
			click = 2.0
		}
		for i := range modes {
			modes[i].freq *= 0.85 + 0.3*rng.Float64()
		}
		for j := 0; j < 4000 && pos+j < len(buf); j++ {
			t := float64(j) / SampleRate * 1000 // ms
			v := 0.9 * rng.NormFloat64() * math.Exp(-t/click)
			for _, m := range modes {
				v += m.gain * math.Sin(2*math.Pi*m.freq*t/1000) * math.Exp(-t/m.tau)
			}
			buf[pos+j] += amp * v
		}
	}
	for pos := 2000; pos < len(buf)-6000; pos += 5000 + rng.Intn(4000) { // ~7 teclas/s
		amp := 5000 + 5000*rng.Float64()
		hit(pos, amp)
		hit(pos+3800+rng.Intn(1200), amp*0.4)
	}
}

// RenderTransmit devolve o que seria transmitido para uma gravação de
// microfone (48 kHz mono), fora do tempo real: tratamento, detecção de voz e
// voz modificada, como numa chamada. configure pode ajustar o motor antes.
func RenderTransmit(pcm []int16, configure func(*Engine)) ([]int16, error) {
	e, err := newEngine(func([]byte) {})
	if err != nil {
		return nil, err
	}
	if configure != nil {
		configure(e)
	}
	e.SetMicTest(true)

	delay := lookahead*FrameSize + NoiseDelay(e.NoiseMode())
	out := make([]int16, 0, len(pcm)+delay)
	frame := make([]int16, FrameSize)
	play := make([]int16, FrameSize)
	for pos := 0; pos < len(pcm)+delay+FrameSize; pos += FrameSize {
		for i := range frame {
			frame[i] = 0
			if pos+i < len(pcm) {
				frame[i] = pcm[pos+i]
			}
		}
		e.txFrame(frame)
		e.fillPlayback(play)
		out = append(out, play...)
	}
	return out[delay:], nil
}
