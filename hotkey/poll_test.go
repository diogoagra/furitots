package hotkey

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeKeyboard é o teclado simulado dos testes: as teclas virtuais apertadas.
type fakeKeyboard struct {
	mu    sync.Mutex
	held  map[uint32]bool
	reads int
}

func (k *fakeKeyboard) down(vk uint32) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reads++
	return k.held[vk]
}

func (k *fakeKeyboard) set(down bool, vks ...uint32) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.held == nil {
		k.held = make(map[uint32]bool)
	}
	for _, vk := range vks {
		k.held[vk] = down
	}
}

func (k *fakeKeyboard) press(vks ...uint32)   { k.set(true, vks...) }
func (k *fakeKeyboard) release(vks ...uint32) { k.set(false, vks...) }

func (k *fakeKeyboard) readCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reads
}

func mustParse(t *testing.T, s string) Combo {
	t.Helper()
	c, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return c
}

// scanTest junta um scanner e um teclado simulado.
type scanTest struct {
	t  *testing.T
	s  scanner
	kb fakeKeyboard
}

func (st *scanTest) bind(action, combo string) {
	st.t.Helper()
	if _, err := st.s.set(action, mustParse(st.t, combo)); err != nil {
		st.t.Fatalf("set(%q, %q): %v", action, combo, err)
	}
}

// expect faz uma consulta e confere os eventos que ela produziu.
func (st *scanTest) expect(step string, want ...keyEvent) {
	st.t.Helper()
	if got := st.s.scan(st.kb.down); !slices.Equal(got, want) {
		st.t.Errorf("%s: eventos %v, esperado %v", step, got, want)
	}
}

func TestScanPressAndRelease(t *testing.T) {
	st := &scanTest{t: t}
	f13 := winKeys["F13"]
	st.bind("ptt", "F13")
	st.expect("nada apertado")
	st.kb.press(f13)
	st.expect("apertou", keyEvent{"ptt", true})
	st.expect("continua apertada")
	st.expect("continua apertada")
	st.kb.release(f13)
	st.expect("soltou", keyEvent{"ptt", false})
	st.expect("continua solta")
	st.kb.press(winKeys["F14"])
	st.expect("outra tecla")
	st.kb.press(f13)
	st.expect("apertou com outra tecla comum junto", keyEvent{"ptt", true})
}

func TestScanNeedsExactModifiers(t *testing.T) {
	st := &scanTest{t: t}
	space := winKeys["Space"]
	st.bind("ptt", "Ctrl+Alt+Space")
	st.expect("nada apertado")
	st.kb.press(space)
	st.expect("só a tecla")
	st.kb.press(vkControl)
	st.expect("falta o Alt")
	st.kb.press(vkAlt)
	st.expect("combinação completa", keyEvent{"ptt", true})
	st.kb.press(vkShift)
	st.expect("modificador a mais solta", keyEvent{"ptt", false})
	st.kb.release(vkShift)
	st.expect("de novo exata", keyEvent{"ptt", true})
	st.kb.release(vkControl)
	st.expect("soltou um modificador", keyEvent{"ptt", false})
	st.kb.release(vkAlt, space)
	st.expect("tudo solto")

	// Tecla sem modificadores não dispara com um modificador apertado.
	st = &scanTest{t: t}
	st.bind("toggleMute", "F13")
	st.expect("nada apertado")
	for _, mod := range []uint32{vkShift, vkControl, vkAlt, vkLWin, vkRWin} {
		st.kb.press(mod, winKeys["F13"])
		st.expect("F13 com modificador")
		st.kb.release(mod, winKeys["F13"])
		st.expect("solto")
	}
}

func TestScanCmdIsEitherWindowsKey(t *testing.T) {
	for _, win := range []uint32{vkLWin, vkRWin} {
		st := &scanTest{t: t}
		st.bind("toggleMute", "Cmd+Shift+KeyM")
		st.expect("nada apertado")
		st.kb.press(vkShift, winKeys["KeyM"])
		st.expect("sem a tecla Windows")
		st.kb.press(win)
		st.expect("com a tecla Windows", keyEvent{"toggleMute", true})
		st.kb.release(win)
		st.expect("soltou a tecla Windows", keyEvent{"toggleMute", false})
	}
}

