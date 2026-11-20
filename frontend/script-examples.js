// ═══════════════ EXEMPLOS: todas as funções dos scripts ═══════════════
//
// Este script vem com o app e mostra, funcionando, tudo o que um script pode
// fazer. Ligado, ele:
//
//   1. escreve no registro (aqui embaixo) cada coisa que acontece no servidor;
//   2. atende comandos que OUTRAS pessoas escrevem no bate-papo do canal ou
//      numa mensagem privada para você — peça para alguém mandar !ajuda
//      (as suas próprias mensagens não contam);
//   3. mostra os temporizadores.
//
// Use como consulta: copie os trechos que servirem para os seus próprios
// scripts ("Novo…" → "Em branco"). A lista resumida está em "Referência…".

// ───────────────────────────────────────────────────────────── ajustes

// Dar boas-vindas, no bate-papo do canal, a quem entra no seu canal?
const BOAS_VINDAS = false;

// Quem pode usar os comandos que mexem em VOCÊ (tocar som, ausente, mutar,
// trocar de canal, cutucar…) e os de moderação. São os identificadores (uid)
// das pessoas, não os apelidos — um apelido qualquer um pode pegar. Cada um
// descobre o seu mandando !eu. Vazio = ninguém.
//   exemplo: const CONFIANCA = ['AbCdEf0123456789abcdefghijk='];
const CONFIANCA = [];

// Os comandos de moderação (!mover, !expulsar, !fora, !banir, !palavra,
// !calar) ainda precisam de "Pode moderar" marcado neste script, aqui em
// cima, e de você ter essas permissões no servidor.

// ───────────────────────────────────────────────────────────── utilidades

const nomeDe = (canal) => (canal && canal.name) || '?';
const apelidos = (lista) => lista.map((c) => c.nickname).join(', ') || 'ninguém';

// Responde por onde a mensagem veio: em privado ou no bate-papo do canal.
function responder(e, texto) {
  return e.target === 'private' ? ts.sendPrivateMessage(e.client, texto) : ts.sendChannelMessage(texto);
}

// Acha no servidor a pessoa cujo apelido é (ou começa com) o texto.
async function pessoa(texto) {
  const alvo = String(texto || '').toLowerCase();
  if (!alvo) throw new Error('faltou o apelido');
  const outros = (await ts.getClients()).filter((c) => !c.self);
  const achou = outros.find((c) => c.nickname.toLowerCase() === alvo) || outros.find((c) => c.nickname.toLowerCase().startsWith(alvo));
  if (!achou) throw new Error('não achei ninguém chamado "' + texto + '"');
  return achou;
}

// Acha o canal cujo nome é (ou começa com) o texto.
async function canal(texto) {
  const alvo = String(texto || '').toLowerCase();
  if (!alvo) throw new Error('faltou o nome do canal');
  const canais = await ts.getChannels();
  const achou = canais.find((c) => c.name.toLowerCase() === alvo) || canais.find((c) => c.name.toLowerCase().startsWith(alvo));
  if (!achou) throw new Error('não achei o canal "' + texto + '"');
  return achou;
}

// ───────────────────────────────────────────────────────────── 1. eventos
// ts.on(evento, função): a função recebe "e", com os dados do que aconteceu.

ts.on('connected', (e) => ts.log('connected: conectei ao servidor', e.server));
ts.on('disconnected', (e) => ts.log('disconnected: saí do servidor.', e.reason || ''));

// alguém entrou no SEU canal — e.reason: connected, switched, moved ou kicked
ts.on('clientJoined', (e) => {
  ts.log('clientJoined:', e.client.nickname, 'entrou em', nomeDe(e.channel), e.from ? '(veio de ' + nomeDe(e.from) + ')' : '(acabou de conectar)');
  if (BOAS_VINDAS) ts.sendChannelMessage('Bem-vindo, ' + e.client.nickname + '!');
});

