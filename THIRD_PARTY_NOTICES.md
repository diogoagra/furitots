# Código e bibliotecas de terceiros

O Furito TS é distribuído sob a [licença MIT](LICENSE). Ele inclui ou
usa o trabalho abaixo, cada um sob a própria licença.

## Incluído neste repositório

| Componente | Onde | Licença |
|---|---|---|
| [RNNoise](https://gitlab.xiph.org/xiph/rnnoise) 0.2 — supressão de ruído por rede neural (Xiph.Org, Jean-Marc Valin, Mozilla, Amazon) | `audio/rnnoise/` (código C sem alterações; origem em [`ORIGEM.md`](audio/rnnoise/ORIGEM.md)) | BSD de 3 cláusulas — [`audio/rnnoise/COPYING`](audio/rnnoise/COPYING) |

O RNNoise é compilado dentro do executável, e a licença dele pede que o aviso
de direitos autorais acompanhe as distribuições em binário: os scripts de
compilação o copiam para dentro do `.app` (`Contents/Resources/licencas`) e,
no Windows, para `build/bin/licencas`, que deve ir junto com o `.exe`.

## Dependências (baixadas na compilação)

| Componente | Uso | Licença |
|---|---|---|
| [Wails](https://wails.io) v2 | Janela, menus e ponte entre o Go e a interface | MIT |
| [malgo](https://github.com/gen2brain/malgo) / [miniaudio](https://miniaud.io) | Captura e reprodução de áudio | Unlicense / domínio público ou MIT-0 |
| [hraban/opus](https://github.com/hraban/opus) | Ligação do Go com o libopus | MIT |
| [libopus](https://opus-codec.org), [opusfile](https://opus-codec.org/docs/), [libogg](https://xiph.org/ogg/) | Codec de voz e leitura de arquivos Opus | BSD de 3 cláusulas |
| [filippo.io/edwards25519](https://filippo.io/edwards25519) | Curva elíptica do handshake | BSD de 3 cláusulas |

As demais dependências indiretas estão em [`go.mod`](go.mod). No executável do
Windows o libopus, o opusfile e o libogg são ligados estaticamente: os avisos
de licença deles (em `build/windows/deps/licenses`, depois de `make
windows-deps`) também devem acompanhar o `.exe`.

## O que não vem de terceiros

Os **avisos sonoros** ("conectado", "entrou no canal"…) são sintetizados pelo
próprio app, por código (`backend/builtinsounds.go`); não há gravações no
repositório. Nenhum arquivo do cliente TeamSpeak — sons, ícones ou imagens — é
distribuído aqui.

## Marcas

TeamSpeak é marca registrada da TeamSpeak Systems GmbH. Este projeto é
independente: não é afiliado, patrocinado nem endossado pela TeamSpeak
Systems GmbH.
