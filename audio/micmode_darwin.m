#import <AVFoundation/AVFoundation.h>
#import <dispatch/dispatch.h>

#include "vpio_darwin.h"

// Modo do microfone escolhido pelo usuário na Central de Controle:
// 0 = padrão, 1 = espectro amplo, 2 = isolamento de voz, -1 = indisponível.
int furito_mic_mode(void) {
    if (@available(macOS 12.0, *)) {
        return (int)[AVCaptureDevice activeMicrophoneMode];
    }
    return -1;
}

// Abre o seletor "Modo do Microfone" do sistema.
void furito_show_mic_modes(void) {
    if (@available(macOS 12.0, *)) {
        dispatch_async(dispatch_get_main_queue(), ^{
            [AVCaptureDevice showSystemUserInterface:AVCaptureSystemUserInterfaceMicrophoneModes];
        });
    }
}
