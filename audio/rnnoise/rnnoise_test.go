package rnnoise

import (
	"math"
	"math/rand"
	"testing"
)

func rms(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

// Silêncio entra, silêncio sai; ruído branco sai bem atenuado e com
// probabilidade de voz baixa.
func TestSilenceAndNoise(t *testing.T) {
	d := New()
	frame := make([]float64, FrameSize)
	for i := 0; i < 50; i++ {
		for j := range frame {
			frame[j] = 0
		}
		if p := d.Process(frame); p != 0 || rms(frame) != 0 {
			t.Fatalf("silêncio: probabilidade %.2f, nível %.3f", p, rms(frame))
		}
	}

	rng := rand.New(rand.NewSource(1))
	var in, out, prob float64
	n := 0
	for i := 0; i < 400; i++ { // 4 s
		for j := range frame {
			frame[j] = rng.NormFloat64() * 500
		}
		lvl := rms(frame)
		p := d.Process(frame)
		if i >= 100 {
			in += lvl * lvl
			out += rms(frame) * rms(frame)
			prob += p
			n++
		}
	}
	att := 10 * math.Log10(out/in)
	t.Logf("ruído branco: %.1f dB, probabilidade média de voz %.3f", att, prob/float64(n))
	if att > -25 {
		t.Errorf("ruído branco pouco atenuado: %.1f dB", att)
	}
	if prob/float64(n) > 0.1 {
		t.Errorf("ruído branco com probabilidade de voz de %.2f", prob/float64(n))
	}
}

// Um som com tom e harmônicos, como uma vogal, passa quase inteiro e a rede o
// dá como voz. (O tom é de 220 Hz: com o mesmo zumbido a 130–140 Hz, que não
// tem as ressonâncias de uma voz de verdade, a rede fica em dúvida.)
func TestVoiceLikeSoundPasses(t *testing.T) {
	d := New()
	frame := make([]float64, FrameSize)
	phase := 0.0
	var in, out, prob float64
	n := 0
	for i := 0; i < 300; i++ {
		for j := range frame {
			sec := float64(i*FrameSize+j) / 48000
			phase += 2 * math.Pi * 220 * (1 + 0.02*math.Sin(2*math.Pi*5*sec)) / 48000
			var s float64
			for h := 1; h <= 12; h++ {
				s += math.Sin(float64(h)*phase) / float64(h)
			}
			frame[j] = 3000 * s * (0.25 + 0.75*0.5*(1-math.Cos(2*math.Pi*3*sec)))
		}
		lvl := rms(frame)
		p := d.Process(frame)
		if i >= 50 {
			in += lvl * lvl
			out += rms(frame) * rms(frame)
			prob += p
			n++
		}
	}
	change := 10 * math.Log10(out/in)
	t.Logf("som de voz: %.2f dB, probabilidade média de voz %.3f", change, prob/float64(n))
	if math.Abs(change) > 1.5 {
		t.Errorf("som de voz alterado em %.1f dB", change)
	}
	if prob/float64(n) < 0.9 {
		t.Errorf("som de voz com probabilidade de só %.2f", prob/float64(n))
	}
}

// Dois estados independentes dão a mesma saída para a mesma entrada.
func TestStatesAreIndependent(t *testing.T) {
	a, b := New(), New()
	rng := rand.New(rand.NewSource(2))
	fa, fb := make([]float64, FrameSize), make([]float64, FrameSize)
	for i := 0; i < 100; i++ {
		for j := range fa {
			fa[j] = rng.NormFloat64() * 2000
			fb[j] = fa[j]
		}
		pa, pb := a.Process(fa), b.Process(fb)
		if pa != pb {
			t.Fatalf("quadro %d: probabilidades diferentes (%v e %v)", i, pa, pb)
		}
		for j := range fa {
			if fa[j] != fb[j] {
				t.Fatalf("quadro %d: saídas diferentes", i)
			}
		}
	}
}

// Custo de tratar 10 ms de áudio (o tempo real pede 100 chamadas por segundo).
func BenchmarkProcess(b *testing.B) {
	d := New()
	rng := rand.New(rand.NewSource(3))
	src := make([]float64, FrameSize)
	for j := range src {
		src[j] = rng.NormFloat64() * 2000
	}
	frame := make([]float64, FrameSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(frame, src)
		d.Process(frame)
	}
}
