/** Isolate the fixture's keyring without changing the user's home directories. */
export function keyringEnvironment(parent, config, data, secret) {
  const { GNOME_KEYRING_CONTROL: _control, GNOME_KEYRING_PID: _pid, ...inherited } = parent;
  return Object.freeze({
    ...inherited,
    XDG_CONFIG_HOME: config,
    XDG_DATA_HOME: data,
    XDG_RUNTIME_DIR: `${data}/runtime`,
    XDG_CURRENT_DESKTOP: 'GNOME',
    GUL_KEYRING_FIXTURE_SECRET: secret,
  });
}

/** Only allowlisted scalar proof fields can reach test output; no native error is logged. */
export function keyringProof(output, phases) {
  const fail = () => {
    throw Error('GUL_KEYRING_PROOF_FAILED');
  };
  if (typeof output !== 'string' || output.length > 64 * 1024) fail();
  const lines = output.split(/\r?\n/u).filter((line) => line.startsWith('GUL_KEYRING_PROOF '));
  if (lines.length !== phases.length) fail();
  return Object.freeze(
    lines.map((line, index) => {
      let result;
      try {
        result = JSON.parse(line.slice('GUL_KEYRING_PROOF '.length));
      } catch {
        fail();
      }
      const allowed = [
        'phase',
        'backend',
        'protected',
        'persisted',
        'hasPassword',
        'remember',
        'passwordStatus',
        'correct',
        'ciphertextOnDisk',
        'plaintextOnDisk',
      ];
      if (
        !result ||
        typeof result !== 'object' ||
        Array.isArray(result) ||
        Object.keys(result).some((key) => !allowed.includes(key)) ||
        result.phase !== phases[index] ||
        result.backend !== 'gnome_libsecret' ||
        result.protected !== true ||
        result.persisted !== (phases[index] === 'write' ? true : null) ||
        result.hasPassword !== true ||
        result.remember !== true ||
        result.passwordStatus !== 'saved' ||
        result.correct !== true ||
        result.ciphertextOnDisk !== true ||
        result.plaintextOnDisk !== false
      )
        fail();
      return result.phase;
    }),
  );
}
