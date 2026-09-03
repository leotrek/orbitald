from __future__ import annotations

import os
import shutil
from datetime import datetime, timezone
from pathlib import Path


def env(name: str) -> str:
    return os.environ[name]


def main() -> None:
    payload_path = Path(env("ORBITALD_PAYLOAD_PATH"))
    output_dir = Path(env("ORBITALD_OUTPUT_DIR"))
    output_dir.mkdir(parents=True, exist_ok=True)

    timestamp = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    window_id = env("ORBITALD_WINDOW_ID")
    area = env("ORBITALD_AREA")

    print(f"[{timestamp}] payload-copy starting for window {window_id} in area {area}", flush=True)
    shutil.copyfile(payload_path, output_dir / "payload.json")

    metadata = "\n".join(
        [
            f"window_id={window_id}",
            f"function={env('ORBITALD_FUNCTION')}",
            f"area={area}",
            f"payload_path={payload_path}",
            f"output_dir={output_dir}",
            f"captured_at={timestamp}",
        ]
    )
    (output_dir / "metadata.txt").write_text(metadata + "\n", encoding="utf-8")

    print("payload-copy wrote payload.json and metadata.txt", flush=True)


if __name__ == "__main__":
    main()
