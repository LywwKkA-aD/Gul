# Обработка голоса

## Решение перед beta

Когда включено «Шумоподавление», голос проходит один нейросетевой denoiser:
Chromium AEC/AGC → RNNoise → gain/VAD → mono 48 kHz Opus. Шумоподавление
Chromium выключено в этом пути. AEC, автоматическая громкость, пользовательский
gain и настройки VAD сохраняют прежние значения. Стереозвук демонстрации
не проходит через этот DSP.

Ранее одновременно работали Chromium NS и RNNoise. Это не универсальная ошибка:
[LiveKit допускает сочетание браузерной и дополнительной обработки](https://docs.livekit.io/transport/media/noise-cancellation/).
Для закреплённого Electron и нашей воспроизводимой записи сравнение показало
лучшее сохранение тихой речи при одном RNNoise. Поэтому меняется порядок
обработки, а не модель, громкость или пользовательский режим микрофона.

Если RNNoise не подтверждает готовность, клиент сначала восстанавливает
Chromium NS на выключенном микрофоне. Только успешное завершение позволяет
передачу стандартного звука. Отмена, ошибка восстановления или поздняя ошибка
процессора закрывают capture. Режимы, которым требуется worklet для gain/VAD,
не переходят на необработанный звук. Mute/PTT и смена канала сохраняют свои
проверки поколения и состояние выключенного микрофона.

## Закреплённая модель и альтернативы

Используется `@jitsi/rnnoise-wasm` 0.2.1, синхронный модуль с RNNoise 0.2.
[Jitsi различает версии sync/async сборок](https://github.com/jitsi/rnnoise-wasm/blob/master/README.md);
номер npm-пакета не означает RNNoise 0.1. Состояние WASM и буферы создаются
один раз на поток, освобождаются при закрытии. PCM остаётся в AudioWorklet.

[RNNoise имеет обучающие данные фоновых шумов и ударов клавиш](https://github.com/xiph/rnnoise/blob/main/README).
Обновлённую upstream модель можно сравнить отдельно, но текущая модель и
зависимости в этом изменении не заменяются. Возвращаемая моделью вероятность
речи сейчас не управляет VAD: экспериментальный gate с hangover дал менее
1 dB дополнительного подавления тестовых клавиш и не вошёл в приложение.
[Значение вероятности возвращает upstream process_frame](https://github.com/xiph/rnnoise/blob/372f7b4b76cde4ca1ec4605353dd17898a99de38/src/denoise.c).

[DeepFilterNet поддерживает WASM](https://github.com/Rikorose/DeepFilterNet/blob/main/libDF/src/wasm.rs).
Его upstream binding создаёт состояние через raw pointer без экспортированного
destroy и выделяет выходной массив на каждый кадр. Для AudioWorklet нужны
обёртка с явным освобождением состояния, повторно используемыми буферами,
измерение задержки/CPU и сравнение речи. Доступность WASM сама по себе
не квалифицирует замену RNNoise; DeepFilterNet в Gul не добавлен.

[Официальный LiveKit React hook Krisp предназначен для Cloud](https://github.com/livekit/components-js/blob/main/packages/react/src/hooks/cloud/krisp/useKrispNoiseFilter.ts).
У [отдельного Krisp SDK есть интеграция Electron](https://sdk-docs.krisp.ai/docs/electron),
но это отдельная лицензируемая зависимость. Она не включена в self-hosted Gul
и её качество/CPU здесь не измерены.

## Что проверено

Тестовая речь — публичная лицензированная запись из LibriSpeech. Fixture
добавляет детерминированные модели вентилятора и клавиш в паузах и во время
речи. Это не запись пользователя или измерение настоящего микрофона.
Источник и лицензия описаны в
[README тестовых данных](../desktop/e2e/testdata/noise/README.md).

- Offline тест закреплённого RNNoise сравнивает noisy/clean речь с учётом
  задержки. При шуме во время речи residual proxy улучшился примерно на
  3.6 dB; уровень речи изменился менее чем на 1 dB, без clipping.
- Controlled A/B запускает два изолированных macOS Electron 44.6, подаёт тестовый WAV
  через настоящий Chromium getUserMedia, проверяет фактические NS/AGC/AEC,
  48 kHz/mono и готовность модели. Получатель измеряет декодированный Opus
  через REALITY, а не локальный сигнал до кодека. Перед измерением проходит
  полный цикл записи для прогрева APM/AGC.
- В прогретом normal A/B уровень речи с RNNoise без browser NS отличается
  от двойного NS примерно на 0.3 dB. Клавиши в паузах тише примерно на
  10–20 dB в повторных прогонах этой модели шума.
- В quiet A/B только речь ослаблена на 14 dB; шум оставлен прежним.
  RNNoise без browser NS сохранил речь примерно на 4 dB громче двойного NS,
  при envelope correlation около 0.95 против 0.87–0.89. AGC одинаково включён
  в обоих вариантах; это не результат повышения gain.
- Production-path E2E проверяет собственный Microphone/VoiceProcessor,
  переключения NS/AGC, голос в обе стороны, одновременную стереодемонстрацию
  и восстановление после её остановки. Отдельные deferred unit tests
  проверяют mute, отмену и ошибку browser fallback до открытия передачи.

`speechFidelity` в benchmark — диагностический waveform residual proxy,
который чувствителен к Opus и относительным часам. Его нельзя считать
перцептивной оценкой или доказательством качества всех микрофонов. Envelope
correlation и уровни также не заменяют прослушивание. Сильное подавление
модели шума в паузах не означает такое же подавление клавиатуры во время речи.

Физические микрофоны Windows 10/Ubuntu 26, реальные вентилятор/клавиатура,
AEC с колонками, тихие начала слов/PTT и длительная нагрузка требуют
отдельной пользовательской проверки. В этом изменении нет заявлений
о превосходстве над Discord/Krisp и нет автоматического выбора новой модели.

## Воспроизведение

```sh
cd desktop
node --test test/media-noise-policy.test.ts test/media-microphone*.test.ts \
  test/media-voice*.test.ts test/media-neural-noise.test.ts \
  test/media-pipeline-metrics.test.ts
npm run check
npm run build
GUL_ELECTRON_STAND_DIR=/path/to/private/isolated/stand \
  npx --no-install playwright test --config playwright.config.ts \
  e2e/voice-noise.live.spec.ts e2e/voice-pipeline.live.spec.ts
```

Stand создаётся deployment fixture отдельно от рабочего сервера.
Тест использует fake audio input и отдельные профили. Адреса, пароль,
профили и сырой PCM/SDP нельзя публиковать; безопасный отчёт содержит
только агрегированные значения и статусы проверок.