// Ao escolher o atalho nos Ajustes a combinação ainda está apertada quando é
// registrada: isso não pode contar como um aperto.
func TestScanIgnoresComboHeldAtRegistration(t *testing.T) {
	st := &scanTest{t: t}
	f13 := winKeys["F13"]
	st.kb.press(vkControl, f13)
	st.bind("toggleMute", "Ctrl+F13")
	st.expect("apertada desde antes do registro")
	st.expect("apertada desde antes do registro")
	st.kb.release(f13)
	st.expect("soltou: nenhum aviso, não houve aperto")
	st.kb.press(f13)
	st.expect("apertou de verdade", keyEvent{"toggleMute", true})
	st.kb.release(vkControl, f13)
	st.expect("soltou", keyEvent{"toggleMute", false})
}

func TestScanSameKeyDifferentModifiers(t *testing.T) {
	st := &scanTest{t: t}
	f13 := winKeys["F13"]
	st.bind("ptt", "F13")
	st.bind("toggleMute", "Ctrl+F13")
	st.expect("nada apertado")
	st.kb.press(f13)
	st.expect("F13", keyEvent{"ptt", true})
	st.kb.press(vkControl)
	st.expect("Ctrl+F13", keyEvent{"ptt", false}, keyEvent{"toggleMute", true})
	st.kb.release(f13)
	st.expect("soltou", keyEvent{"toggleMute", false})
}

func TestScannerSetAndClear(t *testing.T) {
	st := &scanTest{t: t}
	f13, f14 := winKeys["F13"], winKeys["F14"]
	st.bind("ptt", "F13")
	st.expect("nada apertado")
	st.kb.press(f13)
	st.expect("apertou", keyEvent{"ptt", true})

	// Registrar de novo a mesma combinação não mexe no estado.
	if released, err := st.s.set("ptt", mustParse(t, "F13")); released || err != nil {
		t.Errorf("mesma combinação: released=%v err=%v", released, err)
	}
	st.expect("continua apertada")

	// Trocar a combinação com a antiga apertada: quem chama avisa o soltar.
	if released, err := st.s.set("ptt", mustParse(t, "F14")); !released || err != nil {
		t.Errorf("troca com a tecla apertada: released=%v err=%v", released, err)
	}
	st.expect("F13 não vale mais")
	st.kb.release(f13)
	st.expect("F13 não vale mais")
	st.kb.press(f14)
	st.expect("apertou a nova", keyEvent{"ptt", true})

	if !st.s.clear("ptt") {
		t.Error("limpar com a tecla apertada deveria avisar o soltar")
	}
	st.expect("sem atalhos")
	if st.s.clear("ptt") || st.s.clear("inexistente") {
		t.Error("limpar o que não existe não solta nada")
	}

	// Tecla sem equivalente no Windows: erro, e a ação fica sem atalho.
	st.bind("ptt", "F13")
	if _, err := st.s.set("ptt", Combo{Code: "TeclaInexistente"}); err == nil {
		t.Error("tecla sem tecla virtual deveria ser recusada")
	}
	if len(st.s.binds) != 0 {
		t.Errorf("sobrou atalho depois do erro: %d", len(st.s.binds))
	}
}

// ---- poller: a goroutine de consulta e a entrega dos eventos

