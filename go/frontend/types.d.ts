// Global type augmentations for the TNT frontend.
// Wails injects window.go (Go bindings) and window.runtime (events) at boot.
// We pull the shape of the Go bindings straight from the Wails-generated
// .d.ts so a Go-side rename surfaces as a TS error here.

import type * as AudioNormalizerBindings from './wailsjs/go/main/AudioNormalizer';

declare global {
  interface Window {
    go: {
      main: {
        AudioNormalizer: typeof AudioNormalizerBindings;
      };
    };
    runtime: {
      EventsOn(event: string, callback: (...args: any[]) => void): void;
      EventsEmit(event: string, ...data: any[]): void;
    };
  }
}

export {};
