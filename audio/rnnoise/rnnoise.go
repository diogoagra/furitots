// Package rnnoise liga o app ao RNNoise 0.2 (Xiph.Org, licença BSD): a
// supressão de ruído por rede neural e o detector de voz que vem com ela.
//
// O código C desta pasta é o da versão oficial, sem alterações, e é compilado
// junto com o app; a origem e a licença estão em ORIGEM.md e COPYING.
package rnnoise

/*
#cgo CFLAGS: -O3
#cgo LDFLAGS: -lm
#include "rnnoise.h"
*/
import "C"

import "runtime"

// FrameSize é o tamanho do quadro do RNNoise: 10 ms a 48 kHz.
const FrameSize = 480

// Delay é o atraso do áudio tratado em relação à entrada, em amostras: 10 ms
// da sobreposição das janelas e 10 ms que a rede olha à frente.
const Delay = 2 * FrameSize

// Denoiser trata um fluxo mono de 48 kHz. Não pode ser usado por duas
// goroutines ao mesmo tempo.
type Denoiser struct {
	st      *C.DenoiseState
	in, out [FrameSize]C.float
}

// New cria um Denoiser com o modelo que vem embutido na biblioteca.
func New() *Denoiser {
	if int(C.rnnoise_get_frame_size()) != FrameSize {
		panic("rnnoise: tamanho de quadro inesperado")
	}
	st := C.rnnoise_create(nil)
	if st == nil {
		panic("rnnoise: falha ao criar o estado")
	}
	d := &Denoiser{st: st}
	runtime.AddCleanup(d, func(st *C.DenoiseState) { C.rnnoise_destroy(st) }, st)
	return d
}

// Process trata 10 ms no lugar (amostras na escala de 16 bits) e devolve a
// probabilidade de haver voz, de 0 a 1. O áudio sai atrasado de Delay
// amostras: o que volta em frame é o trecho entregue duas chamadas atrás. A
// probabilidade é a dos 20 ms mais recentes da entrada (este trecho e o
// anterior), ou seja, está à frente do áudio devolvido.
func (d *Denoiser) Process(frame []float64) float64 {
	_ = frame[FrameSize-1]
	for i := range d.in {
		d.in[i] = C.float(frame[i])
	}
	p := C.rnnoise_process_frame(d.st, &d.out[0], &d.in[0])
	for i := range d.out {
		frame[i] = float64(d.out[i])
	}
	runtime.KeepAlive(d)
	return float64(p)
}
