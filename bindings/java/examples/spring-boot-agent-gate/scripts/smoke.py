#!/usr/bin/env python3
"""Run the drop_prod_db plan against a running agent service and check the order."""
import json
import sys
import urllib.request

base = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8080"
body = json.dumps({"workflow": "db-maintenance", "traceId": "smoke-py", "goal": "drop_prod_db"}).encode()
req = urllib.request.Request(base + "/agent/runs", body, {"Content-Type": "application/json"})
result = json.load(urllib.request.urlopen(req, timeout=10))

want = ["take_backup", "validate_backup", "drop_prod_db"]
if result["status"] != "COMPLETED" or result["trace"] != want:
    sys.exit(f"unexpected result: {json.dumps(result)}")
print("ok:", " -> ".join(result["trace"]))
