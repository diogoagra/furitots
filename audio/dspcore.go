package audio

import "math"

// ---------------------------------------------------------------- FFT

// fft é uma FFT radix-2 simples, com tabelas pré-calculadas.
type fft struct {
	n   int
	rev []int
	cos []float64
	sin []float64
}

func newFFT(n int) *fft {
	f := &fft{n: n, rev: make([]int, n), cos: make([]float64, n/2), sin: make([]float64, n/2)}
	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := 0; i < n; i++ {
		r := 0
		for b := 0; b < bits; b++ {
			if i&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		f.rev[i] = r
	}
	for i := 0; i < n/2; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		f.cos[i] = math.Cos(a)
		f.sin[i] = math.Sin(a)
	}
	return f
}

// transform calcula a FFT no lugar. A inversa já divide por n.
func (f *fft) transform(re, im []float64, inverse bool) {
	n := f.n
	for i := 0; i < n; i++ {
		if j := f.rev[i]; j > i {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				wr, wi := f.cos[k*step], -f.sin[k*step]
				if inverse {
					wi = -wi
				}
				a, b := start+k, start+k+half
				tr := re[b]*wr - im[b]*wi
				ti := re[b]*wi + im[b]*wr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
	if inverse {
		inv := 1 / float64(n)
		for i := 0; i < n; i++ {
			re[i] *= inv
			im[i] *= inv
		}
	}
}

// ---------------------------------------------------------------- filtros

// biquad é um filtro IIR de 2ª ordem (fórmulas do "Audio EQ Cookbook").
type biquad struct {
	b0, b1, b2, a1, a2 float64
	z1, z2             float64
}

func (q *biquad) set(b0, b1, b2, a0, a1, a2 float64) {
	q.b0, q.b1, q.b2, q.a1, q.a2 = b0/a0, b1/a0, b2/a0, a1/a0, a2/a0
}

func newLowpass(freq, q float64) *biquad {
	w := 2 * math.Pi * freq / SampleRate
	alpha, c := math.Sin(w)/(2*q), math.Cos(w)
	f := &biquad{}
	f.set((1-c)/2, 1-c, (1-c)/2, 1+alpha, -2*c, 1-alpha)
	return f
}

func newHighpass(freq, q float64) *biquad {
	w := 2 * math.Pi * freq / SampleRate
	alpha, c := math.Sin(w)/(2*q), math.Cos(w)
	f := &biquad{}
	f.set((1+c)/2, -(1 + c), (1+c)/2, 1+alpha, -2*c, 1-alpha)
	return f
}

func newPeaking(freq, q, gainDB float64) *biquad {
	a := math.Pow(10, gainDB/40)
	w := 2 * math.Pi * freq / SampleRate
	alpha, c := math.Sin(w)/(2*q), math.Cos(w)
	f := &biquad{}
	f.set(1+alpha*a, -2*c, 1-alpha*a, 1+alpha/a, -2*c, 1-alpha/a)
	return f
}

func (q *biquad) step(x float64) float64 {
	y := q.b0*x + q.z1
	q.z1 = q.b1*x - q.a1*y + q.z2
	q.z2 = q.b2*x - q.a2*y
	return y
}

func (q *biquad) run(buf []float64) {
	for i, x := range buf {
		buf[i] = q.step(x)
	}
}

// ---------------------------------------------------------------- dinâmica

// limiter impede que o sinal passe do teto sem cortar a onda: o ganho cai no
// instante do pico e volta devagar.
type limiter struct {
	ceiling float64
	release float64 // fator por amostra
	env     float64
}

func newLimiter(ceiling, releaseMs float64) *limiter {
	return &limiter{ceiling: ceiling, release: math.Exp(-1 / (releaseMs * SampleRate / 1000))}
}

func (l *limiter) run(buf []float64) {
	for i, x := range buf {
		a := math.Abs(x)
		l.env *= l.release
		if a > l.env {
			l.env = a
		}
		if l.env > l.ceiling {
			buf[i] = x * l.ceiling / l.env
		}
	}
}

// leveler aproxima o volume de uma voz de um nível-alvo, devagar o bastante
// para não "bombear". Só mede quadros com sinal de verdade.
type leveler struct {
	target  float64
	maxGain float64
	minGain float64
	level   float64
	gain    float64

	// Opcionais; zero mantém o comportamento base.
	fall   float64 // rapidez com que o ganho desce quando a voz fica mais alta (base: 0,06, a mesma da subida)
	warmup int     // quadros medidos no começo em que o nível desce mais rápido: acha logo quem fala baixo
}

func newLeveler(target, minGain, maxGain float64) *leveler {
	return &leveler{target: target, minGain: minGain, maxGain: maxGain, level: target, gain: 1}
}

// run ajusta o ganho do quadro. measure diz se o quadro deve atualizar a
// medição (voz presente); sem isso o ganho fica parado.
func (l *leveler) run(buf []float64, measure bool) {
	prev := l.gain
	if measure {
		var sum float64
		for _, x := range buf {
			sum += x * x
		}
		lvl := math.Sqrt(sum / float64(len(buf)))
		if lvl > l.level {
			l.level += 0.30 * (lvl - l.level) // sobe rápido: voz mais alta
		} else {
			down := 0.02 // desce devagar: pausas entre sílabas
			if l.warmup > 0 {
				down = 0.10
			}
			l.level += down * (lvl - l.level)
		}
		if l.warmup > 0 {
			l.warmup--
		}
		want := l.target / math.Max(l.level, 1)
		want = math.Min(l.maxGain, math.Max(l.minGain, want))
		rate := 0.06
		if want < l.gain && l.fall > 0 {
			rate = l.fall
		}
		l.gain += rate * (want - l.gain)
	}
	ramp(buf, prev, l.gain)
}

// floorTracker acompanha o ruído de fundo de um sinal pelo nível de cada
// quadro: desce na hora para um quadro mais baixo e sobe devagar (~2 dB/s com
// quadros de 20 ms). Fica no fundo entre as palavras, não na fala.
type floorTracker struct{ level float64 }

func (f *floorTracker) update(rms float64) float64 {
	rms = math.Max(rms, 1)
	if f.level == 0 || rms < f.level {
		f.level = rms
	} else {
		f.level *= 1.005
	}
	return f.level
}

// ramp aplica um ganho que vai de from a to ao longo do quadro: sem degrau
// entre um quadro e o seguinte.
func ramp(buf []float64, from, to float64) {
	n := float64(len(buf))
	for i := range buf {
		buf[i] *= from + (to-from)*float64(i)/n
	}
}

func toFloat(dst []float64, src []int16) {
	for i, s := range src {
		dst[i] = float64(s)
	}
}

func toInt16(dst []int16, src []float64) {
	for i, v := range src {
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		dst[i] = int16(v)
	}
}

func rmsFloat(buf []float64) float64 {
	if len(buf) == 0 {
		return 0
	}
	var sum float64
	for _, x := range buf {
		sum += x * x
	}
	return math.Sqrt(sum / float64(len(buf)))
}
