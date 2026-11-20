package audio

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

// devices guarda os dispositivos nativos. Há dois caminhos:
//
//   - padrão: captura e saída independentes via miniaudio (CoreAudio);
//   - cancelamento de eco: no macOS, captura e saída passam juntas pela
//     unidade de processamento de voz do sistema (vpio_darwin.c), que remove
//     da captura o que sai pelos alto-falantes.
type devices struct {
	mu       sync.Mutex
	ctx      *malgo.AllocatedContext
	playback *malgo.Device
	capture  *malgo.Device
	vp       *vpUnit
	inName   string
	outName  string

	wantPlayback bool
	wantCapture  bool
	echoCancel   bool
	inChoice     string // nome do dispositivo escolhido ("" = padrão do sistema)
	outChoice    string

	playBuf []int16 // só o callback de saída usa
	micAcc  []int16 // só o callback de captura usa
}

func (e *Engine) ensureContextLocked() error {
	if e.dev.ctx != nil {
		return nil
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("falha ao iniciar o áudio: %w", err)
	}
	e.dev.ctx = ctx
	return nil
}

func defaultDeviceName(ctx *malgo.AllocatedContext, kind malgo.DeviceType) string {
	infos, err := ctx.Devices(kind)
	if err != nil {
		return ""
	}
	for _, info := range infos {
		if info.IsDefault != 0 {
			return info.Name()
		}
	}
	if len(infos) > 0 {
		return infos[0].Name()
	}
	return ""
}

// DeviceInfo descreve um dispositivo de áudio do sistema.
type DeviceInfo struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// ListDevices devolve os dispositivos de entrada e de saída disponíveis.
func (e *Engine) ListDevices() (inputs, outputs []DeviceInfo) {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	inputs, outputs = []DeviceInfo{}, []DeviceInfo{}
	if e.ensureContextLocked() != nil {
		return
	}
	list := func(kind malgo.DeviceType) []DeviceInfo {
		out := []DeviceInfo{}
		infos, err := e.dev.ctx.Devices(kind)
		if err != nil {
			return out
		}
		for i := range infos {
			out = append(out, DeviceInfo{Name: infos[i].Name(), IsDefault: infos[i].IsDefault != 0})
		}
		return out
	}
	return list(malgo.Capture), list(malgo.Playback)
}

// findDevice devolve o dispositivo com o nome dado (nil = usa o padrão).
func (e *Engine) findDevice(kind malgo.DeviceType, name string) *malgo.DeviceInfo {
	if name == "" {
		return nil
	}
	infos, err := e.dev.ctx.Devices(kind)
	if err != nil {
		return nil
	}
	for i := range infos {
		if infos[i].Name() == name {
			return &infos[i]
		}
	}
	return nil // foi desconectado: cai no padrão do sistema
}

// SetInputDevice escolhe o microfone pelo nome ("" = padrão do sistema).
// Com o cancelamento de eco ligado vale sempre o padrão do sistema.
func (e *Engine) SetInputDevice(name string) error {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	if e.dev.inChoice == name {
		return nil
	}
	e.dev.inChoice = name
	e.coupling.Store(couplingUnknown) // outro microfone: reaprende se ele ouve os avisos
	if e.dev.capture != nil {
		e.stopMalgoLocked(false, true)
	}
	return e.applyLocked()
}

// SetOutputDevice escolhe a saída de som pelo nome ("" = padrão do sistema).
func (e *Engine) SetOutputDevice(name string) error {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	if e.dev.outChoice == name {
		return nil
	}
	e.dev.outChoice = name
	e.coupling.Store(couplingUnknown) // outra saída (caixas ↔ fone): reaprende
	if e.dev.playback != nil {
		e.stopMalgoLocked(true, false)
	}
	return e.applyLocked()
}

// DeviceChoices devolve os dispositivos escolhidos ("" = padrão).
func (e *Engine) DeviceChoices() (input, output string) {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	return e.dev.inChoice, e.dev.outChoice
}

// feedCapture recebe amostras do microfone (qualquer tamanho de bloco) e
// entrega quadros de 20 ms ao transmissor.
func (e *Engine) feedCapture(samples []int16) {
	e.dev.micAcc = append(e.dev.micAcc, samples...)
	for len(e.dev.micAcc) >= FrameSize {
		frame := make([]int16, FrameSize)
		copy(frame, e.dev.micAcc)
		e.pushMic(frame)
		rest := copy(e.dev.micAcc, e.dev.micAcc[FrameSize:])
		e.dev.micAcc = e.dev.micAcc[:rest]
	}
}

