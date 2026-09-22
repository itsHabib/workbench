#!/usr/bin/env python3
"""Own one bounded native invocation, including if the trial driver disappears."""
import json
from pathlib import Path
import sys

from lab import atomic
from transport import complete


def main():
    request = json.loads(Path(sys.argv[1]).read_text())
    for key in ("cwd", "output_dir", "stop_file"):
        request[key] = Path(request[key])
    result = complete(**request)
    atomic(request["output_dir"] / "result.json", result)


if __name__ == "__main__":
    main()
