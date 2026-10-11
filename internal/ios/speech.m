//go:build ios && cgo

#include "native.h"
#include <stdlib.h>
#include <string.h>

#if MYGO_IOS_SPEECH && MYGO_IOS_MICROPHONE
#import <UIKit/UIKit.h>
#import <AVFoundation/AVFoundation.h>
#import <Speech/Speech.h>
#import "_cgo_export.h"

@interface MyGoSpeechCapture : NSObject
@property(nonatomic) uint64_t token;
@property(nonatomic, strong) AVAudioEngine *engine;
@property(nonatomic, strong) SFSpeechRecognizer *recognizer;
@property(nonatomic, strong) SFSpeechAudioBufferRecognitionRequest *request;
@property(nonatomic, strong) SFSpeechRecognitionTask *task;
@property(nonatomic, copy) NSString *text;
@property(nonatomic) BOOL finished;
@property(nonatomic) BOOL stopped;
@property(nonatomic) BOOL tapped;
@property(nonatomic) BOOL ownsAudio;
@property(nonatomic, strong) id backgroundObserver;
@property(nonatomic, strong) id interruptionObserver;
- (void)cleanup;
- (void)finish:(NSString *)error;
- (void)stop;
@end
static MyGoSpeechCapture *capture;
@implementation MyGoSpeechCapture
- (void)cleanup {
 self.finished = YES;
 [self.engine stop];
 if (self.tapped) { [self.engine.inputNode removeTapOnBus:0]; self.tapped = NO; }
 [self.request endAudio];
 [self.task cancel];
 self.task = nil;
 if (self.ownsAudio) {
  [[AVAudioSession sharedInstance] setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:nil];
  self.ownsAudio = NO;
 }
 if (self.backgroundObserver) [[NSNotificationCenter defaultCenter] removeObserver:self.backgroundObserver];
 if (self.interruptionObserver) [[NSNotificationCenter defaultCenter] removeObserver:self.interruptionObserver];
 self.backgroundObserver = nil; self.interruptionObserver = nil;
 if (capture == self) capture = nil;
}
- (void)finish:(NSString *)error {
 if (self.finished) return;
 NSString *text = self.text ?: @"";
 uint64_t token = self.token;
 [self cleanup];
 goIOSSpeechResult(token, (char *)text.UTF8String, true, error ? (char *)error.UTF8String : NULL);
}
- (void)stop {
 if (self.finished || self.stopped) return;
 self.stopped = YES;
 [self.engine stop];
 if (self.tapped) { [self.engine.inputNode removeTapOnBus:0]; self.tapped = NO; }
 [self.request endAudio];
 if (self.ownsAudio) {
  [[AVAudioSession sharedInstance] setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:nil];
  self.ownsAudio = NO;
 }
 // Speech may fail to deliver a final result after endAudio. Never retain a
 // recording indefinitely; keep the provisional text for user review.
 __weak MyGoSpeechCapture *weak = self;
 dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
  MyGoSpeechCapture *strong = weak;
  if (strong && !strong.finished) [strong finish:@"语音转换超时，请检查草稿后重试"];
 });
}
@end

