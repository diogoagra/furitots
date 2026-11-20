#!/bin/bash
# Gera o Furito TS para Windows: build/bin/Furito TS.exe
#
# Dois jeitos de usar:
#
#  1) Num Windows, pelo terminal "UCRT64" do MSYS2 (https://www.msys2.org), com
#     Go e Wails instalados:
#       pacman -S --needed mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-pkgconf \
#                          mingw-w64-ucrt-x86_64-opus mingw-w64-ucrt-x86_64-opusfile
#       scripts/build-windows.sh
#
#  2) Por compilação cruzada no macOS ou no Linux, com o MinGW-w64
#     (macOS: brew install mingw-w64) e as bibliotecas de áudio para Windows em
#     build/windows/deps — o scripts/windows-deps.sh as baixa do MSYS2:
#       scripts/windows-deps.sh
#       scripts/build-windows.sh
#
# O executável sai sem depender de DLLs do MinGW (ligação estática); precisa
# só do WebView2, que já vem no Windows 10/11.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)

need() { command -v "$1" >/dev/null 2>&1 || { echo "falta $1: $2" >&2; exit 1; }; }
need go "instale o Go (https://go.dev/dl)"
export PATH="$PATH:$(go env GOPATH)/bin"
need wails "go install github.com/wailsapp/wails/v2/cmd/wails@latest"

export CGO_ENABLED=1
WAILS_FLAGS=()
case "$(uname -s)" in
	MINGW* | MSYS*)
		# dentro do MSYS2: compilador e bibliotecas do próprio ambiente
		need gcc "pacman -S --needed mingw-w64-ucrt-x86_64-gcc"
		need pkg-config "pacman -S --needed mingw-w64-ucrt-x86_64-pkgconf"
		pkg-config --exists opus opusfile || { echo "faltam as bibliotecas de áudio: pacman -S --needed mingw-w64-ucrt-x86_64-opus mingw-w64-ucrt-x86_64-opusfile" >&2; exit 1; }
		export CC=gcc CXX=g++
		OBJDUMP=objdump
		;;
	*)
		need x86_64-w64-mingw32-gcc "instale o MinGW-w64 (macOS: brew install mingw-w64)"
		need pkg-config "instale o pkg-config (macOS: brew install pkg-config)"
		DEPS="$ROOT/build/windows/deps"
		if [ ! -f "$DEPS/pkgconfig/opusfile.pc" ]; then
			echo "faltam as bibliotecas de áudio para Windows em build/windows/deps." >&2
			echo "rode antes: scripts/windows-deps.sh   (baixa três pacotes do MSYS2)" >&2
			exit 1
		fi
		export CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++
		# só as bibliotecas de Windows: as do sistema (Homebrew) não servem aqui
		export PKG_CONFIG_LIBDIR="$DEPS/pkgconfig"
		unset PKG_CONFIG_PATH || true
		# O cache de compilação do Go não percebe a troca do PKG_CONFIG_LIBDIR:
		# se a biblioteca de áudio já foi compilada para Windows com os caminhos
		# do sistema (um "go vet" basta), ele reaproveita aquela versão e a
		# ligação falha com "undefined reference to opus_…/op_…". As variáveis
		# CGO_* entram na chave do cache, então passar os caminhos também por
		# elas garante a compilação com as bibliotecas certas.
		export CGO_CFLAGS="-I$DEPS/include/opus -I$DEPS/include ${CGO_CFLAGS:-}"
		export CGO_LDFLAGS="-L$DEPS/lib -lopusfile -logg -lopus ${CGO_LDFLAGS:-}"
		# O Wails gera as "bindings" compilando o app para a máquina em que
		# roda — e essa compilação, com as bibliotecas de Windows acima, não
		# liga. A interface não usa esses arquivos (fala com o app por
		# window.go, criado em tempo de execução), então a etapa é pulada.
		WAILS_FLAGS+=(-skipbindings)
		OBJDUMP=x86_64-w64-mingw32-objdump
		;;
esac

# FURITO_TAGS=devhooks inclui os ganchos de desenvolvimento (devhooks.go). Um
# executável assim é para testes: não distribua.
if [ -n "${FURITO_TAGS:-}" ]; then
	WAILS_FLAGS+=(-tags "$FURITO_TAGS")
	echo "==> ATENÇÃO: compilando com as etiquetas \"$FURITO_TAGS\" (executável de teste, não distribuir)"
fi

