import asyncio
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
# The app directory, with the holon package the host binds dependencies through.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from holon_host import start

asyncio.run(start("server"))
