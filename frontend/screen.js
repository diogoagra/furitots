/*
 * screen.js — compartilhamento de tela entre clientes Furito.
 *
 * O servidor TeamSpeak não transporta vídeo. Os clientes combinam a
 * transmissão por mensagens curtas que o servidor repassa (ScreenSend /
 * evento "screen:signal") e o vídeo vai direto de um computador ao outro por
 * WebRTC, codificado pelo navegador embutido.
 *
 * Mensagens (JSON, campo "t"):
 *   hello               quem entra no canal pergunta se há alguém compartilhando
 *   sharing  {sid}      quem compartilha avisa o canal (e responde ao hello)
 *   stopped  {sid}      parou de compartilhar
 *   watch    {sid}      espectador → quem compartilha: quero assistir
 *   offer    {sid,sdp}  quem compartilha → espectador
 *   answer   {sid,sdp}  espectador → quem compartilha
 *   leave    {sid}      espectador saiu
 *   full     {sid}      limite de espectadores atingido
 *
 * Com vários servidores abertos (um por aba), a transmissão pertence à aba em
 * que começou: quem compartilha continua compartilhando ao olhar outra aba
 * (o estado da aba da transmissão passa a ser consultado de tempos em tempos);
 * quem assiste para de assistir ao sair da aba, porque o vídeo é mostrado nela.
 *
 * Uso:  FuritoScreen.init({ call, forTab, getState, notify, changed })
 *       FuritoScreen.onState(state) a cada estado da aba à vista; FuritoScreen.onSignal(msg) a cada evento.
 */
