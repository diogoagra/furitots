# Roadmap

O que está planejado, em ordem aproximada de prioridade. Nada aqui tem data;
o que já existe está no [README](../README.md#recursos) e o que falta hoje,
em [limitacoes.md](limitacoes.md).

## Transferência de arquivos completa

Hoje o app lista, baixa, envia, cria pasta e apaga arquivos de um canal, mas
carrega cada arquivo inteiro na memória (limite de 256 MB) e usa um prazo
total fixo, que derruba transferências demoradas. O plano, em etapas:

1. **Motor confiável**: transferir em fluxo, direto de/para o disco, sem
   limite de tamanho, com prazo só por inatividade, progresso e cancelamento;
   ícones e avatares numa pista separada da dos arquivos.
2. **Fila e painel de transferências**: várias ao mesmo tempo, com barra,
   velocidade, tempo restante, nova tentativa e pasta de downloads
   configurável.
3. **Navegador de arquivos completo**: seleção múltipla, renomear e mover,
   pastas inteiras, arrastar do Finder/Explorer para um canal.
4. **Pausar, retomar e limitar a velocidade**, inclusive depois de reabrir o
   app. (O servidor TeamSpeak 6 aceita retomar download e envio; foi
   conferido.)
5. **Arquivos no bate-papo**: soltar ou colar um arquivo envia para o canal e
   posta o link; definir o próprio avatar; enviar ícones.

## Recursos novos

- Guardar a identidade de cada servidor e avisar quando ela mudar (hoje o
  servidor não é autenticado; ver [seguranca.md](seguranca.md)).
- Notificações do sistema e contador de não lidas.
- Tema escuro, seguindo o sistema.
- Busca rápida (Cmd/Ctrl+K) de canais, pessoas, favoritos e ações.
- Bate-papo: menções com `@`, "está digitando…", busca no histórico, barra de
  formatação.
- Links `ts3server://` e convites.
- Ícone na barra de menus/bandeja e modo compacto.
- Abas de servidores: reabrir as do último uso ao iniciar; mutar e ausente
  por servidor.
- Instalador para Windows, app do macOS assinado e atualização automática.
- Assistente de áudio na primeira abertura.
- Scripts: dados guardados entre execuções, mais eventos, importar e exportar.

## Paridade com o cliente TeamSpeak que ainda falta

Lista de banimentos · permissões por canal e por cliente · criar e listar
chaves de privilégio · lista de clientes offline · mensagens offline ·
reclamações.

## Fora do plano

Android e versão web, vídeo por câmera, outros idiomas além do português e
sincronização de ajustes em nuvem.