const testTimeout = 5 * time.Second

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("esperando %s: tempo esgotado", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type pollTest struct {
	t      *testing.T
	p      *poller
	kb     fakeKeyboard
	events chan keyEvent
}

func newPollTest(t *testing.T) *pollTest {
	pt := &pollTest{t: t, events: make(chan keyEvent, 64)}
	pt.p = newPoller(pt.kb.down, func(action string, down bool) { pt.events <- keyEvent{action, down} }, time.Millisecond)
	t.Cleanup(func() {
		pt.p.mu.Lock()
		actions := make([]string, 0, len(pt.p.keys.binds))
		for _, b := range pt.p.keys.binds {
			actions = append(actions, b.action)
		}
		pt.p.mu.Unlock()
		for _, a := range actions {
			pt.p.register(a, "")
		}
	})
	return pt
}

// bind registra a combinação e espera uma consulta vê-la solta.
func (pt *pollTest) bind(action, combo string) {
	pt.t.Helper()
	if err := pt.p.register(action, combo); err != nil {
		pt.t.Fatalf("register(%q, %q): %v", action, combo, err)
	}
	pt.waitState(action, false)
}

// waitState espera a consulta enxergar a ação apertada (ou solta).
func (pt *pollTest) waitState(action string, down bool) {
	pt.t.Helper()
	waitFor(pt.t, "a consulta ver "+action, func() bool {
		pt.p.mu.Lock()
		defer pt.p.mu.Unlock()
		for _, b := range pt.p.keys.binds {
			if b.action == action {
				return b.armed && b.down == down
			}
		}
		return false
	})
}

func (pt *pollTest) want(ev keyEvent) {
	pt.t.Helper()
	select {
	case got := <-pt.events:
		if got != ev {
			pt.t.Fatalf("evento %v, esperado %v", got, ev)
		}
	case <-time.After(testTimeout):
		pt.t.Fatalf("evento %v não chegou", ev)
	}
}

func (pt *pollTest) running() bool {
	pt.p.mu.Lock()
	defer pt.p.mu.Unlock()
	return pt.p.stop != nil
}

func TestPollerDeliversAndStops(t *testing.T) {
	pt := newPollTest(t)
	f13 := winKeys["F13"]
	if pt.running() {
		t.Fatal("sem atalhos não deve haver consulta")
	}
	pt.bind("ptt", "F13")
	if !pt.running() {
		t.Fatal("com atalho a consulta deveria estar rodando")
	}
	for range 3 {
		pt.kb.press(f13)
		pt.want(keyEvent{"ptt", true})
		pt.kb.release(f13)
		pt.want(keyEvent{"ptt", false})
	}

	// Sem atalhos a consulta para na hora: nenhuma leitura do teclado depois.
	if err := pt.p.register("ptt", ""); err != nil {
		t.Fatal(err)
	}
	reads := pt.kb.readCount()
	if pt.running() {
		t.Error("sem atalhos a consulta deveria ter parado")
	}
	pt.kb.press(f13)
	time.Sleep(30 * time.Millisecond)
	if got := pt.kb.readCount(); got != reads {
		t.Errorf("o teclado foi lido %d vezes depois de parar", got-reads)
	}
	select {
	case ev := <-pt.events:
		t.Errorf("evento %v depois de parar", ev)
	default:
	}
	pt.kb.release(f13)

	// E volta a funcionar quando um atalho é definido de novo.
	pt.bind("ptt", "F13")
	pt.kb.press(f13)
	pt.want(keyEvent{"ptt", true})
}

func TestPollerReleasesWhenBindingGoesAway(t *testing.T) {
	pt := newPollTest(t)
	f13, f14 := winKeys["F13"], winKeys["F14"]

	// Limpar o atalho com a tecla apertada solta o apertar-para-falar.
	pt.bind("ptt", "F13")
	pt.kb.press(f13)
	pt.want(keyEvent{"ptt", true})
	pt.p.register("ptt", "")
	pt.want(keyEvent{"ptt", false})
	pt.kb.release(f13)

	// Trocar a combinação também.
	pt.bind("ptt", "F13")
	pt.kb.press(f13)
	pt.want(keyEvent{"ptt", true})
	pt.p.register("ptt", "F14")
	pt.want(keyEvent{"ptt", false})
	pt.kb.release(f13)
	pt.waitState("ptt", false)
	pt.kb.press(f14)
	pt.want(keyEvent{"ptt", true})

	// Combinação inválida: erro, a ação fica sem atalho e é solta.
	if err := pt.p.register("ptt", "Hyper+F1"); err == nil {
		t.Error("combinação inválida deveria dar erro")
	}
	pt.want(keyEvent{"ptt", false})
	if pt.running() {
		t.Error("sem atalhos a consulta deveria ter parado")
	}
}

// Os eventos de uma ação chegam na ordem em que aconteceram, mesmo com o
// tratamento lento, e uma ação presa não segura as outras.
func TestPollerKeepsOrderPerAction(t *testing.T) {
	var (
		kb      fakeKeyboard
		mu      sync.Mutex
		got     []bool
		gate    = make(chan struct{})
		entered = make(chan struct{}, 16)
		other   = make(chan keyEvent, 16)
	)
	p := newPoller(kb.down, func(action string, down bool) {
		if action != "ptt" {
			other <- keyEvent{action, down}
			return
		}
		entered <- struct{}{}
		<-gate // o tratamento do apertar-para-falar fica preso
		mu.Lock()
		got = append(got, down)
		mu.Unlock()
	}, time.Millisecond)
	pt := &pollTest{t: t, p: p}
	bind := func(action, combo string) {
		t.Helper()
		if err := p.register(action, combo); err != nil {
			t.Fatal(err)
		}
		pt.waitState(action, false)
	}
	bind("ptt", "F13")
	bind("toggleMute", "F14")
	defer p.register("toggleMute", "")
	defer p.register("ptt", "")

	f13, f14 := winKeys["F13"], winKeys["F14"]
	var want []bool
	for range 4 {
		kb.set(true, f13)
		pt.waitState("ptt", true)
		kb.set(false, f13)
		pt.waitState("ptt", false)
		want = append(want, true, false)
	}
	<-entered // só o primeiro evento está sendo tratado; os outros esperam

	kb.press(f14)
	select {
	case ev := <-other:
		if ev != (keyEvent{"toggleMute", true}) {
			t.Fatalf("evento %v", ev)
		}
	case <-time.After(testTimeout):
		t.Fatal("a outra ação ficou presa atrás do apertar-para-falar")
	}
	select {
	case <-entered:
		t.Fatal("dois eventos da mesma ação tratados ao mesmo tempo")
	default:
	}

	close(gate)
	waitFor(t, "a entrega dos eventos", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == len(want)
	})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("ordem dos eventos %v, esperado %v", got, want)
	}
}

