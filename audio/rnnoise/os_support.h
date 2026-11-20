/* Este arquivo NÃO faz parte do RNNoise: é do TeamSpeak Furito.
 *
 * O pacote oficial rnnoise-0.2.tar.gz inclui "os_support.h" em vec_neon.h e
 * no caminho genérico de vec.h, mas não traz o arquivo, então a versão 0.2 não
 * compila em ARM (Apple Silicon) como veio. A única coisa usada dele é
 * OPUS_CLEAR, definida aqui com o equivalente que o RNNoise já tem. */
#ifndef OS_SUPPORT_H
#define OS_SUPPORT_H

#include "common.h"

#ifndef OPUS_CLEAR
#define OPUS_CLEAR(dst, n) RNN_CLEAR(dst, n)
#endif

#endif
