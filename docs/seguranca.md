# Segurança

O Furito TS conversa com servidores que o usuário não controla e mostra textos
escritos por desconhecidos. Esta página diz de quem o app se defende, como, e
o que fica de fora — inclusive o que é limite do próprio protocolo do
TeamSpeak.

Para relatar uma falha, veja o [SECURITY.md](../SECURITY.md).

## De quem o app se defende

| Quem | O que consegue mandar |
|---|---|
| **Outras pessoas no servidor** | Apelido, mensagens, cutucadas, descrição, motivo de expulsão, nome e descrição de canal (quem pode criar um), arquivos do canal, sinalização do compartilhamento de tela |
| **O servidor** | Tudo o que está acima, mais qualquer pacote do protocolo: listas enormes, valores absurdos, respostas falsas a pedidos |
| **Alguém na mesma rede** (ou no caminho até o servidor) | Ver os pacotes e injetar outros em nome do servidor |
| **Alguém na internet** | Mandar pacotes forjando o endereço do servidor, sem ver a conversa |
| **Um script** que o usuário colou e ligou | Código JavaScript rodando dentro do app |
| **Um arquivo** que o usuário importa | Identidade, pacote de sons, áudio para o soundboard |
| **Outro programa no computador** | Abrir o app com argumentos e variáveis de ambiente |

## O que cada parte faz

### Textos de outras pessoas

- Tudo o que vem de fora é tratado como texto. O BBCode é convertido por um
  analisador próprio que só escreve as tags e os atributos que ele mesmo
  conhece (`frontend/app.js`); links só de `http` e `https`.
- **Imagens de fora não carregam sozinhas.** Buscar uma imagem mostra o
  endereço IP de quem lê a quem a hospeda, e quem escolhe o endereço é quem
  escreveu o texto. Por isso:
  - em mensagens, cutucadas e no registro, `[img]` vira só o endereço,
    clicável (abre no navegador);
  - em descrições, na mensagem de boas-vindas e no banner do servidor, a imagem
    aparece como um marcador ("Imagem de *site* — clique para carregar"). Um
    clique carrega aquela imagem; "Carregar sempre as imagens deste servidor"
    libera o servidor (pela identidade dele). Em Ajustes → Privacidade →
    *Imagens de fora* dá para escolher "carregar sempre" ou "nunca carregar";
  - endereços da rede local (`192.168.x`, `10.x`, `localhost`…) nunca são
    carregados como imagem, em nenhum modo.
- **As linhas do registro não interpretam BBCode de terceiros.** "Fulano
  entrou no canal" é montada pelo app com o apelido de outra pessoa dentro;
  nela só o negrito do próprio app tem efeito. Um apelido como
  `[img]site/x[/img]` aparece escrito assim.
- **Links não fazem a janela navegar.** Um clique abre o endereço no navegador
  do sistema. Quando o texto de um link é o endereço de outro site, o destino
  verdadeiro aparece ao lado.
- **Política de conteúdo (CSP).** A janela é entregue com uma política que só
  deixa rodar os arquivos `.js` do próprio app — nada de script embutido, de
  `eval`, de conexões para fora, de páginas embutidas ou de formulários
  (`pageCSP`, em `backend/scripts.go`). É a segunda barreira, para o caso de um
  erro no tratamento dos textos.
- Caracteres que invertem a direção do texto são retirados de nomes e de nomes
  de arquivo (com o caractere U+202E no meio, `fotos…gnp.exe` apareceria como `fotosexe.png`).

### Protocolo

- **Depois do handshake só valem pacotes cifrados com a chave da sessão.** A
  chave fixa do protocolo (que é pública) só é aceita durante o handshake;
  comandos sem cifra nunca são aceitos; pacotes de início de handshake são
  ignorados com a sessão estabelecida. Sem isso, quem conseguisse mandar
  pacotes em nome do servidor injetaria mensagens e comandos.
- **Limites em tudo o que o servidor manda**: tamanho de cada valor, número de
  itens de um comando, canais, grupos, vozes simultâneas, tamanho das
  mensagens guardadas e do histórico em disco.
