/*
 * bridge.js — ponte entre a UI e o backend Go (Wails v2).
 *
 * Real:  window.go.backend.Bridge.Call(aba, método, argumentos)  +  window.runtime.EventsOn(nome, cb)
 * Mock:  usado quando a página é aberta num navegador comum (sem Wails) ou com ?mock na URL.
 *
 * Uso:  const bridge = await window.FuritoBridge.init();
 *       bridge.api.Connect({...});  bridge.on('state', cb);  bridge.openURL(url);
 *
 * Cada servidor conectado é uma aba. bridge.api age na aba em bridge.tab (0 = a
 * que está à vista); bridge.forTab(id) devolve a API presa a uma aba, para o
 * que continua valendo depois que a pessoa troca de aba.
 */
(function () {
  'use strict';

  const params = new URLSearchParams(location.search);
  const forceMock = params.has('mock');

  function inWails() {
    return !!(window.wails || window.runtime || window.WailsInvoke ||
      (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external));
  }

  function findApi() {
    return (window.go && window.go.backend && window.go.backend.Bridge) || null;
  }

  // apiFor devolve um objeto com os métodos da API: cada chamada vira
  // Bridge.Call(aba, nome, argumentos). tab() diz a aba na hora da chamada.
  function apiFor(root, tab) {
    return new Proxy({}, {
      get(_, name) {
        if (typeof name !== 'string' || name === 'then') return undefined; // não é uma Promise
        return (...args) => root.Call(tab(), name, args);
      },
    });
  }

  function waitForApi(timeoutMs) {
    return new Promise((resolve) => {
      const t0 = Date.now();
      (function poll() {
        const api = findApi();
        if (api) return resolve(api);
        if (Date.now() - t0 >= timeoutMs) return resolve(null);
        setTimeout(poll, 25);
      })();
    });
  }

  function openExternal(url) {
    if (window.runtime && typeof window.runtime.BrowserOpenURL === 'function') window.runtime.BrowserOpenURL(url);
    else window.open(url, '_blank', 'noopener,noreferrer');
  }

  async function init() {
    if (!forceMock) {
      const root = await waitForApi(inWails() ? 5000 : 0);
      if (root) {
        const bridge = {
          isMock: false,
          missingBackend: false,
          tab: 0,
          forTab: (id) => apiFor(root, () => id),
          on(name, cb) { window.runtime.EventsOn(name, cb); },
          openURL: openExternal,
        };
        bridge.api = apiFor(root, () => bridge.tab);
        return bridge;
      }
    }
    const mock = createMock(params);
    return { isMock: true, missingBackend: !forceMock && inWails(), tab: 0, api: mock.api, forTab: () => mock.api, on: mock.on, openURL: openExternal };
  }

  // ------------------------------------------------------------------ MOCK
  function createMock(params) {
    const demo = params.get('demo') || '';
    const listeners = {};
    const clone = (v) => (v === undefined ? null : JSON.parse(JSON.stringify(v)));
    const emit = (name, data) => (listeners[name] || []).forEach((cb) => cb(clone(data)));
    const ok = (v) => Promise.resolve(clone(v));
    const fail = (msg) => Promise.reject(msg);

    const settings = { host: 'localhost', nickname: 'Furico Caolho', defaultChannel: '' };
    const audio = {
      effect: 'normal', vadMode: 'auto', vadThreshold: 300, vadFloor: 0, outputVolume: 80, soundboardPlaying: false,
      noiseSuppression: true, noiseMode: 'neural', autoGain: true, echoCancellation: false, echoCancellationAvailable: true,
      normalizeVoices: true, micTest: false, micMode: 'standard', soundboardVolume: 80,
      effects: [['normal', 'Normal'], ['grave', 'Voz grave'], ['agudo', 'Voz aguda'], ['monster', 'Monstro'], ['squirrel', 'Esquilo'],
        ['robot', 'Robô'], ['metallic', 'Metálico'], ['radio', 'Rádio'], ['megaphone', 'Megafone'], ['cave', 'Caverna'], ['echo', 'Eco'],
        ['demon', 'Demônio'], ['alien', 'Alienígena'], ['whisper', 'Sussurro'], ['underwater', "Debaixo d'água"]].map(([id, name]) => ({ id, name })),
      sounds: [{ id: 'u1', name: 'Gol do Brasil', builtin: false }, { id: 'u2', name: 'Vinheta', builtin: false }].concat(
        // ?sounds=N: mais sons, alguns com atalho, para ver o painel cheio
        ['porta', 'lobo', 'preto_filha_do_vento', 'trabalhador', 'au', 'buzina', 'cabra', 'gato', 'macaco', 'olhaomacaco', 'pato', 'grupocalado', 'risada', 'aplausos', 'tambor', 'sirene']
          .slice(0, Math.max(0, Number(params.get('sounds')) || 0))
          .map((name, i) => ({ id: 'd' + i, name, builtin: false, hotkey: ['Ctrl+Alt+Digit1', '', 'F13', '', '', 'Numpad5'][i] || '', hotkeyError: '' }))),
      notifySounds: true, notifyPack: 'padrao',
      notifyPacks: [{ id: 'padrao', name: 'Padrão' }, { id: 'meus-sons', name: 'Meus sons' }],
      captureAvailable: true, inputDevice: 'Microfone (MacBook Pro)', outputDevice: 'Alto-falantes (MacBook Pro)',
      inputDevices: [{ name: 'Microfone (MacBook Pro)', isDefault: true }, { name: 'HyperX QuadCast', isDefault: false }],
      outputDevices: [{ name: 'Alto-falantes (MacBook Pro)', isDefault: true }, { name: 'Fones de ouvido externos', isDefault: false }],
      inputChoice: '', outputChoice: '',
    };
    const selfFlags = { muted: false, deafened: false, away: false, awayMessage: '', recordingSince: 0 };
    const SELF_ID = 12;

    // recursos do dia a dia e de administração
    // compartilhamento de tela: o cliente 3 (Zé Pequeno) faz o papel do outro
    // cliente Furito — compartilha uma animação e assiste à nossa tela — com
    // conexões WebRTC de verdade dentro desta mesma página
    const screenCfg = { stunUrl: '', turnUrl: '', turnUser: '', relayOnly: false, warned: params.has('warned') };
    const PEER_ID = 3;
    let pokePopup = true; // cutucadas abrem uma janela (senão, só o som)
    const imagesCfg = { mode: 'ask', trusted: false }; // imagens de fora: só com clique, até o usuário liberar o servidor
    const UNMUTE_ERR = 'por segurança, um script não liga o microfone nem os alto-falantes: ele só pode mutar; quem desmuta é você';
    const peer = { sharingSid: '', out: null, in: null, received: 0, patternTimer: 0 };
    const peerSignal = (msg) => setTimeout(() => {
      const c = findClient(PEER_ID);
      if (st.status === 'connected' && c) emit('screen:signal', { from: PEER_ID, fromName: c.nickname, payload: JSON.stringify(msg) });
    }, 30);
    const peerGathered = (pc) => new Promise((resolve) => {
      if (pc.iceGatheringState === 'complete') return resolve();
      const t = setTimeout(resolve, 2000);
      pc.addEventListener('icegatheringstatechange', () => { if (pc.iceGatheringState === 'complete') { clearTimeout(t); resolve(); } });
    });
    function peerReset() {
      [peer.out, peer.in].forEach((pc) => { if (pc) { try { pc.close(); } catch (e) { /* já fechada */ } } });
      clearInterval(peer.patternTimer);
      peer.out = peer.in = null; peer.received = 0; peer.sharingSid = '';
    }
    async function peerOffer(sid) {
      // o "outro cliente" manda a tela dele: uma animação num canvas
      const cv = document.createElement('canvas'); cv.width = 640; cv.height = 360;
      const g = cv.getContext('2d'); let n = 0;
      clearInterval(peer.patternTimer);
      peer.patternTimer = setInterval(() => { n++; g.fillStyle = 'hsl(' + (n * 11 % 360) + ',70%,45%)'; g.fillRect(0, 0, 640, 360); g.fillStyle = '#fff'; g.fillRect((n * 12) % 640, 120, 40, 120); }, 66);
      const stream = cv.captureStream(15);
      if (peer.out) peer.out.close();
      const pc = peer.out = new RTCPeerConnection({ iceServers: [] });
      stream.getTracks().forEach((t) => pc.addTrack(t, stream));
      await pc.setLocalDescription(await pc.createOffer());
      await peerGathered(pc);
      if (peer.out === pc) peerSignal({ t: 'offer', sid, sdp: pc.localDescription.sdp });
    }
    async function peerAnswer(sid, sdp) {
      // o "outro cliente" assiste à nossa tela
      if (peer.in) peer.in.close();
      const pc = peer.in = new RTCPeerConnection({ iceServers: [] });
      pc.addEventListener('track', (e) => {
        const v = document.createElement('video');
        v.muted = true; v.autoplay = true; v.playsInline = true;
        v.srcObject = e.streams[0] || new MediaStream([e.track]);
        const tick = () => { if (peer.in !== pc) return; if (v.videoWidth > 0) peer.received++; setTimeout(tick, 100); };
        const p = v.play(); if (p && p.catch) p.catch(() => {});
        tick();
      });
      await pc.setRemoteDescription({ type: 'offer', sdp });
      await pc.setLocalDescription(await pc.createAnswer());
      await peerGathered(pc);
      if (peer.in === pc) peerSignal({ t: 'answer', sid, sdp: pc.localDescription.sdp });
    }
    function peerHandle(to, payload) {
      let msg; try { msg = JSON.parse(payload); } catch (e) { return; }
      const c = findClient(PEER_ID), me = findClient(SELF_ID);
      if (!c || !me || c.channelId !== me.channelId) return;
      if (to !== 0 && to !== PEER_ID) return;
      switch (msg.t) {
        case 'hello': if (peer.sharingSid) peerSignal({ t: 'sharing', sid: peer.sharingSid }); break;
        case 'watch': if (msg.sid === peer.sharingSid && peer.sharingSid) peerOffer(msg.sid); else peerSignal({ t: 'stopped', sid: msg.sid }); break;
        case 'answer': if (peer.out) peer.out.setRemoteDescription({ type: 'answer', sdp: msg.sdp }).catch(() => {}); break;
        case 'leave': if (peer.out) { peer.out.close(); peer.out = null; clearInterval(peer.patternTimer); } break;
        case 'sharing': if (to === 0) peerSignal({ t: 'watch', sid: msg.sid }); break; // o outro quer assistir
        case 'offer': peerAnswer(msg.sid, msg.sdp); break;
        case 'stopped': if (peer.in) { peer.in.close(); peer.in = null; peer.received = 0; } break;
        default: break;
      }
    }

    // identidades: várias, uma padrão; a "em uso" é a da conexão atual
    let identities = [{ id: 'i1', name: 'Principal', nickname: '', uid: 'mK9xT2v0QwE7LrZp4JhYc1NdUas=', securityLevel: 8 }];
    let identDefault = 'i1', identActive = 'i1', identSeq = 1;
    const findIdent = (id) => identities.find((x) => x.id === id);
    const fakeUid = () => Array.from({ length: 27 }, () => 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'[Math.floor(Math.random() * 64)]).join('') + '=';
    const groups = {
      server: [
        { id: 6, name: 'Server Admin', iconId: 300, type: 1 }, { id: 7, name: 'Normal', iconId: 0, type: 1 },
        { id: 8, name: 'Guest', iconId: 0, type: 1 }, { id: 9, name: 'VIP', iconId: 7654321, type: 1 },
        { id: 2, name: 'Admin Server Query', iconId: 500, type: 2 },
      ],
      channel: [
        { id: 5, name: 'Channel Admin', iconId: 100, type: 1 }, { id: 6, name: 'Operator', iconId: 200, type: 1 },
        { id: 7, name: 'Voice', iconId: 600, type: 1 }, { id: 8, name: 'Guest', iconId: 0, type: 1 },
      ],
    };
    let bookmarks = [
      { id: 'b1', name: 'Servidor da galera', host: 'ts.exemplo.com', nickname: 'Furico Caolho', defaultChannel: 'Counter-Strike' },
      { id: 'b2', name: 'Teste local', host: 'localhost', nickname: 'Furico Caolho', defaultChannel: '' },
    ];
    let bmSeq = 2;
    // global: false = sem atalhos de sistema; a UI trata as teclas com a janela em foco
    const hotkeys = { ptt: 'Backquote', toggleMute: 'Ctrl+Alt+KeyM', toggleDeafen: '', soundStop: '', global: false, errors: {} };
    if (demo === 'settings') hotkeys.errors.toggleMute = 'não foi possível registrar: combinação já usada pelo sistema';
    let autoReconnect = true, reconnectAttempt = 0, pttDown = false;
    let whisper = { kind: '', id: 0, name: '' }, talkRequested = false;
    const chanPw = {}; // senhas de canais criados/editados aqui; os demais usam 1234
    const pwOf = (c) => (chanPw[c.id] != null ? chanPw[c.id] : '1234');
    const perm = (name, value, negated, skip) => ({ name, value, negated: !!negated, skip: !!skip });
    const perms = {
      'server:6': [perm('b_client_ignore_antiflood', 1, false, true), perm('b_virtualserver_modify_name', 1), perm('i_client_ban_power', 75),
        perm('i_client_kick_from_server_power', 75), perm('i_client_move_power', 75), perm('i_group_modify_power', 75)],
      'server:7': [perm('b_channel_create_temporary', 1), perm('i_channel_join_power', 10), perm('i_client_poke_power', 25)],
      'server:8': [perm('b_channel_create_temporary', 1), perm('b_client_server_textmessage_send', 1, true), perm('i_channel_join_power', 0)],
      'server:9': [perm('b_client_use_priority_speaker', 1), perm('i_icon_id', 7654321)],
      'channel:5': [perm('i_channel_modify_power', 70), perm('i_client_kick_from_channel_power', 50), perm('i_client_talk_power', 100, false, true)],
      'channel:7': [perm('i_client_talk_power', 50)],
    };
    const NOW = Math.floor(Date.now() / 1000);
    const files = {
      3: {
        '/': [
          { name: 'mapas', size: 0, modified: NOW - 3 * 86400, isDir: true },
          { name: 'regras.txt', size: 1830, modified: NOW - 40 * 86400, isDir: false },
          { name: 'replay-final.dem', size: 48234111, modified: NOW - 5 * 3600, isDir: false },
          { name: 'time <b>novo & cia.png', size: 312400, modified: NOW - 9 * 86400, isDir: false },
        ],
        '/mapas': [
          { name: 'de_dust2.bsp', size: 13400321, modified: NOW - 3 * 86400, isDir: false },
          { name: 'de_inferno.bsp', size: 21503488, modified: NOW - 3 * 86400, isDir: false },
        ],
      },
    };
    const filesOf = (channelId) => files[channelId] || (files[channelId] = { '/': [] });
    const normPath = (p) => '/' + String(p || '/').split('/').filter(Boolean).join('/');
    const parentPath = (p) => normPath(p.split('/').slice(0, -1).join('/'));

    const messages = [];
    let msgSeq = 0;
    let connToken = 0;
    let st = offline('');

    function offline(error) {
      peerReset(); // sem conexão não há compartilhamento de tela
      return {
        status: 'disconnected', error: error || '', host: '',
        server: { name: '', welcomeMessage: '', version: '', platform: '', maxClients: 0 },
        self: Object.assign({ clientId: 0, channelId: 0, nickname: settings.nickname }, selfFlags),
        channels: [], clients: [], audio,
      };
    }

    function ch(id, parentId, depth, name, extra) {
      return Object.assign({
        id, parentId, depth, name, topic: '', description: '', codec: 4, codecQuality: 6, maxClients: -1,
        isDefault: false, hasPassword: false, isPermanent: true, isSemiPermanent: false, neededTalkPower: 0,
      }, extra || {});
    }
    function cl(id, channelId, nickname, extra) {
      return Object.assign({
        id, channelId, nickname, uid: 'uid' + id + 'AAAAAAAAAAAAAAAAAAAAAAA=', databaseId: id + 100, type: 0,
        inputMuted: false, outputMuted: false, away: false, awayMessage: '', talkPower: 0, isRecording: false,
        country: 'BR', serverGroups: ['Guest'], channelGroup: 'Guest', version: '', platform: '', connectedSince: 0, description: '',
        volume: 100, mutedLocally: false, contact: '', serverGroupIds: [8], channelGroupId: 8, isTalker: false, talkRequest: false, talkRequestMessage: '', avatar: '',
      }, extra || {});
    }

    const DESCRIPTIONS = {
      3: '[center][size=14][b]Informações[/b][/size][/center]\n[img]https://static.tibia.com/images/global/header/monsters/eradicator.gif[/img]\nBoss Boostado [b]Eradicator[/b]\n[hr]\n' +
        '[table]\n[tr][th]Personagem[/th][th]Level[/th][th]Vocação[/th][/tr]\n' +
        '[tr][td]Furico Caolho[/td][td][right]1250[/right][/td][td][color=#b00020]Elite Knight[/color][/td][/tr]\n' +
        '[tr]\n  [td][img]https://www.tibiawiki.com.br/images/f/f5/Rashid.gif[/img] Leko[/td]\n  [td][right]1850[/right][/td]\n  [td][url=https://www.tibia.com]Royal Paladin[/url][/td]\n[/tr]\n' +
        '[tr][td][s]Antigo[/s][/td][td]x[sup]2[/sup][/td][td]sem fechar[/tr]\n[/table]\ndepois da tabela\n[list=1][*]um[*]dois[/list]',
      1: 'Este é o canal padrão.\n[b]Regras:[/b] seja educado. Mais em [url=https://www.teamspeak.com]teamspeak.com[/url].',
      33: '[color=#b00020][b]Counter-Strike[/b][/color]\nPartidas todas as noites às [i]21h[/i].',
      8: 'Só música. [u]Sem conversa[/u] aqui!',
    };
    const CLIENT_DETAILS = {
      12: { version: '1.0.0', platform: 'Diogo Agra', furito: true, connectedSince: Date.now() - 1000 },
      3: { version: '1.0.0', platform: 'Diogo Agra', connectedSince: Date.now() - 3725 * 1000 },
      4: { version: '3.6.2 [Stable]', platform: 'Windows', connectedSince: Date.now() - 95 * 1000 },
      5: { version: '6.0.0-beta3', platform: 'Windows', connectedSince: Date.now() - 2 * 86400 * 1000 },
      6: { version: '3.5.6 [Stable]', platform: 'Linux', connectedSince: Date.now() - 40 * 60 * 1000 },
      7: { version: '3.6.2 [Stable]', platform: 'Windows', connectedSince: Date.now() - 12 * 60 * 1000 },
      8: { version: '3.?.? [Bot]', platform: 'Linux', connectedSince: Date.now() - 9 * 86400 * 1000 },
      9: { version: '3.6.1 [Stable]', platform: 'Android', connectedSince: Date.now() - 4 * 3600 * 1000 },
    };

    function buildOnline(host) {
      const channels = [
        ch(1, 0, 0, 'Default Channel', { topic: 'Default Channel has no topic', isDefault: true }),
        ch(2, 0, 0, '[cspacer1]Jogos'),
        ch(3, 0, 0, 'Counter-Strike', { topic: 'Mix e competitivo' }),
        ch(4, 3, 1, 'Time A', { maxClients: 5 }),
        ch(5, 3, 1, 'Time B', { hasPassword: true, maxClients: 5 }),
        ch(6, 0, 0, 'Minecraft'),
        ch(7, 0, 0, '[*spacer2]_'),
        ch(8, 0, 0, 'Música', { codec: 5, codecQuality: 10, topic: 'Rádio 24h' }),
        ch(9, 0, 0, 'AFK', { neededTalkPower: 100 }),
        ch(10, 0, 0, 'Staff', { hasPassword: true, isSemiPermanent: false }),
        ch(11, 10, 1, 'Reunião', { isPermanent: false, isSemiPermanent: true }),
      ];
      const clients = [
        cl(SELF_ID, 1, settings.nickname, Object.assign({ inputMuted: selfFlags.muted, outputMuted: selfFlags.deafened, away: selfFlags.away, awayMessage: selfFlags.awayMessage })),
        cl(3, 1, 'Zé Pequeno', { avatar: 'mock', furito: true, serverGroups: ['Server Admin', 'Normal'], serverGroupIds: [6, 7], channelGroupId: 5, channelGroup: 'Channel Admin', channelGroupIcon: 100, inputHardware: false,
          groups: [{ name: 'Server Admin', iconId: 300 }, { name: 'Normal', iconId: 0 }],
          icons: [{ id: 100, title: 'Channel Admin' }, { id: 300, title: 'Server Admin' }, { id: 1234567, title: 'Druid' }, { id: 7654321, title: 'VIP' }] }),
        cl(4, 4, 'Ana Banana', { serverGroups: ['Normal'], serverGroupIds: [7], contact: 'friend', namePrefix: '[1050+] ', icons: [{ id: 600, title: 'Voice' }, { id: 222333, title: 'Knight' }] }),
        cl(5, 4, 'Marcão', { serverGroups: ['Normal'], serverGroupIds: [7], isRecording: true, volume: 140, contact: 'blocked', mutedLocally: true }),
        cl(6, 5, 'Tião', { inputMuted: true }),
        cl(7, 6, 'Cabeça', { outputMuted: true, inputMuted: true }),
        cl(8, 8, 'Bot DJ', { serverGroups: ['Bot'], description: 'Toca música a pedido.' }),
        cl(9, 9, 'Dona Morte', { away: true, awayMessage: 'almoçando', talkRequest: true, talkRequestMessage: 'posso falar um minuto?' }),
      ].sort((a, b) => a.nickname.localeCompare(b.nickname, 'pt-BR'));
      channels.forEach((c) => { c.isTemporary = !c.isPermanent && !c.isSemiPermanent; });
      return {
        status: 'connected', error: '', host,
        server: {
          name: 'TeamSpeak 6 Server',
          welcomeMessage: 'Welcome to TeamSpeak, check [URL]www.teamspeak.com[/URL] for latest information',
          version: '6.0.0-beta13.1 [Build: 1790080330]', platform: 'Linux', maxClients: 32,
          // imagem de banner: só com ?banner=<url http(s)> (o mock não aponta para sites de terceiros)
          uid: 'Sv3rT9kq0YbN8uHc2LxWm5PaEoI=', bannerUrl: params.get('banner') || '', bannerLink: 'https://www.teamspeak.com',
        },
        self: Object.assign({ clientId: SELF_ID, channelId: 1, nickname: settings.nickname }, selfFlags),
        channels, clients, audio,
      };
    }

    function sync() {
      const on = st.status === 'connected';
      st.audio = audio;
      if (!on) selfFlags.recordingSince = 0; // a gravação termina ao desconectar
      Object.assign(st.self, selfFlags);
      if (!on) { whisper = { kind: '', id: 0, name: '' }; talkRequested = false; pttDown = false; }
      st.self.whisper = whisper;
      st.self.talkRequested = talkRequested;
      const act = findIdent(identActive) || identities[0];
      st.identity = { id: act.id, name: act.name, uid: act.uid, securityLevel: act.securityLevel, serverUid: '' };
      st.identities = identities.map((x) => Object.assign({}, x, { isDefault: x.id === identDefault, inUse: x.id === act.id }));
      st.appVersion = '1.0.0';
      st.appAuthor = 'Diogo Agra';
      st.appSite = 'https://diogoagra.dev';
      st.platform = params.get('platform') || 'darwin';
      st.pokePopup = pokePopup;
      st.images = { mode: imagesCfg.mode, trusted: imagesCfg.trusted };
      st.screen = { iceServers: [], relayOnly: screenCfg.relayOnly, warned: screenCfg.warned, nativeCapture: false, stunUrl: screenCfg.stunUrl, turnUrl: screenCfg.turnUrl, turnUser: screenCfg.turnUser, hasTurn: !!screenCfg.turnUrl };
      st.groups = groups;
      st.bookmarks = bookmarks;
      st.hotkeys = hotkeys;
      st.autoReconnect = autoReconnect;
      st.reconnectAttempt = st.status === 'reconnecting' ? reconnectAttempt : 0;
      return st;
    }
    function pushState() { emit('state', sync()); }

    function addMsg(m, silent, at) {
      const msg = Object.assign({
        id: ++msgSeq, time: at || Date.now(), kind: 'log', level: 'info', targetMode: 0,
        peerId: 0, peerName: '', invokerId: 0, invokerName: '', text: '', self: false,
      }, m);
      messages.push(msg);
      if (messages.length > 500) messages.shift();
      if (!silent) emit('message', msg);
      return msg;
    }
    const log = (level, text, silent, at) => addMsg({ kind: 'log', level, text }, silent, at);
    function chat(mode, fromId, fromName, text, extra, silent, at) {
      if (fromId !== SELF_ID && !silent) {
        const from = findClient(fromId);
        scriptEvent({ type: 'message', text, target: mode === 1 ? 'private' : mode === 3 ? 'server' : 'channel', client: from ? scriptClient(from) : { id: fromId, nickname: fromName, uid: '', serverGroups: [] } });
      }
      return addMsg(Object.assign({ kind: 'chat', targetMode: mode, invokerId: fromId, invokerName: fromName, text, self: fromId === SELF_ID }, extra || {}), silent, at);
    }
    // scripts (automações): guardados só na memória do modo demonstração
    const scripts = [];
    let scriptSeq = 0;
    let scriptsSeeded = false; // o script de exemplos entra na lista na primeira consulta, como no app
    async function seedScripts() {
      if (scriptsSeeded) return;
      scriptsSeeded = true;
      try {
        const r = await fetch('script-examples.js');
        if (r.ok) scripts.unshift({ id: 'exemplos', name: 'Exemplos: todas as funções', code: await r.text(), enabled: false, moderation: false });
      } catch (e) { /* página aberta como arquivo: fica sem os exemplos */ }
    }
    function scriptEvent(ev) { if (scripts.some((x) => x.enabled)) emit('script:event', ev); }
    const scriptClient = (c) => ({
      id: c.id, nickname: c.nickname, uid: c.uid, channelId: c.channelId, away: !!c.away, awayMessage: c.awayMessage || '',
      inputMuted: !!c.inputMuted, outputMuted: !!c.outputMuted, serverGroups: c.serverGroups || [], country: c.country || '',
      isTalker: !!c.isTalker, talkRequest: !!c.talkRequest, self: c.id === SELF_ID,
    });
    const scriptChannel = (c) => (c ? { id: c.id, name: c.name, parentId: c.parentId, topic: c.topic || '', hasPassword: !!c.hasPassword, isDefault: !!c.isDefault } : null);
    const findClient = (id) => st.clients.find((c) => c.id === id);
    const findChannel = (id) => st.channels.find((c) => c.id === id);
    const defaultChannel = () => st.channels.find((c) => c.isDefault) || st.channels[0];

    function refreshGroups(c) {
      const list = c.serverGroupIds.map((id) => groups.server.find((g) => g.id === id)).filter(Boolean);
      c.serverGroups = list.map((g) => g.name);
      c.groups = list.map((g) => ({ name: g.name, iconId: g.iconId }));
      const cg = groups.channel.find((g) => g.id === c.channelGroupId);
      if (cg) { c.channelGroup = cg.name; c.channelGroupIcon = cg.iconId; }
    }
    function removeClient(id) {
      st.clients = st.clients.filter((c) => c.id !== id);
      if (whisper.kind === 'client' && whisper.id === id) whisper = { kind: '', id: 0, name: '' };
    }
    function subtreeIds(id) {
      const ids = [id];
      for (let i = 0; i < ids.length; i++) st.channels.forEach((c) => { if (c.parentId === ids[i]) ids.push(c.id); });
      return ids;
    }
    function applySpec(c, spec, creating) {
      c.name = String(spec.name).trim();
      c.topic = String(spec.topic || '');
      // como no backend: na edição a descrição só muda quando pedido
      if (creating || spec.changeDescription) {
        c.description = String(spec.description || '');
        DESCRIPTIONS[c.id] = c.description;
      }
      if (creating || spec.changePassword) { c.hasPassword = !!spec.password; chanPw[c.id] = String(spec.password || ''); }
      c.isPermanent = spec.kind === 'permanent';
      c.isSemiPermanent = spec.kind === 'semi';
      c.isTemporary = !c.isPermanent && !c.isSemiPermanent;
      c.maxClients = Number(spec.maxClients) >= 0 ? Number(spec.maxClients) : -1;
      c.codec = Number(spec.codec) === 5 ? 5 : 4;
      c.codecQuality = Math.max(0, Math.min(10, Number(spec.codecQuality) || 0));
      c.neededTalkPower = Math.max(0, Number(spec.neededTalkPower) || 0);
      if (spec.isDefault) { st.channels.forEach((x) => { x.isDefault = false; }); c.isDefault = true; }
    }
    function fileAccess(channelId, password) {
      if (st.status !== 'connected') return 'não conectado';
      const c = findChannel(channelId);
      if (!c) return 'canal inexistente';
      if (c.hasPassword && String(password || '') !== pwOf(c)) return 'senha do canal inválida (no modo demonstração a senha é 1234)';
      return '';
    }
    // linhas de uma sessão anterior, como o backend real carrega do histórico salvo
    function seedHistory() {
      const t = Date.now() - 26 * 3600 * 1000;
      addMsg({ kind: 'log', level: 'info', text: 'Conectado ao servidor: [b]"TeamSpeak 6 Server"[/b]', history: true }, true, t);
      chat(2, 4, 'Ana Banana', 'alguém anima um mix hoje à noite?', { history: true }, true, t + 60000);
      chat(2, SELF_ID, settings.nickname, 'eu topo, [b]21h[/b]', { history: true }, true, t + 95000);
      addMsg({ kind: 'log', level: 'info', text: 'Desconectado do servidor', history: true }, true, t + 3600000);
    }

    function seedOnline(host, silent) {
      const t = Date.now() - 1000;
      log('info', 'Tentando resolver nome do host [b]' + host + '[/b]', silent, t);
      log('info', 'Tentando conectar-se ao servidor [b]' + host + '[/b]', silent, t);
      st = buildOnline(host);
      addMsg({ kind: 'log', level: 'success', text: st.server.welcomeMessage, rich: true }, silent, t); // a única linha de registro com BBCode
      log('info', 'Conectado ao servidor: [b]"' + st.server.name + '"[/b]', silent, t);
      if (silent) {
        chat(2, 3, 'Zé Pequeno', 'e aí, bora uma partida? link do mapa: https://example.com/mapa', null, true, t + 300);
        chat(2, SELF_ID, settings.nickname, 'bora, [b]5 min[/b] 😄', null, true, t + 600);
        log('info', '[b]"Ana Banana"[/b] entrou no canal [b]"Time A"[/b]', true, t + 700);
      }
    }

    // estado inicial
    if (demo !== 'dialog' && demo !== 'disconnected') { seedHistory(); seedOnline('localhost', true); }
    else log('info', 'Bem-vindo ao Furito TS (modo demonstração).', true);
    if (demo === 'reconnect') {
      // conexão caiu: o backend avisa "reconnecting" + número da tentativa
      st = offline('Conexão com o servidor perdida');
      st.status = 'reconnecting';
      st.host = 'localhost';
      reconnectAttempt = 3;
      log('warn', 'Conexão com o servidor perdida. Reconectando…', true);
    }

    // simulação de voz / nível do microfone
    let tick = 0;
    let lastTalking = '';
    setInterval(() => {
      tick++;
      if (st.status !== 'connected') {
        if (lastTalking !== 'off') { lastTalking = 'off'; emit('talking', { clients: [], self: false, micLevel: 0 }); }
        return;
      }
      const speaking = Math.floor(tick / 25) % 2 === 0; // alterna a cada 2,5 s
      const micLevel = selfFlags.muted || !audio.captureAvailable ? 0
        : Math.round(40 + (Math.floor(tick / 18) % 3 === 1 ? 900 + 700 * Math.abs(Math.sin(tick / 2)) : 60 * Math.abs(Math.sin(tick / 3))));
      const voiceProb = micLevel > 500 ? 0.95 : 0.03;
      const self = !selfFlags.muted && (audio.vadMode === 'ptt' ? pttDown : audio.vadMode === 'level' ? micLevel >= audio.vadThreshold : voiceProb > 0.5 && micLevel >= audio.vadFloor);
      const clients = [];
      if (!selfFlags.deafened) {
        if (speaking && findClient(3)) clients.push(3);
        if (!speaking && findClient(4) && tick % 50 < 30) clients.push(4);
      }
      if (self) clients.push(SELF_ID);
      const sig = clients.join(',') + '|' + self + '|' + micLevel;
      if (sig !== lastTalking) { lastTalking = sig; emit('talking', { clients, self, micLevel, voiceProb }); }
    }, 100);

    const api = {
      GetState: () => ok(sync()),
      GetMessages: () => ok(messages),
      GetSettings: () => ok(settings),

      Connect(req) {
        req = req || {};
        const host = String(req.host || '').trim();
        if (!host) return fail('endereço do servidor vazio');
        if (req.identityId && !findIdent(req.identityId)) return fail('identidade não encontrada (ela pode ter sido apagada)');
        identActive = req.identityId || identDefault;
        settings.host = host;
        if (req.nickname) settings.nickname = String(req.nickname);
        settings.defaultChannel = req.defaultChannel || '';
        const token = ++connToken;
        st = offline('');
        st.status = 'connecting';
        st.host = host;
        log('info', 'Tentando resolver nome do host [b]' + host + '[/b]');
        pushState();
        setTimeout(() => { if (token === connToken) log('info', 'Tentando conectar-se ao servidor [b]' + host + '[/b]'); }, 300);
        setTimeout(() => {
          if (token !== connToken) return;
          if (/erro|falha|fail/i.test(host)) {
            st = offline('Falha ao conectar: o servidor não respondeu (tempo esgotado)');
            log('error', st.error);
            pushState();
            return;
          }
          if (req.serverPassword === 'errada') {
            st = offline('Senha do servidor inválida');
            log('error', st.error);
            pushState();
            return;
          }
          seedOnline(host, false);
          pushState();
        }, 900);
        return ok(null);
      },

      Disconnect() {
        connToken++;
        reconnectAttempt = 0;
        const was = st.status;
        st = offline('');
        if (was !== 'disconnected') log('info', 'Desconectado do servidor');
        pushState();
        return ok(null);
      },

      JoinChannel(channelId, password) {
        if (st.status !== 'connected') return fail('não conectado');
        const c = findChannel(channelId);
        if (!c) return fail('canal inexistente');
        if (c.hasPassword && password !== pwOf(c)) {
          log('error', 'Senha do canal inválida (no modo demonstração a senha é 1234)');
          return fail('senha do canal inválida');
        }
        const me = findClient(SELF_ID);
        if (me) me.channelId = channelId;
        st.self.channelId = channelId;
        log('info', 'Você entrou no canal [b]"' + c.name + '"[/b]');
        pushState();
        return ok(null);
      },

      SendText(targetMode, target, text) {
        if (st.status !== 'connected') return fail('não conectado');
        text = String(text || '');
        if (targetMode === 1) {
          const peer = findClient(target);
          if (!peer) return fail('cliente não está mais online');
          chat(1, SELF_ID, settings.nickname, text, { peerId: peer.id, peerName: peer.nickname });
          setTimeout(() => {
            if (findClient(peer.id)) chat(1, peer.id, peer.nickname, 'recebi: "' + text + '"', { peerId: peer.id, peerName: peer.nickname });
          }, 900);
        } else if (targetMode === 3) {
          log('error', 'insufficient client permissions (failed_permid=223)');
          return fail('insufficient client permissions');
        } else {
          chat(2, SELF_ID, settings.nickname, text);
        }
        return ok(null);
      },

      ToggleMute() {
        selfFlags.muted = !selfFlags.muted;
        const me = findClient(SELF_ID); if (me) me.inputMuted = selfFlags.muted;
        pushState();
        return ok(selfFlags.muted);
      },
      ToggleRecording() {
        if (selfFlags.recordingSince) {
          const seconds = Math.round((Date.now() - selfFlags.recordingSince) / 1000);
          selfFlags.recordingSince = 0;
          const me = findClient(SELF_ID); if (me) me.isRecording = false;
          log('success', 'Gravação salva (' + Math.floor(seconds / 60) + ':' + String(seconds % 60).padStart(2, '0') + '): ~/Music/Furito TS/demonstração.wav');
          pushState();
          return ok({ recording: false, path: '~/Music/Furito TS/demonstração.wav', seconds });
        }
        if (st.status !== 'connected') return fail('conecte-se a um servidor para gravar a conversa');
        selfFlags.recordingSince = Date.now();
        const me = findClient(SELF_ID); if (me) me.isRecording = true;
        log('info', 'Gravando a conversa em ~/Music/Furito TS/demonstração.wav — os outros veem o indicador de gravação ao lado do seu nome.');
        pushState();
        return ok({ recording: true, path: '~/Music/Furito TS/demonstração.wav', seconds: 0 });
      },
      OpenRecordingsFolder() { return ok(null); },
      ToggleDeafen() {
        selfFlags.deafened = !selfFlags.deafened;
        const me = findClient(SELF_ID); if (me) me.outputMuted = selfFlags.deafened;
        pushState();
        return ok(selfFlags.deafened);
      },
      SetAway(away, message) {
        selfFlags.away = !!away;
        selfFlags.awayMessage = away ? String(message || '') : '';
        const me = findClient(SELF_ID); if (me) { me.away = selfFlags.away; me.awayMessage = selfFlags.awayMessage; }
        pushState();
        return ok(null);
      },
      SetEffect(effect) { audio.effect = String(effect || 'normal'); pushState(); return ok(null); },
      SetVADThreshold(v) { audio.vadThreshold = Math.max(50, Math.min(8000, Number(v) || 50)); pushState(); return ok(null); },
      SetVADFloor(v) { v = Number(v) || 0; audio.vadFloor = v > 0 ? Math.max(50, Math.min(8000, v)) : 0; pushState(); return ok(null); },
      // A calibração de mentira leva 0,6 s e "mede" uma voz de nível 2400.
      CalibrateVoice() {
        return new Promise((resolve) => setTimeout(() => {
          audio.vadFloor = 600; pushState();
          resolve({ voice: 2400, floor: 600 });
        }, 600));
      },
      SetOutputVolume(p) { audio.outputVolume = Math.max(0, Math.min(100, Number(p) || 0)); pushState(); return ok(null); },
      SoundboardPlay(name) {
        audio.soundboardPlaying = true; pushState();
        clearTimeout(api._sb);
        api._sb = setTimeout(() => { audio.soundboardPlaying = false; pushState(); }, 1200);
        return ok(null);
      },
      SoundboardStop() { clearTimeout(api._sb); audio.soundboardPlaying = false; pushState(); return ok(null); },
      _sbSeq: 0,
      SetNoiseSuppression(on) { audio.noiseSuppression = !!on; pushState(); return ok(null); },
      SetNoiseMode(mode) { audio.noiseMode = mode === 'classic' ? 'classic' : 'neural'; pushState(); return ok(null); },
      SetAutoGain(on) { audio.autoGain = !!on; pushState(); return ok(null); },
      SetEchoCancellation(on) { audio.echoCancellation = !!on; pushState(); return ok(null); },
      SetNormalizeVoices(on) { audio.normalizeVoices = !!on; pushState(); return ok(null); },
      SetMicTest(on) { audio.micTest = !!on; pushState(); return ok(null); },
      SetNotifySounds(on) { audio.notifySounds = !!on; pushState(); return ok(null); },
      SetNotifyPack(id) { audio.notifyPack = String(id); pushState(); return ok(null); },
      ImportSoundPack() {
        if (!audio.notifyPacks.some((x) => x.id === 'importado')) audio.notifyPacks = audio.notifyPacks.concat([{ id: 'importado', name: 'Pacote importado' }]);
        audio.notifyPack = 'importado';
        audio.notifySounds = true;
        pushState();
        return ok(1);
      },
      GetIcon(id) {
        // ícone de exemplo: um quadradinho colorido a partir do id
        const hue = (Number(id) * 47) % 360;
        const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><rect width="16" height="16" rx="4" fill="hsl(${hue},65%,50%)"/><circle cx="8" cy="8" r="3" fill="#fff"/></svg>`;
        return ok('data:image/svg+xml;base64,' + btoa(svg));
      },
      GetAvatar(clientId) {
        // avatar de exemplo: um retrato desenhado a partir do cliente e da marca do avatar dele
        const c = findClient(Number(clientId));
        if (!c) return fail('cliente não está mais online');
        if (!c.avatar) return fail('este cliente não tem avatar');
        const hue = (c.id * 67 + c.avatar.length * 53) % 360;
        const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="160" height="160"><rect width="160" height="160" fill="hsl(${hue},45%,82%)"/>` +
          `<circle cx="80" cy="64" r="30" fill="hsl(${hue},45%,42%)"/><path d="M24 160c4-38 28-56 56-56s52 18 56 56z" fill="hsl(${hue},45%,42%)"/></svg>`;
        return ok('data:image/svg+xml;base64,' + btoa(svg));
      },
      // só no modo demonstração: alguém põe, troca ("" = tira) o avatar
      _mockAvatar(clientId, flag) {
        const c = findClient(Number(clientId));
        if (!c) return fail('cliente não está mais online');
        c.avatar = String(flag || '');
        pushState();
        return ok(null);
      },
      ShowMicModes() { audio.micMode = audio.micMode === 'isolation' ? 'standard' : 'isolation'; pushState(); return ok(null); },
      SetSoundboardVolume(p) { audio.soundboardVolume = Math.max(0, Math.min(100, Number(p) || 0)); pushState(); return ok(null); },
      SoundboardAddFiles() {
        audio.sounds = audio.sounds.concat([{ id: 'n' + (++api._sbSeq), name: 'Som novo ' + audio.sounds.length, builtin: false }]);
        pushState();
        return ok(1);
      },
      SoundboardRemove(id) { audio.sounds = audio.sounds.filter((x) => x.id !== id || x.builtin); pushState(); return ok(null); },
      SoundboardRename(id, name) {
        audio.sounds = audio.sounds.map((x) => (x.id === id ? Object.assign({}, x, { name: String(name) }) : x));
        pushState();
        return ok(null);
      },

      // ---------------------------------------------------------- recursos do dia a dia e de administração
      SetNickname(name) {
        name = String(name || '').trim();
        if (name.length < 3) return fail('apelido muito curto');
        if (st.clients.some((c) => c.id !== SELF_ID && c.nickname.toLowerCase() === name.toLowerCase())) return fail('apelido já está em uso');
        settings.nickname = name;
        st.self.nickname = name;
        const me = findClient(SELF_ID); if (me) me.nickname = name;
        pushState();
        return ok(null);
      },
      UsePrivilegeKey(key) {
        if (st.status !== 'connected') return fail('não conectado');
        if (!/^[A-Za-z0-9+/=]{8,}$/.test(String(key || ''))) return fail('chave de privilégio inválida');
        const me = findClient(SELF_ID);
        if (me && !me.serverGroupIds.includes(6)) { me.serverGroupIds = me.serverGroupIds.filter((id) => id !== 8).concat([6]); refreshGroups(me); }
        log('success', 'Chave de privilégio aceita: você entrou no grupo [b]Server Admin[/b]');
        pushState();
        return ok(null);
      },
      ExportIdentity(id) {
        const e = findIdent(id || identActive);
        if (!e) return fail('identidade não encontrada');
        return ok('/Users/voce/Documents/furito-identidade-' + e.name.toLowerCase().replace(/[^a-z0-9]+/g, '-') + '.json');
      },
      ImportIdentity() {
        const uid = 'Zp7QmB1xImportadaK3vT9wYc2Ld=';
        const dup = identities.find((x) => x.uid === uid);
        if (dup) return fail('esta identidade já está na lista, com o nome "' + dup.name + '"');
        const e = { id: 'i' + (++identSeq), name: 'Usuario Normal', nickname: 'Fulano', uid, securityLevel: 11 };
        identities.push(e);
        pushState();
        return ok(e.id);
      },
      ImproveIdentity(id, level) {
        const e = findIdent(id || identActive);
        if (!e) return fail('identidade não encontrada');
        level = Math.floor(Number(level) || 0);
        if (level <= e.securityLevel) return fail('o nível pedido não é maior que o atual');
        return new Promise((resolve) => setTimeout(() => {
          e.securityLevel = Math.min(level, 40);
          pushState();
          resolve(e.securityLevel);
        }, 1500));
      },
      CreateIdentity(name) {
        name = String(name || '').trim();
        if (!name) return fail('informe um nome para a identidade');
        const e = { id: 'i' + (++identSeq), name, nickname: '', uid: fakeUid(), securityLevel: 8 };
        identities.push(e);
        pushState();
        return ok(e.id);
      },
      UpdateIdentity(id, name, nickname) {
        const e = findIdent(id);
        if (!e) return fail('identidade não encontrada');
        name = String(name || '').trim();
        if (!name) return fail('informe um nome para a identidade');
        e.name = name; e.nickname = String(nickname || '').trim();
        pushState();
        return ok(null);
      },
      SetDefaultIdentity(id) {
        if (!findIdent(id)) return fail('identidade não encontrada');
        identDefault = id;
        if (st.status === 'disconnected') identActive = id;
        pushState();
        return ok(null);
      },
      DeleteIdentity(id) {
        if (!findIdent(id)) return fail('identidade não encontrada');
        if (identities.length === 1) return fail('esta é a única identidade; crie ou importe outra antes de apagá-la');
        if (id === identActive && st.status !== 'disconnected') return fail('esta identidade está em uso na conexão atual; desconecte antes de apagá-la');
        identities = identities.filter((x) => x.id !== id);
        if (identDefault === id) identDefault = identities[0].id;
        if (identActive === id) identActive = identDefault;
        pushState();
        return ok(null);
      },

      SetVADMode(mode) { audio.vadMode = mode === 'level' || mode === 'ptt' ? mode : 'auto'; pttDown = false; pushState(); return ok(null); },
      SetHotkey(action, combo) {
        const fixed = ['ptt', 'toggleMute', 'toggleDeafen', 'soundStop'];
        combo = String(combo || '');
        if (String(action).indexOf('sound:') === 0) {
          // atalho de um som do soundboard: como no backend, combinação repetida ou tecla de digitar sozinha é recusada
          const id = String(action).slice(6);
          const snd = audio.sounds.find((x) => x.id === id);
          if (!snd) return fail('ação de atalho desconhecida');
          const set = (hotkey, hotkeyError) => { audio.sounds = audio.sounds.map((x) => (x.id === id ? Object.assign({}, x, { hotkey, hotkeyError }) : x)); pushState(); };
          const typing = /^(Key|Digit|Arrow)|^(Space|Enter|Backspace|Tab|Escape)$/.test(combo.split('+').pop()) && !/Ctrl|Alt|Cmd/.test(combo);
          const taken = combo && (fixed.some((other) => hotkeys[other] === combo) || audio.sounds.some((x) => x.id !== id && x.hotkey === combo));
          if (typing || taken) {
            const why = typing ? 'combine essa tecla com Ctrl, Alt ou Cmd, ou use uma tecla de função' : 'esse atalho já está em uso por outra ação';
            set(snd.hotkey || '', why);
            return fail(why);
          }
          set(combo, '');
          return ok(null);
        }
        if (!fixed.includes(action)) return fail('atalho desconhecido');
        delete hotkeys.errors[action];
        if (combo && (fixed.some((other) => other !== action && hotkeys[other] === combo) || audio.sounds.some((x) => x.hotkey === combo))) {
          hotkeys.errors[action] = 'combinação já usada por outro atalho';
        }
        hotkeys[action] = combo;
        pushState();
        return ok(null);
      },
      SetPTT(down) { pttDown = !!down; return ok(null); },
      SetInputDevice(name) {
        name = String(name || '');
        if (name && !audio.inputDevices.some((x) => x.name === name)) return fail('dispositivo não encontrado');
        audio.inputChoice = name;
        audio.inputDevice = name || audio.inputDevices.find((x) => x.isDefault).name;
        pushState();
        return ok(null);
      },
      SetOutputDevice(name) {
        name = String(name || '');
        if (name && !audio.outputDevices.some((x) => x.name === name)) return fail('dispositivo não encontrado');
        audio.outputChoice = name;
        audio.outputDevice = name || audio.outputDevices.find((x) => x.isDefault).name;
        pushState();
        return ok(null);
      },

      SetClientVolume(clientId, percent) {
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        c.volume = Math.max(0, Math.min(200, Math.round(Number(percent) || 0)));
        pushState();
        return ok(null);
      },
      SetClientMuted(clientId, muted) {
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        c.mutedLocally = !!muted;
        pushState();
        return ok(null);
      },
      SetContact(clientId, status) {
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        c.contact = status === 'friend' || status === 'blocked' ? status : '';
        pushState();
        return ok(null);
      },

      SaveBookmark(b) {
        b = b || {};
        const host = String(b.host || '').trim();
        if (!host) return fail('endereço do servidor vazio');
        const item = { id: String(b.id || ''), name: String(b.name || host), host, nickname: String(b.nickname || ''), defaultChannel: String(b.defaultChannel || ''), identityId: String(b.identityId || '') };
        const at = bookmarks.findIndex((x) => x.id === item.id);
        if (item.id && at === -1) return fail('favorito inexistente');
        if (at === -1) { item.id = 'b' + (++bmSeq); bookmarks.push(item); } else bookmarks[at] = item;
        pushState();
        return ok(item.id);
      },
      DeleteBookmark(id) {
        bookmarks = bookmarks.filter((x) => x.id !== id);
        pushState();
        return ok(null);
      },
      ConnectBookmark(id) {
        const b = bookmarks.find((x) => x.id === id);
        if (!b) return fail('favorito inexistente');
        return api.Connect({ host: b.host, nickname: b.nickname || settings.nickname, defaultChannel: b.defaultChannel, identityId: b.identityId || '' });
      },
      SetAutoReconnect(on) { autoReconnect = !!on; pushState(); return ok(null); },
      SetPokePopup(on) { pokePopup = !!on; pushState(); return ok(null); },
      SetRemoteImages(mode) {
        if (!/^(ask|always|never)$/.test(mode)) return fail('opção de imagens desconhecida');
        imagesCfg.mode = mode; pushState(); return ok(null);
      },
      SetServerImages(on) {
        if (st.status !== 'connected') return fail('conecte-se ao servidor para escolher isto');
        imagesCfg.trusted = !!on; pushState(); return ok(null);
      },

      ScreenSend(to, payload) {
        if (st.status !== 'connected') return fail('não conectado');
        peerHandle(Number(to) || 0, String(payload || ''));
        return ok(null);
      },
      SetScreenSettings(stunUrl, turnUrl, turnUser, turnPass, relayOnly) {
        if (stunUrl && stunUrl !== 'none' && !/^stuns?:.+/.test(stunUrl)) return fail('servidor STUN: endereço inválido: use o formato stun:servidor:porta');
        if (turnUrl && !/^turns?:.+/.test(turnUrl)) return fail('servidor de retransmissão: endereço inválido: use o formato turn:servidor:porta');
        if (relayOnly && !turnUrl) return fail('para esconder o IP é preciso informar um servidor de retransmissão (TURN)');
        Object.assign(screenCfg, { stunUrl: String(stunUrl || ''), turnUrl: String(turnUrl || ''), turnUser: String(turnUser || ''), relayOnly: !!relayOnly });
        pushState();
        return ok(null);
      },
      AcceptScreenWarning() { screenCfg.warned = true; pushState(); return ok(null); },
      ScreenDisplays() { return ok([]); },
      ScreenCaptureStart() { return fail('o modo demonstração não captura a tela de verdade'); },
      ScreenCaptureStop() { return ok(null); },
      // só no modo demonstração: o outro cliente começa/para de compartilhar e conta o que recebeu
      _mockPeerShare(on) {
        peer.sharingSid = on ? 'mock' + Date.now().toString(36) : '';
        if (on) peerSignal({ t: 'sharing', sid: peer.sharingSid });
        else { peerSignal({ t: 'stopped', sid: '' }); if (peer.out) { peer.out.close(); peer.out = null; } clearInterval(peer.patternTimer); }
        return ok(null);
      },
      _mockPeerReceived() { return ok(peer.received); },

      MoveClient(clientId, channelId, password) {
        if (st.status !== 'connected') return fail('não conectado');
        const c = findClient(clientId), dest = findChannel(channelId);
        if (!c) return fail('cliente não está mais online');
        if (!dest) return fail('canal inexistente');
        if (dest.hasPassword && String(password || '') !== pwOf(dest)) return fail('senha do canal inválida (no modo demonstração a senha é 1234)');
        c.channelId = channelId;
        if (clientId === SELF_ID) st.self.channelId = channelId;
        log('info', '[b]"' + c.nickname + '"[/b] foi movido para o canal [b]"' + dest.name + '"[/b]');
        pushState();
        return ok(null);
      },
      KickClient(clientId, fromServer, reason) {
        if (st.status !== 'connected') return fail('não conectado');
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        if (clientId === SELF_ID) return fail('você não pode expulsar a si mesmo');
        const why = reason ? ' (' + reason + ')' : '';
        if (fromServer) {
          removeClient(clientId);
          log('warn', '[b]"' + c.nickname + '"[/b] foi expulso do servidor por [b]"' + settings.nickname + '"[/b]' + why);
        } else {
          c.channelId = defaultChannel().id;
          log('warn', '[b]"' + c.nickname + '"[/b] foi expulso do canal por [b]"' + settings.nickname + '"[/b]' + why);
        }
        pushState();
        return ok(null);
      },
      BanClient(clientId, seconds, reason) {
        if (st.status !== 'connected') return fail('não conectado');
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        if (clientId === SELF_ID) return fail('você não pode banir a si mesmo');
        seconds = Math.max(0, Number(seconds) || 0);
        removeClient(clientId);
        log('warn', '[b]"' + c.nickname + '"[/b] foi banido ' + (seconds ? 'por ' + seconds + ' segundos' : 'permanentemente') + (reason ? ' (' + reason + ')' : ''));
        pushState();
        return ok(null);
      },
      PokeClient(clientId, message) {
        if (st.status !== 'connected') return fail('não conectado');
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        log('info', 'Você cutucou [b]"' + c.nickname + '"[/b]' + (message ? ': ' + message : ''));
        // no modo demonstração a pessoa cutuca de volta, para exercitar o evento "poke"
        setTimeout(() => {
          if (!findClient(clientId)) return;
          log('warn', '"' + c.nickname + '" cutucou você: para de me cutucar!'); // a linha no registro sai sempre
          if (pokePopup) emit('poke', { fromName: c.nickname, message: 'para de me cutucar!' });
        }, 900);
        return ok(null);
      },
      SetClientTalker(clientId, on) {
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        c.isTalker = !!on;
        if (on) { c.talkRequest = false; c.talkRequestMessage = ''; if (clientId === SELF_ID) talkRequested = false; }
        pushState();
        return ok(null);
      },
      RequestTalk(on, message) {
        if (st.status !== 'connected') return fail('não conectado');
        talkRequested = !!on;
        const me = findClient(SELF_ID);
        if (me) { me.talkRequest = talkRequested; me.talkRequestMessage = talkRequested ? String(message || '') : ''; }
        pushState();
        return ok(null);
      },
      SetWhisperTarget(kind, id) {
        if (kind === 'client') {
          const c = findClient(id);
          if (!c) return fail('cliente não está mais online');
          whisper = { kind, id, name: c.nickname };
        } else if (kind === 'channel') {
          const c = findChannel(id);
          if (!c) return fail('canal inexistente');
          whisper = { kind, id, name: c.name };
        } else {
          whisper = { kind: '', id: 0, name: '' };
        }
        pushState();
        return ok(null);
      },

      CreateChannel(spec) {
        if (st.status !== 'connected') return fail('não conectado');
        spec = spec || {};
        const name = String(spec.name || '').trim();
        if (!name) return fail('nome de canal inválido');
        const parentId = Number(spec.parentId) || 0;
        const parent = parentId ? findChannel(parentId) : null;
        if (parentId && !parent) return fail('canal pai inexistente');
        if (st.channels.some((c) => c.parentId === parentId && c.name === name)) return fail('já existe um canal com esse nome');
        const id = st.channels.reduce((m, c) => Math.max(m, c.id), 0) + 1;
        const c = ch(id, parentId, parent ? (parent.depth || 0) + 1 : 0, name);
        applySpec(c, spec, true);
        let at = st.channels.length;
        if (parent) {
          const inside = subtreeIds(parentId);
          at = st.channels.reduce((last, x, i) => (inside.includes(x.id) ? i : last), -1) + 1;
        }
        st.channels.splice(at, 0, c);
        // como no TeamSpeak: quem cria o canal é movido para dentro dele
        const me = findClient(SELF_ID); if (me) me.channelId = id;
        st.self.channelId = id;
        log('info', 'Canal [b]"' + name + '"[/b] criado');
        pushState();
        return ok(null);
      },
      EditChannel(channelId, spec) {
        if (st.status !== 'connected') return fail('não conectado');
        spec = spec || {};
        const c = findChannel(channelId);
        if (!c) return fail('canal inexistente');
        const name = String(spec.name || '').trim();
        if (!name) return fail('nome de canal inválido');
        if (st.channels.some((x) => x.id !== c.id && x.parentId === c.parentId && x.name === name)) return fail('já existe um canal com esse nome');
        applySpec(c, spec, false);
        log('info', 'Canal [b]"' + name + '"[/b] editado');
        pushState();
        return ok(null);
      },
      DeleteChannel(channelId) {
        if (st.status !== 'connected') return fail('não conectado');
        const c = findChannel(channelId);
        if (!c) return fail('canal inexistente');
        const ids = subtreeIds(channelId);
        if (ids.some((id) => findChannel(id).isDefault)) return fail('o canal padrão não pode ser apagado');
        const def = defaultChannel();
        st.clients.forEach((x) => { if (ids.includes(x.channelId)) x.channelId = def.id; });
        if (ids.includes(st.self.channelId)) st.self.channelId = def.id;
        st.channels = st.channels.filter((x) => !ids.includes(x.id));
        if (whisper.kind === 'channel' && ids.includes(whisper.id)) whisper = { kind: '', id: 0, name: '' };
        log('info', 'Canal [b]"' + c.name + '"[/b] apagado');
        pushState();
        return ok(null);
      },

      AddServerGroup(clientId, groupId) {
        const c = findClient(clientId), g = groups.server.find((x) => x.id === groupId);
        if (!c) return fail('cliente não está mais online');
        if (!g) return fail('grupo inexistente');
        if (!c.serverGroupIds.includes(groupId)) c.serverGroupIds = c.serverGroupIds.concat([groupId]);
        refreshGroups(c);
        pushState();
        return ok(null);
      },
      RemoveServerGroup(clientId, groupId) {
        const c = findClient(clientId);
        if (!c) return fail('cliente não está mais online');
        c.serverGroupIds = c.serverGroupIds.filter((id) => id !== groupId);
        refreshGroups(c);
        pushState();
        return ok(null);
      },
      SetChannelGroup(clientId, groupId) {
        const c = findClient(clientId), g = groups.channel.find((x) => x.id === groupId);
        if (!c) return fail('cliente não está mais online');
        if (!g) return fail('grupo inexistente');
        c.channelGroupId = groupId;
        refreshGroups(c);
        pushState();
        return ok(null);
      },
      GetGroupPermissions(kind, groupId) {
        if (st.status !== 'connected') return fail('não conectado');
        if (!(groups[kind] || []).some((g) => g.id === groupId)) return fail('grupo inexistente');
        return ok(perms[kind + ':' + groupId] || []);
      },
      SetGroupPermission(kind, groupId, name, value, negated, skip) {
        if (st.status !== 'connected') return fail('não conectado');
        if (!(groups[kind] || []).some((g) => g.id === groupId)) return fail('grupo inexistente');
        name = String(name || '');
        if (!/^[bi]_[a-z0-9_]+$/.test(name)) return fail('permissão desconhecida: ' + name);
        const list = perms[kind + ':' + groupId] || (perms[kind + ':' + groupId] = []);
        const item = { name, value: Math.round(Number(value) || 0), negated: !!negated, skip: !!skip };
        const at = list.findIndex((p) => p.name === name);
        if (at === -1) list.push(item); else list[at] = item;
        list.sort((a, b) => a.name.localeCompare(b.name));
        return ok(null);
      },
      RemoveGroupPermission(kind, groupId, name) {
        const key = kind + ':' + groupId;
        perms[key] = (perms[key] || []).filter((p) => p.name !== name);
        return ok(null);
      },

      ListChannelFiles(channelId, path, password) {
        const err = fileAccess(channelId, password);
        if (err) return fail(err);
        const list = filesOf(channelId)[normPath(path)];
        return list ? ok(list) : fail('pasta inexistente');
      },
      DownloadChannelFile(channelId, path, password) {
        const err = fileAccess(channelId, password);
        if (err) return fail(err);
        const p = normPath(path);
        const name = p.split('/').pop();
        const list = filesOf(channelId)[parentPath(p)] || [];
        if (!list.some((f) => f.name === name && !f.isDir)) return fail('arquivo inexistente');
        return ok('/Users/voce/Downloads/' + name);
      },
      UploadChannelFile(channelId, dir, password) {
        const err = fileAccess(channelId, password);
        if (err) return fail(err);
        const list = filesOf(channelId)[normPath(dir)];
        if (!list) return fail('pasta inexistente');
        list.push({ name: 'enviado-' + (list.length + 1) + '.png', size: 245760, modified: Math.floor(Date.now() / 1000), isDir: false });
        return ok(1);
      },
      DeleteChannelFile(channelId, path, password) {
        const err = fileAccess(channelId, password);
        if (err) return fail(err);
        const p = normPath(path);
        const all = filesOf(channelId);
        const list = all[parentPath(p)] || [];
        const at = list.findIndex((f) => f.name === p.split('/').pop());
        if (at === -1) return fail('arquivo inexistente');
        list.splice(at, 1);
        Object.keys(all).forEach((k) => { if (k === p || k.indexOf(p + '/') === 0) delete all[k]; });
        return ok(null);
      },
      CreateChannelDirectory(channelId, path, password) {
        const err = fileAccess(channelId, password);
        if (err) return fail(err);
        const p = normPath(path);
        const name = p.split('/').pop();
        const all = filesOf(channelId);
        const list = all[parentPath(p)];
        if (!list || !name) return fail('pasta inexistente');
        if (list.some((f) => f.name === name)) return fail('já existe um arquivo ou pasta com esse nome');
        list.push({ name, size: 0, modified: Math.floor(Date.now() / 1000), isDir: true });
        all[p] = [];
        return ok(null);
      },

      ClearHistory() {
        for (let i = messages.length - 1; i >= 0; i--) if (messages[i].history) messages.splice(i, 1);
        return ok(null);
      },

      // scripts (automações)
      GetScripts() { return seedScripts().then(() => clone(scripts)); },
      SaveScript(def) {
        def = def || {};
        const name = String(def.name || '').trim();
        if (!name) return fail('dê um nome ao script');
        const saved = { id: def.id || 'm' + (++scriptSeq), name, code: String(def.code || ''), enabled: !!def.enabled, moderation: !!def.moderation };
        const i = scripts.findIndex((x) => x.id === saved.id);
        if (def.id && i === -1) return fail('script não encontrado');
        if (i === -1) scripts.push(saved); else scripts[i] = saved;
        return ok(saved);
      },
      DeleteScript(id) {
        const i = scripts.findIndex((x) => x.id === id);
        if (i === -1) return fail('script não encontrado');
        scripts.splice(i, 1);
        return ok(null);
      },
      SetScriptEnabled(id, on) {
        const sc = scripts.find((x) => x.id === id);
        if (!sc) return fail('script não encontrado');
        sc.enabled = !!on;
        return ok(null);
      },
      // tudo o que um script pede passa por aqui (no app de verdade, com limite contra flood)
      ScriptCall(id, action, args) {
        const sc = scripts.find((x) => x.id === id);
        if (!sc || !sc.enabled) return fail('este script está desligado');
        args = args || {};
        const on = st.status === 'connected';
        const text = String(args.text || '');
        switch (action) {
          case 'sendChannelMessage': return text.trim() ? api.SendText(2, 0, text) : fail('mensagem vazia');
          case 'sendServerMessage': return text.trim() ? api.SendText(3, 0, text) : fail('mensagem vazia');
          case 'sendPrivateMessage':
            if (!text.trim()) return fail('mensagem vazia');
            return on && Number(args.clientId) !== SELF_ID && findClient(Number(args.clientId)) ? api.SendText(1, Number(args.clientId), text) : fail(on ? 'cliente não encontrado' : 'não conectado');
          case 'poke': return api.PokeClient(Number(args.clientId), text);
          case 'moveClient': case 'kickFromChannel': case 'kickFromServer': case 'ban': case 'setTalker': {
            if (!sc.moderation) return fail('este script não tem permissão para moderar: marque "Pode moderar" nele, na janela de scripts');
            if (!on) return fail('não conectado');
            const target = Number(args.clientId);
            if (target === SELF_ID || !findClient(target)) return fail('cliente não encontrado');
            const reason = String(args.reason || '');
            if (action === 'moveClient') return findChannel(Number(args.channelId)) ? api.MoveClient(target, Number(args.channelId), String(args.password || '')) : fail('canal não encontrado');
            if (action === 'setTalker') return api.SetClientTalker(target, !!args.talker);
            if (action === 'ban') return typeof args.seconds === 'number' && args.seconds >= 0 ? api.BanClient(target, args.seconds, reason) : fail('informe a duração do banimento em segundos (0 = permanente)');
            return api.KickClient(target, action === 'kickFromServer', reason);
          }
          case 'playSound': {
            const snd = audio.sounds.find((x) => x.name.toLowerCase() === String(args.name || '').trim().toLowerCase());
            if (!snd) return fail('não há som chamado "' + args.name + '" no soundboard');
            return args.local ? ok(null) : api.SoundboardPlay(snd.id);
          }
          case 'stopSound': return api.SoundboardStop();
          case 'setAway': return api.SetAway(!!args.away, String(args.message || ''));
          // um script só fecha o microfone e os alto-falantes, nunca abre
          case 'setMuted':
            if (!args.muted) return selfFlags.muted ? fail(UNMUTE_ERR) : ok(null);
            return selfFlags.muted ? ok(null) : api.ToggleMute().then(() => null);
          case 'setDeafened':
            if (!args.deafened) return selfFlags.deafened ? fail(UNMUTE_ERR) : ok(null);
            return selfFlags.deafened ? ok(null) : api.ToggleDeafen().then(() => null);
          case 'switchChannel': return api.JoinChannel(Number(args.channelId), String(args.password || ''));
          case 'getSelf': {
            const ch = on ? findChannel(st.self.channelId) : null;
            return ok({
              connected: on, id: on ? SELF_ID : 0, nickname: on ? st.self.nickname : '', channelId: on ? st.self.channelId : 0, channelName: ch ? ch.name : '',
              server: on ? st.server.name : '', muted: selfFlags.muted, deafened: selfFlags.deafened, away: selfFlags.away, awayMessage: selfFlags.awayMessage,
            });
          }
          case 'getClients': return ok(on ? st.clients.map(scriptClient) : []);
          case 'getChannels': return ok(on ? st.channels.map(scriptChannel) : []);
          case 'getSounds': return ok(audio.sounds.map((x) => x.name));
          default: return fail('ação desconhecida: "' + action + '"');
        }
      },
      // só no modo demonstração: outra pessoa escreve no bate-papo do canal ou em privado para nós
      _mockSay(clientId, text, priv) {
        const c = findClient(clientId);
        if (st.status !== 'connected' || !c) return fail('cliente não está mais online');
        chat(priv ? 1 : 2, c.id, c.nickname, String(text), priv ? { peerId: c.id, peerName: c.nickname } : undefined);
        return ok(null);
      },
      // só no modo demonstração: alguém conecta e entra no nosso canal (true) ou sai do servidor (false)
      _mockVisitor(on) {
        if (st.status !== 'connected') return ok(null);
        const id = 77;
        const here = scriptChannel(findChannel(st.self.channelId));
        const old = findClient(id);
        if (on && !old) {
          const c = cl(id, st.self.channelId, 'Visitante');
          st.clients.push(c);
          log('info', '[b]"Visitante"[/b] conectou ao canal [b]"' + (here ? here.name : '') + '"[/b]');
          pushState();
          scriptEvent({ type: 'clientConnected', client: scriptClient(c), channel: here, to: here, reason: 'connected' });
          scriptEvent({ type: 'clientJoined', client: scriptClient(c), channel: here, to: here, reason: 'connected' });
        } else if (!on && old) {
          const was = scriptChannel(findChannel(old.channelId));
          st.clients = st.clients.filter((x) => x.id !== id);
          log('info', '[b]"Visitante"[/b] desconectou');
          pushState();
          if (old.channelId === st.self.channelId) scriptEvent({ type: 'clientLeft', client: scriptClient(old), channel: was, from: was, reason: 'disconnected' });
          scriptEvent({ type: 'clientDisconnected', client: scriptClient(old), channel: was, from: was, reason: 'disconnected' });
        }
        return ok(null);
      },

      // só no modo demonstração: simula uma queda de conexão seguida de reconexão automática
      _mockDrop() {
        if (st.status !== 'connected') return ok(null);
        const host = st.host;
        const token = ++connToken;
        st = offline('Conexão com o servidor perdida');
        st.status = 'reconnecting';
        st.host = host;
        reconnectAttempt = 1;
        log('warn', 'Conexão com o servidor perdida. Reconectando…');
        pushState();
        setTimeout(() => {
          if (token !== connToken) return;
          reconnectAttempt = 0;
          seedOnline(host, false);
          pushState();
        }, 700);
        return ok(null);
      },

      RequestChannelInfo(channelId) {
        setTimeout(() => {
          const c = findChannel(channelId);
          if (c && DESCRIPTIONS[channelId] && c.description !== DESCRIPTIONS[channelId]) { c.description = DESCRIPTIONS[channelId]; pushState(); }
        }, 150);
        return ok(null);
      },
      RequestClientInfo(clientId) {
        setTimeout(() => {
          const c = findClient(clientId);
          if (c && CLIENT_DETAILS[clientId] && !c.connectedSince) { Object.assign(c, CLIENT_DETAILS[clientId]); pushState(); }
        }, 150);
        return ok(null);
      },
    };

    return {
      api,
      on(name, cb) { (listeners[name] = listeners[name] || []).push(cb); },
    };
  }

  window.FuritoBridge = { init };
})();
