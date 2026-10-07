interface Device {
  readonly kind: string;
  readonly deviceId: string;
  readonly label: string;
}

/** The private screen mix is an implementation device, never a voice or playback choice. */
export function audioDeviceOptions<T extends Device>(
  devices: readonly T[],
  kind: 'audioinput' | 'audiooutput',
): readonly T[] {
  return devices.filter(
    (device) =>
      device.kind === kind &&
      device.deviceId !== 'default' &&
      !/^Gul-Screen-Audio-[a-f0-9]{32}\b/u.test(device.label),
  );
}
