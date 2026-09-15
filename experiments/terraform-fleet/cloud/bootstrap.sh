#!/usr/bin/env bash
# Run as the ephemeral VM's rooms user. No model or GCP credentials needed.
set -euo pipefail
cd /home/rooms/src
export DEBIAN_FRONTEND=noninteractive
bash scripts/setup-rooms-host.sh
bash scripts/setup-nix-host.sh
sudo apt-get install -y squashfs-tools musl-tools
mkdir -p /home/rooms/.ssh /home/rooms/lab
test -f /home/rooms/.ssh/id_rooms || ssh-keygen -q -t ed25519 -N '' -f /home/rooms/.ssh/id_rooms
sudo bash scripts/setup-tap.sh --host
source /home/rooms/.cargo/env
make check
cargo build --release --locked -j8
test -f /home/rooms/rooms/images/agent.ext4 || sudo bash scripts/build-rootfs-alpine.sh --out /home/rooms/rooms/images/agent.ext4 --ssh-key /home/rooms/.ssh/id_rooms.pub
test -f /home/rooms/lab/python/toolstore.sqfs || sudo -u rooms python3 scripts/build-toolstore.py --preset python --out /home/rooms/lab/python
sudo install -d -m 700 /root/.ssh
sudo install -m 600 /home/rooms/.ssh/id_rooms /root/.ssh/id_rooms
sudo target/release/rooms doctor --image /home/rooms/rooms/images/agent.ext4 --json
sha256sum target/release/rooms /home/rooms/rooms/images/agent.ext4 /home/rooms/rooms/images/vmlinux.bin /home/rooms/lab/python/toolstore.sqfs
touch /home/rooms/lab/bootstrap.done
