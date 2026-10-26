"""Let Hatchling write platform metadata and RECORD for the bundled executable."""

import os

from hatchling.builders.hooks.plugin.interface import BuildHookInterface


class CustomBuildHook(BuildHookInterface):
    def initialize(self, version, build_data):
        build_data["tag"] = os.environ["HOLON_WHEEL_TAG"]
        build_data["pure_python"] = False
