import assert from "node:assert/strict";
import test from "node:test";
import { cliInstallCommand } from "./cliInstall.ts";

test("the installer downloads binaries from the same public or LAN server", () => {
    assert.equal(cliInstallCommand("https://notes.example.com"), "curl -fsSL 'https://notes.example.com/install.sh' | bash -s -- 'https://notes.example.com'");
    assert.equal(cliInstallCommand("http://192.168.10.253:8084"), "curl -fsSL 'http://192.168.10.253:8084/install.sh' | bash -s -- 'http://192.168.10.253:8084'");
    assert.equal(cliInstallCommand("https://notes.example.com/settings?tab=cli"), cliInstallCommand("https://notes.example.com"));
    assert.throws(() => cliInstallCommand("file:///tmp/install.sh"));
    assert.throws(() => cliInstallCommand("https://user:secret@example.com"));
});
