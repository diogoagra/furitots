# RNNoise 0.2

Supressão de ruído por rede neural, da Xiph.Org (Jean-Marc Valin), licença
BSD de 3 cláusulas — o texto está em `COPYING` e precisa acompanhar o app
distribuído (os scripts de `scripts/` cuidam disso).

- Origem: <https://github.com/xiph/rnnoise/releases/download/v0.2/rnnoise-0.2.tar.gz>
  (cópia de conveniência do repositório oficial, <https://gitlab.xiph.org/xiph/rnnoise>)
- SHA-256 do pacote: `90fce4b00b9ff24c08dbfe31b82ffd43bae383d85c5535676d28b0a2b11c0d37`
- Trazido em 19/11/2026.

## O que veio do pacote

Sem nenhuma alteração:

- de `src/`: `denoise.c`, `rnn.c`, `pitch.c`, `kiss_fft.c`, `celt_lpc.c`,
  `nnet.c`, `nnet_default.c`, `parse_lpcnet_weights.c`, `rnnoise_data.c` (os
  pesos da rede, 4,9 MB de texto), `rnnoise_tables.c` e os `.h` que eles usam;
- `src/x86/x86_arch_macros.h` (em `x86/`), que `vec.h` inclui;
- `include/rnnoise.h`, `COPYING` e `AUTHORS`.

São os arquivos que o `Makefile.am` do pacote põe na biblioteca. Ficaram de
fora os programas de treino (`dump_features.c`, `write_weights.c`), os exemplos
e a escolha de instruções em tempo de execução para x86 (`src/x86/*.c`): sem
ela o código usa SSE2 em x86-64, que todo processador de 64 bits tem, e NEON
em ARM.

## O que é do Furito TS

- `rnnoise.go`, `rnnoise_test.go`: a ligação com o Go e os testes.
- `os_support.h`: o pacote 0.2 inclui esse arquivo em `vec_neon.h` mas não o
  traz, então não compila em ARM (Apple Silicon) como veio. O daqui só define
  `OPUS_CLEAR`, a única coisa usada dele.
- Este arquivo.

## Para atualizar

Baixar o pacote novo, conferir o SHA-256 publicado, copiar por cima os mesmos
arquivos e rodar `go test ./audio/...` e as avaliações descritas no `readme.md`
(os limiares do detector de voz em `audio/voiceproc.go` foram medidos com esta
versão da rede).
