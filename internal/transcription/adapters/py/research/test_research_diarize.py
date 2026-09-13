"""Output-bound checks without loading speech models."""
from types import SimpleNamespace
import unittest

from research_diarize import serialize_output


def annotation(turns):
    tracks = [(SimpleNamespace(start=start, end=end), index, speaker)
              for index, (start, end, speaker) in enumerate(turns)]
    return SimpleNamespace(itertracks=lambda yield_label: iter(tracks))


class OutputBoundsTests(unittest.TestCase):
    def test_padding_is_clipped_without_losing_overlap(self):
        output = serialize_output(annotation([(-0.1, 3, "A"), (2, 15.9125, "B"),
                                               (15.2, 16, "padding")]), "model", "cpu", 15.0)
        self.assertEqual(output["segments"], [{"start": 0.0, "end": 3.0, "speaker": "A"},
                                              {"start": 2.0, "end": 15.0, "speaker": "B"}])
        self.assertEqual(output["speakers"], ["A", "B"])

    def test_invalid_timestamps_fail_instead_of_being_clamped(self):
        for start, end in [(float("nan"), 1), (0, float("inf")), (2, 1)]:
            with self.subTest(start=start, end=end), self.assertRaises(ValueError):
                serialize_output(annotation([(start, end, "A")]), "model", "cpu", 15.0)


if __name__ == "__main__":
    unittest.main()