// alguém saiu do SEU canal — e.reason: switched, moved, kicked, disconnected, lost, server-kick ou banned
ts.on('clientLeft', (e) => ts.log('clientLeft:', e.client.nickname, 'saiu de', nomeDe(e.channel), '(' + e.reason + (e.invoker ? ', por ' + e.invoker : '') + ')'));

// alguém conectou ao servidor ou saiu dele, em qualquer canal que você enxerga
ts.on('clientConnected', (e) => ts.log('clientConnected:', e.client.nickname, 'conectou, no canal', nomeDe(e.channel)));
ts.on('clientDisconnected', (e) => ts.log('clientDisconnected:', e.client.nickname, 'saiu do servidor (' + e.reason + ')'));

// você mudou de canal (por conta própria, movido ou expulso do canal)
ts.on('channelChanged', (e) => ts.log('channelChanged: agora estou em', nomeDe(e.channel), '(' + e.reason + ')'));

ts.on('poke', (e) => ts.log('poke:', e.client.nickname, 'me cutucou:', e.text));

// e.target: private, channel ou server
ts.on('message', (e) => ts.log('message (' + e.target + '):', e.client.nickname + ':', e.text));

// ts.off tira uma função de um evento: este aviso só vale para a primeira mensagem
function primeiraMensagem(e) {
  ts.log('ts.off: primeira mensagem desde que o script ligou (de ' + e.client.nickname + '); não aviso de novo');
  ts.off('message', primeiraMensagem);
}
ts.on('message', primeiraMensagem);

// ───────────────────────────────────────────────────────────── 2. comandos
// Cada comando usa uma função do ts. As ações devolvem uma Promise: com
// "await" o erro (não conectado, sem permissão, limite de mensagens…) cai no
// catch lá embaixo.

const AJUDA = 'Comandos: !ajuda · !eu · !hora · !quem · !canais · !sons — ' +
  'só para a lista de confiança: !som Nome · !somlocal Nome · !parar · !ausente [mensagem] · !voltei · !mudo · !surdo · ' +
  '!ir Canal · !cutucar Apelido [texto] · !aviso Texto — ' +
  'moderação: !mover Apelido Canal · !expulsar Apelido [motivo] · !fora Apelido [motivo] · !banir Apelido Minutos [motivo] · !palavra Apelido · !calar Apelido';

// qualquer pessoa pode usar
const livres = {
  ajuda: (e) => ts.sendPrivateMessage(e.client, AJUDA),
  eu: (e) => ts.sendPrivateMessage(e.client, 'Você é ' + e.client.nickname + ' · uid: ' + e.client.uid + ' · grupos: ' + (e.client.serverGroups.join(', ') || 'nenhum')),
  hora: (e) => responder(e, 'Agora são ' + new Date().toLocaleTimeString()),
  async quem(e) {
    const eu = await ts.getSelf();
    const aqui = (await ts.getClients()).filter((c) => c.channelId === eu.channelId);
    return responder(e, 'No canal ' + eu.channelName + ': ' + apelidos(aqui));
  },
  async canais(e) {
    return responder(e, ('Canais: ' + (await ts.getChannels()).map((c) => c.name).join(', ')).slice(0, 900));
  },
  async sons(e) {
    return responder(e, 'Sons do soundboard: ' + ((await ts.getSounds()).join(', ') || 'nenhum'));
  },
};

