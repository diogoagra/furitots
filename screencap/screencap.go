// Package screencap captura a tela e entrega os quadros em JPEG.
//
// No macOS usa o ScreenCaptureKit (precisa da permissão "Gravação de Tela").
// Nos outros sistemas não há captura nativa: lá a interface usa a captura do
// próprio navegador embutido (getDisplayMedia). Há também um padrão de teste,
// que gera quadros sem tocar na tela, para testar o caminho inteiro.
package screencap

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"sync"
	"time"
)

// Frame é um quadro capturado.
type Frame struct {
	Seq    uint64 // cresce a cada quadro; começa em 1
	JPEG   []byte
	Width  int
	Height int
}

// Display é uma tela que pode ser capturada.
type Display struct {
	ID     uint32 `json:"id"`
	Name   string `json:"name"`
	Width  int    `json:"width"` // em pixels
	Height int    `json:"height"`
	Main   bool   `json:"main"`
}

// ErrPermission indica que o sistema não deixou capturar a tela.
var ErrPermission = errors.New("sem permissão para gravar a tela: libere o Furito TS em Ajustes do Sistema → Privacidade e Segurança → Gravação de Tela e Áudio do Sistema, e abra o app de novo")

// ErrUnavailable indica que este sistema não tem captura nativa.
var ErrUnavailable = errors.New("captura de tela nativa indisponível neste sistema")

type store struct {
	mu      sync.Mutex
	cond    *sync.Cond
	frame   Frame
	running bool
	stop    func() // encerra a fonte atual
	gen     uint64 // muda a cada Start/Stop: quadros de uma captura antiga são descartados
}

var st = func() *store {
	s := &store{}
	s.cond = sync.NewCond(&s.mu)
	return s
}()

// begin registra uma captura nova e devolve a geração dela.
func (s *store) begin(stop func()) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.running = true
	s.stop = stop
	s.frame.JPEG = nil // a numeração continua: quem pede "depois de N" não vê quadro velho
	return s.gen
}

// push guarda um quadro da captura de geração gen.
func (s *store) push(gen uint64, jpg []byte, w, h int) {
	s.mu.Lock()
	if gen == s.gen && s.running {
		s.frame = Frame{Seq: s.frame.Seq + 1, JPEG: jpg, Width: w, Height: h}
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

// end marca o fim da captura de geração gen (encerrada pelo sistema).
func (s *store) end(gen uint64) {
	s.mu.Lock()
	if gen == s.gen {
		s.running = false
		s.stop = nil
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

// Stop encerra a captura em andamento, se houver.
func Stop() {
	st.mu.Lock()
	stop := st.stop
	st.stop = nil
	st.running = false
	st.gen++
	st.cond.Broadcast()
	st.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Running indica se há uma captura em andamento.
func Running() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.running
}

// Next devolve o quadro mais recente com Seq maior que after, esperando até
// wait por um novo. ok é false se nenhum chegou a tempo ou não há captura.
func Next(after uint64, wait time.Duration) (Frame, bool) {
	deadline := time.Now().Add(wait)
	timer := time.AfterFunc(wait, func() {
		st.mu.Lock()
		st.cond.Broadcast()
		st.mu.Unlock()
	})
	defer timer.Stop()

	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		if st.frame.JPEG != nil && st.frame.Seq > after {
			return st.frame, true
		}
		if !st.running || !time.Now().Before(deadline) {
			return Frame{}, false
		}
		st.cond.Wait()
	}
}

// TestColors são as cores de fundo do padrão de teste, na ordem em que se
// alternam (uma por segundo).
var TestColors = []color.RGBA{
	{200, 40, 40, 255}, {40, 160, 60, 255}, {40, 80, 200, 255}, {210, 170, 30, 255},
}

// StartTestPattern começa uma "captura" de mentira: quadros coloridos com uma
// barra que anda, gerados aqui mesmo. Serve para testar o caminho inteiro
// (captura → interface → vídeo → outro cliente) sem permissão de tela.
func StartTestPattern(width, height, fps int) error {
	if width < 16 || height < 16 || width > 3840 || height > 2160 {
		return errors.New("tamanho inválido para o padrão de teste")
	}
	if fps < 1 || fps > 60 {
		fps = 15
	}
	Stop()
	done := make(chan struct{})
	gen := st.begin(func() { close(done) })
	go func() {
		tick := time.NewTicker(time.Second / time.Duration(fps))
		defer tick.Stop()
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		start := time.Now()
		for n := 0; ; n++ {
			bg := TestColors[int(time.Since(start)/time.Second)%len(TestColors)]
			draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
			// barra branca que atravessa a tela a cada 2 s
			x := int(time.Since(start)/time.Millisecond) % 2000 * width / 2000
			bar := image.Rect(x, height/3, x+width/20, height*2/3).Intersect(img.Bounds())
			draw.Draw(img, bar, &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err == nil {
				st.push(gen, buf.Bytes(), width, height)
			}
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	return nil
}

// fit devolve o maior tamanho que cabe em maxW×maxH mantendo a proporção de
// w×h, sem ampliar, com lados pares (os codificadores de vídeo exigem).
func fit(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if maxW <= 0 || maxW > w {
		maxW = w
	}
	if maxH <= 0 || maxH > h {
		maxH = h
	}
	ow, oh := maxW, h*maxW/w
	if oh > maxH {
		ow, oh = w*maxH/h, maxH
	}
	ow, oh = ow&^1, oh&^1
	if ow < 2 {
		ow = 2
	}
	if oh < 2 {
		oh = 2
	}
	return ow, oh
}
