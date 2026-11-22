package audio

import (
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
)

// Várias conexões ao mesmo tempo (uma por aba): o número do cliente se repete
// entre servidores, cada conexão entrega a voz pela sua goroutine de rede, e só
// uma delas recebe o que transmitimos.

// toneAmp mede a amplitude da componente de frequência freq em x.
func toneAmp(x []int16, freq float64) float64 {
	var re, im float64
	for i, v := range x {
		a := 2 * math.Pi * freq * float64(i) / SampleRate
		re += float64(v) * math.Cos(a)
		im += float64(v) * math.Sin(a)
	}
	return 2 * math.Hypot(re, im) / float64(len(x))
}

// twoServers alimenta o motor com a mesma pessoa (cliente 7) em duas conexões:
// na 1 ela é um tom de 1 kHz, na 2 um de 2,5 kHz. play toca n quadros e devolve
// a amplitude de cada tom na saída, sem contar os skip primeiros quadros.
type twoServers struct {
	e         *Engine
	low, high [][]byte
	seq       int
}

func newTwoServers(t *testing.T, frames int) *twoServers {
	t.Helper()
	e, _ := newTestEngine(t)
	e.SetNormalizeVoices(false)
	return &twoServers{e: e, low: encodeTone(t, frames, 1000, 5000), high: encodeTone(t, frames, 2500, 5000)}
}

func (s *twoServers) push() {
	s.e.PushVoice(1, 7, uint16(s.seq), s.low[s.seq])
	s.e.PushVoice(2, 7, uint16(s.seq), s.high[s.seq])
	s.seq++
}

func (s *twoServers) play(n, skip int) (low, high float64) {
	var out []int16
	block := make([]int16, FrameSize)
	for f := 0; f < n; f++ {
		s.push()
		s.e.fillPlayback(block)
		if f >= skip {
			out = append(out, block...)
		}
	}
	return toneAmp(out, 1000), toneAmp(out, 2500)
}

// O mesmo número de cliente em duas conexões são duas pessoas: cada uma tem o
// seu fluxo, as duas tocam, e o "mutar só para mim" de uma não cala a outra.
func TestSameClientIDOnTwoConnections(t *testing.T) {
	s := newTwoServers(t, 80)
	e := s.e
	low, high := s.play(40, 10)
	if low < 3500 || high < 3500 {
		t.Errorf("as duas vozes deveriam tocar: 1 kHz %.0f, 2,5 kHz %.0f", low, high)
	}
	e.rxMu.Lock()
	streams := len(e.streams)
	e.rxMu.Unlock()
	if streams != 2 {
		t.Errorf("%d fluxos, esperado 2 (um por conexão)", streams)
	}
	for conn := uint32(1); conn <= 2; conn++ {
		if ids := e.TalkingClients(conn); len(ids) != 1 || ids[0] != 7 {
			t.Errorf("conexão %d: falando %v, esperado [7]", conn, ids)
		}
	}
	if ids := e.TalkingClients(3); len(ids) != 0 {
		t.Errorf("conexão sem ninguém: falando %v", ids)
	}
	if conns := e.TalkingConns(); !slices.Equal(conns, []uint32{1, 2}) {
		t.Errorf("conexões com gente falando: %v, esperado [1 2]", conns)
	}

	e.SetClientMuted(1, 7, true) // o cliente 7 da conexão 1, não o da 2
	low, high = s.play(40, 10)
	if low > 100 || high < 3500 {
		t.Errorf("mutado só o 7 da conexão 1: 1 kHz %.0f (esperado 0), 2,5 kHz %.0f", low, high)
	}
}

// Cada conexão entrega a voz pela sua goroutine de rede, ao mesmo tempo que a
// placa de som pede áudio e a interface pergunta quem está falando. Com gente
// bastante para o limite de vozes e a limpeza dos fluxos parados entrarem em
// ação. O que este teste confere é a ausência de corrida (go test -race).
func TestPushVoiceFromTwoConnectionsAtOnce(t *testing.T) {
	e, _ := newTestEngine(t)
	frames := encodeFrames(t, 3, false)
	var net sync.WaitGroup
	for conn := uint32(1); conn <= 2; conn++ {
		net.Add(1)
		go func(conn uint32) {
			defer net.Done()
			for round := 0; round < 4; round++ {
				for id := uint16(1); id <= 100; id++ {
					for i, f := range frames {
						e.PushVoice(conn, id, uint16(round*len(frames)+i), f)
					}
				}
				e.PushVoice(conn, 1, 0, nil) // fim de uma fala
			}
		}(conn)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		out := make([]int16, FrameSize)
		for {
			select {
			case <-stop:
				return
			default:
			}
			e.fillPlayback(out)
			e.TalkingClients(1)
			e.TalkingConns()
			e.SetClientGain(2, 5, 0.5)
			runtime.Gosched()
		}
	}()
	net.Wait()
	close(stop)
	<-done

	e.rxMu.Lock()
	n := len(e.streams)
	e.rxMu.Unlock()
	if n == 0 || n > maxStreams {
		t.Errorf("%d vozes acompanhadas, esperado entre 1 e %d", n, maxStreams)
	}
}

