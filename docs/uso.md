# Uso diário e administração

O que o app faz além da voz: atalhos, identidades, moderação, arquivos, ícones
e onde ficam os dados.

## Recursos do dia a dia

- **Apertar para falar e atalhos globais** (`hotkey/`): funcionam com o app em
  segundo plano, pelos atalhos globais do macOS (não pedem permissão de
  Acessibilidade). Limite do sistema: precisa ser uma tecla de função ou uma
  combinação com Ctrl/Alt/Cmd — tecla modificadora sozinha e botão do mouse
  não dá.
- **Dispositivos de áudio** escolhidos por nome; **volume por pessoa**, "mutar
  só para mim" e **contatos** (amigo/bloqueado) ficam guardados pela
  identidade de cada pessoa.
- **Favoritos** e **reconexão automática**: quando a conexão cai, tenta de
  novo com espera crescente e volta ao canal em que você estava; não insiste
  se você foi expulso ou banido.
- **Identidades**: várias, como no TeamSpeak 3 (Ajustes → Identidades): criar,
  renomear, escolher a padrão, apagar, exportar e importar — também o `.ini`
  exportado pelo cliente TeamSpeak 3 (Ferramentas → Identidades), de onde vêm o
  nome e o apelido. A janela de conexão e cada favorito podem usar uma
  identidade diferente da padrão. Servidores TeamSpeak 6 mostram um UID mais
  longo (SHA-256 da mesma chave) que o do TS3 (SHA-1); os Ajustes mostram os
  dois. Também: aumentar o nível de segurança e usar chave de privilégio.
- **Sobre** (botão "i" da barra, ao lado dos Ajustes, ou Ajuda → Sobre): a
  versão do app, o autor, a licença e os créditos.
- **Versão**: entre clientes Furito a versão aparece como "1.0.1 Diogo Agra".
  Para o servidor e para os clientes oficiais continua "3.?.? [Build:
  5680278000] Linux": o servidor confere a assinatura da TeamSpeak sobre a
  versão e recusa qualquer outro texto ("client is modified").
- **Histórico** das conversas de canal e de servidor é
  salvo por servidor em `~/Library/Application Support/FuritoTS/history/`.
- **Moderação**: mover, expulsar (canal/servidor), banir, cutucar, dar e pedir
  a palavra. Uma cutucada recebida abre uma janela, toca o aviso sonoro e
  aparece no registro do servidor; em Ajustes → Conexão dá para desligar a
  janela e ficar só com o som e o registro. **Canais**: criar, editar, apagar. **Grupos**: atribuir grupos de
  servidor e de canal; ver e editar as permissões de um grupo.
- **Sussurro** para um cliente ou um canal; **arquivos do canal** (listar,
  baixar, enviar, criar pasta, apagar).

Cada ação espera a confirmação do servidor e mostra o erro dele (por exemplo
"permissão insuficiente") em vez de falhar em silêncio.

As janelas internas (Ajustes, Favoritos, cutucada, permissões…) são painéis
dentro da janela do app: arrastam pelo título, sempre cabem na janela (o miolo
rola; título e botões ficam à vista) e voltam ao centro a cada abertura.

## Ícones, avatares e descrições

- Os ícones à direita do apelido (grupo do canal, grupos do servidor, ícone do
  cliente) são imagens guardadas no servidor, baixadas pela transferência de
  arquivos do TeamSpeak (`ts3/filetransfer.go`, porta TCP 30033 por padrão) e
  guardadas em `~/Library/Application Support/FuritoTS/icons/`.
- Descrições de canal mostram `[img]` como imagem, além de `[center]`,
  `[size]`, `[hr]` e listas. Buscar uma imagem mostra o seu endereço IP ao
  site que a hospeda, então ela não carrega sozinha: aparece um marcador
  ("Imagem de *site* — clique para carregar"), e "Carregar sempre as imagens
  deste servidor" libera o servidor de uma vez. Em Ajustes → Conexão →
  *Imagens de fora* dá para carregar sempre (como o cliente original) ou
  nunca. Em mensagens e cutucadas, `[img]` vira só o endereço. Os motivos
  estão em [seguranca.md](seguranca.md).
- **Avatares**: quem tem um avatar aparece com ele nas informações do cliente
  (bloco "Avatar", desenhado como as imagens das descrições). O servidor avisa
  que o cliente tem avatar por uma marca (`client_flag_avatar`, o MD5 do
  arquivo); a imagem é baixada do servidor, pela transferência de arquivos,
  só quando as informações da pessoa são abertas, e fica guardada em
  `avatars/` na pasta de dados — quando a pessoa troca o avatar, a marca muda
  e a imagem nova é buscada. O app só mostra avatares; ainda não tem como
  definir o seu.

## Dados locais

- Identidades: `~/.ts_furito_identity.json` (não apague: são as chaves que os servidores usam para reconhecer você). O arquivo do formato antigo, de uma identidade só, é convertido na primeira abertura e fica copiado em `~/.ts_furito_identity.json.v1`.
- Registro: `furito.log` na mesma pasta dos ajustes (no Windows,
  `%AppData%\FuritoTS\furito.log`): passos da abertura, erros e panes.
  Se o app não abrir, o motivo está nele; no Windows também aparece numa caixa
  de mensagem.
- Scripts: `scripts.json`, na mesma pasta dos ajustes (o código de cada script e se está ligado).
- Ajustes: `~/Library/Application Support/FuritoTS/settings.json`. Não guarda senhas de servidor nem de canal; guarda, se configurada, a senha do servidor de retransmissão (TURN) do compartilhamento de tela.
