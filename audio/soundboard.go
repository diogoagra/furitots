package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

const (
	maxSoundSeconds = 5 * 60   // arquivos maiores são cortados em 5 minutos
	soundCacheBytes = 96 << 20 // memória máxima para sons já decodificados
)

// SoundboardManager toca um som por vez, entregue em blocos ao transmissor.
type SoundboardManager struct {
	mu        sync.Mutex
	activePCM []int16
	activePos int
	volume    float64
	loopback  bool
	isPlaying bool

	cacheMu    sync.Mutex
	cache      map[string][]int16
	cacheOrder []string
	cacheSize  int
}

func NewSoundboardManager() *SoundboardManager {
	return &SoundboardManager{volume: 0.8, loopback: true, cache: make(map[string][]int16)}
}

// PlayFile decodifica (uma vez; depois fica em memória) e toca um arquivo.
func (s *SoundboardManager) PlayFile(path string) error {
	pcm, err := s.Load(path)
	if err != nil {
		return err
	}
	s.play(pcm)
	return nil
}

// Load decodifica um arquivo para 48 kHz mono e o guarda em memória.
func (s *SoundboardManager) Load(path string) ([]int16, error) {
	s.cacheMu.Lock()
	if pcm, ok := s.cache[path]; ok {
		s.cacheMu.Unlock()
		return pcm, nil
	}
	s.cacheMu.Unlock()

	pcm, err := DecodeAudioFile(path)
	if err != nil {
		return nil, err
	}

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if _, ok := s.cache[path]; !ok {
		s.cache[path] = pcm
		s.cacheOrder = append(s.cacheOrder, path)
		s.cacheSize += len(pcm) * 2
		for s.cacheSize > soundCacheBytes && len(s.cacheOrder) > 1 {
			oldest := s.cacheOrder[0]
			s.cacheOrder = s.cacheOrder[1:]
			s.cacheSize -= len(s.cache[oldest]) * 2
			delete(s.cache, oldest)
		}
	}
	return pcm, nil
}

// Forget tira um arquivo da memória (ex.: quando o som é removido).
func (s *SoundboardManager) Forget(path string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if pcm, ok := s.cache[path]; ok {
		s.cacheSize -= len(pcm) * 2
		delete(s.cache, path)
		for i, p := range s.cacheOrder {
			if p == path {
				s.cacheOrder = append(s.cacheOrder[:i], s.cacheOrder[i+1:]...)
				break
			}
		}
	}
}

func (s *SoundboardManager) play(pcm []int16) {
	s.mu.Lock()
	s.activePCM = pcm
	s.activePos = 0
	s.isPlaying = len(pcm) > 0
	s.mu.Unlock()
}

func (s *SoundboardManager) StopSound() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activePCM = nil
	s.activePos = 0
	s.isPlaying = false
}

func (s *SoundboardManager) IsPlaying() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isPlaying
}

// SetVolume ajusta o volume do soundboard (0 a 100).
func (s *SoundboardManager) SetVolume(vol float64) {
	if vol < 0 {
		vol = 0
	}
	if vol > 100 {
		vol = 100
	}
	s.mu.Lock()
	s.volume = vol / 100
	s.mu.Unlock()
}

func (s *SoundboardManager) Volume() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.volume * 100
}

// SetLoopback define se o som também toca localmente.
func (s *SoundboardManager) SetLoopback(enabled bool) {
	s.mu.Lock()
	s.loopback = enabled
	s.mu.Unlock()
}

// GetNextChunk devolve o próximo bloco do som ativo (nil se não há som) e se
// ele deve tocar localmente.
func (s *SoundboardManager) GetNextChunk(numSamples int) ([]int16, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isPlaying || s.activePos >= len(s.activePCM) {
		s.isPlaying = false
		return nil, s.loopback
	}

	end := s.activePos + numSamples
	if end > len(s.activePCM) {
		end = len(s.activePCM)
	}
	chunk := make([]int16, numSamples)
	for i := 0; i < end-s.activePos; i++ {
		val := float64(s.activePCM[s.activePos+i]) * s.volume
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		chunk[i] = int16(val)
	}
	s.activePos = end
	if s.activePos >= len(s.activePCM) {
		s.isPlaying = false
	}
	return chunk, s.loopback
}

// ---------------------------------------------------------------- decodificação

