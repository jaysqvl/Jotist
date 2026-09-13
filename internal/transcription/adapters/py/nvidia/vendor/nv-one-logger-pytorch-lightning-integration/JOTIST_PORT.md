# OneLogger compatibility port

This is NVIDIA's Apache-2.0 OneLogger PyTorch Lightning integration 2.3.1.
Its two Python modules are retained; only the `save_checkpoint` signature and its
matching parameter documentation change. Lightning 2.6.6 changed `weights_only`
to `Optional[bool] = None`; the old `bool = False` annotation made `@override`
reject NeMo imports. The method body and the `@override` check remain intact.

The package uses a local version and public build metadata. `UPSTREAM.json`
records the official source archive and before/after source hashes. The rest of
OneLogger and NeMo remain official PyPI packages. The parent runtime explicitly
uses patched Lightning 2.6.6 and Hydra 1.3.6; no checkpoint loading safeguards
are disabled by this port.
