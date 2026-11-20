//go:build devhooks

package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"furito-ts/backend"
	"furito-ts/screencap"
)

// Ganchos de desenvolvimento: executar um arquivo .js dentro da janela e dar a
// ele funções que a interface normal não tem (importar uma identidade de um
// caminho, pôr arquivos no soundboard sem o seletor de arquivos).
//
// Só existem em executáveis compilados com a etiqueta "devhooks"
// (FURITO_TAGS=devhooks make mac). No app que vai para os usuários eles não
// podem existir: quem conseguisse abrir o app com uma variável de ambiente
// rodaria código dentro da janela, com tudo o que a janela alcança — inclusive
// o microfone e a captura de tela que o sistema liberou para o app, e não para
// quem o abriu.

// DevHooks são as funções extras que a página enxerga em window.go.main.
type DevHooks struct{ app *backend.App }

// DebugReport imprime no terminal um texto enviado pela página (FURITO_DEV_JS).
func (*DevHooks) DebugReport(text string) { fmt.Println("[ui]", text) }

// NoticeCoupling expõe aos testes o que o motor de áudio aprendeu sobre o
// microfone ouvir os avisos sonoros.
func (d *DevHooks) NoticeCoupling() string { return d.app.NoticeCoupling() }

// AddSoundFiles põe arquivos de áudio no soundboard, sem a janela de escolha
// de arquivo.
func (d *DevHooks) AddSoundFiles(paths []string) { d.app.AddSoundFiles(paths) }

// ScreenAccess diz se o sistema já liberou a gravação de tela para o app (não
// mostra aviso nenhum).
func (*DevHooks) ScreenAccess() bool { return screencap.HasAccess() }

// ImportIdentityFile importa uma identidade de um caminho, sem a janela de
// escolha de arquivo (que um script não consegue operar).
func (d *DevHooks) ImportIdentityFile(path string) (string, error) {
	return d.app.ImportIdentityFile(path)
}

// devScript é o arquivo .js executado na página ao abrir ("" = nenhum).
func devScript() string { return os.Getenv("FURITO_DEV_JS") }

// devBindings devolve o que a página enxerga além da API.
func devBindings(app *backend.App) []interface{} {
	if devScript() == "" {
		return nil
	}
	return []interface{}{&DevHooks{app: app}}
}

// devDomReady roda o script de desenvolvimento quando a interface carrega.
func devDomReady(ctx context.Context) {
	file := devScript()
	if file == "" {
		return
	}
	if js, err := os.ReadFile(file); err == nil {
		runtime.WindowExecJS(ctx, string(js))
	}
	if secs, err := strconv.Atoi(os.Getenv("FURITO_DEV_QUIT_AFTER")); err == nil && secs > 0 {
		time.AfterFunc(time.Duration(secs)*time.Second, func() { runtime.Quit(ctx) })
	}
}
