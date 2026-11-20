package hotkey

import (
	"fmt"
	"sync"
	"time"
)

// Atalhos por consulta: em vez de pedir ao sistema que avise, olha-se o estado
// das teclas em intervalos curtos. É o que o Windows usa (ver
// hotkey_windows.go). Este arquivo não tem tag de plataforma nem chamada de
// sistema, para ser testado em qualquer sistema com um teclado simulado.

// keyState diz se uma tecla virtual está apertada neste instante.
type keyState func(vk uint32) bool

// keyEvent é uma mudança detectada: a ação e se foi apertada ou solta.
type keyEvent struct {
	action string
	down   bool
}

type binding struct {
	action string
	combo  Combo
	vk     uint32
	armed  bool // já foi vista solta desde o registro
	down   bool // último estado avisado
}

// scanner guarda as combinações acompanhadas e transforma o estado consultado
// das teclas em eventos de apertar e soltar. Não tem trava: quem usa cuida.
type scanner struct {
	binds []*binding
}

// set passa a acompanhar a combinação da ação, no lugar da anterior. Devolve
// released = true se a anterior estava apertada: como ela deixa de ser
// acompanhada, quem chama precisa avisar que foi solta.
func (s *scanner) set(action string, c Combo) (released bool, err error) {
	vk, ok := winKeys[c.Code]
	if !ok {
		return s.clear(action), fmt.Errorf("tecla não suportada em atalhos: %q", c.Code)
	}
	for _, b := range s.binds {
		if b.action != action {
			continue
		}
		if b.combo == c {
			return false, nil // a mesma de antes: nada muda
		}
		released = b.down
		*b = binding{action: action, combo: c, vk: vk}
		return released, nil
	}
	s.binds = append(s.binds, &binding{action: action, combo: c, vk: vk})
	return false, nil
}

// clear deixa de acompanhar a ação. Devolve true se ela estava apertada.
func (s *scanner) clear(action string) (released bool) {
	for i, b := range s.binds {
		if b.action == action {
			s.binds = append(s.binds[:i], s.binds[i+1:]...)
			return b.down
		}
	}
	return false
}

// scan consulta as teclas e devolve o que mudou desde a última consulta. Uma
// combinação está apertada quando a tecla principal e exatamente os
// modificadores pedidos estão apertados.
func (s *scanner) scan(down keyState) []keyEvent {
	if len(s.binds) == 0 {
		return nil
	}
	ctrl, alt, shift := down(vkControl), down(vkAlt), down(vkShift)
	cmd := down(vkLWin) || down(vkRWin)
	var events []keyEvent
	for _, b := range s.binds {
		c := b.combo
		pressed := c.Ctrl == ctrl && c.Alt == alt && c.Shift == shift && c.Cmd == cmd && down(b.vk)
		if !b.armed {
			// Registrada com a combinação já apertada (acabou de ser
			// escolhida nos Ajustes, por exemplo): só vale depois de solta.
			b.armed = !pressed
			continue
		}
		if pressed != b.down {
			b.down = pressed
			events = append(events, keyEvent{b.action, pressed})
		}
	}
	return events
}

// poller consulta o teclado numa goroutine enquanto houver combinação
// acompanhada e entrega os eventos. Pode ser usado por várias goroutines.
type poller struct {
	down     keyState
	emit     func(action string, down bool)
	interval time.Duration

	mu      sync.Mutex
	keys    scanner
	stop    chan struct{}     // encerra a goroutine de consulta; nil se parada
	pending map[string][]bool // ação → eventos esperando a entrega
}

func newPoller(down keyState, emit func(action string, down bool), interval time.Duration) *poller {
	return &poller{down: down, emit: emit, interval: interval, pending: make(map[string][]bool)}
}

// register define (ou limpa, com "") a combinação acompanhada de uma ação.
func (p *poller) register(action, combo string) error {
	var (
		c        Combo
		released bool
		err      error
	)
	if combo != "" {
		c, err = Parse(combo)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if combo == "" || err != nil {
		released = p.keys.clear(action)
	} else {
		released, err = p.keys.set(action, c)
	}
	if released {
		// O atalho deixou de existir com a tecla apertada: sem este aviso o
		// apertar-para-falar ficaria ligado.
		p.sendLocked(keyEvent{action, false})
	}
	switch {
	case len(p.keys.binds) > 0 && p.stop == nil:
		p.stop = make(chan struct{})
		go p.run(p.stop)
	case len(p.keys.binds) == 0 && p.stop != nil:
		close(p.stop)
		p.stop = nil
	}
	return err
}

func (p *poller) run(stop chan struct{}) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		if p.stop != stop { // parou enquanto esta goroutine esperava
			p.mu.Unlock()
			return
		}
		for _, ev := range p.keys.scan(p.down) {
			p.sendLocked(ev)
		}
		p.mu.Unlock()
	}
}

// sendLocked põe o evento na fila da ação. A entrega acontece fora das travas
// e da goroutine de consulta (o tratamento pode chamar o servidor ou mexer nos
// próprios atalhos), numa goroutine por ação: uma ação lenta não segura as
// outras, e o soltar nunca chega antes do apertar da mesma ação.
func (p *poller) sendLocked(ev keyEvent) {
	queue, delivering := p.pending[ev.action]
	p.pending[ev.action] = append(queue, ev.down)
	if !delivering {
		go p.deliver(ev.action)
	}
}

func (p *poller) deliver(action string) {
	for {
		p.mu.Lock()
		queue := p.pending[action]
		if len(queue) == 0 {
			delete(p.pending, action)
			p.mu.Unlock()
			return
		}
		down := queue[0]
		p.pending[action] = queue[1:]
		p.mu.Unlock()
		p.emit(action, down)
	}
}
