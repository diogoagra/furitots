/*
 * scripts.js — pequenas automações escritas pelo usuário em JavaScript.
 *
 * Cada script ligado roda no seu próprio Web Worker (script-worker.js),
 * isolado da janela e da rede. Este arquivo é o intermediário: cria e encerra
 * os workers, entrega a eles os eventos do backend ("script:event") e repassa
 * ao backend o que eles pedem (ScriptCall), onde cada pedido é conferido.
 *
 * Uso:  FuritoScripts.init({ call, on, notify })   call(nome, ...args) → Promise; on(evento, cb); notify(nível, texto)
 *       FuritoScripts.list() / save(def) / remove(id) / setEnabled(id, ligado) / restart(id)
 *       FuritoScripts.onChange(cb)   avisa quando a lista, um estado ou um registro muda
 */
(function () {
  'use strict';

  const MAX_LOG = 300;       // linhas de registro guardadas por script
  const PING_MS = 4000;      // de quanto em quanto tempo cada script é chamado
  const HUNG_MS = 12000;     // sem resposta por esse tempo = travado (laço sem fim)
  const NOTIFY_GAP = 15000;  // no máximo um erro por script no registro do servidor nesse intervalo

  let deps = null;
  const entries = new Map(); // id → { def, worker, status, error, log, lastPong, lastNotify }
  const listeners = new Set();
  let lastTick = 0;

  const errText = (e) => String((e && e.message) || e || 'erro desconhecido').replace(/^Error: /, '');
  // Avisa a interface de que algo mudou — uma vez só por rodada, mesmo que
  // cheguem muitas linhas de registro de uma vez.
  let changeQueued = false;
  function changed() {
    if (changeQueued) return;
    changeQueued = true;
    setTimeout(() => {
      changeQueued = false;
      listeners.forEach((cb) => { try { cb(); } catch (e) { /* um erro de desenho não pode parar os scripts */ } });
    }, 0);
  }

  function addLog(entry, level, text) {
    const last = entry.log[entry.log.length - 1];
    if (last && last.level === level && last.text === text) { last.count++; last.time = Date.now(); }
    else {
      entry.log.push({ time: Date.now(), level, text, count: 1 });
      if (entry.log.length > MAX_LOG) entry.log.splice(0, entry.log.length - MAX_LOG);
    }
    changed();
  }

  // erro de script também aparece no registro do servidor (sem inundá-lo)
  function notifyError(entry, text) {
    const now = Date.now();
    if (now - (entry.lastNotify || 0) < NOTIFY_GAP) return;
    entry.lastNotify = now;
    if (deps.notify) deps.notify('error', 'Script "' + entry.def.name + '": ' + text);
  }

  function stop(entry) {
    if (entry.worker) { try { entry.worker.terminate(); } catch (e) { /* já encerrado */ } }
    entry.worker = null;
  }

  function halt(entry, text) {
    stop(entry);
    entry.status = 'error';
    entry.error = text;
    addLog(entry, 'error', text);
    notifyError(entry, text);
  }

  function start(entry) {
    stop(entry);
    entry.error = '';
    if (!entry.def.enabled) { entry.status = 'off'; changed(); return; }
    let w;
    try { w = new Worker('script-worker.js'); } catch (e) { halt(entry, 'não foi possível iniciar o script: ' + errText(e)); return; }
    entry.worker = w;
    entry.status = 'starting';
    entry.lastPong = Date.now();
    w.addEventListener('message', (e) => { if (entry.worker === w) onMessage(entry, w, e.data || {}); });
    w.addEventListener('error', (e) => {
      if (entry.worker !== w) return;
      if (e.preventDefault) e.preventDefault();
      halt(entry, 'não foi possível iniciar o script' + (e.message ? ': ' + e.message : ''));
    });
    changed();
  }

  // O que chega do worker é tratado como vindo do script (que é código de
  // terceiros): os limites de tamanho e de ritmo valem aqui, na página, e não
  // só dentro do worker.
  const MAX_TEXT = 2000;       // caracteres de uma linha de registro ou de erro
  const MAX_ARGS = 16 * 1024;  // caracteres dos argumentos de uma chamada, em JSON
  const MAX_PENDING = 64;      // chamadas em andamento por script
  const MSG_BURST = 60, MSG_PER_SEC = 20; // linhas de registro: rajada e ritmo
  const clip = (v) => { const t = String(v == null ? '' : v); return t.length > MAX_TEXT ? t.slice(0, MAX_TEXT) + '…' : t; };

  // allowLine: ritmo das linhas de registro de um script (as que passam do
  // limite são contadas e viram uma linha só).
  function allowLine(entry) {
    const now = Date.now();
    if (entry.msgAt === undefined) { entry.msgAt = now; entry.msgTokens = MSG_BURST; entry.msgDropped = 0; }
    entry.msgTokens = Math.min(MSG_BURST, entry.msgTokens + (now - entry.msgAt) / 1000 * MSG_PER_SEC);
    entry.msgAt = now;
    if (entry.msgTokens < 1) { entry.msgDropped++; return false; }
    entry.msgTokens--;
    if (entry.msgDropped) {
      addLog(entry, 'warn', entry.msgDropped + ' linha(s) descartada(s): o script escreveu demais em pouco tempo');
      entry.msgDropped = 0;
    }
    return true;
  }

  function onMessage(entry, w, m) {
    if (!m || typeof m !== 'object') return;
    switch (m.t) {
      case 'ready':
        w.postMessage({ t: 'init', code: entry.def.code });
        break;
      case 'started':
        entry.status = 'running';
        addLog(entry, 'info', 'script iniciado');
        break;
      case 'call': {
        const reply = (msg) => { if (entry.worker === w) w.postMessage(Object.assign({ t: 'result', n: m.n }, msg)); };
        let args = m.args, size = 0;
        if (args == null) args = {};
        try { size = JSON.stringify(args).length; } catch (e) { size = Infinity; }
        if (typeof args !== 'object' || Array.isArray(args) || size > MAX_ARGS) { reply({ ok: false, error: 'argumentos inválidos ou grandes demais' }); break; }
        if ((entry.pending || 0) >= MAX_PENDING) { reply({ ok: false, error: 'muitas chamadas ao mesmo tempo' }); break; }
        entry.pending = (entry.pending || 0) + 1;
        const done = () => { entry.pending = Math.max(0, (entry.pending || 1) - 1); };
        deps.call('ScriptCall', entry.def.id, String(m.action).slice(0, 64), args).then(
          (value) => { done(); reply({ ok: true, value: value === undefined ? null : value }); },
          (err) => { done(); reply({ ok: false, error: errText(err) }); });
        break;
      }
      case 'log':
        if (allowLine(entry)) addLog(entry, m.level === 'warn' || m.level === 'error' ? m.level : 'info', clip(m.text));
        break;
      case 'error':
        if (m.fatal) halt(entry, clip(m.text)); // não chegou a rodar (ou parou logo no começo)
        else if (allowLine(entry)) { addLog(entry, 'error', clip(m.text)); notifyError(entry, clip(m.text)); }
        break;
      case 'pong':
        entry.lastPong = Date.now();
        break;
      default: break;
    }
  }

  // Um script preso num laço sem fim não responde: é encerrado.
  function watchdog() {
    const now = Date.now();
    const late = lastTick && now - lastTick > PING_MS * 2; // o computador dormiu ou a janela ficou parada: não é culpa do script
    lastTick = now;
    entries.forEach((entry) => {
      if (!entry.worker) return;
      if (late) entry.lastPong = now;
      else if (now - entry.lastPong > HUNG_MS) { halt(entry, 'o script parou de responder (laço sem fim?) e foi encerrado'); return; }
      entry.worker.postMessage({ t: 'ping' });
    });
  }

  function dispatch(event) {
    entries.forEach((entry) => {
      if (entry.worker && entry.status === 'running') entry.worker.postMessage({ t: 'event', event });
    });
  }

  function adopt(def) {
    let entry = entries.get(def.id);
    if (!entry) { entry = { def, worker: null, status: 'off', error: '', log: [], lastPong: 0, lastNotify: 0 }; entries.set(def.id, entry); }
    entry.def = def;
    return entry;
  }

  const view = (entry) => ({
    id: entry.def.id, name: entry.def.name, code: entry.def.code, enabled: !!entry.def.enabled, moderation: !!entry.def.moderation,
    status: entry.status, error: entry.error,
  });

  // ------------------------------------------------------------ exemplos e referência
  const TEMPLATES = [
    {
      name: 'Boas-vindas no canal',
      code: [
        '// Dá as boas-vindas, no bate-papo do canal, a quem entra no seu canal.',
        "ts.on('clientJoined', (e) => {",
        "  ts.sendChannelMessage('Bem-vindo, ' + e.client.nickname + '!');",
        '});',
        '',
      ].join('\n'),
    },
    {
      name: 'Som quando alguém entra',
      code: [
        '// Toca um som do soundboard quando alguém entra no seu canal.',
        '// Troque pelo nome de um som que você adicionou ao soundboard.',
        "const SOM = 'Vinheta';",
        '',
        "ts.on('clientJoined', (e) => {",
        '  ts.playSound(SOM);         // todos no canal ouvem',
        '  // ts.playSoundLocal(SOM); // só você ouve',
        '});',
        '',
      ].join('\n'),
    },
    {
      name: 'Mensagem programada',
      code: [
        '// Manda uma mensagem no canal todo dia num horário, e outra de tempos em tempos.',
        "ts.at('20:00', () => {",
        "  ts.sendChannelMessage('São 20h: hora do evento!');",
        '});',
        '',
        'ts.every(30 * 60, () => {   // a cada 30 minutos',
        "  ts.sendChannelMessage('Lembrete: as regras estão na descrição do canal.');",
        '});',
        '',
      ].join('\n'),
    },
    {
      name: 'Responder a comandos',
      code: [
        '// Responde a quem escreve um comando no bate-papo do canal.',
        "ts.on('message', async (e) => {",
        "  if (e.target !== 'channel') return;",
        "  if (e.text === '!hora') {",
        "    ts.sendChannelMessage('Agora são ' + new Date().toLocaleTimeString());",
        "  } else if (e.text === '!quem') {",
        '    const eu = await ts.getSelf();',
        '    const aqui = (await ts.getClients()).filter((c) => c.channelId === eu.channelId);',
        "    ts.sendChannelMessage('No canal: ' + aqui.map((c) => c.nickname).join(', '));",
        '  }',
        '});',
        '',
      ].join('\n'),
    },
    {
      name: 'Mover ausentes para o AFK',
      code: [
        '// A cada minuto, move para o canal AFK quem está ausente no seu canal.',
        '// Precisa de "Pode moderar" marcado e de você poder mover pessoas no servidor.',
        "const CANAL_AFK = 'AFK';",
        '',
        'ts.every(60, async () => {',
        '  const eu = await ts.getSelf();',
        '  if (!eu.connected) return;',
        '  const afk = (await ts.getChannels()).find((c) => c.name === CANAL_AFK);',
        '  if (!afk || afk.id === eu.channelId) return;',
        '  for (const c of await ts.getClients()) {',
        '    if (c.channelId === eu.channelId && c.away && !c.self) await ts.moveClient(c, afk);',
        '  }',
        '});',
        '',
      ].join('\n'),
    },
    {
      name: 'Expulsar por palavra proibida',
      code: [
        '// Expulsa do canal quem escrever uma palavra proibida no bate-papo do canal.',
        '// Precisa de "Pode moderar" marcado e de você poder expulsar pessoas no servidor.',
        "const PROIBIDAS = ['palavra1', 'palavra2'];",
        '',
        "ts.on('message', async (e) => {",
        "  if (e.target !== 'channel') return;",
        '  const texto = e.text.toLowerCase();',
        '  if (!PROIBIDAS.some((p) => texto.includes(p))) return;',
        "  await ts.kickFromChannel(e.client, 'linguagem');",
        "  ts.sendChannelMessage(e.client.nickname + ' saiu do canal: olha a linguagem.');",
        '});',
        '',
      ].join('\n'),
    },
    // o script que já vem na lista, com todas as funções (o código fica em script-examples.js)
    { name: 'Exemplos: todas as funções', file: 'script-examples.js' },
  ];

  // Devolve o código de um exemplo (alguns ficam em arquivo próprio).
  async function templateCode(t) {
    if (!t.file) return t.code;
    const r = await fetch(t.file);
    if (!r.ok) throw new Error('não foi possível ler ' + t.file);
    return r.text();
  }

  // [título, [[assinatura, explicação], …]]
  const REFERENCE = [
    ['Eventos — ts.on(evento, (e) => { … })', [
      ["'clientJoined'", 'alguém entrou no seu canal. e.client, e.channel, e.from, e.reason (connected, switched, moved, kicked), e.invoker'],
      ["'clientLeft'", 'alguém saiu do seu canal. e.client, e.channel, e.to, e.reason (switched, moved, kicked, disconnected, lost, server-kick, banned)'],
      ["'clientConnected'", 'alguém conectou ao servidor (em qualquer canal que você enxerga). e.client, e.channel'],
      ["'clientDisconnected'", 'alguém saiu do servidor. e.client, e.reason'],
      ["'channelChanged'", 'você mudou de canal. e.channel (o novo), e.from, e.reason'],
      ["'message'", 'chegou uma mensagem de outra pessoa. e.text, e.target (private, channel, server), e.client'],
      ["'poke'", 'alguém cutucou você. e.text, e.client'],
      ["'connected' / 'disconnected'", 'você conectou a um servidor (e.server é o nome dele) ou saiu'],
    ]],
    ['Ações (devolvem uma Promise; falham com um erro explicando o motivo)', [
      ['ts.sendChannelMessage(texto)', 'mensagem no bate-papo do seu canal'],
      ['ts.sendPrivateMessage(cliente, texto)', 'mensagem privada (cliente: e.client ou o id)'],
      ['ts.sendServerMessage(texto)', 'mensagem no bate-papo do servidor'],
      ['ts.poke(cliente, texto)', 'cutuca alguém'],
      ['ts.playSound(nome)', 'toca um som do soundboard para o canal (como um clique nele)'],
      ['ts.playSoundLocal(nome)', 'toca um som do soundboard só para você'],
      ['ts.stopSound()', 'para o soundboard'],
      ['ts.setAway(ausente, mensagem)', 'liga ou desliga o status ausente'],
      ['ts.setMuted(true) / ts.setDeafened(true)', 'muta o microfone / os alto-falantes (um script não desmuta: abrir o microfone é sempre com você)'],
      ['ts.switchChannel(canal, senha)', 'entra em outro canal (canal: o id ou um item de getChannels)'],
    ]],
    ['Moderação (o script precisa de "Pode moderar" marcado; valem as suas permissões no servidor)', [
      ['ts.moveClient(cliente, canal, senha)', 'move alguém para outro canal'],
      ['ts.kickFromChannel(cliente, motivo)', 'expulsa do canal (a pessoa volta para o canal padrão)'],
      ['ts.kickFromServer(cliente, motivo)', 'expulsa do servidor'],
      ['ts.ban(cliente, segundos, motivo)', 'bane do servidor por esse tempo; a duração é obrigatória (0 = permanente)'],
      ['ts.setTalker(cliente, true ou false)', 'dá ou tira a palavra num canal moderado'],
    ]],
    ['Consultas (use com await)', [
      ['await ts.getSelf()', '{ connected, id, nickname, channelId, channelName, server, muted, deafened, away }'],
      ['await ts.getClients()', 'lista de { id, nickname, uid, channelId, away, inputMuted, outputMuted, serverGroups, country, isTalker, talkRequest, self }'],
      ['await ts.getChannels()', 'lista de { id, name, parentId, topic, hasPassword, isDefault }'],
      ['await ts.getSounds()', 'nomes dos sons do soundboard'],
    ]],
    ['Tempo e registro', [
      ['ts.every(segundos, fn)', 'repete a cada tantos segundos (mínimo 1)'],
      ['ts.after(segundos, fn)', 'uma vez, depois de tantos segundos'],
      ["ts.at('HH:MM', fn)", 'todo dia nesse horário'],
      ['ts.cancel(id)', 'cancela um every/after/at (o id é o que eles devolvem)'],
      ['ts.log(…) / console.log(…)', 'escreve no registro do script, nesta janela'],
    ]],
  ];

  window.FuritoScripts = {
    TEMPLATES,
    templateCode,
    REFERENCE,

    // Carrega os scripts guardados e liga os que estão marcados como ligados.
    async init(d) {
      deps = d;
      deps.on('script:event', dispatch);
      setInterval(watchdog, PING_MS);
      const list = await deps.call('GetScripts');
      (Array.isArray(list) ? list : []).forEach((def) => start(adopt(def)));
      changed();
    },

    list: () => Array.from(entries.values()).map(view),
    get: (id) => { const e = entries.get(id); return e ? view(e) : null; },
    logOf: (id) => { const e = entries.get(id); return e ? e.log : []; },
    clearLog(id) { const e = entries.get(id); if (e) { e.log = []; changed(); } },
    onChange(cb) { listeners.add(cb); return () => listeners.delete(cb); },

    // Cria (sem id) ou atualiza um script; reinicia-o com o código novo.
    async save(def) {
      const saved = await deps.call('SaveScript', { id: def.id || '', name: def.name, code: def.code, enabled: !!def.enabled, moderation: !!def.moderation });
      start(adopt(saved));
      return view(entries.get(saved.id));
    },

    async setEnabled(id, on) {
      const entry = entries.get(id);
      if (!entry) return;
      await deps.call('SetScriptEnabled', id, !!on);
      entry.def = Object.assign({}, entry.def, { enabled: !!on });
      start(entry);
    },

    restart(id) { const e = entries.get(id); if (e) start(e); },

    async remove(id) {
      const entry = entries.get(id);
      if (!entry) return;
      await deps.call('DeleteScript', id);
      stop(entry);
      entries.delete(id);
      changed();
    },
  };
})();
