/** Local previews are ephemeral. Native capture IDs and broker credentials remain in main. */
export interface CapturePickerSource {
  readonly sourceKey: string;
  readonly kind: 'screen' | 'window';
  readonly name: string;
  readonly thumbnail: string | null;
}
export interface CapturePickerRequest {
  readonly requestId: string;
  readonly sources: readonly CapturePickerSource[];
  readonly audio: boolean;
  readonly details: string;
}
