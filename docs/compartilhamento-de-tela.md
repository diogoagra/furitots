# Compartilhamento de tela

Entre clientes Furito TS que estão no mesmo canal (os clientes oficiais
não veem nem recebem nada). Botão do monitor na barra de ferramentas para
compartilhar; quem está compartilhando ganha um ícone de monitor na árvore —
clique nele (ou botão direito → "Assistir à tela") para abrir o vídeo.

Com vários servidores abertos, a transmissão é do servidor (da aba) em que
começou: você continua compartilhando enquanto olha outra aba, e o indicador
da barra de estado segue à vista. Assistir é diferente: o vídeo é mostrado na
aba de quem compartilha, então sair dela para de assistir. Só há uma
transmissão sua por vez.

Como funciona:

- O servidor TeamSpeak não transporta vídeo. Os clientes combinam a
  transmissão por **comandos de plugin** (`plugincmd`, nome `furito.screen`),
  que o servidor repassa sem interpretar; funciona em qualquer servidor TS3 ou
  TS6, sem instalar nada nele.
- O vídeo vai **direto de um computador ao outro** por WebRTC, codificado pelo
  navegador embutido (H.264/VP8/VP9). Um servidor STUN (padrão:
  `stun.l.google.com`) só informa a cada lado o próprio endereço público.
- **Privacidade:** na conexão direta, quem compartilha e quem assiste podem
  descobrir o endereço IP um do outro (o TeamSpeak normalmente esconde o IP
  dos outros usuários). O app avisa antes do primeiro uso. Para esconder o IP,
  configure um servidor de retransmissão TURN (por exemplo um `coturn` da
  comunidade) em Ajustes → Tela e marque "Esconder meu
  IP": todo o vídeo passa por ele. A senha do TURN fica no arquivo de ajustes.
- **Captura:** no macOS é feita pelo app (ScreenCaptureKit, macOS 12.3+); na
  primeira vez o sistema pede a permissão de Gravação de Tela e o app precisa
  ser reaberto. No Windows a captura é a do próprio WebView2, que mostra a
  janela de escolha de tela/janela.
- Limites: até 6 espectadores por transmissão (cada um recebe um fluxo
  codificado à parte); 720p ou 1080p, 5/15/30 quadros por segundo; sem áudio
  do sistema (a voz continua pelo TeamSpeak). Trocar de canal ou desconectar
  encerra a transmissão.

Para testar sem permissão de tela, `FuritoScreen.start({source: 'pattern'})`
no console usa um padrão de teste gerado pelo app.
