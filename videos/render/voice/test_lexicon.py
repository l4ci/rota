"""Pronunciation check: rota is voiced Ro-Ta with a hard T, never the flapped 'roda'.
Run: videos/render/.venv/bin/python -m unittest videos/render/voice/test_lexicon.py"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import synth  # noqa: E402


class Lexicon(unittest.TestCase):
    def phonemes(self, text):
        return synth.phonemize(synth.kokoro().tokenizer, text, synth.load_lexicon())

    def test_rota_has_a_hard_t(self):
        for text in ('rota', 'Rota ships.', 'Meet rota, a workflow.'):
            p = self.phonemes(text)
            self.assertIn('t', p, text)
            self.assertNotIn('ɾ', p, text)

    def test_rota_has_no_stress_or_length_marks(self):
        # Stress (ˈ ˌ) and length (ː) made the voice drag "roo-taah"; plain rota is short vowels.
        entry = synth.load_lexicon()['rota']
        for text in ('rota', 'Rota ships.', 'Meet rota, a workflow.'):
            p = self.phonemes(text)
            self.assertIn(entry, p, text)
            i = p.index(entry)
            span = p[i:i + len(entry) + 1]  # the word and the character right after it
            for mark in ('ˈ', 'ˌ', 'ː'):
                self.assertNotIn(mark, span, text)

    def test_other_words_are_untouched(self):
        self.assertIn('ɾ', self.phonemes('water'))


if __name__ == '__main__':
    unittest.main()
