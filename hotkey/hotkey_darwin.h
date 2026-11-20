#ifndef FURITO_HOTKEY_DARWIN_H
#define FURITO_HOTKEY_DARWIN_H

// Registra um atalho global. Devolve 0 se deu certo (código do sistema caso
// contrário) e, em *ref, a referência para remover depois.
int furito_hotkey_register(int id, unsigned keycode, int ctrl, int alt, int shift, int cmd, void **ref);
void furito_hotkey_unregister(void *ref);

#endif
