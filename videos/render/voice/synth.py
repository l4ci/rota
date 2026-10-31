#!/usr/bin/env python3
"""Voiceover for the video series: Kokoro (kokoro-onnx, int8 model) on CPU.

  synth.py --phonemes "text"            print the phonemes the voice would say
  synth.py --plan PARSED.json --out DIR write one wav per shot and DIR/audio.json

The pronunciation lexicon (lexicon.json) is applied to every line: "rota" and
the script spelling "Ro-Ta" are spoken with a hard T. Audio is cached by a hash
of text, voice, speed, lexicon and model, so an unchanged line is never
re-synthesised and a re-render keeps the same bytes.
"""
import hashlib
import json
import os
import re
import shutil
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
MODEL = os.path.join(ROOT, 'models', 'kokoro-v1.0.int8.onnx')
VOICES = os.path.join(ROOT, 'models', 'voices-v1.0.bin')
VOICE = 'af_heart'
SPEED = 1.0
LANG = 'en-us'
CACHE = os.environ.get('VIDEOS_AUDIO_CACHE') or os.path.join(os.path.dirname(ROOT), '.cache', 'audio')


def load_lexicon():
    with open(os.path.join(HERE, 'lexicon.json'), encoding='utf-8') as f:
        lex = json.load(f)
    return {k: v for k, v in lex.items() if not k.startswith('_')}


def lexicon_re(lex):
    # "Ro-Ta" is the script's spelling of "rota"; both map to the same entry.
    alts = [re.escape(w) for w in sorted(lex, key=len, reverse=True)]
    if 'rota' in lex:
        alts.append('Ro-Ta')
    return re.compile(r'\b(%s)\b' % '|'.join(alts), re.I)


def phonemize(tokenizer, text, lex):
    """Phonemes for `text`, with lexicon words replaced by their fixed phonemes."""
    rx = lexicon_re(lex)
    out, pos = [], 0
    for m in rx.finditer(text):
        seg = text[pos:m.start()]
        if seg.strip():
            out.append(tokenizer.phonemize(seg, LANG).strip())
        word = m.group(1).lower()
        out.append(lex['rota' if word == 'ro-ta' else word])
        pos = m.end()
    tail = text[pos:]
    if tail.strip():
        out.append(tokenizer.phonemize(tail, LANG).strip())
    # Punctuation after a lexicon word ("Ro-Ta.") rides on the tail segment.
    return re.sub(r'\s+([.,!?;:])', r'\1', ' '.join(out))


def kokoro():
    from kokoro_onnx import Kokoro
    return Kokoro(MODEL, VOICES)


def cache_key(text, lex):
    h = hashlib.sha256()
    for part in (text, VOICE, str(SPEED), LANG, json.dumps(lex, sort_keys=True),
                 str(os.path.getsize(MODEL))):
        h.update(part.encode('utf-8') + b'\0')
    return h.hexdigest()[:24]


def synth_plan(plan_path, out_dir):
    import soundfile as sf
    with open(plan_path, encoding='utf-8') as f:
        plan = json.load(f)
    lex = load_lexicon()
    os.makedirs(CACHE, exist_ok=True)
    os.makedirs(out_dir, exist_ok=True)
    k = None
    clips = []
    for shot in plan['shots']:
        wav = os.path.join(CACHE, cache_key(shot['vo'], lex) + '.wav')
        if not os.path.isfile(wav):
            k = k or kokoro()
            phonemes = phonemize(k.tokenizer, shot['vo'], lex)
            samples, sr = k.create(phonemes, voice=VOICE, speed=SPEED, lang=LANG, is_phonemes=True)
            sf.write(wav, samples, sr, subtype='PCM_16')
        info = sf.info(wav)
        name = 'shot-%s.wav' % shot['n']
        shutil.copyfile(wav, os.path.join(out_dir, name))
        clips.append({'n': shot['n'], 'file': name, 'seconds': round(info.frames / info.samplerate, 3)})
    with open(os.path.join(out_dir, 'audio.json'), 'w', encoding='utf-8') as f:
        json.dump({'voice': VOICE, 'clips': clips}, f, indent=1)
    return clips


def main(argv):
    if len(argv) == 2 and argv[0] == '--phonemes':
        print(phonemize(kokoro().tokenizer, argv[1], load_lexicon()))
        return 0
    if len(argv) == 4 and argv[0] == '--plan' and argv[2] == '--out':
        for c in synth_plan(argv[1], argv[3]):
            print('shot %s: %.2fs' % (c['n'], c['seconds']))
        return 0
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
