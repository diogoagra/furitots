#ifndef FURITO_VPIO_DARWIN_H
#define FURITO_VPIO_DARWIN_H

#include <stdint.h>

// Unidade de processamento de voz do macOS (VoiceProcessingIO): captura e
// saída na mesma unidade, com cancelamento de eco feito pelo sistema.
typedef struct FuritoVP FuritoVP;

// Inicia a unidade em 48 kHz mono de 16 bits. Devolve NULL em caso de falha,
// com o código do sistema em *status.
FuritoVP *furito_vp_start(uintptr_t handle, int *status);
void furito_vp_stop(FuritoVP *vp);

int furito_mic_mode(void);
void furito_show_mic_modes(void);

#endif
