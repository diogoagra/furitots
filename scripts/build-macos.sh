#!/bin/bash
# Gera o Furito TS para macOS em build/bin/.
#
#   scripts/build-macos.sh          app para usar neste Mac
#   scripts/build-macos.sh --dist   app para levar a outros Macs: as bibliotecas
#                                   de áudio vão dentro dele e sai também um .zip
#
# Requisitos: Go, Wails (go install github.com/wailsapp/wails/v2/cmd/wails@latest),
# Xcode Command Line Tools e, pelo Homebrew: pkg-config opus opusfile
set -euo pipefail
cd "$(dirname "$0")/.."

APP="build/bin/Furito TS.app"
BIN="$APP/Contents/MacOS/Furito TS"
DIST=0
case "${1:-}" in
	"") ;;
	--dist) DIST=1 ;;
	*) echo "uso: $0 [--dist]" >&2; exit 2 ;;
esac

need() { command -v "$1" >/dev/null 2>&1 || { echo "falta $1: $2" >&2; exit 1; }; }
need go "instale o Go (https://go.dev/dl)"
export PATH="$PATH:$(go env GOPATH)/bin"
need wails "go install github.com/wailsapp/wails/v2/cmd/wails@latest"
need pkg-config "brew install pkg-config"
pkg-config --exists opus opusfile || { echo "faltam as bibliotecas de áudio: brew install opus opusfile" >&2; exit 1; }

ARCH=$(go env GOARCH)
VERSION=$(sed -n 's/.*"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
# -skipbindings: para gerar as "bindings" o Wails executa o app recém-compilado,
# que abriria o registro e os ajustes de quem está compilando. A interface não
# usa esses arquivos (fala com o app por window.go, criado em tempo de execução).
# FURITO_TAGS=devhooks inclui os ganchos de desenvolvimento (devhooks.go). Um
# app assim é para testes: não distribua.
TAGS=()
if [ -n "${FURITO_TAGS:-}" ]; then
	TAGS=(-tags "$FURITO_TAGS")
	echo "==> ATENÇÃO: compilando com as etiquetas \"$FURITO_TAGS\" (app de teste, não distribuir)"
fi
echo "==> compilando o Furito TS $VERSION para macOS ($ARCH)"
wails build -platform "darwin/$ARCH" -skipbindings ${TAGS[@]+"${TAGS[@]}"}

# O RNNoise (supressão de ruído) é compilado dentro do executável; a licença
# dele (BSD) pede que o aviso de direitos autorais acompanhe o app.
mkdir -p "$APP/Contents/Resources/licencas"
cp audio/rnnoise/COPYING "$APP/Contents/Resources/licencas/rnnoise.txt"

# Copia para dentro do app as bibliotecas que não são do sistema (as do
# Homebrew: libopus, libopusfile, libogg) e aponta o executável para elas.
bundle_libs() {
	local fw="$APP/Contents/Frameworks"
	mkdir -p "$fw"
	local todo=("$BIN")
	local i=0
	while [ "$i" -lt "${#todo[@]}" ]; do
		local target="${todo[$i]}"
		i=$((i + 1))
		while IFS= read -r dep; do
			case "$dep" in
				"" | /System/* | /usr/lib/* | @*) continue ;;
			esac
			local name
			name=$(basename "$dep")
			if [ ! -f "$fw/$name" ]; then
				cp -L "$dep" "$fw/$name"
				chmod u+w "$fw/$name"
				install_name_tool -id "@executable_path/../Frameworks/$name" "$fw/$name"
				todo+=("$fw/$name")
				echo "    levando junto: $name"
			fi
			install_name_tool -change "$dep" "@executable_path/../Frameworks/$name" "$target"
		done < <(otool -L "$target" | tail -n +2 | awk '{print $1}')
	done
	# mexer nos caminhos invalida a assinatura de cada biblioteca
	for lib in "$fw"/*.dylib; do
		codesign --force --sign - "$lib"
	done
}

if [ "$DIST" = 1 ]; then
	echo "==> incluindo as bibliotecas de áudio no app"
	bundle_libs
	left=$(otool -L "$BIN" "$APP/Contents/Frameworks/"*.dylib | grep -E '^[[:space:]]+/(opt|usr/local)/' || true)
	if [ -n "$left" ]; then
		echo "ainda há dependências fora do app:" >&2
		echo "$left" >&2
		exit 1
	fi
fi

# Versão mínima do macOS: a maior entre a do executável, a declarada no
# Info.plist e a de cada biblioteca levada junto. As bibliotecas do Homebrew
# são compiladas para o macOS em que foram instaladas, então um app feito aqui
# normalmente só roda nessa versão do macOS ou em mais novas.
minos_of() { otool -l "$1" | awk '/LC_BUILD_VERSION/{f=1} f && /minos/{print $2; exit}'; }
PLIST="$APP/Contents/Info.plist"
MINOS=$( {
	minos_of "$BIN"
	/usr/libexec/PlistBuddy -c "Print :LSMinimumSystemVersion" "$PLIST" 2>/dev/null || true
	if [ "$DIST" = 1 ]; then
		for lib in "$APP/Contents/Frameworks/"*.dylib; do minos_of "$lib"; done
	fi
} | sort -V | tail -1)
if [ "$DIST" = 1 ]; then
	# num macOS mais antigo o sistema avisa que o app pede uma versão mais nova,
	# em vez de o app fechar sozinho ao abrir
	/usr/libexec/PlistBuddy -c "Set :LSMinimumSystemVersion $MINOS" "$PLIST"
fi

# Assinatura local ("ad hoc"): suficiente para rodar neste Mac. Para distribuir
# sem o aviso do macOS é preciso assinar com uma conta de desenvolvedor da Apple.
codesign --force --deep --sign - "$APP"
codesign --verify --deep "$APP"

echo "==> pronto: $APP (macOS $MINOS ou mais novo, $ARCH)"

if [ "$DIST" = 1 ]; then
	ZIP="build/bin/Furito-TS-$VERSION-macos-$ARCH.zip"
	ditto -c -k --sequesterRsrc --keepParent "$APP" "$ZIP"
	echo "==> para distribuir: $ZIP"
	echo "    Em outro Mac, na primeira vez: botão direito no app → Abrir (o app não é assinado pela Apple)."
fi
