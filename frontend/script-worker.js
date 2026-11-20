/*
 * script-worker.js — onde roda cada script do usuário (um Web Worker por script).
 *
 * Isolamento: daqui o script não enxerga a janela, a página nem a ponte com o
 * backend. A resposta que entrega este arquivo traz uma política de segurança
 * (CSP, ver backend/scripts.go) que proíbe qualquer acesso à rede, e as
 * funções de rede também são removidas antes de o script rodar. O script só
 * fala com o TeamSpeak pelo objeto `ts`; cada pedido vira uma mensagem para a
 * página (scripts.js), que o repassa ao backend, onde é conferido.
 *
 * Mensagens (campo "t"):
 *   página → worker   init {code} · event {event} · result {n, ok, value|error} · ping
 *   worker → página   ready · started · call {n, action, args} · log {level, text}
 *                     error {text, fatal} · pong
 */
(function () {
  'use strict';

  const post = self.postMessage.bind(self);
  const setTimer = self.setTimeout.bind(self), clearTimer = self.clearTimeout.bind(self);
  const setRepeat = self.setInterval.bind(self), clearRepeat = self.clearInterval.bind(self);
  const AsyncFunction = (async function () {}).constructor;

  // ------------------------------------------------------------ isolamento
  // Nada de rede, de outros workers, de armazenamento do navegador, nem de
  // falar com a página por fora do `ts`.
  const BLOCKED = ['fetch', 'XMLHttpRequest', 'WebSocket', 'WebSocketStream', 'WebTransport', 'EventSource', 'importScripts',
    'Worker', 'SharedWorker', 'BroadcastChannel', 'indexedDB', 'caches', 'RTCPeerConnection', 'RTCDataChannel',
    'FontFace', 'fonts', 'Notification', 'cookieStore', // FontFace busca um endereço ao carregar a fonte
    'postMessage', 'close'];
  function block(obj, name) {
    for (let o = obj; o && o !== Object.prototype; o = Object.getPrototypeOf(o)) {
      try { delete o[name]; } catch (e) { /* segue: a definição abaixo ainda cobre */ }
    }
    try { Object.defineProperty(obj, name, { value: undefined, writable: false, configurable: false }); } catch (e) { /* já não existe */ }
  }
  BLOCKED.forEach((name) => block(self, name));
  if (self.navigator) {
    ['serviceWorker', 'sendBeacon', 'storage', 'locks', 'usb', 'hid', 'serial', 'bluetooth', 'credentials', 'permissions',
      'clipboard', 'mediaDevices', 'geolocation', 'connection'].forEach((name) => block(self.navigator, name));
  }

  // ------------------------------------------------------------ erros com o número da linha
  // O V8 (Windows) põe "arquivo:linha" na pilha do erro. O JavaScriptCore
  // (macOS) não, mas guarda no próprio erro a linha em que ele nasceu — o que
  // só serve quando nasceu no código do script (e não aqui dentro, num pedido
  // recusado, por exemplo). Sem ter como saber, a mensagem sai sem a linha.
  const SOURCE = 'furito-script.js';
  let lineOffset = -1;   // linhas que o motor põe antes do código do usuário (-1 = não dá para saber)
  let syntaxOffset = -1;

  const compile = (code) => new AsyncFunction('ts', 'console', code + '\n//# sourceURL=' + SOURCE);

  function stackLine(err) {
    const m = /furito-script\.js:(\d+)/.exec(String((err && err.stack) || ''));
    return m ? Number(m[1]) : 0;
  }
  const ownLine = (err) => (typeof err.line === 'number' && !err.sourceURL ? err.line : 0);

  // Descobre, com dois códigos de teste, quanto o motor desloca as linhas.
  async function calibrate() {
    try { await compile('throw new Error("x")')(); } catch (e) {
      const n = stackLine(e) || ownLine(e);
      if (n > 0) lineOffset = n - 1;
    }
    try { compile('\n)'); } catch (e) {
      if (typeof e.line === 'number' && e.line >= 2) syntaxOffset = e.line - 2;
    }
  }

  function lineOf(err) {
    if (!err || typeof err !== 'object') return 0;
    let n = stackLine(err), off = lineOffset;
    if (!n && err instanceof SyntaxError && typeof err.line === 'number') { n = err.line; off = syntaxOffset; } // não compilou
    if (!n) n = ownLine(err);
    return off >= 0 && n > off ? n - off : 0;
  }

  function describe(err) {
    let text;
    if (err instanceof Error) text = (err.name && err.name !== 'Error' ? err.name + ': ' : '') + err.message;
    else text = format([err]);
    const line = lineOf(err);
    return line > 0 ? text + ' (linha ' + line + ')' : text;
  }

  function format(args) {
    return args.map((v) => {
      if (typeof v === 'string') return v;
      if (v instanceof Error) return describe(v);
      if (v === undefined) return 'undefined';
      try { const s = JSON.stringify(v); return s === undefined ? String(v) : s; } catch (e) { return String(v); }
    }).join(' ').slice(0, 2000);
  }

  // Um script preso num laço não pode afogar a página: no máximo LOG_RATE
  // linhas de registro por segundo (o excesso é contado e descartado) e
  // MAX_CALLS pedidos ao app em aberto.
  const LOG_RATE = 40, MAX_CALLS = 100;
  let logSecond = 0, logCount = 0, logDropped = 0;
  function report(msg) {
    const now = Date.now();
    if (now - logSecond >= 1000) {
      if (logDropped) post({ t: 'log', level: 'warn', text: '… ' + logDropped + ' linhas descartadas: o script escreve rápido demais no registro' });
      logSecond = now; logCount = 0; logDropped = 0;
    }
    if (++logCount > LOG_RATE) logDropped++;
    else post(msg);
  }
  const fail = (err, fatal) => {
    const msg = { t: 'error', text: describe(err), fatal: !!fatal };
    if (fatal) post(msg); else report(msg);
  };
  const log = (level, args) => report({ t: 'log', level, text: format(args) });

  // Roda uma função do script sem deixar um erro dela derrubar o resto.
  function run(fn, args) {
    try {
      const r = fn.apply(undefined, args || []);
      if (r && typeof r.then === 'function') r.then(null, (e) => fail(e));
    } catch (e) { fail(e); }
  }

  // ------------------------------------------------------------ pedidos ao app
  const calls = new Map(); // n → { resolve, reject, err }
  let callSeq = 0;
  function call(action, args) {
    const err = new Error(''); // criado aqui para o erro apontar a linha do script que fez o pedido
    if (calls.size >= MAX_CALLS) {
      err.message = 'pedidos demais em aberto: o script chama o app mais rápido do que ele responde';
      return Promise.reject(err);
    }
    return new Promise((resolve, reject) => {
      const n = ++callSeq;
      calls.set(n, { resolve, reject, err });
      post({ t: 'call', n, action, args: args || {} });
    });
  }

  const idOf = (v) => Number(v && typeof v === 'object' ? v.id : v) || 0;
  function needFn(fn, where) { if (typeof fn !== 'function') throw new TypeError(where + ': esperava uma função'); }

  // ------------------------------------------------------------ a API dos scripts
  const EVENTS = ['connected', 'disconnected', 'clientJoined', 'clientLeft', 'clientConnected', 'clientDisconnected',
    'channelChanged', 'message', 'poke'];
  const handlers = new Map(); // evento → funções
  const timers = new Map();   // id → função que cancela
  let timerSeq = 0;

  function addTimer(cancel) { const id = ++timerSeq; timers.set(id, cancel); return id; }

  const ts = Object.freeze({
    // eventos
    on(event, fn) {
      if (!EVENTS.includes(event)) throw new RangeError('ts.on: evento desconhecido "' + event + '" (existem: ' + EVENTS.join(', ') + ')');
      needFn(fn, 'ts.on("' + event + '")');
      if (!handlers.has(event)) handlers.set(event, []);
      handlers.get(event).push(fn);
      return ts;
    },
    off(event, fn) {
      const list = handlers.get(event) || [];
      const i = list.indexOf(fn);
      if (i !== -1) list.splice(i, 1);
      return ts;
    },

    // mensagens
    sendChannelMessage: (text) => call('sendChannelMessage', { text: String(text) }),
    sendServerMessage: (text) => call('sendServerMessage', { text: String(text) }),
    sendPrivateMessage: (client, text) => call('sendPrivateMessage', { clientId: idOf(client), text: String(text) }),
    poke: (client, text) => call('poke', { clientId: idOf(client), text: String(text) }),

    // moderação: só funciona se o script estiver com "Pode moderar" marcado (e
    // você tiver a permissão no servidor)
    moveClient: (client, channel, password) => call('moveClient', { clientId: idOf(client), channelId: idOf(channel), password: password == null ? '' : String(password) }),
    kickFromChannel: (client, reason) => call('kickFromChannel', { clientId: idOf(client), reason: reason == null ? '' : String(reason) }),
    kickFromServer: (client, reason) => call('kickFromServer', { clientId: idOf(client), reason: reason == null ? '' : String(reason) }),
    ban(client, seconds, reason) {
      // a duração tem de vir escrita: esquecer o número não pode virar banimento permanente
      if (typeof seconds !== 'number' || !(seconds >= 0) || !Number.isFinite(seconds)) throw new TypeError('ts.ban: informe a duração em segundos (0 = permanente)');
      return call('ban', { clientId: idOf(client), seconds: Math.floor(seconds), reason: reason == null ? '' : String(reason) });
    },
    setTalker: (client, talker) => call('setTalker', { clientId: idOf(client), talker: talker !== false }),

    // sons do soundboard, pelo nome
    playSound: (name) => call('playSound', { name: String(name), local: false }),
    playSoundLocal: (name) => call('playSound', { name: String(name), local: true }),
    stopSound: () => call('stopSound'),

    // o próprio estado
    setAway: (away, message) => call('setAway', { away: !!away, message: message == null ? '' : String(message) }),
    setMuted: (muted) => call('setMuted', { muted: !!muted }),
    setDeafened: (deafened) => call('setDeafened', { deafened: !!deafened }),
    switchChannel: (channel, password) => call('switchChannel', { channelId: idOf(channel), password: password == null ? '' : String(password) }),

    // consultas
    getSelf: () => call('getSelf'),
    getClients: () => call('getClients'),
    getChannels: () => call('getChannels'),
    getSounds: () => call('getSounds'),

    // tempo
    every(seconds, fn) {
      seconds = Number(seconds);
      if (!(seconds >= 1)) throw new RangeError('ts.every: o intervalo mínimo é de 1 segundo');
      needFn(fn, 'ts.every');
      const h = setRepeat(() => run(fn), seconds * 1000);
      return addTimer(() => clearRepeat(h));
    },
    after(seconds, fn) {
      seconds = Number(seconds);
      if (!(seconds >= 0)) throw new RangeError('ts.after: tempo inválido');
      needFn(fn, 'ts.after');
      const id = addTimer(() => clearTimer(h));
      const h = setTimer(() => { timers.delete(id); run(fn); }, seconds * 1000);
      return id;
    },
    // todo dia no horário local "HH:MM"
    at(time, fn) {
      const m = /^(\d{1,2}):(\d{2})$/.exec(String(time).trim());
      if (!m || Number(m[1]) > 23 || Number(m[2]) > 59) throw new RangeError('ts.at: horário inválido "' + time + '" (use "HH:MM")');
      needFn(fn, 'ts.at');
      let h = 0;
      const arm = () => {
        const now = new Date(), next = new Date(now);
        next.setHours(Number(m[1]), Number(m[2]), 0, 0);
        if (next <= now) next.setDate(next.getDate() + 1);
        h = setTimer(() => {
          const late = Date.now() - next.getTime();
          arm();
          if (late < 60000) run(fn); // o computador dormiu e o horário passou: fica para amanhã
        }, next - now);
      };
      arm();
      return addTimer(() => clearTimer(h));
    },
    cancel(id) {
      const cancel = timers.get(id);
      if (cancel) { cancel(); timers.delete(id); }
    },

    log: (...args) => log('info', args),
  });

  const consoleApi = Object.freeze({
    log: (...a) => log('info', a), info: (...a) => log('info', a), debug: (...a) => log('info', a),
    warn: (...a) => log('warn', a), error: (...a) => log('error', a),
  });

  // ------------------------------------------------------------ ciclo de vida
  async function start(code) {
    await calibrate();
    let main;
    try { main = compile(String(code)); } catch (e) { fail(e, true); return; }
    post({ t: 'started' });
    try { await main.call(undefined, ts, consoleApi); } catch (e) { fail(e, true); }
  }

  self.addEventListener('message', (e) => {
    const m = e.data || {};
    switch (m.t) {
      case 'init': start(m.code); break;
      case 'event': {
        const ev = m.event || {};
        (handlers.get(ev.type) || []).slice().forEach((fn) => run(fn, [ev]));
        break;
      }
      case 'result': {
        const c = calls.get(m.n);
        if (!c) break;
        calls.delete(m.n);
        if (m.ok) c.resolve(m.value);
        else { c.err.message = String(m.error || 'falhou'); c.reject(c.err); }
        break;
      }
      case 'ping': post({ t: 'pong' }); break;
      default: break;
    }
  });
  // erros que escapam (um setTimeout do próprio script, uma promessa sem tratamento)
  self.addEventListener('error', (e) => { fail(e.error || e.message); e.preventDefault(); });
  self.addEventListener('unhandledrejection', (e) => { fail(e.reason); e.preventDefault(); });

  post({ t: 'ready' });
})();