// "Silenciar esta aba": as vozes de uma conexão param de tocar e somem de quem
// está falando; as outras conexões seguem, e o volume por pessoa não se perde.
func TestConnMutedSilencesOnlyThatConnection(t *testing.T) {
	s := newTwoServers(t, 90)
	e := s.e
	e.SetClientGain(1, 7, 0.5)
	low, high := s.play(30, 10)
	if low < 1750 || low > 3250 || high < 3500 {
		t.Fatalf("antes de silenciar: 1 kHz %.0f (metade do volume), 2,5 kHz %.0f", low, high)
	}

	e.SetConnMuted(1, true)
	if !e.ConnMuted(1) || e.ConnMuted(2) {
		t.Errorf("silenciada: conexão 1 = %v, conexão 2 = %v", e.ConnMuted(1), e.ConnMuted(2))
	}
	if ids := e.TalkingClients(1); len(ids) != 0 {
		t.Errorf("o áudio pendente da conexão silenciada deveria sumir na hora (falando: %v)", ids)
	}
	low, high = s.play(30, 0)
	if low > 100 || high < 3500 {
		t.Errorf("conexão 1 silenciada: 1 kHz %.0f (esperado 0), 2,5 kHz %.0f", low, high)
	}
	if conns := e.TalkingConns(); !slices.Equal(conns, []uint32{2}) {
		t.Errorf("conexões com gente falando: %v, esperado [2]", conns)
	}

	e.SetConnMuted(1, false)
	low, high = s.play(30, 10)
	if low < 1750 || low > 3250 || high < 3500 {
		t.Errorf("de volta: 1 kHz %.0f (o volume escolhido, a metade, deveria ter ficado), 2,5 kHz %.0f", low, high)
	}
}

// Uma conexão que cai leva embora só o que é dela: o áudio pendente e os
// ajustes por pessoa (os números de cliente dela deixam de valer).
func TestResetStreamsOnlyThatConnection(t *testing.T) {
	s := newTwoServers(t, 10)
	e := s.e
	e.SetClientGain(1, 7, 0.5)
	e.SetClientMuted(1, 8, true)
	e.SetClientGain(2, 7, 0.5)
	e.SetClientMuted(2, 8, true)
	for f := 0; f < 10; f++ {
		s.push() // fica tudo na fila, nada tocou ainda
	}

	e.ResetStreams(1)
	e.rxMu.Lock()
	_, gone := e.streams[voiceKey{1, 7}]
	kept := e.streams[voiceKey{2, 7}]
	pending := 0
	if kept != nil {
		pending = len(kept.pcm)
	}
	_, gain1 := e.clientGain[voiceKey{1, 7}]
	muted1 := e.clientMuted[voiceKey{1, 8}]
	gain2, muted2 := e.clientGain[voiceKey{2, 7}], e.clientMuted[voiceKey{2, 8}]
	e.rxMu.Unlock()
	if gone || gain1 || muted1 {
		t.Errorf("conexão 1 depois de ResetStreams: fluxo=%v volume=%v mudo=%v, esperado tudo esquecido", gone, gain1, muted1)
	}
	if pending < 9*FrameSize || gain2 != 0.5 || !muted2 {
		t.Errorf("conexão 2 não deveria mudar: %d amostras na fila, volume %.1f, mudo %v", pending, gain2, muted2)
	}

	var out []int16
	block := make([]int16, FrameSize)
	for f := 0; f < 10; f++ {
		e.fillPlayback(block)
		if f >= 2 {
			out = append(out, block...)
		}
	}
	if low, high := toneAmp(out, 1000), toneAmp(out, 2500); low > 100 || high < 1750 || high > 3250 {
		t.Errorf("só a conexão 2 deveria tocar, na metade do volume: 1 kHz %.0f, 2,5 kHz %.0f", low, high)
	}
}

