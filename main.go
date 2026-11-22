package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"furito-ts/backend"
)

//go:embed all:frontend
var frontendFiles embed.FS

// windowEmitter entrega os eventos do backend para a janela.
type windowEmitter struct {
	ctx atomic.Pointer[context.Context]
}

func (w *windowEmitter) Emit(event string, data any) {
	if ctx := w.ctx.Load(); ctx != nil {
		runtime.EventsEmit(*ctx, event, data)
	}
}

func main() {
	logFile := startLog(os.Getenv("FURITO_SETTINGS"))
	// o script de exemplos que entra na lista de scripts na primeira abertura
	exampleScript, _ := frontendFiles.ReadFile("frontend/script-examples.js")
	app, err := backend.NewApp(backend.Options{
		Debug:               os.Getenv("FURITO_DEBUG") != "",
		DisableAudioDevices: os.Getenv("FURITO_NO_AUDIO") != "",
		SettingsPath:        os.Getenv("FURITO_SETTINGS"), // vazio = local padrão
		IdentityPath:        os.Getenv("FURITO_IDENTITY"), // vazio = local padrão
		SoundPacksZip:       backend.BuiltinSoundPacks(),  // os avisos sonoros do próprio app
		ExampleScript:       string(exampleScript),
	})
	if err != nil {
		fatal(logFile, "Erro ao preparar identidades e áudio", err)
	}
	log.Print("identidades e áudio prontos; abrindo a janela")

	assets, err := fs.Sub(frontendFiles, "frontend")
	if err != nil {
		fatal(logFile, "Erro ao carregar a interface", err)
	}

	emitter := &windowEmitter{}
	var startHotkeys sync.Once
	// Só a API vai para a janela; os ganchos de desenvolvimento entram apenas
	// em executáveis compilados para isso (ver devhooks.go).
	bind := append([]interface{}{app.Bridge()}, devBindings(app)...)

	err = wails.Run(&options.App{
		Title:     "Furito TS",
		Width:     1100,
		Height:    720,
		MinWidth:  760,
		MinHeight: 520,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Handler:    app.ScreenFrameHandler(), // quadros da captura de tela nativa
			Middleware: backend.ScriptSandbox,    // o código que roda os scripts do usuário sai com a política de isolamento
		},
		BackgroundColour: &options.RGBA{R: 236, G: 236, B: 236, A: 255},
		Menu:             buildMenu(app),
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true},
		Bind:             bind,
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title: "Furito TS",
				Message: "Cliente de voz compatível com servidores TeamSpeak 3 e TeamSpeak 6.\n\n" +
					backend.AppVendor + " · " + strings.TrimPrefix(backend.AppSite, "https://"),
			},
		},
		OnStartup: func(ctx context.Context) {
			emitter.ctx.Store(&ctx)
			log.Print("janela criada")
			app.SetEmitter(emitter)
			app.SetQuit(func() { runtime.Quit(ctx) })
			app.SetOpenPicker(func(title string, multiple bool) ([]string, error) {
				if multiple {
					return runtime.OpenMultipleFilesDialog(ctx, runtime.OpenDialogOptions{Title: title})
				}
				file, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{Title: title})
				if err != nil || file == "" {
					return nil, err
				}
				return []string{file}, nil
			})
			app.SetDirPicker(func(title string) (string, error) {
				// pacotes como diretórios: dá para entrar no "TeamSpeak 3 Client.app" e escolher a pasta de sons
				return runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: title, TreatPackagesAsDirectories: true})
			})
			app.SetSavePicker(func(title, defaultName string) (string, error) {
				return runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{Title: title, DefaultFilename: defaultName})
			})
			runtime.OnFileDrop(ctx, func(_, _ int, paths []string) {
				go app.AddSoundFiles(paths) // áudio arrastado para a janela vai para o soundboard
			})
			app.SetFilePicker(func() ([]string, error) {
				return runtime.OpenMultipleFilesDialog(ctx, runtime.OpenDialogOptions{
					Title: "Adicionar sons ao soundboard",
					Filters: []runtime.FileFilter{{
						DisplayName: "Áudio (MP3, WAV, M4A, AIFF, FLAC, OGG…)",
						Pattern:     "*.mp3;*.wav;*.m4a;*.aac;*.aif;*.aiff;*.flac;*.caf;*.ogg;*.opus;*.wma;*.webm",
					}},
				})
			})
		},
		OnDomReady: func(ctx context.Context) {
			log.Print("interface carregada")
			startHotkeys.Do(app.StartHotkeys) // precisa da janela já em execução
			devDomReady(ctx)
		},
		OnShutdown: func(ctx context.Context) {
			app.Shutdown()
		},
	})
	if err != nil {
		fatal(logFile, "Erro ao abrir a janela", err)
	}
	log.Print("encerrado")
}

func buildMenu(app *backend.App) *menu.Menu {
	action := func(name string) menu.Callback {
		return func(*menu.CallbackData) { app.EmitMenu(name) }
	}

	root := menu.NewMenu()
	mac := goruntime.GOOS == "darwin"
	if mac {
		root.Append(menu.AppMenu()) // Sobre, Ocultar, Encerrar: papéis que só o macOS tem
	}

	conn := root.AddSubmenu("Conexões")
	conn.AddText("Conectar…", keys.CmdOrCtrl("s"), action("connect"))
	conn.AddText("Desconectar", keys.CmdOrCtrl("d"), action("disconnect"))
	conn.AddSeparator()
	conn.AddText("Favoritos…", keys.CmdOrCtrl("b"), action("bookmarks"))
	conn.AddText("Scripts…", keys.Combo("j", keys.CmdOrCtrlKey, keys.ShiftKey), action("scripts"))
	conn.AddText("Ajustes…", keys.CmdOrCtrl(","), action("settings"))
	if !mac {
		conn.AddSeparator()
		conn.AddText("Sair", keys.CmdOrCtrl("q"), func(*menu.CallbackData) { app.Quit() })
	}

	self := root.AddSubmenu("Próprio")
	self.AddText("Mutar/desmutar microfone", keys.Combo("m", keys.CmdOrCtrlKey, keys.ShiftKey), action("toggleMute"))
	self.AddText("Mutar/desmutar alto-falantes", keys.Combo("s", keys.CmdOrCtrlKey, keys.ShiftKey), action("toggleDeafen"))
	self.AddSeparator()
	self.AddText("Alternar ausente", keys.Combo("a", keys.CmdOrCtrlKey, keys.ShiftKey), action("toggleAway"))

	if mac {
		root.Append(menu.EditMenu())
		root.Append(menu.WindowMenu())
	}
	help := root.AddSubmenu("Ajuda")
	help.AddText("Sobre o Furito TS…", nil, action("about"))
	return root
}
