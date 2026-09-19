# Rooms readiness: blocked before workload

Existing local Lima rooms-host started, initially stopped. Used the existing SSH identity by
preserving HOME for sudo rather than copying private key material. Installed documented host
forwarding for the boot. Attempted:

```sh
sudo --preserve-env=HOME rooms-readiness-src/target/release/rooms run \
  --image rooms/images/rootfs.ext4 --memory 512 --readonly-rootfs --egress none \
  --command 'command -v python3; uname -m' --max-wall 25s \
  --out /tmp/import-lab-probe --json
```

Runner returned exit 4 immediately:

```json
{"error_kind":"pool_full","message":"pool full: all 8 slots claimed","cap":8}
```

`rooms ls` returned `no rooms`; process inspection showed no Firecracker process. Existing
slot records predate this run. We did not remove those records or claim recovery was safe.
No import app ran in a guest. No cloud resources created. Forwarding torn down and the local
VM returned to stopped state. This receipt contains selected observed output, not a full log.
