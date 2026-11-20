package backend

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"furito-ts/audio"
)

// noticeEvents lê do código-fonte todo aviso que o app pode tocar: os nomes
// passados a sound(…) no protocolo e a notify(…) aqui no backend.
func noticeEvents(t *testing.T) []string {
	t.Helper()
	call := regexp.MustCompile(`\b(?:sound|notify)\("([A-Z_]+)"\)`)
	seen := map[string]bool{}
	for _, dir := range []string{".", filepath.Join("..", "ts3")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range call.FindAllSubmatch(src, -1) {
				seen[string(m[1])] = true
			}
		}
	}
	var events []string
	for ev := range seen {
		events = append(events, ev)
	}
	sort.Strings(events)
	if len(events) < 30 {
		t.Fatalf("só %d avisos encontrados no código: a busca deixou de funcionar", len(events))
	}
	return events
}

// O pacote do app vem selecionado numa instalação nova, tem som para todo
// aviso que o app pode tocar, e eles tocam.
func TestBuiltinSoundPack(t *testing.T) {
	opts := testOptions(t)
	opts.SoundPacksZip = BuiltinSoundPacks()
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	st := app.API().GetState()
	if len(st.Audio.NotifyPacks) != 1 || st.Audio.NotifyPacks[0].ID != builtinPackID || st.Audio.NotifyPacks[0].Name != builtinPackName ||
		st.Audio.NotifyPack != builtinPackID || !st.Audio.NotifySounds {
		t.Fatalf("pacotes: %+v, em uso %q, ligados %v", st.Audio.NotifyPacks, st.Audio.NotifyPack, st.Audio.NotifySounds)
	}
	events := noticeEvents(t)
	for _, ev := range events {
		if app.packs[0].events[ev] == "" {
			t.Errorf("o pacote do app não tem som para %s", ev)
		}
	}
	t.Logf("%d avisos no código, todos com som", len(events))
	for _, ev := range []string{"CONNECTION_CONNECTED", "CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS", "SOUND_CAPTURE_MUTED", "OTHER_RECEIVED_POKE"} {
		app.notify(ev)
		if lvl, _ := playedOut(app); lvl < 500 {
			t.Errorf("%s não tocou (nível %.0f)", ev, lvl)
		}
	}
	// E nenhum evento do pacote sobra sem uso: um nome digitado errado não toca nunca.
	known := map[string]bool{}
	for _, ev := range events {
		known[ev] = true
	}
	for i := range builtinChimes {
		for _, ev := range builtinChimes[i].events {
			if !known[ev] {
				t.Errorf("o aviso %q está ligado a %s, que o app nunca toca", builtinChimes[i].file, ev)
			}
		}
	}
}

// Cada som do pacote: abre com o leitor de WAV do app, tem duração e volume
// de aviso, começa e termina em silêncio e não tem estalo no meio.
func TestBuiltinChimesAreClean(t *testing.T) {
	dir := t.TempDir()
	names := map[string]bool{}
	for i := range builtinChimes {
		c := &builtinChimes[i]
		if names[c.file] {
			t.Errorf("dois avisos com o arquivo %q", c.file)
		}
		names[c.file] = true
		path := filepath.Join(dir, c.file+".wav")
		if err := os.WriteFile(path, wavBytes(c.render()), 0o600); err != nil {
			t.Fatal(err)
		}
		pcm, err := audio.DecodeAudioFile(path)
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		var peak, jump, sum float64
		for i, s := range pcm {
			peak = math.Max(peak, math.Abs(float64(s)))
			sum += float64(s) * float64(s)
			if i > 0 {
				jump = math.Max(jump, math.Abs(float64(s)-float64(pcm[i-1])))
			}
		}
		ms := len(pcm) * 1000 / audio.SampleRate
		t.Logf("%-17s %4d ms, pico %5.0f, nível médio %4.0f, maior salto entre amostras %4.0f", c.file, ms, peak, math.Sqrt(sum/float64(len(pcm))), jump)
		if ms < 200 || ms > 1000 {
			t.Errorf("%s: duração de %d ms", c.file, ms)
		}
		if math.Abs(peak-chimePeak) > 2 {
			t.Errorf("%s: pico de %.0f, esperado %.0f", c.file, peak, chimePeak)
		}
		if first, last := math.Abs(float64(pcm[0])), math.Abs(float64(pcm[len(pcm)-1])); first > 30 || last > 30 {
			t.Errorf("%s: não começa ou não termina em silêncio (%.0f … %.0f)", c.file, first, last)
		}
		// a nota mais aguda (1175 Hz) com os harmônicos sobe no máximo ~2600 por amostra
		if jump > 3500 {
			t.Errorf("%s: estalo (salto de %.0f entre duas amostras)", c.file, jump)
		}
	}
}

// O aviso de "conectado" soa por mais de 300 ms: é o que o motor de áudio
// precisa para aprender, logo ao conectar, se o microfone ouve os avisos (com
// caixas de som) ou não (com fone de ouvido).
func TestConnectedChimeIsLongEnoughToLearnFrom(t *testing.T) {
	for i := range builtinChimes {
		c := &builtinChimes[i]
		if c.file != "conectado" {
			continue
		}
		pcm := c.render()
		audible := 0
		for s := 0; s+audio.FrameSize <= len(pcm); s += audio.FrameSize {
			var sum float64
			for _, v := range pcm[s : s+audio.FrameSize] {
				sum += float64(v) * float64(v)
			}
			if math.Sqrt(sum/audio.FrameSize) > 100 {
				audible++
			}
		}
		t.Logf("%d quadros de 20 ms com som", audible)
		if audible < 15 {
			t.Errorf("só %d quadros com som: curto demais para o motor concluir alguma coisa", audible)
		}
		return
	}
	t.Fatal("não há aviso de conectado")
}

// O pacote sai idêntico a cada vez, e um som refeito substitui o que estava
// na pasta de dados mesmo tendo a mesma duração.
func TestBuiltinSoundPackIsStableAndUpdates(t *testing.T) {
	a, b := BuiltinSoundPacks(), BuiltinSoundPacks()
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Fatalf("o pacote deveria sair igual a cada chamada (%d e %d bytes)", len(a), len(b))
	}
	opts := testOptions(t)
	opts.SoundPacksZip = a
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	app.Shutdown()
	file := filepath.Join(filepath.Dir(opts.SettingsPath), "soundpacks", builtinPackID, "conectado.wav")
	good, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	old := append([]byte(nil), good...)
	for i := 44; i < len(old); i++ { // mesmo tamanho, outro som
		old[i] = 0
	}
	if err := os.WriteFile(file, old, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err = NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	app.Shutdown()
	if now, _ := os.ReadFile(file); !bytes.Equal(now, good) {
		t.Error("o som antigo, do mesmo tamanho, não foi substituído pelo do app")
	}
}
