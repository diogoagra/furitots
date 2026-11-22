package audio

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// Gravação da conversa de uma conexão: as vozes que chegam por ela e o que nós
// transmitimos para ela (já com o tratamento do microfone, a voz modificada e
// o soundboard), somados num arquivo WAV mono de 48 kHz. Avisos sonoros, o
// teste de microfone e as vozes das outras conexões ficam de fora.
//
// O relógio da gravação é o da saída de áudio: a cada bloco tocado entra o
// mesmo tanto de gravação. O que transmitimos chega por outro relógio (o do
// microfone) e espera numa fila curta até o próximo bloco.

const (
	recQueueBlocks = 512        // blocos esperando o disco; além disso são perdidos
	recMaxTx       = SampleRate // 1 s da nossa voz esperando a saída
	recHeaderEvery = 5 * SampleRate
	// Um WAV guarda o tamanho em 32 bits: ~12 h a 48 kHz mono. Perto disso a
	// gravação para de crescer em vez de corromper o arquivo.
	recMaxBytes = 0xF0000000
)

type recorder struct {
	path    string
	conn    uint32 // a conexão gravada
	started time.Time
	blocks  chan []int16
	done    chan struct{}
	samples atomic.Int64
	err     error // preenchido pela goroutine de escrita antes de fechar done
}

func wavHeader(dataBytes int64) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+dataBytes))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(dataBytes))
	return b
}

// write roda numa goroutine própria: o disco nunca atrasa o áudio.
func (r *recorder) write(f *os.File) {
	defer close(r.done)
	w := bufio.NewWriterSize(f, 1<<16)
	fail := func(err error) {
		if err != nil && r.err == nil {
			r.err = err
		}
	}
	_, err := w.Write(wavHeader(0))
	fail(err)

	var written, patched int64
	buf := make([]byte, 0, 4*FrameSize)
	for block := range r.blocks {
		if r.err != nil || written+int64(len(block))*2 > recMaxBytes {
			continue
		}
		buf = buf[:0]
		for _, s := range block {
			buf = append(buf, byte(s), byte(s>>8))
		}
		if _, err := w.Write(buf); err != nil {
			fail(err)
			continue
		}
		written += int64(len(buf))
		r.samples.Store(written / 2)
		// De tempos em tempos o cabeçalho é atualizado: se o app fechar à
		// força, o arquivo continua abrindo até esse ponto.
		if written-patched >= recHeaderEvery*2 {
			fail(w.Flush())
			_, err := f.WriteAt(wavHeader(written), 0)
			fail(err)
			patched = written
		}
	}
	fail(w.Flush())
	_, err = f.WriteAt(wavHeader(written), 0)
	fail(err)
	fail(f.Close())
}

// StartRecording começa a gravar a conversa da conexão conn no arquivo path
// (WAV), que não pode existir ainda. A nossa voz só entra enquanto a
// transmissão estiver nessa conexão (SetTxConn).
func (e *Engine) StartRecording(path string, conn uint32) error {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.rec != nil {
		return errors.New("já existe uma gravação em andamento")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("não foi possível criar o arquivo da gravação: %w", err)
	}
	r := &recorder{
		path:    path,
		conn:    conn,
		started: time.Now(),
		blocks:  make(chan []int16, recQueueBlocks),
		done:    make(chan struct{}),
	}
	e.rec, e.recTx = r, e.recTx[:0]
	e.recLimiter = newLimiter(30000, 120)
	go r.write(f)
	return nil
}

// StopRecording encerra a gravação e devolve o arquivo e a duração gravada.
// Sem gravação em andamento devolve path vazio.
func (e *Engine) StopRecording() (path string, duration time.Duration, err error) {
	e.recMu.Lock()
	r := e.rec
	e.rec = nil
	e.recMu.Unlock()
	if r == nil {
		return "", 0, nil
	}
	close(r.blocks) // ninguém mais envia: os envios são feitos com recMu e rec != nil
	<-r.done
	duration = time.Duration(r.samples.Load()) * time.Second / SampleRate
	if r.err != nil {
		return r.path, duration, fmt.Errorf("a gravação ficou incompleta: %w", r.err)
	}
	return r.path, duration, nil
}

// RecordingSince diz quando a gravação em andamento começou (zero = não está
// gravando).
func (e *Engine) RecordingSince() time.Time {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.rec == nil {
		return time.Time{}
	}
	return e.rec.started
}

// recordingConn diz se há gravação em andamento e de qual conexão.
func (e *Engine) recordingConn() (conn uint32, recording bool) {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.rec == nil {
		return 0, false
	}
	return e.rec.conn, true
}

// recordPlayback recebe a soma das vozes da conexão conn num bloco que está
// sendo tocado e grava esse bloco junto com o que transmitimos no mesmo
// período. É chamado no relógio da saída de áudio e não altera voices.
func (e *Engine) recordPlayback(voices []float64, conn uint32) {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.rec == nil || e.rec.conn != conn {
		return
	}
	buf := make([]float64, len(voices))
	copy(buf, voices)
	n := len(e.recTx)
	if n > len(buf) {
		n = len(buf)
	}
	for i := 0; i < n; i++ {
		buf[i] += e.recTx[i]
	}
	e.recTx = e.recTx[:copy(e.recTx, e.recTx[n:])]
	e.recLimiter.run(buf) // todos falando juntos: baixa no pico em vez de distorcer
	block := make([]int16, len(buf))
	toInt16(block, buf)
	select {
	case e.rec.blocks <- block:
	default: // o disco travou: este trecho se perde, o áudio ao vivo segue
	}
}

// recordTx guarda um quadro que acabamos de transmitir para a conexão conn,
// para entrar na gravação (se for a dela) no próximo bloco tocado.
func (e *Engine) recordTx(sent []float64, conn uint32) {
	e.recMu.Lock()
	defer e.recMu.Unlock()
	if e.rec == nil || e.rec.conn != conn {
		return
	}
	if over := len(e.recTx) + len(sent) - recMaxTx; over > 0 {
		// A saída parou de pedir áudio: não acumula atraso.
		if over > len(e.recTx) {
			over = len(e.recTx)
		}
		e.recTx = e.recTx[:copy(e.recTx, e.recTx[over:])]
	}
	e.recTx = append(e.recTx, sent...)
}