func (e *Engine) startMalgoPlaybackLocked() error {
	if e.dev.playback != nil {
		return nil
	}
	if err := e.ensureContextLocked(); err != nil {
		return err
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	chosen := e.findDevice(malgo.Playback, e.dev.outChoice)
	if chosen != nil {
		cfg.Playback.DeviceID = chosen.ID.Pointer()
	}
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInMilliseconds = 10

	dev, err := malgo.InitDevice(e.dev.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(out, _ []byte, frames uint32) {
			n := int(frames)
			if n*2 > len(out) {
				n = len(out) / 2
			}
			if cap(e.dev.playBuf) < n {
				e.dev.playBuf = make([]int16, n)
			}
			buf := e.dev.playBuf[:n]
			e.fillPlayback(buf)
			for i, s := range buf {
				binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
			}
		},
	})
	if err != nil {
		return fmt.Errorf("falha ao abrir a saída de áudio: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return fmt.Errorf("falha ao iniciar a saída de áudio: %w", err)
	}
	e.dev.playback = dev
	e.dev.outName = defaultDeviceName(e.dev.ctx, malgo.Playback)
	if chosen != nil {
		e.dev.outName = chosen.Name()
	}
	return nil
}

func (e *Engine) startMalgoCaptureLocked() error {
	if e.dev.capture != nil {
		return nil
	}
	if err := e.ensureContextLocked(); err != nil {
		return err
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	chosen := e.findDevice(malgo.Capture, e.dev.inChoice)
	if chosen != nil {
		cfg.Capture.DeviceID = chosen.ID.Pointer()
	}
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.SampleRate = SampleRate
	cfg.PeriodSizeInMilliseconds = 10

	e.dev.micAcc = e.dev.micAcc[:0]
	samples := make([]int16, 0, 4096)
	dev, err := malgo.InitDevice(e.dev.ctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(_, in []byte, frames uint32) {
			n := int(frames)
			if n*2 > len(in) {
				n = len(in) / 2
			}
			samples = samples[:0]
			for i := 0; i < n; i++ {
				samples = append(samples, int16(binary.LittleEndian.Uint16(in[i*2:])))
			}
			e.feedCapture(samples)
		},
	})
	if err != nil {
		return fmt.Errorf("falha ao abrir o microfone: %w", err)
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return fmt.Errorf("falha ao iniciar o microfone: %w", err)
	}
	e.dev.capture = dev
	e.dev.inName = defaultDeviceName(e.dev.ctx, malgo.Capture)
	if chosen != nil {
		e.dev.inName = chosen.Name()
	}
	return nil
}

func (e *Engine) stopMalgoLocked(playback, capture bool) {
	if capture && e.dev.capture != nil {
		e.dev.capture.Uninit()
		e.dev.capture = nil
	}
	if playback && e.dev.playback != nil {
		e.dev.playback.Uninit()
		e.dev.playback = nil
	}
}

// applyLocked deixa os dispositivos abertos de acordo com o que foi pedido.
func (e *Engine) applyLocked() error {
	if e.dev.echoCancel && e.dev.wantCapture && vpAvailable {
		if e.dev.vp != nil {
			return nil
		}
		e.stopMalgoLocked(true, true)
		e.dev.micAcc = e.dev.micAcc[:0]
		vp, err := startVP(e)
		if err == nil {
			e.dev.vp = vp
			if e.ensureContextLocked() == nil {
				e.dev.inName = defaultDeviceName(e.dev.ctx, malgo.Capture)
				e.dev.outName = defaultDeviceName(e.dev.ctx, malgo.Playback)
			}
			return nil
		}
		// Sem processamento de voz: segue pelo caminho padrão e avisa.
		e.dev.echoCancel = false
		if fallback := e.applyLocked(); fallback != nil {
			return fallback
		}
		return fmt.Errorf("cancelamento de eco indisponível (%w); usando o áudio padrão", err)
	}

	if e.dev.vp != nil {
		e.dev.vp.stop()
		e.dev.vp = nil
	}
	var firstErr error
	if e.dev.wantPlayback {
		firstErr = e.startMalgoPlaybackLocked()
	}
	if e.dev.wantCapture {
		if err := e.startMalgoCaptureLocked(); err != nil && firstErr == nil {
			firstErr = err
		}
	} else {
		e.stopMalgoLocked(false, true)
	}
	return firstErr
}

// StartPlayback abre a saída de áudio padrão (se ainda não estiver aberta).
func (e *Engine) StartPlayback() error {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	e.dev.wantPlayback = true
	return e.applyLocked()
}

// StartCapture abre o microfone padrão. No macOS isso dispara o pedido de
// permissão na primeira vez.
func (e *Engine) StartCapture() error {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	e.dev.wantCapture = true
	return e.applyLocked()
}

// StopCapture fecha o microfone (o indicador de uso do macOS apaga).
func (e *Engine) StopCapture() {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	e.dev.wantCapture = false
	e.applyLocked()
}

// SetEchoCancellation liga/desliga o processamento de voz do macOS
// (cancelamento de eco). Reabre os dispositivos se já estiverem em uso.
func (e *Engine) SetEchoCancellation(on bool) error {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	e.dev.echoCancel = on && vpAvailable
	e.coupling.Store(couplingUnknown)
	return e.applyLocked()
}

func (e *Engine) EchoCancellation() bool {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	return e.dev.echoCancel
}

// EchoCancellationAvailable indica se o sistema oferece cancelamento de eco.
func EchoCancellationAvailable() bool { return vpAvailable }

// StopDevices fecha todos os dispositivos.
func (e *Engine) StopDevices() {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	e.dev.wantPlayback, e.dev.wantCapture = false, false
	if e.dev.vp != nil {
		e.dev.vp.stop()
		e.dev.vp = nil
	}
	e.stopMalgoLocked(true, true)
	if e.dev.ctx != nil {
		e.dev.ctx.Uninit()
		e.dev.ctx.Free()
		e.dev.ctx = nil
	}
}

// CaptureAvailable indica se o microfone está aberto.
func (e *Engine) CaptureAvailable() bool {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	return e.dev.capture != nil || e.dev.vp != nil
}

// DeviceNames devolve os nomes dos dispositivos em uso.
func (e *Engine) DeviceNames() (input, output string) {
	e.dev.mu.Lock()
	defer e.dev.mu.Unlock()
	return e.dev.inName, e.dev.outName
}

// FeedCapture entrega amostras de microfone ao motor, como um dispositivo de
// captura faria. Serve para testes e para fontes de áudio alternativas; não
// use junto com um microfone aberto.
func (e *Engine) FeedCapture(samples []int16) { e.feedCapture(samples) }

// ReadPlayback preenche out com o áudio que iria para os alto-falantes, como
// um dispositivo de saída faria. Não use junto com a saída aberta.
func (e *Engine) ReadPlayback(out []int16) { e.fillPlayback(out) }
