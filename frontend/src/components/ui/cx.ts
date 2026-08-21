/** Join class names, dropping anything falsy. Local so the primitives do not
    pull in a dependency for eight lines of string handling. */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ');
}
