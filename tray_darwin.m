//go:build darwin

#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>
#import <objc/runtime.h>

#include "_cgo_export.h"

@interface MaxKBTrayTarget : NSObject
- (void)showMainWindow:(id)sender;
- (void)exitApplication:(id)sender;
@end

@implementation MaxKBTrayTarget
- (void)showMainWindow:(id)sender {
    darwinTrayShowMainWindow();
}

- (void)exitApplication:(id)sender {
    darwinTrayExitApplication();
}
@end

static NSStatusItem *maxKBStatusItem;
static MaxKBTrayTarget *maxKBTrayTarget;
static NSMenu *maxKBDockMenu;

static BOOL maxKBApplicationShouldHandleReopen(id self, SEL command, NSApplication *sender, BOOL hasVisibleWindows) {
    darwinTrayShowMainWindow();
    return YES;
}

static NSMenu *maxKBApplicationDockMenu(id self, SEL command, NSApplication *sender) {
    return maxKBDockMenu;
}

// Wails owns the application delegate. Register only the selectors it does
// not provide instead of replacing that delegate or linking against Wails'
// private AppDelegate class directly.
static void maxKBInstallDockDelegateHooks(void) {
    id delegate = NSApp.delegate;
    if (delegate == nil) {
        return;
    }
    Class delegateClass = object_getClass(delegate);
    char reopenTypes[] = {@encode(BOOL)[0], '@', ':', '@', @encode(BOOL)[0], '\0'};
    class_addMethod(delegateClass,
                    @selector(applicationShouldHandleReopen:hasVisibleWindows:),
                    (IMP)maxKBApplicationShouldHandleReopen,
                    reopenTypes);
    class_addMethod(delegateClass,
                    @selector(applicationDockMenu:),
                    (IMP)maxKBApplicationDockMenu,
                    "@@:@");
}

@interface MaxKBTrayInstaller : NSObject
+ (void)installWithIconData:(NSData *)iconData;
+ (void)remove;
@end

@implementation MaxKBTrayInstaller
+ (void)installWithIconData:(NSData *)iconData {
    if (maxKBStatusItem != nil) {
        return;
    }

    maxKBTrayTarget = [[MaxKBTrayTarget alloc] init];
    maxKBStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];

    NSImage *icon = [[NSImage alloc] initWithData:iconData];
    [icon setSize:NSMakeSize(18, 18)];
    [icon setTemplate:YES];
    maxKBStatusItem.button.image = icon;
    maxKBStatusItem.button.toolTip = @"MaxKB 本地文件同步工具";

    NSMenu *menu = [[NSMenu alloc] initWithTitle:@"MaxKB 本地文件同步工具"];
    NSMenuItem *showItem = [[NSMenuItem alloc] initWithTitle:@"显示主界面"
                                                     action:@selector(showMainWindow:)
                                              keyEquivalent:@""];
    showItem.target = maxKBTrayTarget;
    [menu addItem:showItem];
    [menu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *exitItem = [[NSMenuItem alloc] initWithTitle:@"退出"
                                                     action:@selector(exitApplication:)
                                              keyEquivalent:@""];
    exitItem.target = maxKBTrayTarget;
    [menu addItem:exitItem];
    maxKBStatusItem.menu = menu;

    maxKBDockMenu = [[NSMenu alloc] initWithTitle:@"MaxKB 本地文件同步工具"];
    NSMenuItem *dockShowItem = [[NSMenuItem alloc] initWithTitle:@"显示主界面"
                                                         action:@selector(showMainWindow:)
                                                  keyEquivalent:@""];
    dockShowItem.target = maxKBTrayTarget;
    [maxKBDockMenu addItem:dockShowItem];
    [maxKBDockMenu addItem:[NSMenuItem separatorItem]];
    NSMenuItem *dockExitItem = [[NSMenuItem alloc] initWithTitle:@"退出"
                                                         action:@selector(exitApplication:)
                                                  keyEquivalent:@""];
    dockExitItem.target = maxKBTrayTarget;
    [maxKBDockMenu addItem:dockExitItem];
    maxKBInstallDockDelegateHooks();
}

+ (void)remove {
    if (maxKBStatusItem != nil) {
        [[NSStatusBar systemStatusBar] removeStatusItem:maxKBStatusItem];
        maxKBStatusItem = nil;
    }
    maxKBDockMenu = nil;
    maxKBTrayTarget = nil;
}
@end

static void maxKBRunOnMainThread(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_sync(dispatch_get_main_queue(), block);
}

void startDarwinTray(const void *iconBytes, int iconLength) {
    NSData *iconData = [NSData dataWithBytes:iconBytes length:(NSUInteger)iconLength];
    [MaxKBTrayInstaller performSelectorOnMainThread:@selector(installWithIconData:)
                                        withObject:iconData
                                     waitUntilDone:NO];
}

void stopDarwinTray(void) {
    [MaxKBTrayInstaller performSelectorOnMainThread:@selector(remove)
                                        withObject:nil
                                     waitUntilDone:NO];
}

void hideDarwinApplicationFromDock(void) {
    maxKBRunOnMainThread(^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    });
}

void showDarwinApplicationInDock(void) {
    maxKBRunOnMainThread(^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        [NSApp unhide:nil];
        [NSApp activateIgnoringOtherApps:YES];
    });
}
