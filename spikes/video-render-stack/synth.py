import soundfile as sf
from kokoro_onnx import Kokoro
k=Kokoro("models/kokoro-v1.0.int8.onnx","models/voices-v1.0.bin")
s,sr=k.create("Coding agents write fast. rota makes sure what they merge works.",voice="af_heart",speed=1.0,lang="en-us")
sf.write("vo.wav",s,sr); print("dur",len(s)/sr,sr)
