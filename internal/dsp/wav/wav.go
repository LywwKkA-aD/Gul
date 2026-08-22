// Package wav reads and writes the one WAV shape the project uses: RIFF/WAVE
// holding PCM 16-bit little-endian mono. It exists for DSP test fixtures and
// offline tooling (golden tests, the A/B listening kit), not as a general
// audio library - anything but that exact shape is rejected loudly.
package wav

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// ErrFormat is returned for structurally valid WAV files whose audio shape
// is not PCM 16-bit mono.
var ErrFormat = errors.New("wav: not 16-bit PCM mono")

// Read loads a PCM16 mono WAV file and reports its sample rate.
func Read(path string) ([]int16, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("wav: %s is not a RIFF/WAVE file", path)
	}

	var (
		rate     int
		haveFmt  bool
		data     []byte
		haveData bool
	)
	// Chunk walk: encoders pad with chunks like LIST or FLLR; skip anything
	// that is not fmt or data. Chunks are word-aligned.
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := off + 8
		if size < 0 || body+size > len(raw) {
			return nil, 0, fmt.Errorf("wav: chunk %q overruns the file", id)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, fmt.Errorf("wav: fmt chunk of %d bytes", size)
			}
			format := binary.LittleEndian.Uint16(raw[body : body+2])
			channels := binary.LittleEndian.Uint16(raw[body+2 : body+4])
			rate = int(binary.LittleEndian.Uint32(raw[body+4 : body+8]))
			bits := binary.LittleEndian.Uint16(raw[body+14 : body+16])
			if format != 1 || channels != 1 || bits != 16 {
				return nil, 0, fmt.Errorf("%w: format %d, %d ch, %d bit",
					ErrFormat, format, channels, bits)
			}
			haveFmt = true
		case "data":
			data = raw[body : body+size]
			haveData = true
		}
		off = body + size + size%2
	}
	if !haveFmt || !haveData {
		return nil, 0, fmt.Errorf("wav: %s misses fmt or data chunk", path)
	}

	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[2*i : 2*i+2]))
	}
	return samples, rate, nil
}

// Write stores samples as a PCM16 mono WAV file.
func Write(path string, samples []int16, sampleRate int) error {
	if sampleRate <= 0 {
		return fmt.Errorf("wav: sample rate %d", sampleRate)
	}
	dataLen := 2 * len(samples)
	buf := make([]byte, 0, 44+dataLen)

	le16 := func(v int) []byte { return binary.LittleEndian.AppendUint16(nil, uint16(v)) }
	le32 := func(v int) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(v)) }

	buf = append(buf, "RIFF"...)
	buf = append(buf, le32(36+dataLen)...)
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = append(buf, le32(16)...)
	buf = append(buf, le16(1)...) // PCM
	buf = append(buf, le16(1)...) // mono
	buf = append(buf, le32(sampleRate)...)
	buf = append(buf, le32(sampleRate*2)...) // byte rate
	buf = append(buf, le16(2)...)            // block align
	buf = append(buf, le16(16)...)           // bits
	buf = append(buf, "data"...)
	buf = append(buf, le32(dataLen)...)
	for _, s := range samples {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(s))
	}
	return os.WriteFile(path, buf, 0o644)
}
