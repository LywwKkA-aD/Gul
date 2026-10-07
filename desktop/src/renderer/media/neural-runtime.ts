import create from '@jitsi/rnnoise-wasm/dist/rnnoise-sync.js';
import { NeuralDenoiser, type NoiseRuntime } from './neural-noise.ts';

// One compiled model per SDK-owned audio thread, with independent state per mic.
let runtime: NoiseRuntime | undefined;
export function createNeuralDenoiser(): NeuralDenoiser {
  runtime ??= create() as NoiseRuntime;
  return new NeuralDenoiser(runtime);
}