// DecodeAudioFile lê um arquivo de áudio e devolve 48 kHz mono.
//
// WAV sem compressão é lido direto. O resto passa pelo afconvert, que vem com
// o macOS (MP3, AAC/M4A, AIFF, FLAC, CAF…); o que ele não abrir (OGG, Opus,
// WebM) é tentado com o ffmpeg, se estiver instalado.
func DecodeAudioFile(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("não foi possível abrir %s: %w", filepath.Base(path), err)
	}
	if pcm, err := parseWAV(data); err == nil {
		return capSound(pcm), nil
	}

	if _, err := os.Stat("/usr/bin/afconvert"); err == nil {
		tmp, err := os.CreateTemp("", "furito-*.wav")
		if err == nil {
			tmpPath := tmp.Name()
			tmp.Close()
			defer os.Remove(tmpPath)
			if exec.Command("/usr/bin/afconvert", "-f", "WAVE", "-d", "LEI16@48000", "-c", "1", path, tmpPath).Run() == nil {
				if converted, err := os.ReadFile(tmpPath); err == nil {
					if pcm, err := parseWAV(converted); err == nil && len(pcm) > 0 {
						return capSound(pcm), nil
					}
				}
			}
		}
	}

	if ffmpeg := findFFmpeg(); ffmpeg != "" {
		out, err := exec.Command(ffmpeg, "-v", "quiet", "-t", fmt.Sprint(maxSoundSeconds), "-i", path,
			"-f", "s16le", "-ar", "48000", "-ac", "1", "pipe:1").Output()
		if err == nil && len(out) >= 2 {
			pcm := make([]int16, len(out)/2)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(out[i*2:]))
			}
			return capSound(pcm), nil
		}
	}
	return nil, fmt.Errorf("formato de áudio não suportado: %s (use MP3, WAV, M4A, AIFF ou FLAC)", filepath.Base(path))
}

func capSound(pcm []int16) []int16 {
	if len(pcm) > maxSoundSeconds*SampleRate {
		pcm = pcm[:maxSoundSeconds*SampleRate]
	}
	return pcm
}

// findFFmpeg procura o ffmpeg também nos lugares do Homebrew: apps abertos
// pelo Finder não herdam o PATH do terminal.
func findFFmpeg() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// parseWAV lê PCM inteiro (8/16/24/32 bits) ou float de 32 bits, mistura os
// canais em mono e reamostra para 48 kHz.
func parseWAV(data []byte) ([]int16, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("não é WAV")
	}
	var format, channels, bits int
	var rate int
	var pcmData []byte
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4:]))
		body := pos + 8
		if size < 0 || body+size > len(data) {
			size = len(data) - body // arquivos gravados em streaming deixam o tamanho errado
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("cabeçalho WAV inválido")
			}
			format = int(binary.LittleEndian.Uint16(data[body:]))
			channels = int(binary.LittleEndian.Uint16(data[body+2:]))
			rate = int(binary.LittleEndian.Uint32(data[body+4:]))
			bits = int(binary.LittleEndian.Uint16(data[body+14:]))
			if format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE
				format = int(binary.LittleEndian.Uint16(data[body+24:]))
			}
		case "data":
			pcmData = data[body : body+size]
		}
		pos = body + size + size%2
	}
	if pcmData == nil || channels < 1 || channels > 8 || rate < 4000 || rate > 384000 {
		return nil, errors.New("WAV sem áudio utilizável")
	}
	bytesPer := bits / 8
	var sample func(b []byte) float64
	switch {
	case format == 1 && bits == 8:
		sample = func(b []byte) float64 { return (float64(b[0]) - 128) * 256 }
	case format == 1 && bits == 16:
		sample = func(b []byte) float64 { return float64(int16(binary.LittleEndian.Uint16(b))) }
	case format == 1 && bits == 24:
		sample = func(b []byte) float64 {
			return float64(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)) / 65536
		}
	case format == 1 && bits == 32:
		sample = func(b []byte) float64 { return float64(int32(binary.LittleEndian.Uint32(b))) / 65536 }
	case format == 3 && bits == 32:
		sample = func(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) * 32767 }
	default:
		return nil, errors.New("WAV comprimido")
	}

	frames := len(pcmData) / (bytesPer * channels)
	mono := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < channels; c++ {
			off := (i*channels + c) * bytesPer
			sum += sample(pcmData[off : off+bytesPer])
		}
		mono[i] = sum / float64(channels)
	}

	if rate != SampleRate {
		mono = resampleLinear(mono, float64(rate)/SampleRate)
	}
	out := make([]int16, len(mono))
	toInt16(out, mono)
	return out, nil
}

// resampleLinear lê a entrada com passo `step` amostras por amostra de saída.
// Ao reduzir a taxa, um passa-baixas antes evita aliasing.
func resampleLinear(in []float64, step float64) []float64 {
	if step > 1 {
		// Corte logo abaixo da nova frequência de Nyquist. Os coeficientes são
		// calculados em relação a 48 kHz, por isso a divisão pelo passo.
		cut := 0.45 * SampleRate / step
		lp1, lp2 := newLowpass(cut, 0.54), newLowpass(cut, 1.31)
		filtered := make([]float64, len(in))
		for i, x := range in {
			filtered[i] = lp2.step(lp1.step(x))
		}
		in = filtered
	}
	n := int(float64(len(in)) / step)
	out := make([]float64, n)
	for i := range out {
		pos := float64(i) * step
		j := int(pos)
		if j+1 >= len(in) {
			out[i] = in[len(in)-1]
			continue
		}
		frac := pos - float64(j)
		out[i] = in[j]*(1-frac) + in[j+1]*frac
	}
	return out
}