// A gravação é de uma conexão: entram as vozes dela e a nossa enquanto
// falamos nela. As vozes das outras conexões não entram, nem a nossa depois
// que passamos a falar em outra.
func TestRecordingOnlyThatConnection(t *testing.T) {
	s := newTwoServers(t, 200)
	e := s.e
	e.SetVADMode(VADLevel)
	e.SetVADThreshold(0)
	e.SetAutoGain(false)
	e.SetNoiseMode(NoiseOff)
	path := filepath.Join(t.TempDir(), "conversa.wav")
	e.SetTxConn(1, true)
	if err := e.StartRecording(path, 1); err != nil {
		t.Fatal(err)
	}
	var micPhase float64
	play := make([]int16, FrameSize)
	for f := 0; f < 200; f++ {
		if f == 100 {
			e.SetTxConn(2, true) // a partir daqui falamos na outra conexão
		}
		s.push()
		e.txFrame(sine(FrameSize, 300, 5000, &micPhase)) // nós, em 300 Hz
		e.fillPlayback(play)
	}
	if _, _, err := e.StopRecording(); err != nil {
		t.Fatal(err)
	}
	pcm, err := DecodeAudioFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 4*SampleRate {
		t.Fatalf("arquivo com %d amostras, esperado %d", len(pcm), 4*SampleRate)
	}
	part := func(fromMs, toMs int) (theirs, others, ours float64) {
		x := pcm[fromMs*SampleRate/1000 : toMs*SampleRate/1000]
		return toneAmp(x, 1000), toneAmp(x, 2500), toneAmp(x, 300)
	}
	theirs, others, ours := part(300, 1700)
	t.Logf("falando na conexão gravada: a pessoa dela %.0f, a da outra conexão %.0f, nós %.0f", theirs, others, ours)
	if theirs < 3500 || ours < 3500 || others > 100 {
		t.Errorf("deveriam entrar a voz da conexão gravada e a nossa, e só elas (%.0f, %.0f; a outra conexão %.0f)", theirs, ours, others)
	}
	theirs, others, ours = part(2300, 3700)
	t.Logf("falando na outra conexão: a pessoa da gravada %.0f, a da outra %.0f, nós %.0f", theirs, others, ours)
	if theirs < 3500 || ours > 100 || others > 100 {
		t.Errorf("falando em outra conexão, só a voz da conexão gravada deveria entrar (%.0f; nós %.0f, a outra conexão %.0f)", theirs, ours, others)
	}
}

// Sem conexão para onde transmitir (nenhuma aba com o microfone), nada é
// entregue — nem voz, nem soundboard — mas o medidor e o teste de microfone
// continuam. Ao trocar de conexão no meio da fala, o fim da fala é entregue ao
// destino antigo antes da troca.
func TestTxOffDeliversNothing(t *testing.T) {
	e, c := newTestEngine(t)
	e.SetVADMode(VADLevel)
	e.SetVADThreshold(0)
	e.SetAutoGain(false)
	e.SetNoiseMode(NoiseOff)
	e.SetMicTest(true)
	voice := &voiceSynth{f0: 140, amp: 5000}
	play := make([]int16, FrameSize)
	var level, prob float64 // o máximo que o medidor mostrou
	speak := func(n int) (heard int) {
		for i := 0; i < n; i++ {
			e.txFrame(voice.nextPCM(FrameSize))
			e.fillPlayback(play)
			if rms(play) > 200 {
				heard++
			}
			level, prob = math.Max(level, e.MicLevel()), math.Max(prob, e.VoiceProbability())
		}
		return heard
	}

	e.SetTxConn(0, false)
	heard := speak(50)
	if f, ends := c.get(); f != 0 || ends != 0 {
		t.Errorf("sem destino nada deveria ser entregue: %d quadros, %d fins de fala", f, ends)
	}
	if e.Transmitting() || e.Muted() {
		t.Errorf("sem destino: transmitindo=%v (esperado não), mutado=%v (esperado não)", e.Transmitting(), e.Muted())
	}
	if level < 1000 || prob < 0.5 {
		t.Errorf("o medidor deveria continuar: nível %.0f, voz %.2f", level, prob)
	}
	if heard < 40 {
		t.Errorf("o teste de microfone deveria continuar: a voz voltou em %d de 50 quadros", heard)
	}
	var sbPhase float64
	e.Soundboard().play(sine(SampleRate/5, 330, 9000, &sbPhase))
	speak(20)
	if f, ends := c.get(); f != 0 || ends != 0 {
		t.Errorf("o soundboard também não deveria ser entregue: %d quadros, %d fins de fala", f, ends)
	}

	e.SetTxConn(0, true)
	speak(20)
	sent, ends := c.get()
	if sent == 0 || ends != 0 || !e.Transmitting() {
		t.Errorf("com destino: %d quadros, %d fins de fala, transmitindo=%v", sent, ends, e.Transmitting())
	}

	e.SetTxConn(3, true) // troca no meio da fala
	if _, ends := c.get(); ends != 1 || e.Transmitting() {
		t.Errorf("a troca deveria encerrar a fala no destino antigo: %d fins de fala, transmitindo=%v", ends, e.Transmitting())
	}
	speak(20)
	moved, _ := c.get()
	if moved <= sent || !e.Transmitting() {
		t.Errorf("depois da troca a fala deveria seguir para o destino novo (%d quadros, eram %d)", moved, sent)
	}

	e.SetTxConn(3, false)
	speak(20)
	if f, ends := c.get(); f != moved || ends != 2 {
		t.Errorf("destino retirado: %d quadros (eram %d), %d fins de fala (esperado 2)", f, moved, ends)
	}
}
