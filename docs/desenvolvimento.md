# Desenvolvimento

Como o projeto está organizado, como rodar os testes e as ferramentas que
ajudam a mexer nele sem um servidor de verdade por perto.

## Estrutura

| Caminho | Conteúdo |
|---|---|
| `crypto/` | EAX/CMAC, derivação de chaves, QuickLZ |
| `ts3/` | Protocolo de cliente: handshake, criptografia de sessão, transporte confiável (acks, reenvio, ordenação, fragmentação), comandos, transferência de arquivos e o estado do servidor |
| `audio/` | Captura e reprodução nativas, Opus, tratamento do microfone (supressão de ruído, ganho automático, detecção de voz), buffer de jitter, vozes modificadas, soundboard, gravação |
| `audio/rnnoise/` | O RNNoise 0.2 (código C da Xiph.Org, licença BSD, sem alterações) e a ligação com o Go; a origem e o que é do projeto estão em [`ORIGEM.md`](../audio/rnnoise/ORIGEM.md) |
| `hotkey/` | Atalhos globais de teclado (macOS e Windows) |
| `screencap/` | Captura de tela para o compartilhamento (ScreenCaptureKit no macOS) e um padrão de teste |
| `backend/` | Liga protocolo e áudio e expõe a API e os eventos que a interface usa: estado, identidades, favoritos, avisos sonoros (sintetizados em `builtinsounds.go`), scripts, gravação, compartilhamento de tela. Cada servidor conectado (cada aba) é uma `conn` (`conn.go`, `tabs.go`); a janela chega à API por `Bridge.Call(aba, método, argumentos)` (`bridge.go`), para um pedido nunca ir parar no servidor de outra aba |
| `frontend/` | A interface, em HTML/CSS/JS puro (sem etapa de build). `bridge.js` fala com o backend ou, com `?mock`, com um backend simulado; `screen.js` é o compartilhamento de tela (WebRTC); `scripts.js` e `script-worker.js` rodam os scripts do usuário |
| `cmd/audiodemo/` | Gera WAVs para ouvir, fora do app, cada voz e o que seria transmitido com cada supressão de ruído |
| `main.go`, `startup.go`, `fatal_*.go` | Janela e menus (Wails), registro de inicialização e erros fatais |
| `scripts/`, `Makefile` | Compilação para macOS e Windows |
| `build/` | Ícone, `Info.plist` e os arquivos de versão/manifesto do Windows; o que é gerado (`build/bin/` etc.) fica fora do git |

A interface não tem dependências nem empacotador: o Wails serve a pasta
`frontend/` como está.

## Rodar durante o desenvolvimento

```sh
make dev     # wails dev: recompila o Go e recarrega a interface ao salvar
make run     # gera o app e abre
```

Para mexer só na interface, sem compilar nada, sirva a pasta `frontend/` e
abra `index.html?mock` num navegador: o `bridge.js` troca o backend por um
simulado, com um servidor de mentira cheio de canais e pessoas.

```sh
python3 -m http.server 8765 --directory frontend
# http://127.0.0.1:8765/index.html?mock
```

`?mock&demo=NOME` abre o app já numa situação (por exemplo `capture`, o painel
do microfone; `effects`, o menu de vozes; `scripts`; `settings`; `about`; `files`); a
lista está em `runDemo`, em `frontend/app.js`. `?mock&demo=selftest`
roda o teste automático da interface e mostra o resultado na faixa do topo.

## Testes

```sh
make test        # go vet + go test -race em todos os pacotes
make test-live   # testes contra um servidor de verdade (padrão: 127.0.0.1:9987)
```

Um servidor TeamSpeak 6 descartável para os testes ao vivo:

```sh
docker run -d --name teamspeak-server -e TSSERVER_LICENSE_ACCEPTED=accept \
  -p 9987:9987/udp -p 30033:30033/tcp teamspeaksystems/teamspeak6-server:latest
```

Os testes ao vivo rodam um pacote por vez (`-p 1`): muitas conexões seguidas
acionam o bloqueio contra flood do servidor. Os de ícones e de administração
precisam de uma chave de privilégio de um servidor de teste:

```sh
FURITO_ICON_TEST_HOST=host:porta FURITO_ICON_TEST_TOKEN=chave go test ./ts3/ -run LiveIcons -v
FURITO_ICON_TEST_HOST=host:porta FURITO_ICON_TEST_TOKEN=chave FURITO_TEST_CONTAINER=nome \
  go test ./backend/ -run LiveAdmin -v
```

O teste de vários servidores ao mesmo tempo precisa de dois servidores
diferentes (um segundo contêiner, em outra porta):

```sh
docker run -d --name ts-b -e TSSERVER_LICENSE_ACCEPTED=accept \
  -p 9988:9987/udp teamspeaksystems/teamspeak6-server:latest
FURITO_TABS_HOST_A=127.0.0.1:9987 FURITO_TABS_HOST_B=127.0.0.1:9988 \
  go test ./backend/ -run LiveTwoServers -v
```

As avaliações de áudio (detector de voz, supressão de ruído, vozes) pedem
gravações de fala e estão descritas em [audio.md](audio.md#medir).

## Variáveis de ambiente

| Variável | Efeito |
|---|---|
| `FURITO_DEBUG=1` | Imprime os comandos trocados com o servidor (senhas ocultas) |
| `FURITO_NO_AUDIO=1` | Não abre microfone nem alto-falantes |
| `FURITO_SETTINGS=arquivo.json` | Usa outro arquivo de ajustes (não mexe nos seus) |
| `FURITO_IDENTITY=arquivo.json` | Usa outro arquivo de identidades (não mexe nas suas) |
| `FURITO_DEV_JS=arquivo.js` | Executa o script na janela ao abrir (só em app compilado com `devhooks`, ver abaixo) |
| `FURITO_DEV_QUIT_AFTER=N` | Fecha o app depois de N segundos (idem) |
| `FURITO_TEST_HOST=host:porta` | Servidor usado pelos testes ao vivo |
| `FURITO_TABS_HOST_A`, `FURITO_TABS_HOST_B` | Dois servidores diferentes, para o teste de várias conexões |
| `FURITO_SPEECH=a.wav,b.wav` | Gravações de fala para as avaliações de áudio |
| `FURITO_EVAL=1` | Liga a avaliação do detector de voz contra o teclado |
| `FURITO_SIGN_PFX`, `FURITO_SIGN_PASS`, `FURITO_SIGN_CERT`, `FURITO_SIGN_KEY`, `FURITO_SIGN_TIMESTAMP` | Certificado para assinar o executável do Windows (ver [windows.md](windows.md)) |

Com `FURITO_SETTINGS` e `FURITO_IDENTITY` apontando para arquivos de teste dá
para abrir uma segunda instância do app sem tocar nos seus dados.

`FURITO_DEV_JS` e `FURITO_DEV_QUIT_AFTER` são ganchos de desenvolvimento e não
existem no app normal: é preciso compilar com a etiqueta `devhooks`
(`FURITO_TAGS=devhooks make mac`). O motivo é segurança: um app que executa na
própria janela um arquivo indicado por variável de ambiente deixaria qualquer
programa do computador usar o microfone e a captura de tela liberados para o
Furito TS. Um app compilado assim serve para testes e não deve ser distribuído.

## Convenções

- Código, comentários e mensagens de commit em português. Os comentários dizem
  o porquê de uma decisão, não o que a linha faz.
- Uma ação que depende do servidor espera a resposta dele e mostra o erro que
  vier; nada falha em silêncio.
- Código de terceiros entra sem alterações, com a origem e a licença ao lado
  (como em `audio/rnnoise/`).
