import importlib.machinery
import importlib.util
import unittest
from pathlib import Path

loader = importlib.machinery.SourceFileLoader("third_party_notices", str(Path(__file__).with_name("third-party-notices")))
notices = importlib.util.module_from_spec(importlib.util.spec_from_loader(loader.name, loader))
loader.exec_module(notices)

MIT = "MIT License\n\n{}\n\nPermission is hereby granted, free of charge, to any person obtaining a copy."


class GroupTests(unittest.TestCase):
    def test_group(self):
        sections = notices.group([
            ("a v1", MIT.format("Copyright (c) 2020 A"), ""),
            ("b v2", MIT.format("Copyright (c) 2021 B"), ""),
            ("c v3", MIT.format("Copyright (c) 2022 C"), "Licensed under the Apache License."),
        ])
        self.assertEqual(len(sections), 2)
        self.assertIn("a v1: Copyright (c) 2020 A\nb v2: Copyright (c) 2021 B\n\nMIT License", sections[0])
        self.assertTrue(sections[1].startswith("c v3: Copyright (c) 2022 C") and sections[1].endswith("Licensed under the Apache License."))
