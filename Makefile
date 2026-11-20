APP := build/bin/Furito TS.app

.PHONY: app mac mac-dist windows windows-deps run dev test test-live

# Gera o app do macOS em build/bin/ (para usar neste Mac)
app mac:
	scripts/build-macos.sh

# App do macOS para levar a outros Macs: bibliotecas de áudio dentro + .zip
mac-dist:
	scripts/build-macos.sh --dist

# Bibliotecas de áudio para Windows (baixa três pacotes do MSYS2). Só é preciso
# na compilação cruzada a partir do macOS/Linux, uma vez.
windows-deps:
	scripts/windows-deps.sh

# Executável do Windows em build/bin/ (ver o cabeçalho de scripts/build-windows.sh)
windows:
	scripts/build-windows.sh

# Gera e abre o app
run: app
	open "$(APP)"

# Modo desenvolvimento (recarrega a interface ao salvar)
dev:
	wails dev

test:
	go vet ./...
	go test -race ./...

# Testes contra um servidor real (padrão: o TS6 do Docker em localhost).
# Um pacote por vez: muitas conexões juntas acionam o bloqueio do servidor.
test-live:
	FURITO_TEST_HOST=$${FURITO_TEST_HOST:-127.0.0.1:9987} go test -p 1 -race -count=1 -run Live -v ./ts3/ ./backend/
