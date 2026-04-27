#!/usr/bin/env python3
"""
patch_piper_models.py
Patches standard Rhasspy Piper ONNX models to embed metadata required by
sherpa-onnx (sample_rate, voice, model_type, has_espeak, etc.).

Usage:
    python3 patch_piper_models.py ./models
"""

import os
import sys
import json
import glob

try:
    import onnx
except ImportError:
    import subprocess, sys
    print("Installing onnx...")
    subprocess.check_call([sys.executable, "-m", "pip", "install", "--quiet", "onnx"])
    import onnx


# Maps from Piper language codes -> eSpeak-ng voice names
# eSpeak voice list: espeak-ng --voices
# Piper uses IETF tags (en-US), eSpeak uses its own identifiers (en-us, en-gb-x-rp)
ESPEAK_VOICE_MAP = {
    "en-us":  "en-us",
    "en_us":  "en-us",
    "en-gb":  "en-gb",
    "en_gb":  "en-gb",
    "en":     "en",
    "de-de":  "de",
    "de_de":  "de",
    "fr-fr":  "fr",
    "fr_fr":  "fr",
    "es-es":  "es",
    "es_es":  "es",
    "it-it":  "it",
    "it_it":  "it",
    "nl-nl":  "nl",
    "pl-pl":  "pl",
    "pt-br":  "pt-br",
    "pt-pt":  "pt",
    "ru-ru":  "ru",
    "zh-cn":  "cmn",
    "zh-tw":  "cmn-tw",
    "ja-jp":  "ja",
    "ko-kr":  "ko",
    "ar":     "ar",
    "tr-tr":  "tr",
    "uk-ua":  "uk",
    "vi-vn":  "vi",
    "sv-se":  "sv",
    "nb-no":  "nb",
    "da-dk":  "da",
    "fi-fi":  "fi",
    "cs-cz":  "cs",
    "sk-sk":  "sk",
    "hu-hu":  "hu",
    "ro-ro":  "ro",
    "bg-bg":  "bg",
    "hr-hr":  "hr",
    "ca-es":  "ca",
    "el-gr":  "el",
}


def resolve_espeak_voice(config: dict) -> str:
    """
    Resolve the eSpeak voice identifier from a Piper JSON config.
    Priority:
      1. config["espeak"]["voice"]  — explicit eSpeak voice, most reliable
      2. Map from config["language"]["code"]
      3. Fallback to "en-us"
    """
    # Best case: explicit eSpeak voice in config
    espeak_voice = config.get("espeak", {}).get("voice", "")
    if espeak_voice:
        return espeak_voice

    # Second: language code mapping
    lang_code = config.get("language", {}).get("code", "").lower().replace("_", "-")
    if lang_code in ESPEAK_VOICE_MAP:
        return ESPEAK_VOICE_MAP[lang_code]

    # Try prefix match (e.g. "en-us-libritts" -> "en-us")
    for key, val in ESPEAK_VOICE_MAP.items():
        if lang_code.startswith(key):
            return val

    print(f"    [WARN] Unknown language code '{lang_code}', defaulting to 'en-us'")
    return "en-us"


def patch_model(onnx_path: str):
    json_path = onnx_path + ".json"
    if not os.path.exists(json_path):
        print(f"  [SKIP] No sidecar JSON found for {os.path.basename(onnx_path)}")
        return False

    with open(json_path) as f:
        config = json.load(f)

    sample_rate = config.get("audio", {}).get("sample_rate", 22050)
    num_speakers = config.get("num_speakers", 1)
    language = config.get("language", {}).get("name_english", 
               config.get("language", {}).get("code", "English"))
    espeak_voice = resolve_espeak_voice(config)

    model = onnx.load(onnx_path)

    # Check if already fully patched
    existing_keys = {p.key for p in model.metadata_props}
    required = {"comment", "sample_rate", "voice", "model_type", "has_espeak"}
    if required.issubset(existing_keys):
        print(f"  [OK]   {os.path.basename(onnx_path)} already patched")
        return True

    def set_meta(key, value):
        # Remove existing key first to allow re-patching
        for i, p in enumerate(model.metadata_props):
            if p.key == key:
                del model.metadata_props[i]
                break
        entry = model.metadata_props.add()
        entry.key = key
        entry.value = str(value)

    set_meta("model_type",     "vits")        # Required: model architecture
    set_meta("comment",        "piper")       # Required: phonemizer selection
    set_meta("has_espeak",     "1")           # Required: tells sherpa to use eSpeak
    set_meta("language",       language)      # Human-readable language name
    set_meta("voice",          espeak_voice)  # eSpeak voice ID (e.g. "en-us")
    set_meta("sample_rate",    sample_rate)
    set_meta("n_speakers",     num_speakers)
    set_meta("speaker_id_map", "{}")
    set_meta("speaker_id",     "0")

    onnx.save(model, onnx_path)
    print(f"  [PATCHED] {os.path.basename(onnx_path)} — voice={espeak_voice}, sample_rate={sample_rate}, speakers={num_speakers}")
    return True


def main():
    models_dir = sys.argv[1] if len(sys.argv) > 1 else "./models"
    if not os.path.isdir(models_dir):
        print(f"Error: directory not found: {models_dir}")
        sys.exit(1)

    onnx_files = glob.glob(os.path.join(models_dir, "*.onnx"))
    if not onnx_files:
        print(f"No .onnx files found in {models_dir}")
        sys.exit(1)

    print(f"Patching {len(onnx_files)} model(s) in {models_dir}...\n")
    for f in sorted(onnx_files):
        patch_model(f)

    print("\nDone! Restart the piper-service to use patched models.")


if __name__ == "__main__":
    main()
