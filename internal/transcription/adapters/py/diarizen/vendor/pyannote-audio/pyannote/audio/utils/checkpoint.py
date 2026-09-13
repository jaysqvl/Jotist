"""Restricted deserialization for the vendored pyannote checkpoint format."""
from contextlib import contextmanager

import torch


@contextmanager
def restricted_checkpoint_loading():
    # These local data/enum types occur in publisher Lightning checkpoints.
    # Resolve them lazily because task.py also imports the Model class.
    from pyannote.audio.core.task import Problem, Resolution, Specifications
    from torch.torch_version import TorchVersion

    with torch.serialization.safe_globals([Problem, Resolution, Specifications, TorchVersion]):
        yield
