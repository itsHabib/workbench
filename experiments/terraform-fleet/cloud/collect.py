"""Collect public experiment facts before/after teardown; no credentials exported."""
import json
from pathlib import Path
import subprocess
import time

import cloud


def captured(args):
    result = subprocess.run(args, capture_output=True, text=True)
    return {"argv": args, "exit": result.returncode, "stdout": result.stdout, "stderr": result.stderr}


def main():
    report = {"at": time.time(), "project": cloud.PROJECT, "zone": cloud.ZONE}
    report["instances"] = captured(["gcloud", "compute", "instances", "list", "--project=" + cloud.PROJECT,
                                      "--filter=name=tf-fleet-rooms-0915", "--format=json(name,id,status,creationTimestamp,machineType,scheduling,serviceAccounts)"])
    report["disks"] = captured(["gcloud", "compute", "disks", "list", "--project=" + cloud.PROJECT,
                                  "--filter=name=tf-fleet-rooms-0915", "--format=json(name,id,sizeGb,status)"])
    report["networks"] = captured(["gcloud", "compute", "networks", "list", "--project=" + cloud.PROJECT,
                                     "--filter=name=tf-fleet-rooms-0915", "--format=json(name,id)"])
    report["subnets"] = captured(["gcloud", "compute", "networks", "subnets", "list", "--project=" + cloud.PROJECT,
                                    "--filter=name=tf-fleet-rooms-0915", "--format=json(name,id)"])
    report["firewalls"] = captured(["gcloud", "compute", "firewall-rules", "list", "--project=" + cloud.PROJECT,
                                      "--filter=name=tf-fleet-rooms-0915-ssh", "--format=json(name,id,sourceRanges)"])
    if json.loads(report["instances"]["stdout"] or "[]"):
        report["rooms"] = captured([*cloud.ssh_args(), "sudo -H /home/rooms/src/target/release/rooms ls --json"])
        report["vmm_processes"] = captured([*cloud.ssh_args(), "pgrep -x firecracker"])
    path = cloud.STATE / ("inventory-" + str(time.time_ns()) + ".json")
    path.write_text(json.dumps(report, indent=2) + "\n")
    print(path)


if __name__ == "__main__":
    main()