(function () {
  'use strict';

  const MAX_VIEWERS = 6;
  const QUALITIES = {
    720: { w: 1280, h: 720, bitrate: 1500000, label: '720p' },
    1080: { w: 1920, h: 1080, bitrate: 3000000, label: '1080p' },
  };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
  const newId = () => Math.random().toString(36).slice(2, 10) + Date.now().toString(36).slice(-4);

  const POLL_MS = 1500; // de quanto em quanto tempo se consulta a aba da transmissão quando ela não está à vista

  let deps = null;
  // O que se sabe de cada aba: aba → { st (o último estado visto), sharers: Map(id do cliente → { sid, name }),
  // lastChannel, lastConnected, helloTimer, rehello }
  const tabs = new Map();
  let viewTab = 0;           // a aba à vista
  let share = null;          // { tab, sid, source, track, viewers: Map(id → { pc, state }), quality, fps }
  let watch = null;          // { tab, from, name, sid, pc, state, timer }
  let videoEl = null;
  let pollTimer = 0;

  const state = () => deps.getState(); // o estado da aba à vista
  const changed = () => { if (deps && deps.changed) deps.changed(); };
  const errText = (e) => (e && (e.message || e.error)) || String(e || 'erro desconhecido');

  function ctx(tab) {
    let c = tabs.get(tab);
    if (!c) { c = { st: null, sharers: new Map(), lastChannel: 0, lastConnected: false, helloTimer: 0, rehello: false }; tabs.set(tab, c); }
    return c;
  }
  // tabState devolve o último estado visto de uma aba (o de "desconectado" se não se sabe nada dela).
  const tabState = (tab) => (tabs.get(tab) && tabs.get(tab).st) || { status: 'disconnected', self: { clientId: 0, channelId: 0 }, clients: [] };
  // fresh: só se confia no estado da aba à vista e no da aba da transmissão (que é consultada).
  const fresh = (tab) => tab === viewTab || (!!share && share.tab === tab);

  // callTab chama o backend numa aba específica (sem forTab, só existe a aba atual).
  function callTab(tab, name, ...args) {
    return deps.forTab ? Promise.resolve(deps.forTab(tab)[name](...args)) : deps.call(name, ...args);
  }

  function send(tab, to, msg) {
    return callTab(tab, 'ScreenSend', to, JSON.stringify(msg)).catch((e) => { deps.notify('error', 'Compartilhamento de tela: ' + errText(e)); throw e; });
  }

  function rtcConfig() {
    const sc = state().screen || {};
    return {
      iceServers: (sc.iceServers || []).map((s) => ({ urls: s.urls, username: s.username || undefined, credential: s.credential || undefined })),
      iceTransportPolicy: sc.relayOnly ? 'relay' : 'all',
      bundlePolicy: 'max-bundle',
    };
  }

  // Espera a coleta de endereços terminar (ou o prazo): a oferta/resposta vai
  // de uma vez só, com todos os candidatos, para gastar um comando só.
  function gathered(pc, ms) {
    if (pc.iceGatheringState === 'complete') return Promise.resolve();
    return new Promise((resolve) => {
      const done = () => { clearTimeout(timer); pc.removeEventListener('icegatheringstatechange', check); resolve(); };
      const check = () => { if (pc.iceGatheringState === 'complete') done(); };
      const timer = setTimeout(done, ms);
      pc.addEventListener('icegatheringstatechange', check);
    });
  }

  // ------------------------------------------------------------------ fontes de vídeo

  // Captura feita pelo app (macOS): os quadros chegam em JPEG por um endereço
  // da própria janela e são desenhados num canvas, que vira a faixa de vídeo.
  async function nativeSource(displayId, q, fps) {
    await deps.call('ScreenCaptureStart', displayId, q.w, q.h, fps);
    const cv = document.createElement('canvas');
    cv.width = q.w; cv.height = q.h;
    const ctx = cv.getContext('2d', { alpha: false });
    ctx.fillStyle = '#000'; ctx.fillRect(0, 0, cv.width, cv.height);
    // Com a janela escondida o navegador redesenha a página mais devagar, e a
    // captura automática do canvas cai junto. Pedindo cada quadro na mão
    // (captureStream(0) + requestFrame) o vídeo segue o ritmo da captura.
    let stream = cv.captureStream(0);
    let push = () => {};
    const vtrack = stream.getVideoTracks()[0];
    if (vtrack && typeof vtrack.requestFrame === 'function') push = () => vtrack.requestFrame();
    else { stream.getTracks().forEach((t) => t.stop()); stream = cv.captureStream(fps); } // sem requestFrame: captura automática
    const src = { stream, frames: 0, alive: true, onended: null };
    src.stop = () => {
      if (!src.alive) return;
      src.alive = false;
      stream.getTracks().forEach((t) => t.stop());
      deps.call('ScreenCaptureStop').catch(() => {});
    };
    (async function pump() {
      let seq = 0;
      while (src.alive) {
        try {
          const res = await fetch('/__screen/frame.jpg?after=' + seq, { cache: 'no-store' });
          if (!src.alive) return;
          if (res.status === 200) {
            const n = Number(res.headers.get('X-Frame-Seq'));
            const bmp = await createImageBitmap(await res.blob());
            if (n > 0) seq = n; else await sleep(1000 / fps); // sem o número do quadro, segura o ritmo
            if (cv.width !== bmp.width || cv.height !== bmp.height) { cv.width = bmp.width; cv.height = bmp.height; }
            ctx.drawImage(bmp, 0, 0);
            if (bmp.close) bmp.close();
            push();
            src.frames++;
          } else if (res.headers.get('X-Screen-Ended') === '1') {
            if (src.alive && src.onended) src.onended('a captura de tela foi encerrada pelo sistema');
            return;
          } else if (res.status !== 204) {
            await sleep(300);
          }
        } catch (e) {
          await sleep(300);
        }
      }
    })();
    return src;
  }

  // Animação gerada na própria página: para testes e para o modo demonstração.
  function canvasPattern(q, fps) {
    const cv = document.createElement('canvas');
    cv.width = q.w; cv.height = q.h;
    const ctx = cv.getContext('2d', { alpha: false });
    const colors = ['#c82828', '#28a03c', '#2850c8', '#d2aa1e'];
    const t0 = Date.now();
    const draw = () => {
      const t = Date.now() - t0;
      ctx.fillStyle = colors[Math.floor(t / 1000) % colors.length];
      ctx.fillRect(0, 0, cv.width, cv.height);
      ctx.fillStyle = '#fff';
      ctx.fillRect((t % 2000) * cv.width / 2000, cv.height / 3, cv.width / 20, cv.height / 3);
      src.frames++;
    };
    const stream = cv.captureStream(fps);
    const src = { stream, frames: 0, alive: true, onended: null };
    const timer = setInterval(draw, 1000 / fps);
    draw();
    src.stop = () => { src.alive = false; clearInterval(timer); stream.getTracks().forEach((t) => t.stop()); };
    return src;
  }

  // Captura do navegador embutido (Windows e navegadores comuns): ele mesmo
  // mostra a janela de escolha de tela ou janela.
  async function browserSource(q, fps) {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getDisplayMedia) throw new Error('este sistema não oferece captura de tela');
    let stream;
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: { ideal: fps, max: fps }, width: { max: q.w }, height: { max: q.h } }, audio: false });
    } catch (e) {
      if (e && e.name === 'NotAllowedError') throw new Error('a captura de tela foi cancelada ou não foi permitida');
      throw e;
    }
    const src = { stream, frames: 0, alive: true, onended: null };
    src.stop = () => { src.alive = false; stream.getTracks().forEach((t) => t.stop()); };
    const track = stream.getVideoTracks()[0];
    if (track) track.addEventListener('ended', () => { if (src.alive && src.onended) src.onended(''); }); // o usuário parou pela barra do sistema
    return src;
  }

  function openSource(opts, q, fps) {
    if (opts.source === 'pattern-js') return Promise.resolve(canvasPattern(q, fps));
    if (opts.source === 'pattern') return nativeSource(-1, q, fps);
    if ((state().screen || {}).nativeCapture) return nativeSource(Number(opts.displayId) || 0, q, fps);
    return browserSource(q, fps);
  }

  // ------------------------------------------------------------------ quem compartilha

  async function start(opts) {
    opts = opts || {};
    if (share) return;
    const tab = viewTab; // a transmissão é desta aba, mesmo que a pessoa troque de aba enquanto escolhe a tela
    if (tabState(tab).status !== 'connected') throw new Error('conecte-se a um servidor para compartilhar a tela');
    const quality = QUALITIES[opts.quality] ? Number(opts.quality) : 1080;
    const q = QUALITIES[quality];
    const fps = clamp(Number(opts.fps) || 15, 1, 30);
    const source = await openSource(opts, q, fps);
    const track = source.stream.getVideoTracks()[0];
    if (!track) { source.stop(); throw new Error('a captura não entregou vídeo'); }
    try { track.contentHint = 'detail'; } catch (e) { /* opcional */ }
    if (tabState(tab).status !== 'connected') { source.stop(); throw new Error('a conexão caiu antes de a captura começar'); }
    share = { tab, sid: newId(), source, track, viewers: new Map(), quality, fps };
    source.onended = (why) => { if (why) deps.notify('error', 'Compartilhamento de tela: ' + why); stop(); };
    changed();
    pollShareTab();
    await send(tab, 0, { t: 'sharing', sid: share.sid }).catch(() => {});
    deps.notify('info', 'Você está compartilhando a tela com o canal.');
  }

  function dropViewer(id) {
    const v = share && share.viewers.get(id);
    if (!v) return;
    share.viewers.delete(id);
    try { v.pc.close(); } catch (e) { /* já fechada */ }
    changed();
  }

  function stop(silent) {
    if (!share) return;
    const s = share;
    share = null;
    for (const v of s.viewers.values()) { try { v.pc.close(); } catch (e) { /* já fechada */ } }
    s.viewers.clear();
    s.source.onended = null;
    s.source.stop();
    clearInterval(pollTimer); pollTimer = 0;
    if (!silent && tabState(s.tab).status === 'connected') send(s.tab, 0, { t: 'stopped', sid: s.sid }).catch(() => {});
    changed();
  }

  // Com a aba da transmissão fora da vista, o estado dela deixa de chegar
  // sozinho: é consultado, para a transmissão acabar se a conexão cair ou
  // sairmos do canal, e para tirar os espectadores que foram embora.
  function pollShareTab() {
    const wanted = !!share && share.tab !== viewTab;
    if (!wanted) { clearInterval(pollTimer); pollTimer = 0; return; }
    if (pollTimer) return;
    pollTimer = setInterval(() => {
      const s = share;
      if (!s || s.tab === viewTab) { clearInterval(pollTimer); pollTimer = 0; return; }
      callTab(s.tab, 'GetState').then(
        (st) => { if (share === s && s.tab !== viewTab && st) process(s.tab, st); },
        () => { if (share === s) { stop(true); deps.notify('info', 'O compartilhamento de tela foi encerrado: a aba em que ele estava foi fechada.'); } });
    }, POLL_MS);
  }

  // Respostas automáticas à sinalização de outros clientes. Qualquer um no
  // canal manda essas mensagens, quantas quiser: sem um ritmo, as nossas
  // respostas (cada uma é um comando ao servidor) fariam o servidor nos
  // bloquear por flood a pedido de outra pessoa.
  const REPLY_GAP = 2000;                 // ms entre respostas a um mesmo cliente
  const REPLY_WINDOW = 10000, REPLY_MAX = 12; // e no total, por janela de tempo
  const lastReply = new Map();
  let replyTimes = [];
  function mayReply(tab, from, kind) { // o ritmo é por cliente e por tipo de pedido (perguntar e assistir não se atrapalham)
    const now = Date.now(), key = kind + ':' + tab + ':' + from;
    if (now - (lastReply.get(key) || 0) < REPLY_GAP) return false;
    replyTimes = replyTimes.filter((t) => now - t < REPLY_WINDOW);
    if (replyTimes.length >= REPLY_MAX) return false;
    replyTimes.push(now);
    if (lastReply.size > 1024) lastReply.clear();
    lastReply.set(key, now);
    return true;
  }

  async function onWatch(tab, from, msg) {
    if (!share || share.tab !== tab) return; // não estamos compartilhando neste servidor: nada a responder
    if (!mayReply(tab, from, 'watch')) return;
    if (msg.sid !== share.sid) { send(tab, from, { t: 'stopped', sid: msg.sid || '' }).catch(() => {}); return; }
    if (share.viewers.has(from)) dropViewer(from); // pediu de novo: recomeça
    if (share.viewers.size >= MAX_VIEWERS) { send(tab, from, { t: 'full', sid: share.sid }).catch(() => {}); return; }
    const s = share;
    const pc = new RTCPeerConnection(rtcConfig());
    const viewer = { pc, state: 'connecting' };
    s.viewers.set(from, viewer);
    changed();
    pc.addEventListener('connectionstatechange', () => {
      if (pc.connectionState === 'connected') { viewer.state = 'live'; changed(); }
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') { if (share === s && s.viewers.get(from) === viewer) dropViewer(from); }
    });
    try {
      const sender = pc.addTrack(s.track, s.source.stream);
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      try {
        const p = sender.getParameters();
        if (!p.encodings || !p.encodings.length) p.encodings = [{}];
        p.encodings[0].maxBitrate = QUALITIES[s.quality].bitrate;
        p.encodings[0].maxFramerate = s.fps;
        p.degradationPreference = 'maintain-resolution'; // texto legível vale mais que fluidez
        await sender.setParameters(p);
      } catch (e) { /* limites são opcionais */ }
      await gathered(pc, 2500);
      if (share !== s || s.viewers.get(from) !== viewer) return;
      await send(tab, from, { t: 'offer', sid: s.sid, sdp: pc.localDescription.sdp });
    } catch (e) {
      if (share === s && s.viewers.get(from) === viewer) dropViewer(from);
    }
  }

  async function onAnswer(from, msg) {
    const v = share && msg.sid === share.sid && share.viewers.get(from);
    if (!v || typeof msg.sdp !== 'string') return;
    try { await v.pc.setRemoteDescription({ type: 'answer', sdp: msg.sdp }); } catch (e) { dropViewer(from); }
  }

  // ------------------------------------------------------------------ quem assiste

  function showStream(stream) {
    if (!videoEl) return;
    videoEl.srcObject = stream;
    if (stream) { const p = videoEl.play(); if (p && p.catch) p.catch(() => {}); }
  }

  function stopWatching(reason, quiet) {
    if (!watch) return;
    const w = watch;
    watch = null;
    clearTimeout(w.timer);
    if (w.pc) { try { w.pc.close(); } catch (e) { /* já fechada */ } }
    showStream(null);
    if (!quiet && tabState(w.tab).status === 'connected') send(w.tab, w.from, { t: 'leave', sid: w.sid }).catch(() => {});
    if (reason) deps.notify('info', reason);
    changed();
  }

  async function watchClient(id) {
    id = Number(id);
    const tab = viewTab;
    const s = ctx(tab).sharers.get(id);
    if (!s) throw new Error('este cliente não está compartilhando a tela');
    if (watch && watch.tab === tab && watch.from === id) return;
    if (watch) stopWatching('');
    const w = { tab, from: id, name: s.name, sid: s.sid, pc: null, state: 'asking', timer: 0 };
    watch = w;
    w.timer = setTimeout(() => { if (watch === w && w.state !== 'live') stopWatching('Não foi possível conectar à tela de ' + w.name + '.'); }, 20000);
    changed();
    try { await send(tab, id, { t: 'watch', sid: s.sid }); } catch (e) { if (watch === w) stopWatching('', true); throw e; }
  }

  async function onOffer(tab, from, msg) {
    const w = watch;
    if (!w || w.tab !== tab || w.from !== from || msg.sid !== w.sid || typeof msg.sdp !== 'string') return;
    if (w.pc) { try { w.pc.close(); } catch (e) { /* já fechada */ } }
    const pc = new RTCPeerConnection(rtcConfig());
    w.pc = pc;
    w.state = 'connecting';
    changed();
    pc.addEventListener('track', (e) => {
      if (watch !== w) return;
      showStream(e.streams && e.streams[0] ? e.streams[0] : new MediaStream([e.track]));
    });
    pc.addEventListener('connectionstatechange', () => {
      if (watch !== w || w.pc !== pc) return;
      if (pc.connectionState === 'connected') { w.state = 'live'; clearTimeout(w.timer); changed(); }
      if (pc.connectionState === 'failed') stopWatching('A conexão com a tela de ' + w.name + ' caiu.');
    });
    try {
      await pc.setRemoteDescription({ type: 'offer', sdp: msg.sdp });
      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);
      await gathered(pc, 2500);
      if (watch !== w || w.pc !== pc) return;
      await send(tab, from, { t: 'answer', sid: w.sid, sdp: pc.localDescription.sdp });
    } catch (e) {
      if (watch === w) stopWatching('Não foi possível abrir a tela de ' + w.name + ': ' + errText(e), true);
    }
  }

  // ------------------------------------------------------------------ mensagens e estado

  function onSignal(sig) {
    if (!deps || !sig) return;
    const from = Number(sig.from);
    const tab = Number(sig.tab) || viewTab;
    // De uma aba que não está à vista nem transmitindo não se sabe quem está
    // no canal: a mensagem é ignorada. Ao voltar a ela, perguntamos de novo.
    if (!fresh(tab)) return;
    let msg;
    try { msg = JSON.parse(sig.payload); } catch (e) { return; }
    if (!msg || typeof msg !== 'object' || typeof msg.t !== 'string') return;
    const c = ctx(tab), st = tabState(tab);
    if (st.status !== 'connected' || from === st.self.clientId) return;
    const cl = (st.clients || []).find((x) => x.id === from);
    if (!cl || cl.channelId !== st.self.channelId) return; // só vale entre quem está no mesmo canal
    const sid = typeof msg.sid === 'string' ? msg.sid.slice(0, 40) : '';
    const watchingHim = !!watch && watch.tab === tab && watch.from === from;
    const sharingHere = !!share && share.tab === tab;
    switch (msg.t) {
      case 'hello':
        if (sharingHere && mayReply(tab, from, 'hello')) send(tab, from, { t: 'sharing', sid: share.sid }).catch(() => {});
        break;
      case 'sharing':
        if (!sid) break;
        if (watchingHim && watch.sid !== sid) stopWatching(cl.nickname + ' reiniciou o compartilhamento.', true);
        c.sharers.set(from, { sid, name: cl.nickname });
        changed();
        break;
      case 'stopped':
        if (c.sharers.delete(from)) changed();
        if (watchingHim) stopWatching(cl.nickname + ' parou de compartilhar a tela.', true);
        break;
      case 'full':
        if (watchingHim) stopWatching('A tela de ' + cl.nickname + ' já está com o máximo de espectadores.', true);
        break;
      case 'watch': onWatch(tab, from, { sid }); break;
      case 'offer': onOffer(tab, from, { sid, sdp: msg.sdp }); break;
      case 'answer': if (sharingHere) onAnswer(from, { sid, sdp: msg.sdp }); break;
      case 'leave': if (sharingHere) dropViewer(from); break;
      default: break;
    }
  }

  // onState recebe cada estado da aba à vista.
  function onState(st) {
    if (!deps || !st) return;
    const tab = Number(st.tab) || 0;
    if (tab !== viewTab) {
      // Trocou de aba. Quem assistia para (o vídeo é mostrado na aba que ficou
      // para trás). Na aba que volta à vista, o que se sabia de quem
      // compartilha pode ter mudado enquanto ninguém olhava: pergunta de novo.
      if (watch) stopWatching('Você saiu da aba em que assistia à tela de ' + watch.name + '.');
      viewTab = tab;
      const c = ctx(tab);
      if (c.lastConnected && !(share && share.tab === tab)) { c.sharers.clear(); c.rehello = true; }
    }
    process(tab, st);
    pollShareTab();
  }

  // process aplica um estado (da aba à vista, ou o consultado da aba da transmissão).
  function process(tab, st) {
    const c = ctx(tab);
    c.st = st;
    const mine = !!share && share.tab === tab, seeing = !!watch && watch.tab === tab;
    const connected = st.status === 'connected';
    const channel = connected ? st.self.channelId : 0;
    if (!connected) {
      if (mine) stop(true);
      if (seeing) stopWatching('', true);
      if (c.sharers.size) { c.sharers.clear(); changed(); }
    } else if (channel !== c.lastChannel && c.lastChannel !== 0) {
      // trocou de canal: a transmissão é do canal
      if (mine) { stop(true); deps.notify('info', 'O compartilhamento de tela foi encerrado: você saiu do canal.'); }
      if (seeing) stopWatching('', true);
      if (c.sharers.size) { c.sharers.clear(); changed(); }
    }
    if (connected) {
      const here = new Set((st.clients || []).filter((x) => x.channelId === channel).map((x) => x.id));
      for (const id of Array.from(c.sharers.keys())) if (!here.has(id)) { c.sharers.delete(id); changed(); }
      if (watch && watch.tab === tab && !here.has(watch.from)) stopWatching(watch.name + ' saiu do canal.', true);
      if (share && share.tab === tab) for (const id of Array.from(share.viewers.keys())) if (!here.has(id)) dropViewer(id);
      // ao entrar num canal (ou voltar à aba), pergunta se alguém está
      // compartilhando — só se houver outro cliente Furito ali, para não
      // gastar comandos à toa
      if (channel && (channel !== c.lastChannel || !c.lastConnected || c.rehello)) {
        clearTimeout(c.helloTimer);
        c.helloTimer = setTimeout(() => {
          const now = tabState(tab);
          if (!fresh(tab) || now.status !== 'connected' || now.self.channelId !== channel) return;
          const peers = (now.clients || []).some((x) => x.channelId === channel && x.id !== now.self.clientId && x.furito);
          if (peers) send(tab, 0, { t: 'hello' }).catch(() => {});
        }, 600);
      }
    }
    c.rehello = false;
    c.lastChannel = channel;
    c.lastConnected = connected;
  }

  window.FuritoScreen = {
    QUALITIES,
    MAX_VIEWERS,
    init(d) { deps = d; },
    attachVideo(el) { videoEl = el; },
    onState,
    onSignal,
    start,
    stop: () => stop(false),
    watch: watchClient,
    stopWatching: () => stopWatching('', false),
    // isSharer: o cliente id, da aba à vista, está compartilhando a tela?
    isSharer: (id) => ctx(viewTab).sharers.has(Number(id)),
    // sharing: a nossa transmissão (tab = a aba em que ela está; here = é a aba à vista).
    sharing: () => (share ? { tab: share.tab, here: share.tab === viewTab, viewers: share.viewers.size, live: Array.from(share.viewers.values()).filter((v) => v.state === 'live').length, frames: share.source.frames, quality: share.quality, fps: share.fps } : null),
    watching: () => (watch ? { from: watch.from, name: watch.name, state: watch.state } : null),
  };
})();