VERSION=$(sed -n 's/.*"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
echo "==> compilando o Furito TS $VERSION para Windows (amd64)"
wails build -platform windows/amd64 ${WAILS_FLAGS[@]+"${WAILS_FLAGS[@]}"} -ldflags '-extldflags "-static"'

EXE="build/bin/Furito TS.exe"
[ -f "$EXE" ] || { echo "o executável não foi gerado" >&2; exit 1; }

# Assinatura digital do executável (Authenticode), com o osslsigncode.
#
# Sem configuração nenhuma, usa um certificado autoassinado "Diogo Agra",
# criado na primeira vez em build/windows/signing (fora do git). Uma assinatura
# autoassinada identifica o editor, mas o Windows só confia nela nos
# computadores em que o certificado (build/bin/Diogo-Agra.cer) for instalado
# como confiável — e o Controle Inteligente de Aplicativos do Windows 11 não a
# aceita de jeito nenhum: ele exige certificado emitido por uma autoridade
# certificadora reconhecida pela Microsoft.
#
# Com um certificado comprado, aponte para ele:
#   FURITO_SIGN_PFX=arquivo.pfx FURITO_SIGN_PASS=senha  (ou FURITO_SIGN_CERT + FURITO_SIGN_KEY, em PEM)
#   FURITO_SIGN_TIMESTAMP=http://…   servidor de carimbo de tempo (recomendado com certificado comprado)
sign_exe() {
	if ! command -v osslsigncode >/dev/null 2>&1; then
		echo "!!  o executável NÃO foi assinado: falta o osslsigncode"
		echo "    (macOS: brew install osslsigncode; MSYS2: pacman -S mingw-w64-ucrt-x86_64-osslsigncode)"
		return 0
	fi
	local args=(sign -h sha256 -n "Furito TS" -i "https://diogoagra.dev")
	local cert=""
	if [ -n "${FURITO_SIGN_PFX:-}" ]; then
		args+=(-pkcs12 "$FURITO_SIGN_PFX" -pass "${FURITO_SIGN_PASS:-}")
		echo "==> assinando com o certificado de $FURITO_SIGN_PFX"
	else
		local dir="$ROOT/build/windows/signing"
		cert="${FURITO_SIGN_CERT:-$dir/autoassinado.crt}"
		local key="${FURITO_SIGN_KEY:-$dir/autoassinado.key}"
		if [ ! -f "$cert" ] || [ ! -f "$key" ]; then
			if [ -n "${FURITO_SIGN_CERT:-}${FURITO_SIGN_KEY:-}" ]; then
				echo "certificado ou chave não encontrados: $cert / $key" >&2
				return 1
			fi
			need openssl "instale o OpenSSL"
			mkdir -p "$dir"
			chmod 700 "$dir"
			echo "==> criando o certificado autoassinado em build/windows/signing (vale 5 anos)"
			openssl req -x509 -newkey rsa:3072 -sha256 -days 1825 -nodes \
				-keyout "$key" -out "$cert" -subj "/CN=Diogo Agra/O=Diogo Agra/C=BR" \
				-addext "basicConstraints=critical,CA:FALSE" \
				-addext "keyUsage=critical,digitalSignature" \
				-addext "extendedKeyUsage=codeSigning" 2>/dev/null
			chmod 600 "$key"
		fi
		args+=(-certs "$cert" -key "$key")
		echo "==> assinando com o certificado autoassinado (Diogo Agra)"
	fi
	if [ -n "${FURITO_SIGN_TIMESTAMP:-}" ]; then
		args+=(-ts "$FURITO_SIGN_TIMESTAMP")
	fi
	# assina numa cópia e só troca o executável se a assinatura der certo
	local signed="$EXE.assinado"
	osslsigncode "${args[@]}" -in "$EXE" -out "$signed"
	mv "$signed" "$EXE"
	if [ -n "$cert" ]; then
		# o certificado público, para quem quiser instalá-lo como confiável no Windows
		openssl x509 -in "$cert" -outform DER -out "build/bin/Diogo-Agra.cer"
		osslsigncode verify -CAfile "$cert" -in "$EXE" >/dev/null 2>&1 &&
			echo "    assinatura conferida" ||
			echo "!!  o osslsigncode não confirmou a assinatura; confira no Windows (Propriedades → Assinaturas Digitais)"
	fi
}
sign_exe

# O RNNoise (supressão de ruído) é compilado dentro do executável; a licença
# dele (BSD) pede que o aviso de direitos autorais acompanhe o app.
mkdir -p build/bin/licencas
cp audio/rnnoise/COPYING build/bin/licencas/rnnoise.txt

echo "==> pronto: $EXE"
echo "    ao distribuir, mande junto a pasta build/bin/licencas (aviso de licença do RNNoise)"
if command -v "$OBJDUMP" >/dev/null 2>&1; then
	echo "    DLLs de que ele depende (devem ser só as do Windows):"
	"$OBJDUMP" -p "$EXE" | sed -n 's/.*DLL Name: /      /p' | sort -u
fi
