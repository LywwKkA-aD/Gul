export function validateReleaseTag(tag: string, version: string): void;
export function collectArtifacts(
  directory: string,
  version: string,
  platform: string,
  arch: string,
): Promise<string[]>;
