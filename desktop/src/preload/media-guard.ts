/** Serialized into the main world before app scripts. Keep every dependency inside this function. */
export function installDeviceAudioGuard(): boolean {
  const define = Object.defineProperty;
  const descriptors = Object.getOwnPropertyDescriptors;
  const prototypeOf = Object.getPrototypeOf;
  const keys = Reflect.ownKeys;
  const apply = Reflect.apply;
  const create = Object.create;
  const isArray = Array.isArray;
  const finite = Number.isFinite;
  const own = Object.hasOwn;
  const freeze = Object.freeze;
  const isFrozen = Object.isFrozen;
  const reject = Promise.reject.bind(Promise);
  const ErrorType = DOMException;
  const denied = () =>
    new ErrorType('Захват доступен только через выбор источника в Gul.', 'NotAllowedError');
  try {
    const nav = navigator;
    const devices = nav.mediaDevices;
    if (!devices || typeof devices.getUserMedia !== 'function') return false;
    const devicePrototype = prototypeOf(devices);
    const navigatorPrototype = prototypeOf(nav);
    const markerName = '__gulDeviceAudioGuard_v1';
    const marker = descriptors(devices)[markerName];
    const locked = (target: object, name: string, value: unknown): boolean => {
      const property = descriptors(target)[name];
      return !!property && property.value === value && !property.configurable && !property.writable;
    };
    // The first installation runs before page scripts in every new document. Only publish
    // this sealed marker after all locks succeed; the isolated preload can then verify them.
    if (marker) {
      const installed = marker.value;
      return (
        !marker.configurable &&
        !marker.writable &&
        !!installed &&
        typeof installed === 'object' &&
        isFrozen(installed) &&
        typeof installed.capture === 'function' &&
        typeof installed.legacy === 'function' &&
        locked(devicePrototype, 'getUserMedia', installed.capture) &&
        locked(devices, 'getUserMedia', installed.capture) &&
        locked(nav, 'mediaDevices', devices) &&
        ['getUserMedia', 'webkitGetUserMedia', 'mozGetUserMedia'].every(
          (name) => locked(navigatorPrototype, name, installed.legacy) && locked(nav, name, installed.legacy),
        )
      );
    }
    const nativeCapture = devices.getUserMedia;
    const plainPrototype = Object.prototype;
    const allowedAudio = create(null) as Record<string, boolean>;
    for (const name of [
      'deviceId',
      'groupId',
      'autoGainControl',
      'echoCancellation',
      'noiseSuppression',
      'voiceIsolation',
      'channelCount',
      'latency',
      'sampleRate',
      'sampleSize',
    ])
      allowedAudio[name] = true;
    const optionalKeys = create(null) as Record<string, boolean>;
    optionalKeys.exact = optionalKeys.ideal = true;
    const numericKeys = create(null) as Record<string, boolean>;
    numericKeys.exact = numericKeys.ideal = numericKeys.min = numericKeys.max = true;

    const dictionary = (value: unknown): Record<string, unknown> => {
      if (!value || typeof value !== 'object' || isArray(value)) throw denied();
      const prototype = prototypeOf(value);
      if (prototype !== plainPrototype && prototype !== null) throw denied();
      const properties = descriptors(value);
      const copy = create(null) as Record<string, unknown>;
      for (const name of keys(properties)) {
        if (typeof name !== 'string' || !own(properties[name], 'value')) throw denied();
        copy[name] = properties[name].value;
      }
      return copy;
    };
    const stringValue = (value: unknown): unknown => {
      if (typeof value === 'string') {
        if (!value || value.length > 512 || /[\u0000-\u001f\u007f]/u.test(value)) throw denied();
        return value;
      }
      if (!isArray(value) || value.length < 1 || value.length > 16) throw denied();
      const properties = descriptors(value);
      const result: string[] = [];
      for (let index = 0; index < value.length; index++) {
        const property = properties[String(index)];
        if (!property || !own(property, 'value') || typeof property.value !== 'string') throw denied();
        const item = property.value;
        if (!item || item.length > 512 || /[\u0000-\u001f\u007f]/u.test(item)) throw denied();
        result.push(item);
      }
      return result;
    };
    const numericRanges: Readonly<Record<string, readonly [number, number]>> = {
      channelCount: [1, 32],
      latency: [0, 10],
      sampleRate: [1, 384000],
      sampleSize: [1, 64],
    };
    const scalar = (name: string, value: unknown): unknown => {
      if (name === 'deviceId' || name === 'groupId') return stringValue(value);
      if (own(numericRanges, name)) {
        const [minimum, maximum] = numericRanges[name];
        if (
          typeof value !== 'number' ||
          !finite(value) ||
          value < minimum ||
          value > maximum ||
          (name !== 'latency' && value % 1 !== 0)
        )
          throw denied();
        return value;
      }
      if (typeof value !== 'boolean') throw denied();
      return value;
    };
    const constraint = (name: string, value: unknown): unknown => {
      if (!value || typeof value !== 'object' || isArray(value)) return scalar(name, value);
      const input = dictionary(value);
      const result = create(null) as Record<string, unknown>;
      const numeric = own(numericRanges, name);
      for (const key of keys(input)) {
        if (typeof key !== 'string' || !own(numeric ? numericKeys : optionalKeys, key)) throw denied();
        result[key] = scalar(name, input[key]);
      }
      if (
        keys(result).length === 0 ||
        (typeof result.min === 'number' && typeof result.max === 'number' && result.min > result.max)
      )
        throw denied();
      return result;
    };
    const clone = (input: unknown): MediaStreamConstraints => {
      const request = dictionary(input);
      for (const key of keys(request)) if (key !== 'audio' && key !== 'video') throw denied();
      if (own(request, 'video') && request.video !== false && request.video !== undefined) throw denied();
      if (request.audio === true)
        return create(null, {
          audio: { value: true, enumerable: true },
          video: { value: false, enumerable: true },
        });
      const audio = dictionary(request.audio);
      const result = create(null) as Record<string, unknown>;
      for (const name of keys(audio)) {
        if (typeof name !== 'string' || !own(allowedAudio, name)) throw denied();
        // Optional undefined SDK properties are absent in the browser request.
        if (audio[name] !== undefined) result[name] = constraint(name, audio[name]);
      }
      return create(null, {
        audio: { value: result, enumerable: true },
        video: { value: false, enumerable: true },
      });
    };
    const capture = function (this: MediaDevices, input: unknown): Promise<MediaStream> {
      try {
        if (this !== devices) throw denied();
        return apply(nativeCapture, devices, [clone(input)]) as Promise<MediaStream>;
      } catch {
        return reject(denied());
      }
    };
    const legacy = (_constraints: unknown, _success: unknown, failure: unknown): void => {
      if (typeof failure === 'function') apply(failure, undefined, [denied()]);
      else throw denied();
    };
    const lock = (target: object, name: string, value: unknown) =>
      define(target, name, { value, configurable: false, writable: false, enumerable: false });
    lock(devicePrototype, 'getUserMedia', capture);
    lock(devices, 'getUserMedia', capture);
    lock(nav, 'mediaDevices', devices);
    for (const name of ['getUserMedia', 'webkitGetUserMedia', 'mozGetUserMedia']) {
      lock(navigatorPrototype, name, legacy);
      lock(nav, name, legacy);
    }
    lock(devices, markerName, freeze({ capture, legacy }));
    return true;
  } catch {
    return false;
  }
}
