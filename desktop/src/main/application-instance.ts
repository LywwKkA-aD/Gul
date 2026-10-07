interface Application {
  requestSingleInstanceLock(): boolean;
}

/** A second production copy could recapture another Gul copy and race encrypted settings writes. */
export function claimApplicationInstance(
  app: Application,
  environment: NodeJS.ProcessEnv,
  packaged: boolean,
  arguments_: readonly string[],
): boolean {
  const fixture = !packaged && environment.NODE_ENV === 'test' && arguments_.includes('--gul-electron-test');
  return fixture || app.requestSingleInstanceLock();
}