// Uso por várias goroutines ao mesmo tempo, com o tratamento mexendo nos
// próprios atalhos. Vale principalmente com -race; confere também que, por
// ação, apertar e soltar chegam sempre alternados e terminam em "solta".
func TestPollerConcurrentUse(t *testing.T) {
	var (
		kb   fakeKeyboard
		p    *poller
		mu   sync.Mutex
		seq  = make(map[string][]bool)
		work sync.WaitGroup
		done = make(chan struct{})
	)
	p = newPoller(kb.down, func(action string, down bool) {
		mu.Lock()
		seq[action] = append(seq[action], down)
		mu.Unlock()
		if action == "toggleDeafen" { // chamar de dentro do tratamento não pode travar
			if down {
				p.register("extra", "Ctrl+F15")
			} else {
				p.register("extra", "")
			}
		}
	}, 200*time.Microsecond)
	if err := p.register("toggleDeafen", "F16"); err != nil {
		t.Fatal(err)
	}
	loop := func(pause time.Duration, step func(i int)) {
		work.Add(1)
		go func() {
			defer work.Done()
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
				}
				step(i)
				time.Sleep(pause)
			}
		}()
	}

	// Um "usuário" apertando e soltando teclas e modificadores sem parar...
	keys := []uint32{winKeys["F13"], winKeys["F16"], vkControl, winKeys["F14"], winKeys["F16"], vkShift, winKeys["F15"]}
	held := make(map[uint32]bool)
	loop(300*time.Microsecond, func(i int) {
		vk := keys[i%len(keys)]
		held[vk] = !held[vk]
		kb.set(held[vk], vk)
	})
	// ...enquanto os atalhos são trocados e limpos por várias goroutines.
	combos := []string{"F13", "Ctrl+F13", "F14", "", "Shift+F14", "F13"}
	for g := range 4 {
		action := []string{"ptt", "toggleMute"}[g%2]
		loop(time.Millisecond, func(i int) { p.register(action, combos[(i+g)%len(combos)]) })
	}

	time.Sleep(250 * time.Millisecond)
	close(done)
	work.Wait()

	idle := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.pending) == 0
	}
	for _, action := range []string{"ptt", "toggleMute", "toggleDeafen"} {
		p.register(action, "")
	}
	waitFor(t, "a entrega dos eventos", idle)
	p.register("extra", "") // o tratamento de toggleDeafen pode tê-la registrado
	waitFor(t, "a entrega dos eventos", idle)

	p.mu.Lock()
	running, left := p.stop != nil, len(p.keys.binds)
	p.mu.Unlock()
	if running || left != 0 {
		t.Errorf("depois de limpar tudo: consulta rodando=%v, atalhos=%d", running, left)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seq["toggleDeafen"]) == 0 {
		t.Error("nenhum evento entregue durante o teste")
	}
	for action, events := range seq {
		if len(events)%2 != 0 {
			t.Errorf("%s: terminou apertada (%d eventos)", action, len(events))
		}
		for i, down := range events {
			if down != (i%2 == 0) {
				t.Errorf("%s: evento %d fora de ordem (apertar=%v)", action, i, down)
				break
			}
		}
	}
}
