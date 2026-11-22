/*
 * scripts.js — pequenas automações escritas pelo usuário em JavaScript.
 *
 * Cada script ligado roda no seu próprio Web Worker (script-worker.js),
 * isolado da janela e da rede. Este arquivo é o intermediário: cria e encerra
 * os workers, entrega a eles os eventos do backend ("script:event") e repassa
 * ao backend o que eles pedem (ScriptCall), onde cada pedido é conferido.
 *
 * Com vários servidores abertos (um por aba), cada script roda uma vez para
 * cada servidor: uma instância (um worker) por aba. Assim, dentro do script,
 * "ts" é sempre o servidor daquela instância — os eventos, as consultas, as
 * ações e também os temporizadores —, e o que ele guarda em variáveis não se
 * mistura entre servidores.
 *
 * Uso:  FuritoScripts.init({ call, forTab, on, notify })   call(nome, ...args) → Promise; forTab(aba) → API daquela aba;
 *                                                          on(evento, cb); notify(nível, texto)
 *       FuritoScripts.setTabs([{ id, name }])   as abas abertas (a interface chama a cada mudança)
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
  const entries = new Map(); // id → { def, status, error, log, lastNotify, inst: Map(aba → instância) }
  // instância: { tab, worker, started, lastPong, pending } — o script rodando para um servidor
  const tabNames = new Map([[0, '']]); // as abas em que os scripts rodam: aba → nome do servidor
  let tabsKnown = false;               // até a interface dizer quais abas existem (setTabs), há só "a aba atual" (0)
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

  // callTab chama o backend em nome da instância de uma aba.
  function callTab(tab, name, ...args) {
    return deps.forTab ? Promise.resolve(deps.forTab(tab)[name](...args)) : deps.call(name, ...args);
  }

  // Com mais de um servidor aberto, cada linha do registro diz de qual veio.
  function addLog(entry, level, text, inst) {
    if (inst && tabNames.size > 1) text = '[' + (tabNames.get(inst.tab) || 'aba ' + inst.tab) + '] ' + text;
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

  function stopInstance(entry, tab) {
    const inst = entry.inst.get(tab);
    if (!inst) return;
    entry.inst.delete(tab);
    try { inst.worker.terminate(); } catch (e) { /* já encerrado */ }
  }

  function stop(entry) {
    Array.from(entry.inst.keys()).forEach((tab) => stopInstance(entry, tab));
  }

  // Um erro que impede o script de rodar para o script inteiro, em todos os
  // servidores: o código é o mesmo em todas as instâncias.
  function halt(entry, text, inst) {
    stop(entry);
    entry.status = 'error';
    entry.error = text;
    addLog(entry, 'error', text, inst);
    notifyError(entry, text);
  }

  function startInstance(entry, tab) {
    let w;
    try { w = new Worker('script-worker.js'); } catch (e) { halt(entry, 'não foi possível iniciar o script: ' + errText(e)); return; }
    const inst = { tab, worker: w, started: false, lastPong: Date.now(), pending: 0 };
    entry.inst.set(tab, inst);
    w.addEventListener('message', (e) => { if (entry.inst.get(inst.tab) === inst) onMessage(entry, inst, e.data || {}); });
    w.addEventListener('error', (e) => {
      if (entry.inst.get(inst.tab) !== inst) return;
      if (e.preventDefault) e.preventDefault();
      halt(entry, 'não foi possível iniciar o script' + (e.message ? ': ' + e.message : ''), inst);
    });
  }

  function start(entry) {
    stop(entry);
    entry.error = '';
    if (!entry.def.enabled) { entry.status = 'off'; changed(); return; }
    entry.status = 'starting';
    for (const tab of tabNames.keys()) {
      startInstance(entry, tab);
      if (entry.status === 'error') return; // não deu para criar o worker
    }
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

  function onMessage(entry, inst, m) {
    if (!m || typeof m !== 'object') return;
    const w = inst.worker;
    const mine = () => entry.inst.get(inst.tab) === inst;
    switch (m.t) {
      case 'ready':
        w.postMessage({ t: 'init', code: entry.def.code });
        break;
      case 'started':
        inst.started = true;
        if (entry.status === 'starting' && Array.from(entry.inst.values()).every((x) => x.started)) entry.status = 'running';
        addLog(entry, 'info', 'script iniciado', inst);
        break;
      case 'call': {
        const reply = (msg) => { if (mine()) w.postMessage(Object.assign({ t: 'result', n: m.n }, msg)); };
        let args = m.args, size = 0;
        if (args == null) args = {};
        try { size = JSON.stringify(args).length; } catch (e) { size = Infinity; }
        if (typeof args !== 'object' || Array.isArray(args) || size > MAX_ARGS) { reply({ ok: false, error: 'argumentos inválidos ou grandes demais' }); break; }
        if (inst.pending >= MAX_PENDING) { reply({ ok: false, error: 'muitas chamadas ao mesmo tempo' }); break; }
        inst.pending++;
        const done = () => { inst.pending = Math.max(0, inst.pending - 1); };
        // o pedido vai para o servidor desta instância, esteja a aba dele à vista ou não
        callTab(inst.tab, 'ScriptCall', entry.def.id, String(m.action).slice(0, 64), args).then(
          (value) => { done(); reply({ ok: true, value: value === undefined ? null : value }); },
          (err) => { done(); reply({ ok: false, error: errText(err) }); });
        break;
      }
      case 'log':
        if (allowLine(entry)) addLog(entry, m.level === 'warn' || m.level === 'error' ? m.level : 'info', clip(m.text), inst);
        break;
      case 'error':
        if (m.fatal) halt(entry, clip(m.text), inst); // não chegou a rodar (ou parou logo no começo)
        else if (allowLine(entry)) { addLog(entry, 'error', clip(m.text), inst); notifyError(entry, clip(m.text)); }
        break;
      case 'pong':
        inst.lastPong = Date.now();
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
      for (const inst of Array.from(entry.inst.values())) {
        if (late) inst.lastPong = now;
        else if (now - inst.lastPong > HUNG_MS) { halt(entry, 'o script parou de responder (laço sem fim?) e foi encerrado', inst); return; }
        inst.worker.postMessage({ t: 'ping' });
      }
    });
  }

  // Cada evento vai só para as instâncias do servidor em que aconteceu.
  function dispatch(event) {
    if (!event) return;
    const tab = Number(event.tab) || 0;
    const ev = Object.assign({}, event);
    delete ev.tab; // o número da aba é coisa da interface, não do script
    entries.forEach((entry) => {
      const inst = entry.inst.get(tab) || (tabsKnown ? null : entry.inst.get(0));
      if (inst && inst.started && entry.status !== 'error') inst.worker.postMessage({ t: 'event', event: ev });
    });
  }

  // setTabs diz em que servidores (abas) os scripts rodam. Aba nova: os scripts
  // ligados ganham uma instância para ela. Aba fechada: as instâncias dela são
  // encerradas. As outras não são tocadas.
  function setTabs(list) {
    const next = new Map();
    (Array.isArray(list) ? list : []).forEach((t) => { const id = Number(t && t.id) || 0; if (id) next.set(id, String((t && t.name) || '')); });
    if (!next.size) return;
    if (!tabsKnown) {
      // Até aqui os scripts rodavam para "a aba atual" (0), que é a primeira
      // aba: a instância continua a mesma, só passa a ter o número dela.
      tabsKnown = true;
      const first = next.keys().next().value;
      entries.forEach((entry) => {
        const inst = entry.inst.get(0);
        if (inst) { entry.inst.delete(0); inst.tab = first; entry.inst.set(first, inst); }
      });
      tabNames.delete(0);
      tabNames.set(first, next.get(first));
    }
    for (const tab of Array.from(tabNames.keys())) {
      if (next.has(tab)) continue;
      tabNames.delete(tab);
      entries.forEach((entry) => stopInstance(entry, tab));
    }
    next.forEach((name, tab) => {
      const isNew = !tabNames.has(tab);
      tabNames.set(tab, name);
      if (!isNew) return;
      entries.forEach((entry) => { if (entry.def.enabled && entry.status !== 'error' && entry.status !== 'off') startInstance(entry, tab); });
    });
  }

  function adopt(def) {
    let entry = entries.get(def.id);
    if (!entry) { entry = { def, status: 'off', error: '', log: [], lastNotify: 0, inst: new Map() }; entries.set(def.id, entry); }
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

    setTabs,
    // instances: em quantos servidores o script está rodando agora (para os testes e a interface).
    instances: (id) => { const e = entries.get(id); return e ? Array.from(e.inst.keys()) : []; },
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
