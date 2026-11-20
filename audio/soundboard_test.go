package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeWAV monta um WAV PCM de 16 bits com um tom de 440 Hz.
func makeWAV(rate, channels int, seconds float64) []byte {
	n := int(float64(rate) * seconds)
	var data bytes.Buffer
	for i := 0; i < n; i++ {
		v := int16(12000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		for c := 0; c < channels; c++ {
			binary.Write(&data, binary.LittleEndian, v)
		}
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(36+data.Len()))
	out.WriteString("WAVEfmt ")
	binary.Write(&out, binary.LittleEndian, uint32(16))
	binary.Write(&out, binary.LittleEndian, uint16(1))
	binary.Write(&out, binary.LittleEndian, uint16(channels))
	binary.Write(&out, binary.LittleEndian, uint32(rate))
	binary.Write(&out, binary.LittleEndian, uint32(rate*channels*2))
	binary.Write(&out, binary.LittleEndian, uint16(channels*2))
	binary.Write(&out, binary.LittleEndian, uint16(16))
	out.WriteString("data")
	binary.Write(&out, binary.LittleEndian, uint32(data.Len()))
	out.Write(data.Bytes())
	return out.Bytes()
}

func checkTone(t *testing.T, what string, pcm []int16, seconds float64) {
	t.Helper()
	got := float64(len(pcm)) / SampleRate
	if math.Abs(got-seconds) > 0.08 {
		t.Errorf("%s: duração %.2f s, esperado %.2f s", what, got, seconds)
	}
	mid := pcm[len(pcm)/4 : len(pcm)*3/4]
	// 8485 para o tom gravado; o afconvert soma os canais estéreo com +3 dB.
	if level := rms(mid); level < 4000 || level > 13000 {
		t.Errorf("%s: nível %.0f fora do esperado para o tom gravado", what, level)
	}
	buf := make([]float64, 9600)
	toFloat(buf, mid[:9600])
	if f0 := estimateF0(buf); math.Abs(f0-440) > 12 {
		t.Errorf("%s: tom %.0f Hz, esperado 440 (taxa de amostragem errada?)", what, f0)
	}
}

func TestParseWAVFormats(t *testing.T) {
	for _, c := range []struct {
		rate, channels int
	}{{48000, 1}, {44100, 2}, {22050, 1}, {96000, 2}} {
		pcm, err := parseWAV(makeWAV(c.rate, c.channels, 0.5))
		if err != nil {
			t.Fatalf("%d Hz %d canais: %v", c.rate, c.channels, err)
		}
		checkTone(t, "wav", pcm, 0.5)
	}
	if _, err := parseWAV([]byte("isto não é áudio")); err == nil {
		t.Error("lixo deveria dar erro")
	}
}

// Arquivos comprimidos passam pelo afconvert do macOS (ou ffmpeg).
func TestDecodeCompressedFiles(t *testing.T) {
	if _, err := os.Stat("/usr/bin/afconvert"); err != nil {
		t.Skip("afconvert indisponível")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "tom.wav")
	if err := os.WriteFile(wav, makeWAV(44100, 2, 1.0), 0o600); err != nil {
		t.Fatal(err)
	}
	formats := map[string][]string{
		"m4a":  {"-f", "m4af", "-d", "aac"},
		"aiff": {"-f", "AIFF", "-d", "BEI16"},
		"caf":  {"-f", "caff", "-d", "LEI24"},
	}
	for ext, args := range formats {
		out := filepath.Join(dir, "tom."+ext)
		if err := exec.Command("/usr/bin/afconvert", append(args, wav, out)...).Run(); err != nil {
			t.Logf("não foi possível gerar .%s: %v", ext, err)
			continue
		}
		pcm, err := DecodeAudioFile(out)
		if err != nil {
			t.Errorf(".%s: %v", ext, err)
			continue
		}
		checkTone(t, ext, pcm, 1.0)
	}
	if ffmpeg := findFFmpeg(); ffmpeg != "" {
		for _, ext := range []string{"mp3", "ogg"} {
			out := filepath.Join(dir, "tom."+ext)
			if err := exec.Command(ffmpeg, "-v", "quiet", "-y", "-i", wav, out).Run(); err != nil {
				continue
			}
			pcm, err := DecodeAudioFile(out)
			if err != nil {
				t.Errorf(".%s: %v", ext, err)
				continue
			}
			checkTone(t, ext, pcm, 1.0)
		}
	}

	bad := filepath.Join(dir, "nota.txt")
	os.WriteFile(bad, []byte("texto"), 0o600)
	if _, err := DecodeAudioFile(bad); err == nil {
		t.Error("arquivo de texto deveria ser recusado")
	}
}

func TestSoundboardPlaysFileOnceDecoded(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "tom.wav")
	os.WriteFile(wav, makeWAV(48000, 1, 0.1), 0o600)

	s := NewSoundboardManager()
	if err := s.PlayFile(wav); err != nil {
		t.Fatal(err)
	}
	os.Remove(wav) // já está em memória: tocar de novo não depende do disco
	if err := s.PlayFile(wav); err != nil {
		t.Fatalf("segunda reprodução deveria vir da memória: %v", err)
	}
	chunks := 0
	for {
		chunk, _ := s.GetNextChunk(FrameSize)
		if chunk == nil {
			break
		}
		chunks++
	}
	if chunks != 5 {
		t.Errorf("0,1 s deveria render 5 blocos de 20 ms, rendeu %d", chunks)
	}
	s.Forget(wav)
	if err := s.PlayFile(wav); err == nil {
		t.Error("depois de esquecido e sem o arquivo, deveria falhar")
	}
}
