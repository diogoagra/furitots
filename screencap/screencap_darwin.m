#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreImage/CoreImage.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <ImageIO/ImageIO.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>

#include <string.h>

#include "screencap_darwin.h"

// Implementadas em Go (screencap_darwin.go).
extern void furitoScreenFrame(uint64_t gen, void *data, int len, int width, int height);
extern void furitoScreenEnded(uint64_t gen, char *reason);

API_AVAILABLE(macos(12.3))
@interface FuritoScreenOutput : NSObject <SCStreamOutput, SCStreamDelegate>
@property(nonatomic) uint64_t gen;
@property(nonatomic, strong) CIContext *context;
@property(nonatomic) CGColorSpaceRef colorSpace;
@end

@implementation FuritoScreenOutput

- (void)dealloc {
	if (_colorSpace) CGColorSpaceRelease(_colorSpace);
}

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer ofType:(SCStreamOutputType)type {
	if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(sampleBuffer)) return;
	// O sistema também manda quadros "sem novidade" (tela parada); só os
	// completos têm imagem.
	CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, false);
	if (attachments && CFArrayGetCount(attachments) > 0) {
		NSDictionary *info = (__bridge NSDictionary *)CFArrayGetValueAtIndex(attachments, 0);
		NSNumber *status = info[SCStreamFrameInfoStatus];
		if (status && status.integerValue != SCFrameStatusComplete) return;
	}
	CVImageBufferRef pixels = CMSampleBufferGetImageBuffer(sampleBuffer);
	if (!pixels) return;
	@autoreleasepool {
		CIImage *image = [CIImage imageWithCVPixelBuffer:pixels];
		NSData *jpeg = [self.context JPEGRepresentationOfImage:image
		                                            colorSpace:self.colorSpace
		                                               options:@{(id)kCGImageDestinationLossyCompressionQuality : @(0.72)}];
		if (jpeg.length > 0) {
			furitoScreenFrame(self.gen, (void *)jpeg.bytes, (int)jpeg.length,
			                  (int)CVPixelBufferGetWidth(pixels), (int)CVPixelBufferGetHeight(pixels));
		}
	}
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	furitoScreenEnded(self.gen, (char *)(error.localizedDescription.UTF8String ?: "a captura foi encerrada pelo sistema"));
}

@end

static id currentStream;  // SCStream
static id currentOutput;  // FuritoScreenOutput
static dispatch_queue_t captureQueue;

static void set_err(char *err, int err_len, NSString *text) {
	if (!err || err_len <= 0) return;
	const char *s = text.UTF8String ?: "erro desconhecido";
	strncpy(err, s, (size_t)err_len - 1);
	err[err_len - 1] = 0;
}

int furito_screen_supported(void) {
	if (@available(macOS 12.3, *)) return 1;
	return 0;
}

int furito_screen_has_access(void) {
	return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

int furito_screen_request_access(void) {
	return CGRequestScreenCaptureAccess() ? 1 : 0;
}

int furito_screen_displays(furito_display *out, int max) {
	CGDirectDisplayID ids[16];
	uint32_t count = 0;
	if (CGGetActiveDisplayList(16, ids, &count) != kCGErrorSuccess) return 0;
	int n = 0;
	for (uint32_t i = 0; i < count && n < max; i++) {
		CGDisplayModeRef mode = CGDisplayCopyDisplayMode(ids[i]);
		out[n].id = ids[i];
		out[n].width = mode ? (int)CGDisplayModeGetPixelWidth(mode) : (int)CGDisplayPixelsWide(ids[i]);
		out[n].height = mode ? (int)CGDisplayModeGetPixelHeight(mode) : (int)CGDisplayPixelsHigh(ids[i]);
		out[n].main = CGDisplayIsMain(ids[i]) ? 1 : 0;
		if (mode) CGDisplayModeRelease(mode);
		n++;
	}
	return n;
}

int furito_screen_start(uint32_t display_id, int width, int height, int fps, uint64_t gen, char *err, int err_len) {
	if (@available(macOS 12.3, *)) {
		if (!captureQueue) captureQueue = dispatch_queue_create("dev.diogoagra.furito-ts.screencap", DISPATCH_QUEUE_SERIAL);
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		__block int rc = 1;
		__block NSString *message = @"a captura não começou a tempo";
		[SCShareableContent getShareableContentExcludingDesktopWindows:NO
		                                           onScreenWindowsOnly:YES
		                                             completionHandler:^(SCShareableContent *content, NSError *error) {
			if (error || !content) {
				rc = 2;  // quase sempre: sem permissão de gravação de tela
				message = error.localizedDescription ?: @"o sistema não informou as telas";
				dispatch_semaphore_signal(done);
				return;
			}
			SCDisplay *target = nil;
			for (SCDisplay *d in content.displays) {
				if (display_id == 0 ? CGDisplayIsMain(d.displayID) : d.displayID == display_id) {
					target = d;
					break;
				}
			}
			if (!target) target = content.displays.firstObject;
			if (!target) {
				rc = 3;
				message = @"nenhuma tela encontrada";
				dispatch_semaphore_signal(done);
				return;
			}
			SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:target excludingWindows:@[]];
			SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
			config.width = (size_t)width;
			config.height = (size_t)height;
			config.minimumFrameInterval = CMTimeMake(1, fps > 0 ? fps : 15);
			config.pixelFormat = kCVPixelFormatType_32BGRA;
			config.showsCursor = YES;
			config.queueDepth = 4;

			FuritoScreenOutput *output = [[FuritoScreenOutput alloc] init];
			output.gen = gen;
			output.context = [CIContext contextWithOptions:nil];
			output.colorSpace = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);

			SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:config delegate:output];
			NSError *addError = nil;
			if (![stream addStreamOutput:output type:SCStreamOutputTypeScreen sampleHandlerQueue:captureQueue error:&addError]) {
				rc = 4;
				message = addError.localizedDescription ?: @"não foi possível ligar a saída da captura";
				dispatch_semaphore_signal(done);
				return;
			}
			[stream startCaptureWithCompletionHandler:^(NSError *startError) {
				if (startError) {
					rc = 5;
					message = startError.localizedDescription ?: @"a captura não pôde começar";
				} else {
					currentStream = stream;
					currentOutput = output;
					rc = 0;
				}
				dispatch_semaphore_signal(done);
			}];
		}];
		if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) rc = 6;
		if (rc != 0) set_err(err, err_len, message);
		return rc;
	}
	set_err(err, err_len, @"é preciso macOS 12.3 ou mais novo");
	return 7;
}

void furito_screen_stop(void) {
	if (@available(macOS 12.3, *)) {
		SCStream *stream = currentStream;
		currentStream = nil;
		currentOutput = nil;
		if (stream) [stream stopCaptureWithCompletionHandler:^(NSError *error) {}];
	}
}
