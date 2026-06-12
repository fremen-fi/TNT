// Global type augmentations for the TNT frontend.
// Wails injects window.go (Go bindings) and window.runtime (events) at boot.
// We pull the shape of the Go bindings straight from the Wails-generated
// .d.ts so a Go-side rename surfaces as a TS error here.

import type * as AudioNormalizerBindings from './wailsjs/go/main/AudioNormalizer';
import type * as Runtime from './wailsjs/runtime/runtime';

declare global {
  interface Window {
    go: {
      main: {
        AudioNormalizer: typeof AudioNormalizerBindings;
      };
    };
    runtime: typeof Runtime;
  }
}

export {};
