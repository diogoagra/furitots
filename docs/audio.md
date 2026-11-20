# Áudio

Como a voz é tratada do microfone até a rede, o que cada parte faz e o que foi
medido. Para o resumo, veja o [README](../README.md#recursos).

## Do microfone à rede

Do microfone até a rede: passa-altas → supressão de ruído → ganho automático →
detecção de voz → voz modificada → limitador → Opus (64 kbit/s, banda cheia,
com correção de erros).

- **Supressão de ruído**, de dois tipos, escolhidos no painel do microfone:
  - **Neural** (o padrão): o [RNNoise](https://gitlab.xiph.org/xiph/rnnoise)
    0.2 da Xiph.Org, uma rede treinada com fala e ruídos de verdade que
    decide, a cada 10 ms, quanto de cada faixa do som é voz. Tira também o que
    muda depressa (digitação, cliques) e no meio da fala. O código dele vem em
    `audio/rnnoise/` e é compilado junto com o app: não há nada a instalar.
    Custa 20 ms de atraso e cerca de 0,5% de um núcleo (Apple M4).
  - **Clássica**: espectral, própria (`audio/voiceproc.go`): o ruído de cada
    banda é estimado pelos mínimos do sinal e removido com filtro de Wiener.
    Só tira ruído constante (ventilador, chiado), até 24 dB; 10 ms de atraso.

  Medido sem o detector de voz, com vozes sintetizadas do macOS e teclado
  sintético (`go test ./audio/ -run EvalNoiseSuppression`): nas pausas o
  ventilador cai 50 dB com a neural (24 dB com a clássica) e a digitação, 20 a
  23 dB (4 a 5 dB); o nível da fala fica igual nos dois. O RNNoise só reconhece
  uma fala quando a vogal chega e, com ruído ao fundo, abafa a consoante que
  abre a frase ("s", "ch"); por isso cada trecho de fala começa com o áudio
  da supressão clássica e passa para o do RNNoise quando a rede deixa o som
  passar (`Engine.pick`).
- **Detecção de voz**: um quadro só conta como voz se está acima do ruído, é
  periódico (tem tom), mantém o tom entre quadros e se sustenta — uma batida
  de tecla ressoa como um tom, mas desaba em milissegundos. A decisão de
  transmitir cada quadro é tomada 60 ms depois, já sabendo o que veio em
  seguida (`audio/engine.go`, `txFrame`); fora da fala nada é enviado, nem nas
  pausas. Custa 60 ms de atraso.

  Com a supressão neural, quem diz se há voz acima do ruído é a rede (a
  probabilidade de voz que o RNNoise devolve e quanto do som ele deixou
  passar), no lugar da relação sinal/ruído; o tom e a sustentação continuam
  sendo a defesa contra o teclado, que engana a rede por instantes. Cada um dos
  dois só abre o microfone sozinho com uma evidência longa demais para uma
  tecla: 100 ms de certeza da rede com o nível firme (voz rouca ou sem tom
  firme) ou 120 ms de voz pelas medidas clássicas (uma voz que a rede não
  reconheça). O que isso muda, medido (`EvalSpeechEdges`, `EvalKeyboard`):
  - voz baixa com ventilador: de 48 frases, o começo saía cortado em 16 e o
    fim em 17 com a clássica; com a neural, 3 e 0;
  - só teclado, sem ninguém falando, 48 execuções de 10 s por teclado: o
    microfone abre em 6 (notebook) e 16 (mecânico) com a clássica, e em
    nenhuma com a neural.

  As vozes dessas medidas são sintetizadas e o teclado é sintético: o que elas
  não dizem é como fica a **sua** voz. Para comparar de ouvido, o "Testar:
  ouvir minha voz" do painel do microfone, trocando o tipo.
  Sussurro: no modo "Por volume" abre sempre; na detecção de voz, só com a
  supressão neural e se a rede o reconhecer (não foi medido).
- **Começo e fim das frases**: a voz só é confirmada depois de a palavra ter
  começado (a consoante inicial não tem tom), então os últimos 160 ms não
  transmitidos ficam guardados e a transmissão começa onde o som começou. O
  trecho a mais entra numa fila e o atraso é recuperado em seguida, tirando
  períodos inteiros da voz (até 25% mais rápido, sem mudar o tom) e nas
  pausas. No fim, a transmissão segue enquanto o som não parar (voz sumindo,
  "s" final) e reabre para uma última sílaba sem tom ou rouca ("per-to"); um
  nível que salta de repente é tecla e encerra. Para medir com gravações de
  fala (cada avaliação mostra os dois tipos de supressão):
  `FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run 'EvalSpeechEdges|EvalNoiseSuppression|EvalRealSpeech' -v`
  e, para o teclado, `FURITO_EVAL=1 go test ./audio/ -run EvalKeyboard -v`.
- **Cancelamento de eco**: opcional, usa a unidade de processamento de voz do
  macOS (`audio/vpio_darwin.c`). Com ele ligado o sistema oferece o "Modo do
  Microfone" na Central de Controle, incluindo o Isolamento de Voz da Apple
  (que remove digitação até durante a fala); o painel do microfone tem um
  botão que abre esse seletor.
- **Gravação da conversa** (botão ● na barra): as vozes recebidas e o que
  você transmite, somados num WAV mono de 48 kHz (~345 MB por hora) em
  `~/Music/Furito TS/`. Avisos sonoros não entram. Enquanto grava, os
  outros veem o indicador de gravação ao lado do seu nome, como no cliente
  oficial; a gravação termina sozinha ao desconectar ou fechar o app. Clique
  direito no botão abre a pasta.
- **Recepção**: buffer de jitter adaptativo por pessoa, reconstrução de pacote
  perdido (FEC/PLC), nivelamento opcional do volume de cada voz e limitador
  na soma.
- **Vozes** (`audio/effects.go`):
  - **Tom e "cor" separados.** Mudar o tom reamostrando leva junto os
    formantes (a cor da voz, que vem do tamanho da garganta e da boca), e o
    resultado soa como uma fita acelerada ou lenta. A voz aguda, a grave, o
    monstro e o demônio mudam os dois por razões diferentes — entre um homem e
    uma mulher o tom quase dobra, mas os formantes mudam só uns 15% —, e é
    isso que faz soar como outra pessoa. O tom muda por WSOLA + reamostragem;
    a cor, por um bloco que mede o envelope do espectro e redistribui a
    intensidade dos harmônicos (`formantShifter`). Aguda = tom ×1,45 e
    formantes ×1,12; grave = ×0,76 e ×0,90; monstro = ×0,62 e ×0,72. O esquilo
    é a fita acelerada, de propósito.
  - **Demônio**: duas vozes da mesma boca (uma grave e outra uma oitava abaixo
    da original, com formantes de um bicho enorme), mais tremor, saturação e
    uma sala escura.
  - **Sussurro e robô** refazem a voz só a partir do envelope do espectro: o
    sussurro sai sem tom nenhum e o timbre do robô não oscila com a entonação.
  - **Consoantes**: a explosão de um "t", "p" ou "k" não tem forma de onda que
    continue em outro lugar; o esticador de tempo posiciona os trechos de modo
    que cada uma passe inteira e uma vez só (sem isso, com o tom do demônio,
    uma em cada quatro some).
  - **Volume**: todo efeito sai com o volume da voz que entrou. Quanto um
    efeito muda o volume depende da voz (um filtro de rádio tira mais de uma
    voz grave), então o app mede isso enquanto você fala e corrige aos poucos.
  - Atraso: ~33 ms nos efeitos só de tom (esquilo, alienígena), ~54 ms nos
    que mudam tom e cor e 21 ms no robô e no sussurro; os demais não atrasam.

  Os números de cada voz (razões de tom e de formantes, pesos das camadas)
  estão em `effectDefs` e são questão de gosto: foram conferidos por medida
  (ver [Medir](#medir)), não de ouvido.
- **Soundboard**: só arquivos seus (MP3, WAV, M4A, AIFF, FLAC, CAF pelo
  `afconvert` do macOS; OGG/Opus se houver `ffmpeg`), adicionados pelo botão
  ou arrastando para a janela. São copiados para
  `~/Library/Application Support/FuritoTS/sounds/` e transmitidos em
  modo música do Opus. Cada som pode ter um **atalho global** (botão de
  teclado no canto do som): toca mesmo com o app em segundo plano; em Ajustes
  → Atalhos há também o atalho "Parar o soundboard". Valem as mesmas regras
  dos outros atalhos (tecla de função ou do teclado numérico sozinha, ou
  combinação com Ctrl/Alt/Cmd).
- **Avisos sonoros** ("conectado", "usuário entrou no seu canal", "microfone
  mutado"…): o app traz o próprio pacote, feito de tons sintetizados por
  código (`backend/builtinsounds.go`) — notas curtas com timbre de sino macio,
  que sobem para o que chega ou liga e descem para o que sai ou desliga. Não
  há gravação nenhuma no projeto. O pacote é gravado na pasta de dados
  (`soundpacks/`) ao abrir, no formato dos pacotes de sons do TeamSpeak 3 (uma
  pasta com `settings.ini` e os `.wav`); quem preferir outros sons importa um
  pacote nesse formato pelo painel do microfone ("Importar pacote de sons…").
  Com os **alto-falantes mutados** os avisos também ficam calados (o que
  estiver tocando é cortado); só o próprio aviso de "som mutado" toca. O
  retorno local do soundboard some junto — o som continua indo para os outros.
  Os avisos nunca entram na
  transmissão; com caixas de som o microfone os capta, então enquanto um
  aviso toca nenhuma transmissão nova começa (quem já estava falando
  continua). O app aprende sozinho, a cada aviso, se o microfone ouve os
  avisos: com fone de ouvido ele percebe que não e deixa de segurar a fala.

## Ouvir fora do app

Para ouvir o que o motor faz com uma gravação, sem abrir o app (sai o que
seria transmitido com cada tipo de supressão de ruído, e cada voz modificada):

```sh
go run ./cmd/audiodemo minha-voz.wav build/demos
```

## Medir

As avaliações não rodam por padrão; pedem gravações de fala (48 kHz, mono) em
`FURITO_SPEECH`:

```sh
# detector de voz, supressão de ruído e vazamento de digitação, nos dois tipos de supressão
FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run 'EvalSpeechEdges|EvalNoiseSuppression|EvalRealSpeech' -v
# o que cada voz modificada faz com o tom, os formantes, o volume e a aspereza
FURITO_SPEECH=a.wav,b.wav go test ./audio/ -run EvalEffects -v
# teclado sem fala: em quantas execuções o microfone abre
FURITO_EVAL=1 go test ./audio/ -run EvalKeyboard -v
```

No macOS dá para gerar fala de teste com as vozes do sistema:

```sh
say -v Luciana -o fala.aiff "Sim, eu acho que fica perto. [[slnc 700]] Fala comigo quando chegar."
afconvert -f WAVE -d LEI16@48000 -c 1 fala.aiff fala.wav
```

Os números citados nesta página vieram de vozes sintetizadas assim e de um
teclado sintético (`audio.SynthKeyboard`), não de gravações de pessoas nem de
teclados de verdade.
