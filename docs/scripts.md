# Scripts (automações)

Pequenas automações escritas em JavaScript, na janela **Scripts** (botão
`</>` da barra, ou Conexões → Scripts…): mandar uma mensagem ou tocar um som
quando alguém entra no canal, responder a comandos do bate-papo, avisar num
horário, mover quem está ausente para o canal AFK. "Novo…" traz exemplos
prontos para adaptar; marcado como "Ligado", o script roda sempre que o app
está aberto.

```js
// boas-vindas a quem entra no seu canal, com um som para todos
ts.on('clientJoined', (e) => {
  ts.sendChannelMessage('Bem-vindo, ' + e.client.nickname + '!');
  ts.playSound('Vinheta');            // um som do soundboard, pelo nome
});

ts.at('20:00', () => ts.sendChannelMessage('Hora do evento!'));   // todo dia
```

| | |
|---|---|
| **Eventos** `ts.on(evento, (e) => …)` | `clientJoined` e `clientLeft` (alguém entrou/saiu do seu canal: `e.client`, `e.channel`, `e.from`/`e.to`, `e.reason`, `e.invoker`), `clientConnected` e `clientDisconnected` (no servidor), `channelChanged` (você mudou de canal), `message` (`e.text`, `e.target`: `private`/`channel`/`server`, `e.client`), `poke`, `connected`, `disconnected` |
| **Ações** | `ts.sendChannelMessage(texto)`, `ts.sendPrivateMessage(cliente, texto)`, `ts.sendServerMessage(texto)`, `ts.poke(cliente, texto)`, `ts.playSound(nome)` (para o canal, como um clique no soundboard), `ts.playSoundLocal(nome)` (só você ouve), `ts.stopSound()`, `ts.setAway(ausente, mensagem)`, `ts.setMuted(true)`, `ts.setDeafened(true)` (um script muta, mas não desmuta), `ts.switchChannel(canal, senha)` |
| **Moderação** (só com "Pode moderar" marcado no script) | `ts.moveClient(cliente, canal, senha)`, `ts.kickFromChannel(cliente, motivo)`, `ts.kickFromServer(cliente, motivo)`, `ts.ban(cliente, segundos, motivo)` (a duração é obrigatória; 0 = permanente), `ts.setTalker(cliente, true/false)` (dar ou tirar a palavra num canal moderado) |
| **Consultas** (com `await`) | `ts.getSelf()`, `ts.getClients()`, `ts.getChannels()`, `ts.getSounds()` |
| **Tempo e registro** | `ts.every(segundos, fn)`, `ts.after(segundos, fn)`, `ts.at('HH:MM', fn)`, `ts.cancel(id)`, `ts.log(…)` |

A lista completa, com os campos de cada evento, está no botão "Referência…"
da janela.

**Com vários servidores abertos**, cada script ligado roda uma vez para cada
servidor (uma cópia por aba), sem nada em comum entre as cópias. Dentro do
script, `ts` é sempre o servidor daquela cópia: os eventos são os dele, as
consultas e as ações vão para ele, e os temporizadores (`ts.every`, `ts.at`)
disparam em cada servidor. O que o script guarda em variáveis também é de
cada servidor — um contador de entradas conta as daquele servidor. No
registro do script, cada linha diz de que servidor veio. Duas coisas são do
app inteiro e não de um servidor: `ts.playSound` sai pelo microfone, então só
funciona na cópia do servidor em que você fala (nas outras devolve erro; use
`ts.playSoundLocal`), e mutar e ausente valem para todos os servidores.

A lista já começa com o script **"Exemplos: todas as funções"**
(`frontend/script-examples.js`), desligado: é uma consulta que roda de
verdade. Ligado, ele escreve no registro cada evento do servidor, mostra os
temporizadores e atende comandos que outras pessoas mandam no bate-papo do
canal ou em privado — um comando para cada função:

