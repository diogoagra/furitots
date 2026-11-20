package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"

	"furito-ts/backend"
)

// Um programa de janela não tem console: no Windows, o que vai para a saída de
// erro some, e uma falha na abertura parece "o programa não abriu". Por isso
// os passos da abertura e qualquer falha vão para um arquivo de registro, e um
// erro que impede o app de abrir aparece numa caixa de mensagem.

// logPath diz onde fica o registro: ao lado dos ajustes
// (macOS: ~/Library/Application Support/FuritoTS/furito.log;
// Windows: %AppData%\FuritoTS\furito.log).
func logPath(settingsPath string) string {
	if settingsPath != "" {
		return filepath.Join(filepath.Dir(settingsPath), "furito.log")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(backend.DataDir(dir), "furito.log")
}

// startLog abre o registro e passa a escrever nele (e na saída de erro, para
// quem roda pelo terminal). Devolve o caminho, ou "" se não deu para abrir.
func startLog(settingsPath string) string {
	path := logPath(settingsPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	// não deixa o arquivo crescer para sempre
	if info, err := os.Stat(path); err == nil && info.Size() > 512*1024 {
		os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return ""
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.Ldate | log.Ltime)
	// Uma pane do programa (panic não tratado) também fica registrada.
	debug.SetCrashOutput(f, debug.CrashOptions{})
	log.Printf("Furito TS %s iniciando (%s/%s)", backend.AppVersion, goruntime.GOOS, goruntime.GOARCH)
	return path
}

// fatal registra um erro que impede o app de abrir, mostra-o ao usuário e
// encerra o programa.
func fatal(logFile, what string, err error) {
	log.Printf("ERRO: %s: %v", what, err)
	text := fmt.Sprintf("O Furito TS não conseguiu abrir.\n\n%s: %v", what, err)
	if logFile != "" {
		text += "\n\nRegistro: " + logFile
	}
	showFatal("Furito TS", text)
	os.Exit(1)
}
