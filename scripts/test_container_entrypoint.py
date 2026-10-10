"""Exercise the real entrypoint with isolated user/group command doubles.

These tests never change the host's users or filesystem ownership. The image
smoke suite additionally verifies the real Linux UID/GID and mounted writes.
"""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ENTRYPOINT = Path(__file__).resolve().parents[1] / "deploy/docker/entrypoint.sh"
COMMAND_DOUBLE = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
path = pathlib.Path(os.environ["JOTIST_ENTRYPOINT_TEST_STATE"])
state = json.loads(path.read_text())
command, args = pathlib.Path(sys.argv[0]).name, sys.argv[1:]
state["calls"].append([command, *args])
code = 0
if command == "id":
    user = "appuser" in args
    print(state["uid"] if user and "-u" in args else
          state["gid"] if user else state["process_uid"])
elif command == "getent":
    group = args[-1]
    if group in state["groups"]:
        print(f'{state["groups"][group]}:x:{group}:')
    else:
        code = 2
elif command == "groupmod":
    new_gid = args[args.index("-g") + 1]
    old_gid = next(gid for gid, name in state["groups"].items() if name == "appuser")
    del state["groups"][old_gid]
    state["groups"][new_gid] = "appuser"
    if state["gid"] == int(old_gid):
        state["gid"] = int(new_gid)
elif command == "usermod":
    if state.get("fail_usermod"):
        code = 1
    else:
        if "-u" in args:
            state["uid"] = int(args[args.index("-u") + 1])
        if "-g" in args:
            state["gid"] = int(args[args.index("-g") + 1])
elif command == "gosu":
    state["executed"] = args[1:]
    state["executed_uid"] = state["uid"]
    state["executed_gid"] = state["gid"]
path.write_text(json.dumps(state))
sys.exit(code)
'''


class ContainerEntrypointTests(unittest.TestCase):
    def run_entrypoint(self, uid="1000", gid="1000", *, initial_uid=10001,
                       initial_gid=10001, existing_group=None, process_uid=0,
                       fail_usermod=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = root / "command-double"
            command.write_text(COMMAND_DOUBLE)
            command.chmod(0o755)
            for name in ("id", "getent", "groupmod", "usermod", "chown", "mkdir", "gosu"):
                (root / name).symlink_to(command)
            state_path = root / "state.json"
            groups = {str(initial_gid): "appuser"}
            if existing_group is not None:
                groups[str(existing_group)] = "existing"
            state_path.write_text(json.dumps({
                "uid": initial_uid, "gid": initial_gid, "groups": groups,
                "process_uid": process_uid, "calls": [], "fail_usermod": fail_usermod,
            }))
            env = {**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"],
                   "PUID": uid, "PGID": gid,
                   "JOTIST_ENTRYPOINT_TEST_STATE": str(state_path)}
            result = subprocess.run(["bash", str(ENTRYPOINT), "true"], env=env,
                                    capture_output=True, text=True, timeout=15)
            return result, json.loads(state_path.read_text())

    def assert_identity(self, result, state, uid, gid):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state.get("executed"), ["true"])
        self.assertEqual((state["executed_uid"], state["executed_gid"]), (uid, gid))

    def test_default_ids_remap_cuda_image_user(self):
        self.assert_identity(*self.run_entrypoint(), 1000, 1000)

    def test_existing_group_becomes_primary_group(self):
        self.assert_identity(*self.run_entrypoint("99", "100", existing_group=100), 99, 100)

    def test_existing_uid_does_not_prevent_requested_group(self):
        self.assert_identity(*self.run_entrypoint("99", "100", initial_uid=99,
                                                 existing_group=100), 99, 100)

    def test_matching_ids_still_start_application(self):
        self.assert_identity(*self.run_entrypoint(initial_uid=1000, initial_gid=1000),
                             1000, 1000)

    def test_new_custom_group(self):
        self.assert_identity(*self.run_entrypoint("1234", "2345"), 1234, 2345)

    def test_invalid_identity_fails_before_ownership_changes(self):
        for value in ("-1", "someone", "1000:1000", "1 2", "4294967295"):
            for key in ("uid", "gid"):
                with self.subTest(value=value, key=key):
                    result, state = self.run_entrypoint(**{key: value})
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse(any(call[0] in ("usermod", "groupmod", "chown", "gosu")
                                         for call in state["calls"]))

    def test_failed_identity_change_never_runs_as_wrong_user(self):
        result, state = self.run_entrypoint("99", "100", existing_group=100,
                                           fail_usermod=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("executed", state)

    def test_explicit_nonroot_user_does_not_remap_accounts(self):
        result, state = self.run_entrypoint(process_uid=1234)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(any(call[0] in ("usermod", "groupmod", "chown", "gosu")
                             for call in state["calls"]))


if __name__ == "__main__":
    unittest.main()
