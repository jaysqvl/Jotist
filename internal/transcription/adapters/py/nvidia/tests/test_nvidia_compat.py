import importlib.metadata
from pathlib import Path
import runpy
import sys
import types
import unittest
from unittest.mock import patch

HELPER = Path(__file__).resolve().parents[1] / "nvidia_compat.py"

class NeptuneCompatibilityTests(unittest.TestCase):
    def execute(self, versions, existing=None):
        logger = types.ModuleType("lightning.pytorch.loggers")
        if existing is not None:
            logger.NeptuneLogger = existing
        modules = {"lightning": types.ModuleType("lightning"), "lightning.pytorch": types.ModuleType("lightning.pytorch"), "lightning.pytorch.loggers": logger}
        with patch.dict(sys.modules, modules), patch.object(importlib.metadata, "version", side_effect=versions.__getitem__):
            runpy.run_path(str(HELPER))
        return logger

    def test_removed_logger_fails_if_requested(self):
        logger = self.execute({"nemo_toolkit": "3.0.0", "lightning": "2.6.6"})
        with self.assertRaisesRegex(RuntimeError, "Neptune logging is unavailable"):
            logger.NeptuneLogger(api_key="unused")

    def test_other_versions_fail_closed(self):
        with self.assertRaisesRegex(RuntimeError, "Unsupported NeMo/Lightning"):
            self.execute({"nemo_toolkit": "3.1.0", "lightning": "2.6.6"})

    def test_existing_logger_is_never_replaced(self):
        original = object()
        self.assertIs(self.execute({}, original).NeptuneLogger, original)
