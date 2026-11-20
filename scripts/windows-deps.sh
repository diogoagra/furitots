#!/bin/bash
# Baixa as bibliotecas de áudio para Windows (libopus, libopusfile, libogg) do
# repositório oficial do MSYS2 e as deixa em build/windows/deps, para a
# compilação cruzada feita por scripts/build-windows.sh no macOS ou no Linux.
#
# São três pacotes ("mingw-w64-ucrt-x86_64-…"), conferidos pelo SHA-256 que o
# próprio repositório publica. Nada do que vem neles é executado: só os
# cabeçalhos (.h) e as bibliotecas estáticas (.a) são usados, na ligação.
#
# Requisitos: curl, tar, zstd (macOS: brew install zstd) e shasum ou sha256sum.
set -euo pipefail
cd "$(dirname "$0")/.."

REPO="${MSYS2_REPO:-https://repo.msys2.org/mingw/ucrt64}"
DEPS="$(pwd)/build/windows/deps"
PKGS="opus opusfile libogg"

need() { command -v "$1" >/dev/null 2>&1 || { echo "falta $1" >&2; exit 1; }; }
need curl
need tar
need zstd
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }
case "$DEPS" in *" "*) echo "o caminho do projeto tem espaços; o pkg-config não lida bem com isso" >&2; exit 1 ;; esac

# Pasta temporária: fica em $TMPDIR, que o sistema limpa sozinho.
TMP=$(mktemp -d)
mkdir -p "$DEPS/pkgconfig" "$DEPS/include" "$DEPS/lib" "$DEPS/licenses" "$TMP/db" "$TMP/root"

echo "==> lendo o índice de pacotes de $REPO"
curl -fsSL "$REPO/ucrt64.db" -o "$TMP/ucrt64.db"
# o índice é um .tar comprimido (zstd ou gzip) com um arquivo "desc" por pacote
if ! zstd -dc "$TMP/ucrt64.db" 2>/dev/null | tar -xf - -C "$TMP/db" 2>/dev/null; then
	tar -xzf "$TMP/ucrt64.db" -C "$TMP/db"
fi

field() { awk -v tag="%$2%" '$0 == tag { getline; print; exit }' "$1"; }

VERSION_opus="" VERSION_opusfile=""
for pkg in $PKGS; do
	desc=$(ls -d "$TMP/db/mingw-w64-ucrt-x86_64-$pkg"-[0-9]* 2>/dev/null | head -1)
	[ -n "$desc" ] || { echo "pacote não encontrado no índice: $pkg" >&2; exit 1; }
	file=$(field "$desc/desc" FILENAME)
	want=$(field "$desc/desc" SHA256SUM)
	version=$(field "$desc/desc" VERSION)
	echo "==> $pkg $version"
	curl -fsSL "$REPO/$file" -o "$TMP/$file"
	got=$(sha256 "$TMP/$file")
	if [ "$got" != "$want" ]; then
		echo "SHA-256 não confere para $file" >&2
		echo "  esperado: $want" >&2
		echo "  obtido:   $got" >&2
		exit 1
	fi
	zstd -dc "$TMP/$file" | tar -xf - -C "$TMP/root"
	case "$pkg" in
		opus) VERSION_opus=${version%%-*} ;;
		opusfile) VERSION_opusfile=${version%%-*} ;;
	esac
done

# Só o que a ligação usa: cabeçalhos e bibliotecas estáticas (sobrescreve o que houver).
cp -R "$TMP/root/ucrt64/include/opus" "$TMP/root/ucrt64/include/ogg" "$DEPS/include/"
cp "$TMP/root/ucrt64/lib/libopus.a" "$TMP/root/ucrt64/lib/libopusfile.a" "$TMP/root/ucrt64/lib/libogg.a" "$DEPS/lib/"
if [ -d "$TMP/root/ucrt64/share/licenses" ]; then
	cp -R "$TMP/root/ucrt64/share/licenses/." "$DEPS/licenses/"
fi

# Descrições próprias para o pkg-config, apontando para as bibliotecas
# estáticas daqui (as que vêm nos pacotes apontam para /ucrt64).
cat > "$DEPS/pkgconfig/opus.pc" <<PC
prefix=$DEPS
Name: opus
Description: Opus (biblioteca estática para Windows, do MSYS2)
Version: $VERSION_opus
Cflags: -I\${prefix}/include/opus
Libs: -L\${prefix}/lib -lopus
PC
cat > "$DEPS/pkgconfig/opusfile.pc" <<PC
prefix=$DEPS
Name: opusfile
Description: opusfile (biblioteca estática para Windows, do MSYS2)
Version: $VERSION_opusfile
Cflags: -I\${prefix}/include/opus -I\${prefix}/include
Libs: -L\${prefix}/lib -lopusfile -logg -lopus
PC

echo "==> bibliotecas em $DEPS"
echo "    as licenças (BSD) estão em build/windows/deps/licenses; devem acompanhar o executável distribuído"
