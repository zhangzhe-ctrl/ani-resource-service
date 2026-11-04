#!/usr/bin/env python3
"""NET-05A adapter; see net05a-adapt.py for the count-checked runner changes."""
import sys

# Legacy V-09 asserted stale-by-age and freshness-gated admission, both
# removed in the v3 contract. Refuse before opening the saved live fixture.
if sys.argv[1:2] == ["queries"]:
    sys.exit("NET-05A V-09 is incompatible with network observation v3; use the v3 fault tests; live validation remains required")

import importlib.util as _loader
from pathlib import Path as _Path
_spec=_loader.spec_from_file_location("net05a_adapt",_Path(__file__).with_name("net05a-adapt.py"))
_adapter=_loader.module_from_spec(_spec);_spec.loader.exec_module(_adapter)
exec(compile(_adapter.adapted('faults'),__file__,"exec"),globals())
