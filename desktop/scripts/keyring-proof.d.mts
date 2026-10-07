export function keyringProof(
  output: string,
  phases: readonly ('write' | 'read')[],
): readonly ('write' | 'read')[];
export function keyringEnvironment(
  parent: NodeJS.ProcessEnv,
  config: string,
  data: string,
  secret: string,
): NodeJS.ProcessEnv;
