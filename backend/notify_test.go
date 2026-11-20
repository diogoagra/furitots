package backend

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"furito-ts/audio"
)

// testWAV grava um tom curto em WAV (mono, 16 bits, 48 kHz).
func testWAV(t *testing.T, path string) { testTone(t, path, 0.25) }

// testTone grava um tom com a duração pedida, em segundos.
func testTone(t *testing.T, path string, seconds float64) {
	t.Helper()
	pcm := make([]int16, int(seconds*audio.SampleRate))
	for i := range pcm {
		pcm[i] = int16(9000 * math.Sin(2*math.Pi*440*float64(i)/audio.SampleRate))
	}
	b := make([]byte, 44+len(pcm)*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], audio.SampleRate)
	binary.LittleEndian.PutUint32(b[28:], audio.SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)*2))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(s))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakePack monta um pacote de sons no formato do TeamSpeak 3: uma pasta com
// settings.ini e os .wav que ele cita.
func fakePack(t *testing.T, root, id, name string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	testWAV(t, filepath.Join(dir, "connected.wav"))
	testWAV(t, filepath.Join(dir, "neutral", "joined.wav"))
	testWAV(t, filepath.Join(dir, "you_were_poked.wav"))
	ini := "[soundpack]\nname = " + name + "\n\n[soundfiles]\n" +
		"CONNECTION_CONNECTED = play(\"connected.wav\")\n" +
		"CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS = play(\"${clientType}/joined.wav\")\n" +
		"OTHER_RECEIVED_POKE = play(\"you_were_poked.wav\")\n" +
		"FORA_DA_PASTA = play(\"../../segredo.wav\")\n"
	if err := os.WriteFile(filepath.Join(dir, "settings.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "leia-me.txt"), []byte("não é som"), 0o644)
	return dir
}

// playedLevel devolve o pico do que saiu para os alto-falantes em 1 s.
func playedLevel(app *App) float64 {
	var peak float64
	buf := make([]int16, 960)
	for i := 0; i < 50; i++ {
		app.engine.ReadPlayback(buf)
		for _, s := range buf {
			peak = math.Max(peak, math.Abs(float64(s)))
		}
	}
	return peak
}

// zipOf compacta uma pasta do jeito que o soundpacks.zip vai dentro do app.
func zipOf(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := zw.AddFS(os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// playedOut lê a saída dos alto-falantes até o que estiver tocando acabar
// (0,3 s de silêncio) e devolve o pico e quanto tempo de som saiu.
func playedOut(app *App) (peak float64, dur time.Duration) {
	buf := make([]int16, 960) // 20 ms
	quiet := 0
	for i := 0; i < 1000 && quiet < 15; i++ {
		app.engine.ReadPlayback(buf)
		loud := 0.0
		for _, s := range buf {
			loud = math.Max(loud, math.Abs(float64(s)))
		}
		peak = math.Max(peak, loud)
		if loud > 0 {
			quiet = 0
			dur = time.Duration(i+1) * 20 * time.Millisecond
		} else {
			quiet++
		}
	}
	return peak, dur
}

// Os avisos vêm dos pacotes de sons que estão dentro do app: ao abrir, eles
// são gravados na pasta de dados, sem depender do TeamSpeak 3 instalado.
func TestBundledSoundPacks(t *testing.T) {
	src := t.TempDir()
	fakePack(t, filepath.Join(src, "soundpacks"), "default", "Padrão (feminino)")
	fakePack(t, filepath.Join(src, "soundpacks"), "default_male", "Padrão (masculino)")
	os.MkdirAll(filepath.Join(src, "soundpacks", "sem-settings"), 0o755)
	fakePack(t, filepath.Join(src, "fora"), "intruso", "Fora da pasta soundpacks")

	opts := testOptions(t)
	opts.SoundPacksZip = zipOf(t, src)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	st := app.API().GetState()
	if len(st.Audio.NotifyPacks) != 2 || st.Audio.NotifyPacks[0].Name != "Padrão (feminino)" || st.Audio.NotifyPack != "default" || !st.Audio.NotifySounds {
		t.Fatalf("pacotes que vêm com o app: %+v (%q)", st.Audio.NotifyPacks, st.Audio.NotifyPack)
	}
	cache := filepath.Join(filepath.Dir(opts.SettingsPath), "soundpacks")
	for _, f := range []string{"default/settings.ini", "default/connected.wav", "default/neutral/joined.wav", "default_male/you_were_poked.wav"} {
		if _, err := os.Stat(filepath.Join(cache, filepath.FromSlash(f))); err != nil {
			t.Errorf("a pasta de dados deveria ter %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cache, "default", "leia-me.txt")); err == nil {
		t.Error("só o settings.ini e os .wav deveriam ser gravados")
	}
	for _, ev := range []string{"CONNECTION_CONNECTED", "CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS", "OTHER_RECEIVED_POKE"} {
		app.notify(ev)
		if lvl := playedLevel(app); lvl < 1000 {
			t.Errorf("%s deveria tocar (nível %.0f)", ev, lvl)
		}
	}
	app.notify("FORA_DA_PASTA") // o pacote não pode apontar para fora da própria pasta
	if lvl := playedLevel(app); lvl > 10 {
		t.Errorf("som de fora da pasta do pacote não deveria tocar (nível %.0f)", lvl)
	}
	app.API().SetNotifyPack("default_male")
	playedLevel(app) // a amostra do pacote escolhido
	app.Shutdown()

	// Ao reabrir: um arquivo apagado ou estragado volta, o resto fica como
	// estava (inclusive a escolha do pacote).
	gone := filepath.Join(cache, "default_male", "connected.wav")
	broken := filepath.Join(cache, "default", "connected.wav")
	kept := filepath.Join(cache, "default", "you_were_poked.wav")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("lixo"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(kept, old, old); err != nil {
		t.Fatal(err)
	}
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	st = again.API().GetState()
	if len(st.Audio.NotifyPacks) != 2 || st.Audio.NotifyPack != "default_male" {
		t.Fatalf("ao reabrir: %+v (%q)", st.Audio.NotifyPacks, st.Audio.NotifyPack)
	}
	again.notify("CONNECTION_CONNECTED")
	if lvl := playedLevel(again); lvl < 1000 {
		t.Errorf("o som apagado deveria ter voltado (nível %.0f)", lvl)
	}
	if info, err := os.Stat(broken); err != nil || info.Size() < 1000 {
		t.Errorf("o som estragado deveria ter sido regravado: %v", err)
	}
	if info, err := os.Stat(kept); err != nil || !info.ModTime().Equal(old) {
		t.Errorf("arquivo que já estava igual não deveria ser regravado: %v", err)
	}
}

// Um zip estragado não impede o app de abrir: só não há pacotes.
func TestBrokenBundledPacks(t *testing.T) {
	opts := testOptions(t)
	opts.SoundPacksZip = []byte("isto não é um zip")
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	if n := len(app.API().GetState().Audio.NotifyPacks); n != 0 {
		t.Errorf("zip estragado não deveria render pacotes: %d", n)
	}
	app.notify("CONNECTION_CONNECTED") // sem pacote, nada toca e nada quebra
}

// mutePacks monta dois pacotes de sons ("default" e "default_male") com os
// avisos que o teste abaixo usa: um comprido (3 s) e os demais curtos.
func mutePacks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, id := range []string{"default", "default_male"} {
		dir := filepath.Join(root, "soundpacks", id)
		ini := "[soundpack]\nname = " + id + "\n\n[soundfiles]\n"
		for event, seconds := range map[string]float64{
			"CONNECTION_CONNECTED":                     0.5,
			"CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS": 0.5,
			"CLIENT_WAS_BANNED_CURRENT_CHANNEL":        3,
			"OTHER_RECEIVED_POKE":                      0.5,
			"SOUND_CAPTURE_MUTED":                      0.5,
			"SOUND_CAPTURE_UNMUTED":                    0.5,
			"SOUND_PLAYBACK_MUTED":                     1,
			"SOUND_PLAYBACK_UNMUTED":                   1,
			"STATUS_SET_AWAY":                          0.5,
			"STATUS_SET_PRESENT":                       0.5,
		} {
			file := strings.ToLower(event) + ".wav"
			testTone(t, filepath.Join(dir, file), seconds)
			ini += event + " = play(\"" + file + "\")\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.ini"), []byte(ini), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Com os alto-falantes mutados, os avisos sonoros também ficam calados; só o
// próprio aviso de "som mutado" toca. Ao desmutar, voltam.
func TestNotificationsSilentWhileDeafened(t *testing.T) {
	opts := testOptions(t)
	opts.SoundPacksZip = zipOf(t, mutePacks(t))
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	api := app.API()
	silent := func(what string) {
		t.Helper()
		if lvl := playedLevel(app); lvl != 0 {
			t.Errorf("%s: nada deveria tocar com os alto-falantes mutados (nível %.0f)", what, lvl)
		}
	}
	played := func(what string) time.Duration {
		t.Helper()
		lvl, dur := playedOut(app)
		if lvl < 500 {
			t.Errorf("%s deveria tocar (nível %.0f)", what, lvl)
		}
		return dur
	}

	app.notify("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS")
	played("aviso com os alto-falantes ligados")

	// um aviso comprido começa a tocar e o usuário muta o som no meio dele:
	// o aviso é cortado e só o "som mutado" (1 s) toca
	app.notify("CLIENT_WAS_BANNED_CURRENT_CHANNEL")
	if lvl := playedLevel(app); lvl < 500 {
		t.Fatalf("o aviso comprido deveria estar tocando (nível %.0f)", lvl)
	}
	if !api.ToggleDeafen() {
		t.Fatal("deveria ter mutado os alto-falantes")
	}
	if dur := played(`o aviso de "som mutado"`); dur > 1500*time.Millisecond {
		t.Errorf("mutar deveria cortar o aviso que estava tocando: ainda saiu %v de som", dur)
	}

	app.notify("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS")
	silent("alguém entrou no canal")
	app.notify("OTHER_RECEIVED_POKE")
	silent("cutucada")
	api.ToggleMute()
	silent("microfone mutado")
	api.SetAway(true, "")
	silent("ausente")
	api.SetNotifyPack("default_male")
	silent("amostra do pacote escolhido")

	if api.ToggleDeafen() {
		t.Fatal("deveria ter desmutado os alto-falantes")
	}
	played(`o aviso de "som retomado"`)
	app.notify("CLIENT_SWITCHED_TO_CURRENT_CHANNEL_STAYS")
	played("aviso depois de desmutar")
}

// Além dos pacotes que vêm com o app, o usuário pode importar outros: a pasta
// do pacote, a pasta "sound", a instalação do TeamSpeak 3 (Windows) ou o .app
// (macOS).
func TestImportSoundPack(t *testing.T) {
	src := t.TempDir()
	fakePack(t, filepath.Join(src, "sound"), "default", "Padrão")
	fakePack(t, filepath.Join(src, "sound"), "meu pacote!", "Meu pacote")
	fakePack(t, filepath.Join(src, "TeamSpeak 3 Client.app", "Contents", "SharedSupport", "sound"), "do_app", "Do app")

	opts := testOptions(t)
	app, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	api := app.API()
	if n := len(api.GetState().Audio.NotifyPacks); n != 0 {
		t.Fatalf("sem o zip do app não deveria haver pacotes: %d", n)
	}
	if _, err := api.ImportSoundPack(); err == nil {
		t.Error("sem seletor de pastas deveria dar erro")
	}
	if n, err := app.importSoundPacks(t.TempDir()); err == nil || n != 0 {
		t.Errorf("pasta sem pacote deveria ser recusada: %d %v", n, err)
	}

	// a pasta de um pacote
	chosen := filepath.Join(src, "sound", "meu pacote!")
	app.SetDirPicker(func(string) (string, error) { return chosen, nil })
	if n, err := api.ImportSoundPack(); err != nil || n != 1 {
		t.Fatalf("importar a pasta de um pacote: %d %v", n, err)
	}
	st := api.GetState()
	if len(st.Audio.NotifyPacks) != 1 || st.Audio.NotifyPacks[0].ID != "meu_pacote" || st.Audio.NotifyPacks[0].Name != "Meu pacote" || st.Audio.NotifyPack != "meu_pacote" || !st.Audio.NotifySounds {
		t.Errorf("depois de importar: %+v pacote=%q", st.Audio.NotifyPacks, st.Audio.NotifyPack)
	}
	if lvl := playedLevel(app); lvl < 1000 {
		t.Errorf("ao importar, uma amostra do pacote deveria tocar (nível %.0f)", lvl)
	}

	// a pasta "sound" (ou a instalação inteira, no Windows): entram todos os pacotes dela
	if n, err := app.importSoundPacks(src); err != nil || n != 2 {
		t.Errorf("importar a pasta da instalação: %d %v", n, err)
	}
	if n, err := app.importSoundPacks(filepath.Join(src, "sound")); err != nil || n != 2 {
		t.Errorf("importar a pasta sound: %d %v", n, err)
	}
	// o .app do macOS
	if n, err := app.importSoundPacks(filepath.Join(src, "TeamSpeak 3 Client.app")); err != nil || n != 1 {
		t.Errorf("importar o .app: %d %v", n, err)
	}
	ids := map[string]bool{}
	for _, p := range api.GetState().Audio.NotifyPacks {
		ids[p.ID] = true
	}
	if len(ids) != 3 || !ids["default"] || !ids["meu_pacote"] || !ids["do_app"] {
		t.Errorf("pacotes guardados depois das importações: %v", ids)
	}

	// e continuam lá ao reabrir
	app.Shutdown()
	again, err := NewApp(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Shutdown()
	if st := again.API().GetState(); len(st.Audio.NotifyPacks) != 3 || st.Audio.NotifyPack != "do_app" {
		t.Errorf("ao reabrir: %+v pacote=%q", st.Audio.NotifyPacks, st.Audio.NotifyPack)
	}
}
