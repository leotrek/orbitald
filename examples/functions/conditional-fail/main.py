from __future__ import annotations

import json
import os
import shutil
import sys
from pathlib import Path


def env(name: str) -> str:
    return os.environ[name]


def main() -> int:
    payload_path = Path(env("ORBITALD_PAYLOAD_PATH"))
    output_dir = Path(env("ORBITALD_OUTPUT_DIR"))
    output_dir.mkdir(parents=True, exist_ok=True)

    shutil.copyfile(payload_path, output_dir / "payload.json")
    payload = json.loads(payload_path.read_text(encoding="utf-8"))

    if payload.get("should_fail") is True:
        print("conditional-fail exiting with a requested failure", flush=True)
        (output_dir / "status.txt").write_text(
            f"requested failure for {env('ORBITALD_WINDOW_ID')}\n",
            encoding="utf-8",
        )
        return 42

    print("conditional-fail completed successfully", flush=True)
    (output_dir / "status.txt").write_text(
        f"success for {env('ORBITALD_WINDOW_ID')}\n",
        encoding="utf-8",
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
