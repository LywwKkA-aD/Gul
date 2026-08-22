package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LywwKkA-aD/Gul/internal/audio"
	"github.com/LywwKkA-aD/Gul/internal/dsp/apm"
	"github.com/LywwKkA-aD/Gul/internal/dsp/wav"
)

// candidate is one of the two denoiser configurations of the M3 A/B.
type candidate struct {
	label string // how the key file names it
	desc  string // what the listener reads once the choice is made
	opts  audio.DSPOptions
}

// candidates hold the open question of PLAN.md 4.3: whether the DNN
// denoiser earns its place behind a soft APM suppressor, or whether the
// suppressor alone does better. Neither candidate runs the echo canceller -
// a recording has no far end to cancel against.
var candidates = [2]candidate{
	{
		label: "A",
		desc:  "APM NS low + RNNoise",
		opts:  audio.DSPOptions{NS: apm.NSLow, RNNoise: true},
	},
	{
		label: "B",
		desc:  "APM NS high, без RNNoise",
		opts:  audio.DSPOptions{NS: apm.NSHigh},
	},
}

const keyFile = "key.txt"

// runAB processes one recording through both candidates and writes the two
// results out under neutral names, in an order decided by a coin flip. The
// mapping goes into a separate file the listener opens afterwards, so the
// comparison is blind by construction rather than by discipline.
func runAB(args []string) error {
	fs := flag.NewFlagSet("ab", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	in := fs.String("in", "", "исходная запись: WAV PCM 16 бит, моно, 48000 Гц")
	out := fs.String("out", "", "каталог для результатов")
	if err := fs.Parse(args); err != nil {
		abUsage()
		return fmt.Errorf("аргументы команды ab: %w", err)
	}
	if *in == "" || *out == "" {
		abUsage()
		return errors.New("нужны оба аргумента: -in и -out")
	}

	samples, err := readInput(*in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fmt.Errorf("не удалось создать каталог %s: %w", *out, err)
	}

	// Slot i of the listening test gets candidate order[i].
	order, err := coinFlip()
	if err != nil {
		return err
	}
	names := [2]string{"sample-1.wav", "sample-2.wav"}
	for slot, idx := range order {
		processed, err := audio.ProcessOffline(samples, candidates[idx].opts)
		if err != nil {
			return fmt.Errorf("обработка конфигурацией %s: %w", candidates[idx].label, err)
		}
		path := filepath.Join(*out, names[slot])
		if err := wav.Write(path, processed, audio.SampleRate); err != nil {
			return fmt.Errorf("не удалось записать %s: %w", path, err)
		}
	}
	if err := writeKey(*out, *in, names, order); err != nil {
		return err
	}

	seconds := float64(len(samples)) / audio.SampleRate
	fmt.Printf(`Готово. В каталоге %s:
  %s и %s - одна и та же запись (%.1f с), обработанная двумя конфигурациями
  %s - какая из них какая

Слушать в наушниках, сравнить пары несколько раз подряд и выбрать по
разборчивости речи и остатку шума. Ключ открывать только после выбора,
итог занести в docs/DECISIONS.md.
`, *out, names[0], names[1], seconds, keyFile)
	return nil
}

func abUsage() {
	fmt.Fprint(os.Stderr, `gul-dsp ab -in запись.wav -out каталог

Прогоняет запись через две конфигурации шумодава милестоуна M3 и
раскладывает результаты по слепым именам sample-1.wav и sample-2.wav.

Вход: WAV PCM 16 бит, моно, 48000 Гц (сетка проекта).
`)
}

// readInput loads the recording and holds it to the project audio grid: the
// chain processes 48 kHz mono int16 and nothing else, so a file in any other
// shape is rejected with the expected format spelled out.
func readInput(path string) ([]int16, error) {
	samples, rate, err := wav.Read(path)
	if err != nil {
		if errors.Is(err, wav.ErrFormat) {
			return nil, fmt.Errorf("%s: нужен WAV PCM 16 бит, моно, %d Гц (%w)",
				path, audio.SampleRate, err)
		}
		return nil, fmt.Errorf("не удалось прочитать %s: %w", path, err)
	}
	if rate != audio.SampleRate {
		return nil, fmt.Errorf("%s: частота %d Гц, нужен WAV PCM 16 бит, моно, %d Гц",
			path, rate, audio.SampleRate)
	}
	if len(samples) < audio.FrameSamples {
		return nil, fmt.Errorf("%s: запись короче одного кадра (%d мс)",
			path, audio.FrameMs)
	}
	return samples, nil
}

// coinFlip returns the candidate order of the two listening slots.
func coinFlip() ([2]int, error) {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return [2]int{}, fmt.Errorf("нет источника случайности: %w", err)
	}
	if b[0]&1 == 1 {
		return [2]int{1, 0}, nil
	}
	return [2]int{0, 1}, nil
}

// writeKey records which slot holds which candidate.
func writeKey(dir, source string, names [2]string, order [2]int) error {
	text := fmt.Sprintf(`Ключ слепого A/B (PLAN.md M3). Открывать после прослушивания.

%s = %s: %s
%s = %s: %s

Исходная запись: %s
`,
		names[0], candidates[order[0]].label, candidates[order[0]].desc,
		names[1], candidates[order[1]].label, candidates[order[1]].desc,
		source)
	path := filepath.Join(dir, keyFile)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("не удалось записать %s: %w", path, err)
	}
	return nil
}
