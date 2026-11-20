#include <Carbon/Carbon.h>
#include <dispatch/dispatch.h>
#include <pthread.h>

#include "hotkey_darwin.h"
#include "_cgo_export.h"

static OSStatus onHotkey(EventHandlerCallRef next, EventRef event, void *data) {
    EventHotKeyID hk;
    if (GetEventParameter(event, kEventParamDirectObject, typeEventHotKeyID, NULL, sizeof(hk), NULL, &hk) == noErr) {
        furitoHotkeyEvent((int)hk.id, GetEventKind(event) == kEventHotKeyPressed);
    }
    return noErr;
}

// O registro e a entrega dos eventos acontecem na thread principal.
static void onMain(void (^work)(void)) {
    if (pthread_main_np()) {
        work();
    } else {
        dispatch_sync(dispatch_get_main_queue(), work);
    }
}

int furito_hotkey_register(int id, unsigned keycode, int ctrl, int alt, int shift, int cmd, void **ref) {
    __block OSStatus status = -1;
    __block EventHotKeyRef hotkey = NULL;
    onMain(^{
        static int installed = 0;
        if (!installed) {
            EventTypeSpec types[2] = {{kEventClassKeyboard, kEventHotKeyPressed}, {kEventClassKeyboard, kEventHotKeyReleased}};
            if (InstallEventHandler(GetEventDispatcherTarget(), &onHotkey, 2, types, NULL, NULL) == noErr) {
                installed = 1;
            }
        }
        UInt32 mods = (ctrl ? controlKey : 0) | (alt ? optionKey : 0) | (shift ? shiftKey : 0) | (cmd ? cmdKey : 0);
        EventHotKeyID hk = {'FURI', (UInt32)id};
        status = RegisterEventHotKey(keycode, mods, hk, GetEventDispatcherTarget(), 0, &hotkey);
    });
    *ref = hotkey;
    return (int)status;
}

void furito_hotkey_unregister(void *ref) {
    if (ref == NULL) {
        return;
    }
    onMain(^{
        UnregisterEventHotKey((EventHotKeyRef)ref);
    });
}