char *mygo_ios_speech_start(uint64_t token, const char *json) {
 if (capture) return strdup("已有语音录音正在进行");
 if (UIApplication.sharedApplication.applicationState != UIApplicationStateActive) return strdup("请在应用前台使用语音输入");
 if ([SFSpeechRecognizer authorizationStatus] != SFSpeechRecognizerAuthorizationStatusAuthorized ||
     [AVAudioSession sharedInstance].recordPermission != AVAudioSessionRecordPermissionGranted)
  return strdup("请允许麦克风和语音识别权限");
 NSDictionary *options = [NSJSONSerialization JSONObjectWithData:[@(json) dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
 NSString *language = options[@"Language"];
 NSLocale *locale = language.length ? [[NSLocale alloc] initWithLocaleIdentifier:language] : NSLocale.currentLocale;
 MyGoSpeechCapture *c = [MyGoSpeechCapture new];
 c.token = token;
 c.text = @"";
 c.recognizer = [[SFSpeechRecognizer alloc] initWithLocale:locale];
 if (!c.recognizer || !c.recognizer.available) return strdup("当前语言的 Apple 语音识别暂不可用");
 if ([options[@"OnDevice"] boolValue] && !c.recognizer.supportsOnDeviceRecognition)
  return strdup("当前设备暂不支持此语言的离线语音识别");
 c.request = [SFSpeechAudioBufferRecognitionRequest new];
 c.request.shouldReportPartialResults = YES;
 c.request.requiresOnDeviceRecognition = [options[@"OnDevice"] boolValue];
 c.request.taskHint = SFSpeechRecognitionTaskHintDictation;
 if ([options[@"ContextualStrings"] isKindOfClass:NSArray.class]) c.request.contextualStrings = options[@"ContextualStrings"];
 c.engine = [AVAudioEngine new];
 NSError *error = nil;
 AVAudioSession *audio = [AVAudioSession sharedInstance];
 if (![audio setCategory:AVAudioSessionCategoryRecord mode:AVAudioSessionModeMeasurement options:0 error:&error] ||
     ![audio setActive:YES error:&error]) { [c cleanup]; return strdup((error.localizedDescription ?: @"无法启动麦克风").UTF8String); }
 c.ownsAudio = YES;
 AVAudioInputNode *input = c.engine.inputNode;
 AVAudioFormat *format = [input outputFormatForBus:0];
 if (format.sampleRate <= 0 || format.channelCount == 0) { [c cleanup]; return strdup("麦克风暂不可用"); }
 @try {
  SFSpeechAudioBufferRecognitionRequest *request = c.request;
  [input installTapOnBus:0 bufferSize:1024 format:format block:^(AVAudioPCMBuffer *buffer, AVAudioTime *when) {
   [request appendAudioPCMBuffer:buffer];
  }];
  c.tapped = YES;
  [c.engine prepare];
  if (![c.engine startAndReturnError:&error]) { [c cleanup]; return strdup((error.localizedDescription ?: @"无法启动录音").UTF8String); }
 } @catch (NSException *exception) { [c cleanup]; return strdup("麦克风暂不可用，请重试"); }
 capture = c;
 __weak MyGoSpeechCapture *weak = c;
 c.task = [c.recognizer recognitionTaskWithRequest:c.request resultHandler:^(SFSpeechRecognitionResult *result, NSError *err) {
  NSString *text = result.bestTranscription.formattedString;
  BOOL final = result.isFinal;
  dispatch_async(dispatch_get_main_queue(), ^{
   MyGoSpeechCapture *strong = weak;
   if (!strong || strong.finished) return;
   if (text) strong.text = text;
   if (final) [strong finish:nil];
   else if (err) [strong finish:err.localizedDescription];
   else if (text) goIOSSpeechResult(strong.token, (char *)text.UTF8String, false, NULL);
  });
 }];
 NSNotificationCenter *notifications = NSNotificationCenter.defaultCenter;
 c.backgroundObserver = [notifications addObserverForName:UIApplicationDidEnterBackgroundNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
  [weak finish:@"录音已停止"];
 }];
 c.interruptionObserver = [notifications addObserverForName:AVAudioSessionInterruptionNotification object:audio queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
  if ([note.userInfo[AVAudioSessionInterruptionTypeKey] unsignedIntegerValue] == AVAudioSessionInterruptionTypeBegan) [weak finish:@"录音被中断，请重试"];
 }];
 return NULL;
}
void mygo_ios_speech_stop(uint64_t token) { if (capture.token == token) [capture stop]; }
void mygo_ios_speech_cancel(uint64_t token) { if (capture.token == token) [capture cleanup]; }
#else
char *mygo_ios_speech_start(uint64_t token, const char *json) { return strdup("语音输入需要启用 speech 和 microphone 能力"); }
void mygo_ios_speech_stop(uint64_t token) {}
void mygo_ios_speech_cancel(uint64_t token) {}
#endif
