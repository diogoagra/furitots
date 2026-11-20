# Limitações conhecidas

O que o app ainda não faz ou faz com ressalvas. O que está planejado fica em
[roadmap.md](roadmap.md).

- O servidor não é autenticado pelo app (o cliente oficial também não guarda
  a identidade dos servidores): quem está no caminho da rede no momento da
  conexão consegue se passar por ele. Ver [seguranca.md](seguranca.md).
- Uma conexão por vez (sem abas de vários servidores), sem definir o próprio avatar e sem
  lista de banimentos; permissões só por grupo (não por canal ou por cliente).
- Arquivos de canal são carregados inteiros na memória (limite de 256 MB).
- Scripts: rodam dentro da janela do app, então valem enquanto ele está
  aberto; com a janela minimizada por muito tempo, o sistema pode atrasar os
  temporizadores (`ts.every`, `ts.at`) — isso ainda não foi medido. No macOS,
  erros que nascem numa chamada do `ts` (um pedido recusado, por exemplo) saem
  sem o número da linha; os do próprio código saem com ele. Os scripts não
  guardam dados entre uma execução e outra.

- O cancelamento de eco vem desligado: sem fone de ouvido, ligue-o no painel do microfone.
- Com caixas de som, começar a falar enquanto um aviso sonoro toca corta o início da frase (até o aviso acabar). Ao trocar de fone para caixas sem mudar o dispositivo no app, o primeiro aviso ainda pode vazar; do segundo em diante é segurado.
- Digitação no meio de uma frase: a supressão neural a reduz (4 dB a mais de relação sinal/distorção que a clássica, com teclado sintético), mas não a zera; fora da fala nada é transmitido. A supressão neural e os limiares do detector foram medidos com vozes sintetizadas do macOS e teclado sintético, não com gravações de pessoas nem de teclados de verdade.
- Ao distribuir o app, o aviso de licença do RNNoise (BSD) precisa ir junto: os scripts o põem dentro do `.app` (`Contents/Resources/licencas`) e, no Windows, na pasta `build/bin/licencas`, que deve acompanhar o `.exe`.
- O soundboard não traz sons prontos: gravações de meme têm dono e não podem vir junto com o app.
- Ícone de canal (`channel_icon_id`) é lido, mas ainda não foi confirmado contra um servidor real.
- Com o cancelamento de eco ligado, valem sempre os dispositivos padrão do sistema.
- O app de `make mac` depende do `libopus`/`libopusfile` do Homebrew e só roda no Mac em que foi gerado; para levar a outro Mac, `make mac-dist` põe as bibliotecas dentro dele.
- Fone Bluetooth usado também como microfone: ao abrir o microfone do fone, o macOS o passa para o modo de chamada e todo o som fica com qualidade de telefone. É do Bluetooth, não do app; para manter a qualidade, escolha outro microfone em Ajustes → Áudio.
- Compartilhamento de tela: só entre clientes Furito (não é compatível com o do cliente TeamSpeak 6), sem áudio do sistema, e a conexão direta revela o IP entre os participantes (ver a seção própria).
- A versão mostrada pelos clientes oficiais continua a assinada ("3.?.? … Linux"); "1.0.0 Diogo Agra" só aparece entre clientes Furito.
