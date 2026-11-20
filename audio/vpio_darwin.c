#include <AudioToolbox/AudioToolbox.h>
#include <stdlib.h>

#include "vpio_darwin.h"
#include "_cgo_export.h"

#define FURITO_MAX_FRAMES 8192

struct FuritoVP {
    AudioUnit unit;
    uintptr_t handle;
    int16_t *capture;
};

// O sistema avisa que há áudio do microfone; buscamos já processado (sem eco).
static OSStatus onInput(void *ref, AudioUnitRenderActionFlags *flags, const AudioTimeStamp *ts,
                        UInt32 bus, UInt32 frames, AudioBufferList *ioData) {
    FuritoVP *vp = (FuritoVP *)ref;
    if (frames == 0 || frames > FURITO_MAX_FRAMES) {
        return noErr;
    }
    AudioBufferList list;
    list.mNumberBuffers = 1;
    list.mBuffers[0].mNumberChannels = 1;
    list.mBuffers[0].mDataByteSize = frames * sizeof(int16_t);
    list.mBuffers[0].mData = vp->capture;
    OSStatus st = AudioUnitRender(vp->unit, flags, ts, 1, frames, &list);
    if (st == noErr) {
        furitoVPCapture(vp->handle, vp->capture, (int)frames);
    }
    return st;
}

// O sistema pede áudio para tocar; o que entregamos aqui também serve de
// referência para o cancelamento de eco.
static OSStatus onRender(void *ref, AudioUnitRenderActionFlags *flags, const AudioTimeStamp *ts,
                         UInt32 bus, UInt32 frames, AudioBufferList *ioData) {
    FuritoVP *vp = (FuritoVP *)ref;
    if (ioData == NULL || ioData->mNumberBuffers < 1 || ioData->mBuffers[0].mData == NULL) {
        return noErr;
    }
    UInt32 n = ioData->mBuffers[0].mDataByteSize / sizeof(int16_t);
    if (n > frames) {
        n = frames;
    }
    furitoVPRender(vp->handle, (int16_t *)ioData->mBuffers[0].mData, (int)n);
    return noErr;
}

FuritoVP *furito_vp_start(uintptr_t handle, int *status) {
    AudioComponentDescription desc = {0};
    desc.componentType = kAudioUnitType_Output;
    desc.componentSubType = kAudioUnitSubType_VoiceProcessingIO;
    desc.componentManufacturer = kAudioUnitManufacturer_Apple;

    AudioComponent comp = AudioComponentFindNext(NULL, &desc);
    if (comp == NULL) {
        *status = -1;
        return NULL;
    }

    FuritoVP *vp = calloc(1, sizeof(FuritoVP));
    vp->handle = handle;
    vp->capture = malloc(FURITO_MAX_FRAMES * sizeof(int16_t));

    OSStatus st = AudioComponentInstanceNew(comp, &vp->unit);
    if (st != noErr) {
        goto fail;
    }

    UInt32 on = 1;
    st = AudioUnitSetProperty(vp->unit, kAudioOutputUnitProperty_EnableIO, kAudioUnitScope_Input, 1, &on, sizeof(on));
    if (st != noErr) goto dispose;
    st = AudioUnitSetProperty(vp->unit, kAudioOutputUnitProperty_EnableIO, kAudioUnitScope_Output, 0, &on, sizeof(on));
    if (st != noErr) goto dispose;

    // 48 kHz mono 16 bits nas duas pontas; a unidade converte de/para o
    // formato do hardware.
    AudioStreamBasicDescription fmt = {0};
    fmt.mSampleRate = 48000;
    fmt.mFormatID = kAudioFormatLinearPCM;
    fmt.mFormatFlags = kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked;
    fmt.mChannelsPerFrame = 1;
    fmt.mBitsPerChannel = 16;
    fmt.mBytesPerFrame = 2;
    fmt.mBytesPerPacket = 2;
    fmt.mFramesPerPacket = 1;
    st = AudioUnitSetProperty(vp->unit, kAudioUnitProperty_StreamFormat, kAudioUnitScope_Output, 1, &fmt, sizeof(fmt));
    if (st != noErr) goto dispose;
    st = AudioUnitSetProperty(vp->unit, kAudioUnitProperty_StreamFormat, kAudioUnitScope_Input, 0, &fmt, sizeof(fmt));
    if (st != noErr) goto dispose;

    // O ganho automático fica por conta do nosso próprio tratamento.
    UInt32 off = 0;
    AudioUnitSetProperty(vp->unit, kAUVoiceIOProperty_VoiceProcessingEnableAGC, kAudioUnitScope_Global, 1, &off, sizeof(off));

    // Por padrão o sistema abaixa o volume dos outros apps (jogo, música)
    // enquanto a unidade de voz está ativa; pedimos o mínimo possível.
    if (__builtin_available(macOS 14.0, *)) {
        AUVoiceIOOtherAudioDuckingConfiguration duck;
        duck.mEnableAdvancedDucking = false;
        duck.mDuckingLevel = kAUVoiceIOOtherAudioDuckingLevelMin;
        AudioUnitSetProperty(vp->unit, kAUVoiceIOProperty_OtherAudioDuckingConfiguration, kAudioUnitScope_Global, 0, &duck, sizeof(duck));
    }

    AURenderCallbackStruct input = {onInput, vp};
    st = AudioUnitSetProperty(vp->unit, kAudioOutputUnitProperty_SetInputCallback, kAudioUnitScope_Global, 1, &input, sizeof(input));
    if (st != noErr) goto dispose;
    AURenderCallbackStruct render = {onRender, vp};
    st = AudioUnitSetProperty(vp->unit, kAudioUnitProperty_SetRenderCallback, kAudioUnitScope_Input, 0, &render, sizeof(render));
    if (st != noErr) goto dispose;

    st = AudioUnitInitialize(vp->unit);
    if (st != noErr) goto dispose;
    st = AudioOutputUnitStart(vp->unit);
    if (st != noErr) {
        AudioUnitUninitialize(vp->unit);
        goto dispose;
    }
    *status = 0;
    return vp;

dispose:
    AudioComponentInstanceDispose(vp->unit);
fail:
    *status = (int)st;
    free(vp->capture);
    free(vp);
    return NULL;
}

void furito_vp_stop(FuritoVP *vp) {
    if (vp == NULL) {
        return;
    }
    AudioOutputUnitStop(vp->unit);
    AudioUnitUninitialize(vp->unit);
    AudioComponentInstanceDispose(vp->unit);
    free(vp->capture);
    free(vp);
}
