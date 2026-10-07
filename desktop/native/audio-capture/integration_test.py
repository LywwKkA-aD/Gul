"""Focused regressions for the real PulseAudio/PipeWire test fixture."""
import unittest
from io import BytesIO
from unittest.mock import Mock, patch

import integration


class PrivateSourceTest(unittest.TestCase):
    def setUp(self):
        self.nonce = "a" * 32
        self.label = "Gul-Screen-Audio-" + self.nonce

    def source(self, name, label=None, monitor=None):
        return {
            "name": name,
            "properties": {"device.description": label or self.label},
            "monitor_of_sink_name": monitor,
        }

    def test_matches_the_exact_label_when_pulseaudio_renames_the_source(self):
        renamed = "gul_share_" + self.nonce + ".2"
        sources = [
            self.source("gul_share_" + self.nonce + ".monitor", monitor="gul_share_" + self.nonce),
            self.source(renamed),
            self.source("another-source", "Gul-Screen-Audio-" + "b" * 32),
        ]
        self.assertEqual(integration.private_source(sources, self.nonce), renamed)

    def test_rejects_a_monitor_even_if_its_description_matches(self):
        sources = [self.source("monitor", monitor="physical-output")]
        with self.assertRaises(AssertionError):
            integration.private_source(sources, self.nonce)

    def test_rejects_absent_or_ambiguous_sources(self):
        for sources in [[], [self.source("one"), self.source("two")]]:
            with self.subTest(sources=sources), self.assertRaises(AssertionError):
                integration.private_source(sources, self.nonce)

    def test_does_not_select_an_unrelated_or_invalid_name(self):
        for source in [self.source("foreign", "Other audio"), self.source("")]:
            with self.subTest(source=source), self.assertRaises(AssertionError):
                integration.private_source([source], self.nonce)


class RecordTest(unittest.TestCase):
    def test_invalid_source_cannot_be_interpreted_as_silent_pcm(self):
        process = Mock(stdout=BytesIO(b""))
        with patch.object(integration.subprocess, "Popen", return_value=process):
            with self.assertRaisesRegex(AssertionError, "incomplete capture PCM"):
                integration.record("missing-source")
        process.terminate.assert_called_once()
        process.wait.assert_called_once_with(timeout=3)


if __name__ == "__main__":
    unittest.main()
