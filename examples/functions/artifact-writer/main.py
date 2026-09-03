from __future__ import annotations

import json
import os
from datetime import datetime, timezone
from pathlib import Path


def env(name: str) -> str:
    return os.environ[name]


def now_utc() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def main() -> None:
    output_dir = Path(env("ORBITALD_OUTPUT_DIR"))
    frames_dir = output_dir / "frames"
    frames_dir.mkdir(parents=True, exist_ok=True)

    window_id = env("ORBITALD_WINDOW_ID")
    area = env("ORBITALD_AREA")
    function_name = env("ORBITALD_FUNCTION")

    for frame_number in range(1, 4):
        frame_contents = "\n".join(
            [
                f"frame={frame_number}",
                f"window_id={window_id}",
                f"area={area}",
                f"captured_at={now_utc()}",
            ]
        )
        (frames_dir / f"frame-{frame_number}.txt").write_text(frame_contents + "\n", encoding="utf-8")

    manifest = {
        "window_id": window_id,
        "function": function_name,
        "artifact_count": 3,
    }
    (output_dir / "manifest.json").write_text(json.dumps(manifest) + "\n", encoding="utf-8")

    print("artifact-writer created 3 frame files and manifest.json", flush=True)


if __name__ == "__main__":
    main()