- qualquer pessoa: `!ajuda`, `!eu` (mostra o uid de quem pediu), `!hora`,
  `!quem`, `!canais`, `!sons`;
- só quem está na lista `CONFIANCA` do script (por uid; vem vazia): `!som`,
  `!somlocal`, `!parar`, `!ausente`, `!voltei`, `!mudo`, `!surdo`, `!ir Canal`,
  `!cutucar`, `!aviso`;
- moderação, que além da lista pede "Pode moderar" marcado: `!mover`,
  `!expulsar`, `!fora`, `!banir Apelido Minutos`, `!palavra`, `!calar`.

Ele entra na lista uma vez só (também para quem já tinha scripts); editado,
vale a sua versão; apagado, não volta — mas "Novo…" o recria. Um teste
garante que todo evento e toda função oferecidos aos scripts aparecem nele.

Como funciona e o que protege:

- Cada script ligado roda no seu próprio Web Worker dentro da janela do app
  (`frontend/script-worker.js`), usando o motor de JavaScript que o sistema já
  tem — nenhuma dependência a mais. Dali ele não enxerga a janela, os seus
  arquivos nem o resto do app, e **não tem acesso à rede**: o arquivo do
  worker é entregue com uma política de segurança que proíbe qualquer
  conexão ou importação de código, e as funções de rede são removidas antes de
  o script rodar.
- Tudo o que um script faz passa por uma única porta no backend
  (`ScriptCall`, em `backend/scripts.go`), que só conhece as ações da tabela
  acima — não há como um script apagar ou editar canais, mexer em grupos,
  permissões ou arquivos — e confere cada pedido.
- **Moderação é uma permissão por script**: mover, expulsar, banir e dar a
  palavra só funcionam no script em que você marcou "Pode moderar" (a janela
  pede uma confirmação ao marcar; na lista ele ganha a etiqueta "modera"). Sem
  isso o pedido é recusado antes de chegar ao servidor. Com isso, vale o que
  você pode fazer no servidor: se você não tem a permissão, o servidor nega e
  o erro volta para o script. Um script não modera você mesmo, e cada ação
  feita fica anotada no registro do servidor ("Script "nome" expulsou "Fulano"
  do canal (motivo)").
- **Limite contra flood**: mensagens, cutucadas e ações de moderação de
  scripts saem até 4 seguidas e depois uma a cada 5 segundos, somando todos os
  scripts e contando cada servidor à parte (é o servidor que bloqueia quem
  manda comandos demais). O excesso é
  recusado com um erro no registro do script. Sons do soundboard: até 5
  seguidos, depois um a cada 3 segundos.
- **Um script não abre o microfone.** `ts.setMuted(true)` e
  `ts.setDeafened(true)` funcionam; pedir para desmutar é recusado com um
  erro. Ligar o microfone de alguém sem a pessoa pedir é escuta: quem desmuta
  é sempre você, pelo botão ou pelo atalho.
- O que o script escreve no registro (e as mensagens de erro dele) aparece
  como texto puro, com tamanho e ritmo limitados pela própria janela.
- **O que o isolamento não muda**: um script ligado age em seu nome. Ele
  recebe as mensagens que chegam para você (inclusive as privadas) e pode
  mandar mensagens a qualquer pessoa do servidor. "Sem acesso à rede" quer
  dizer que ele não fala com a internet, não que seja inofensivo: só ligue
  código que você entende.
- As suas próprias mensagens não disparam `message` (um script não responde a
  si mesmo); as de contatos bloqueados também não chegam.
- Um script com erro de sintaxe não liga; um erro durante a execução aparece
  no registro da janela (com a linha, quando o motor informa) e, sem inundar,
  no registro do servidor; um script que para de responder — um laço sem fim —
  é encerrado em cerca de 15 segundos, sem travar o app nem os outros scripts.
- Um script age em seu nome (as mensagens saem como suas): só ligue código
  que você entende.
