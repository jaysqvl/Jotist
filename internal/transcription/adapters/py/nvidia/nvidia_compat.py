"""NeMo 3 inference compatibility with security-fixed Lightning 2.6.6.

Lightning 2.6.4 removed the sunset Neptune integration; NeMo 3 still eagerly
imports that optional training logger. Preserve the official NeMo source and
fail explicitly if anything attempts to use the removed integration.
"""
import importlib.metadata


def prepare_nemo_import():
    import lightning.pytorch.loggers as loggers

    if hasattr(loggers, "NeptuneLogger"):
        return
    versions = {name: importlib.metadata.version(name) for name in ("nemo_toolkit", "lightning")}
    if versions != {"nemo_toolkit": "3.0.0", "lightning": "2.6.6"}:
        raise RuntimeError("Unsupported NeMo/Lightning Neptune compatibility combination: " + repr(versions))

    class UnavailableNeptuneLogger:
        def __init__(self, *args, **kwargs):
            raise RuntimeError("Neptune logging is unavailable: Lightning removed the sunset integration; Jotist inference does not use it")

    loggers.NeptuneLogger = UnavailableNeptuneLogger


prepare_nemo_import()