- **Transferência de arquivos**: um servidor da internet não consegue mandar o
  app conectar dentro da rede do usuário (o roteador, outro computador, o
  próprio computador), e a chave que o app escreve na conexão tem de ter a cara
  de uma chave.
- Uma pane ao tratar um pacote encerra a sessão com um aviso; não derruba o app.
- Os analisadores que recebem bytes de fora têm testes de fuzzing (`go test
  -fuzz`): descompressão, comandos, licença, identidade importada, WAV,
  sinalização de tela e os pacotes crus em três momentos da sessão.

### Scripts

Ver [scripts.md](scripts.md). Em resumo: cada script roda num Web Worker sem
rede (nem por `import()`), fala com o app por uma lista fechada de ações
conferidas no backend, só modera se o usuário marcar "Pode moderar" e **não
consegue abrir o microfone** (só mutar). O que o script escreve no registro é
texto puro.

Isso limita o estrago, mas um script ligado **age em seu nome**: lê as
mensagens que você recebe (inclusive as privadas) e pode mandar mensagens a
qualquer pessoa do servidor. Só ligue código que você entende.

### Arquivos locais e o app em si

- A janela não lê nem grava arquivos por conta própria: abrir, salvar e
  importar passam sempre pelo seletor de arquivos do sistema.
- Identidades, ajustes, histórico e scripts são gravados com permissão só para
  o usuário (`0600`).
- O app que vai para os usuários não tem ganchos de desenvolvimento: nenhuma
  variável de ambiente faz a janela executar código (isso deixaria outro
  programa usar o microfone e a captura de tela liberados para o Furito TS).
  Eles só existem em builds feitos com `FURITO_TAGS=devhooks`, que não devem
  ser distribuídos.

## O que fica de fora

- **O servidor não é autenticado.** O protocolo do TeamSpeak não dá ao cliente
  um jeito de saber que o servidor é o mesmo de ontem, e o app (como o cliente
  oficial) não guarda a identidade dos servidores para avisar quando ela muda.
  Alguém no caminho da rede, **no momento da conexão**, consegue se passar pelo
  servidor: ler e alterar voz e texto e receber o resumo da senha do servidor.
  Não consegue entrar no servidor de verdade com a sua identidade. Guardar a
  identidade de cada servidor e avisar quando mudar está no
  [roadmap](roadmap.md).
- **Voz sem cifra.** O servidor decide, por canal, se a voz vai cifrada. Onde
  não vai, quem está no caminho da rede a ouve — vale para qualquer cliente.
- **Compartilhamento de tela ponto a ponto** mostra o IP de cada participante
  ao outro, a menos que se configure um servidor de retransmissão. O app avisa
  antes da primeira vez. Ver
  [compartilhamento-de-tela.md](compartilhamento-de-tela.md).
- **Imagens liberadas** (um clique, ou um servidor liberado) mostram o seu IP
  ao site que as hospeda. É o comportamento do cliente original para todas as
  imagens.
- **Quem já roda programas com o seu usuário** lê os arquivos do app: as
  identidades ficam em disco sem senha, e a senha do servidor de retransmissão
  (TURN), se configurada, fica nos ajustes.
- **Arquivos de áudio** que o WAV próprio não lê passam pelo `afconvert` do
  macOS ou pelo `ffmpeg`, se instalado: uma falha num deles estaria fora do app.
- O porte para **Windows** ainda não foi validado em uso (ver
  [windows.md](windows.md)); as verificações desta página foram feitas no
  macOS e, para a interface, também no Chrome (mesmo motor do WebView2).

## Como conferir

```sh
make test                                   # inclui os testes de segurança
go test ./ts3/ -run '^$' -fuzz FuzzHandlePacket -fuzztime 60s
go test ./ts3/ -run '^$' -fuzz FuzzHandleCommand -fuzztime 60s
```

O auto-teste da interface (`frontend/index.html?mock&demo=selftest`, ver
[desenvolvimento.md](desenvolvimento.md)) tem um bloco de verificações de
segurança (as que começam com `sec`): textos hostis, imagens, links, tempo do
analisador e os limites de janelas e abas.
