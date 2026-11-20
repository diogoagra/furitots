#ifndef FURITO_SCREENCAP_H
#define FURITO_SCREENCAP_H

#include <stdint.h>

typedef struct {
	uint32_t id;
	int width;  // pixels
	int height;
	int main;
} furito_display;

// Há ScreenCaptureKit neste macOS (12.3 ou mais novo)?
int furito_screen_supported(void);
// O app já tem a permissão de gravar a tela? (não mostra nenhum aviso)
int furito_screen_has_access(void);
// Pede a permissão: o sistema mostra o aviso uma vez e põe o app na lista.
int furito_screen_request_access(void);
// Lista as telas ativas; devolve quantas escreveu em out.
int furito_screen_displays(furito_display *out, int max);
// Começa a capturar. Devolve 0, ou um código de erro com o texto em err.
int furito_screen_start(uint32_t display_id, int width, int height, int fps, uint64_t gen, char *err, int err_len);
void furito_screen_stop(void);

#endif
