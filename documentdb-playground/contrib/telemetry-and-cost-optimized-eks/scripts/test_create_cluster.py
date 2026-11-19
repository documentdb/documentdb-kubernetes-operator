import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class DiagnosticCredentialsTests(unittest.TestCase):
    def test_diagnostic_reads_credentials_from_secret(self):
        script = Path(__file__).with_name("create-cluster.sh")
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name in ("aws", "eksctl", "kubectl", "helm", "curl"):
                stub = root / name
                stub.write_text("#!/bin/sh\nexit 99\n")
                stub.chmod(0o755)
            capture = root / "arguments"
            environment = dict(
                os.environ,
                DEPLOY_INSTANCE="true",
                PATH=str(root) + os.pathsep + os.environ["PATH"],
                DIAGNOSTIC_ARGUMENTS=str(capture),
            )
            result = subprocess.run(
                ["bash", "-c", """
script="$1"
shift
source "$script"
kubectl() {
    if [[ "$1" == run ]]; then
        printf '%s\\0' "$@" > "$DIAGNOSTIC_ARGUMENTS"
        printf '{ ok: 1 }\\n'
    fi
}
diagnose_documentdb
""", "test", str(script)],
                env=environment, capture_output=True, text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            arguments = capture.read_bytes().decode().strip("\0").split("\0")
            overrides = json.loads(next(
                value.split("=", 1)[1] for value in arguments
                if value.startswith("--overrides=")
            ))
            container = overrides["spec"]["containers"][0]
            self.assertEqual(container["name"], arguments[1])
            self.assertEqual(container["command"], ["mongosh"])
            self.assertEqual(container["image"], "mongo:7")
            for variable, key in (
                ("DOCUMENTDB_USERNAME", "username"),
                ("DOCUMENTDB_PASSWORD", "password"),
            ):
                env = next(entry for entry in container["env"] if entry["name"] == variable)
                self.assertEqual(env["valueFrom"], {
                    "secretKeyRef": {"name": "documentdb-credentials", "key": key}
                })
                self.assertNotIn("value", env)
            self.assertEqual(container["args"][0].split("://")[1].split("/")[0],
                             "documentdb-service-sample-documentdb:10260")
            self.assertNotIn("authMechanism=", container["args"][0])
            self.check_authentication(container["args"][-1])

    def check_authentication(self, javascript):
        node = shutil.which("node")
        self.assertIsNotNone(node, "Node.js is required to exercise the mongosh diagnostic JavaScript")
        harness = r"""
const vm = require("node:vm");
const assert = require("node:assert/strict");
const code = process.argv[1];
for (const authenticated of [true, false]) {
  let pinged = false;
  const credentials = { DOCUMENTDB_USERNAME: "test @ user", DOCUMENTDB_PASSWORD: "test : / ? & value" };
  const admin = {
    auth(options) {
      assert.equal(options.user, credentials.DOCUMENTDB_USERNAME);
      assert.equal(options.pwd, credentials.DOCUMENTDB_PASSWORD);
      assert.equal(options.mechanism, "SCRAM-SHA-256");
      return { ok: authenticated ? 1 : 0 };
    },
    runCommand(command) {
      assert.equal(command.ping, 1);
      pinged = true;
      return { ok: 1 };
    }
  };
  const context = {
    process: { env: credentials },
    db: { getSiblingDB(name) { assert.equal(name, "admin"); return admin; } }
  };
  if (authenticated) {
    assert.equal(vm.runInNewContext(code, context).ok, 1);
  } else {
    assert.throws(() => vm.runInNewContext(code, context), /Authentication failed/);
  }
  assert.equal(pinged, authenticated);
}
"""
        result = subprocess.run(
            [node, "-e", harness, javascript], text=True, capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
