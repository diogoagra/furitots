# Windows

Estado do porte para Windows, como gerar o executável e como funciona a
assinatura. O passo a passo resumido está no
[README](../README.md#windows).

O executável é gerado por compilação cruzada no Mac (`build/bin/TeamSpeak
Furito.exe`, ligação estática: depende só de DLLs do próprio Windows 10/11),
mas o porte **ainda não foi validado em uso num Windows**: trate como
experimental. O que está abaixo como "tem equivalente" quer dizer que compila
e que a lógica foi testada no macOS.

Como gerar:

- Num Mac ou Linux, com MinGW-w64 (`brew install mingw-w64`): `make
  windows-deps` baixa do repositório do MSYS2 os pacotes `opus`, `opusfile` e
  `libogg` (conferidos por SHA-256) para `build/windows/deps`; `make windows`
  gera o executável.
- Num Windows com [MSYS2](https://www.msys2.org) (terminal UCRT64), Go e Wails:
  as instruções estão no cabeçalho de `scripts/build-windows.sh` (caminho não
  testado).

Ícone, manifesto e informações de versão do `.exe` ficam em `build/windows/`.

**Assinatura.** Se o `osslsigncode` estiver instalado (`brew install
osslsigncode`), o `make windows` assina o executável. Sem configuração, usa um
certificado autoassinado "Diogo Agra", criado na primeira vez em
`build/windows/signing/` (fora do git; a chave privada fica só ali), e deixa o
certificado público em `build/bin/Diogo-Agra.cer`. Uma assinatura autoassinada
só é confiável nos computadores em que esse certificado for instalado, e **não
passa pelo Controle Inteligente de Aplicativos do Windows 11**, que exige
certificado emitido por uma autoridade certificadora reconhecida pela
Microsoft. Com um certificado comprado: `FURITO_SIGN_PFX=arquivo.pfx
FURITO_SIGN_PASS=senha make windows` (e `FURITO_SIGN_TIMESTAMP=URL` para o
carimbo de tempo).

Duas armadilhas da compilação cruzada, já tratadas no script:

- O cache do Go não percebe a troca do `PKG_CONFIG_LIBDIR`. Se o pacote de
  áudio já tiver sido compilado para Windows com os caminhos do sistema (um
  `GOOS=windows go vet` basta), ele é reaproveitado e a ligação falha com
  `undefined reference to opus_…`/`op_…`. Por isso os caminhos vão também em
  `CGO_CFLAGS`/`CGO_LDFLAGS`, que entram na chave do cache.
- O Wails gera as "bindings" compilando o app para a máquina local, o que não
  liga com as bibliotecas de Windows; a etapa é pulada (`-skipbindings`), e a
  interface não depende desses arquivos.

O que já tem equivalente no Windows (só compila; lógica testada no Mac):

- Atalhos globais e apertar-para-falar (`hotkey/hotkey_windows.go`, por
  consulta ao teclado a cada 10 ms; a tecla não é "engolida").
- Avisos sonoros: os pacotes de sons são WAV sem compressão, que o app lê
  sem ajuda de outro programa.
- Identidades em `%AppData%\FuritoTS\identidades.json`.
- Menu sem os itens que só existem no macOS, com "Sair".
- Compartilhamento de tela pela captura do WebView2 (o mesmo código passa no
  Chrome, que usa o mesmo motor).
- Scripts: o mesmo código; a janela, o isolamento (rede bloqueada) e os erros
  com número de linha passam no Chrome, que usa o mesmo motor do WebView2.

O que ainda não tem equivalente:

- Soundboard: sem o `afconvert` do macOS, só WAV (ou qualquer formato se
  houver `ffmpeg`). MP3/M4A precisam de um decodificador a mais.
- Cancelamento de eco e modos de microfone (recursos do macOS).
- Instalador.
