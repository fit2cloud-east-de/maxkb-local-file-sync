//go:build darwin

#import <Cocoa/Cocoa.h>

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
}

+ (void)remove {
    if (maxKBStatusItem != nil) {
        [[NSStatusBar systemStatusBar] removeStatusItem:maxKBStatusItem];
        maxKBStatusItem = nil;
    }
    maxKBTrayTarget = nil;
}
@end

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
