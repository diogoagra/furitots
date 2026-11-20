// audiodemo gera arquivos WAV para ouvir, fora do app, o que o motor de áudio
// faz com uma gravação: cada voz modificada e o antes/depois do tratamento do
// microfone (supressão de ruído + ganho automático), com a supressão neural
// (RNNoise) e com a clássica, para comparar de ouvido.
//
//	go run ./cmd/audiodemo gravacao.wav pasta-de-saida
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"furito-ts/audio"
)

func writeWAV(path string, pcm []int16) error {
	b := make([]byte, 44+len(pcm)*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], audio.SampleRate)
	binary.LittleEndian.PutUint32(b[28:], audio.SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)*2))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(s))
	}
	return os.WriteFile(path, b, 0o644)
}

func clamp(v float64) int16 {
	return int16(math.Max(-32768, math.Min(32767, v)))
}

// addNoise soma ventilador, zumbido de 60 Hz e digitação (teclado de
// notebook) à gravação.
func addNoise(pcm []int16) []int16 {
	rng := rand.New(rand.NewSource(1))
	typing := make([]float64, len(pcm))
	audio.SynthKeyboard(typing, 0, rng)
	out := make([]int16, len(pcm))
	lp := 0.0
	for i, s := range pcm {
		w := rng.NormFloat64()
		lp += 0.03 * (w - lp)
		t := float64(i) / audio.SampleRate
		hum := 250 * (math.Sin(2*math.Pi*60*t) + 0.5*math.Sin(2*math.Pi*180*t))
		out[i] = clamp(float64(s) + w*300 + lp*5000 + hum + typing[i])
	}
	return out
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "uso: audiodemo <gravacao de voz> <pasta de saída>")
		os.Exit(2)
	}
	pcm, err := audio.DecodeAudioFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir := os.Args[2]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	save := func(name string, data []int16) {
		if err := writeWAV(filepath.Join(dir, name), data); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("  ", name)
	}

	// Meio segundo de ambiente antes da fala, como numa chamada de verdade.
	lead := make([]int16, audio.SampleRate/2)
	voice := append(lead, pcm...)
	// 2 s antes e 3 s depois sem fala: aí só há ruído e digitação.
	padded := append(make([]int16, 2*audio.SampleRate), pcm...)
	padded = append(padded, make([]int16, 3*audio.SampleRate)...)
	noisy := addNoise(padded)

	fmt.Println("Microfone (o que entra e o que os outros recebem, com cada supressão de ruído):")
	save("microfone-1-entrada-com-ruido-e-digitacao.wav", noisy)
	for _, m := range []struct{ file, mode string }{
		{"microfone-2-transmitido-neural.wav", audio.NoiseNeural},
		{"microfone-3-transmitido-classica.wav", audio.NoiseClassic},
	} {
		sent, err := audio.RenderTransmit(noisy, func(e *audio.Engine) { e.SetNoiseMode(m.mode) })
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		save(m.file, sent)
	}

	fmt.Println("Vozes:")
	for i, fx := range audio.Effects() {
		save(fmt.Sprintf("voz-%02d-%s.wav", i, fx.ID), audio.RenderEffect(voice, audio.VoiceEffect(fx.ID)))
	}
}
