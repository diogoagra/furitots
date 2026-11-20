package audio

import (
	"encoding/binary"
	"testing"
)

// Arquivos de áudio vêm de fora (o usuário os baixa de qualquer lugar e põe
// no soundboard ou num pacote de avisos): um arquivo malformado tem de ser
// recusado, não derrubar o app.
func FuzzParseWAV(f *testing.F) {
	wav := func(format, channels, bits uint16, rate uint32, data []byte) []byte {
		b := make([]byte, 44+len(data))
		copy(b, "RIFF")
		binary.LittleEndian.PutUint32(b[4:], uint32(36+len(data)))
		copy(b[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(b[16:], 16)
		binary.LittleEndian.PutUint16(b[20:], format)
		binary.LittleEndian.PutUint16(b[22:], channels)
		binary.LittleEndian.PutUint32(b[24:], rate)
		binary.LittleEndian.PutUint16(b[34:], bits)
		copy(b[36:], "data")
		binary.LittleEndian.PutUint32(b[40:], uint32(len(data)))
		copy(b[44:], data)
		return b
	}
	f.Add(wav(1, 1, 16, 48000, make([]byte, 64)))
	f.Add(wav(1, 2, 24, 44100, make([]byte, 60)))
	f.Add(wav(3, 1, 32, 8000, make([]byte, 64)))
	f.Add(wav(1, 8, 8, 384000, make([]byte, 63)))
	f.Add(wav(1, 0, 16, 48000, nil))
	f.Add(wav(1, 1, 0, 48000, make([]byte, 8)))
	f.Add([]byte("RIFF\xff\xff\xff\xffWAVEfmt \xff\xff\xff\xff"))
	f.Add([]byte("RIFF\x00\x00\x00\x00WAVEdata\xff\xff\xff\x7f"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		parseWAV(data)
	})
}
