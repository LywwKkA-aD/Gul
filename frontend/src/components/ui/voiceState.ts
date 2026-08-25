/** What one participant row shows: nothing, the microphone, or the output gate.
 *
 * Deafened implies muted - Mumble carries self_mute with self_deaf and the
 * bottom bar closes both gates together - so two glyphs would be two facts
 * where there is one and the stronger one wins.
 *
 * Kept apart from the component so it can be unit tested: node's test runner
 * strips types but does not parse JSX. */
export type VoiceGlyph = 'none' | 'muted' | 'deaf';

export function voiceGlyph(muted: boolean, deaf: boolean): VoiceGlyph {
  if (deaf) return 'deaf';
  if (muted) return 'muted';
  return 'none';
}
