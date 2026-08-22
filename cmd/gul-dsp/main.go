// Command gul-dsp is the offline DSP bench of the voice client: it runs a
// recording through the candidate denoiser configurations of milestone M3
// and writes the results out as WAV files, so the choice between them is
// made by listening rather than by argument (PLAN.md 4.3, M3 A/B).
//
// It touches no audio device and no network, and it is not part of the
// application: the shipped client never runs this code.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gul-dsp:", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand. The only one so far is the blind A/B kit.
func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("не указана команда")
	}
	switch args[0] {
	case "ab":
		return runAB(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("неизвестная команда %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gul-dsp - оффлайн-стенд шумоподавления.

Команды:
  ab -in запись.wav -out каталог   слепое сравнение двух конфигураций шумодава

Вход: WAV PCM 16 бит, моно, 48000 Гц (сетка проекта).
`)
}