// só para quem está em CONFIANCA
const reservados = {
  // sons do soundboard, pelo nome
  som: (e, resto) => ts.playSound(resto),            // todos no canal ouvem
  somlocal: (e, resto) => ts.playSoundLocal(resto),  // só você ouve
  parar: () => ts.stopSound(),

  // o seu próprio estado
  ausente: (e, resto) => ts.setAway(true, resto),
  voltei: () => ts.setAway(false),
  // um script só fecha o microfone e os alto-falantes; abrir é sempre com você
  mudo: () => ts.setMuted(true),
  surdo: () => ts.setDeafened(true),
  ir: async (e, resto) => ts.switchChannel(await canal(resto)),

  // falar com os outros
  async cutucar(e, resto) {
    const [quem, ...texto] = resto.split(' ');
    return ts.poke(await pessoa(quem), texto.join(' ') || 'ei!');
  },
  aviso: (e, resto) => ts.sendServerMessage(resto),

  // moderação (precisa de "Pode moderar")
  async mover(e, resto) {
    const [quem, ...onde] = resto.split(' ');
    return ts.moveClient(await pessoa(quem), await canal(onde.join(' ')));
  },
  async expulsar(e, resto) {
    const [quem, ...motivo] = resto.split(' ');
    return ts.kickFromChannel(await pessoa(quem), motivo.join(' '));
  },
  async fora(e, resto) {
    const [quem, ...motivo] = resto.split(' ');
    return ts.kickFromServer(await pessoa(quem), motivo.join(' '));
  },
  async banir(e, resto) {
    const [quem, tempo, ...motivo] = resto.split(' ');
    const minutos = Number(tempo === undefined || tempo === '' ? NaN : tempo);
    if (!(minutos >= 0)) throw new Error('uso: !banir Apelido Minutos [motivo] (0 minutos = para sempre)');
    return ts.ban(await pessoa(quem), Math.round(minutos * 60), motivo.join(' '));
  },
  palavra: async (e, resto) => ts.setTalker(await pessoa(resto), true),
  calar: async (e, resto) => ts.setTalker(await pessoa(resto), false),
};

ts.on('message', async (e) => {
  if (e.target === 'server' || !e.text.startsWith('!')) return;
  const [nome, ...partes] = e.text.slice(1).trim().split(/\s+/);
  const comando = nome.toLowerCase();
  const resto = partes.join(' ');
  try {
    if (Object.hasOwn(livres, comando)) {
      await livres[comando](e, resto);
    } else if (Object.hasOwn(reservados, comando)) {
      if (!CONFIANCA.includes(e.client.uid)) throw new Error('é só para quem está na lista de confiança de quem roda o script');
      await reservados[comando](e, resto);
      ts.log('comando de', e.client.nickname + ':', e.text);
    }
  } catch (erro) {
    // não deu (ou a pessoa não pode): fica no registro e a pessoa é avisada em privado
    console.warn('!' + comando, 'de', e.client.nickname, 'não foi atendido:', erro.message);
    ts.sendPrivateMessage(e.client, '!' + comando + ': ' + erro.message).catch(() => {});
  }
});

// ───────────────────────────────────────────────────────────── 3. tempo

// uma vez, daqui a tantos segundos
ts.after(3, async () => {
  const eu = await ts.getSelf();
  ts.log('ts.after: 3 s depois de ligar.', eu.connected ? 'Estou em "' + eu.server + '", no canal ' + eu.channelName + '.' : 'Ainda não conectei a um servidor.');
});

// de tanto em tanto tempo; ts.cancel para (vale também para after e at)
let minutos = 0;
const relogio = ts.every(60, async () => {
  minutos++;
  const eu = await ts.getSelf();
  if (eu.connected) ts.log('ts.every: minuto ' + minutos + ';', (await ts.getClients()).length, 'pessoas à vista no servidor');
  if (minutos === 10) {
    ts.cancel(relogio);
    ts.log('ts.cancel: parei de contar depois de 10 minutos');
  }
});

// todo dia, no horário do seu computador
ts.at('12:00', () => ts.log('ts.at: meio-dia!'));

// ───────────────────────────────────────────────────────────── começo

ts.log('exemplos ligados: peça para alguém mandar !ajuda no bate-papo do canal');
if (!CONFIANCA.length) console.warn('lista de confiança vazia: só os comandos livres funcionam (!ajuda, !eu, !hora, !quem, !canais, !sons)');
