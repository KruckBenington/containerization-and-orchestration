#!/usr/bin/env bash
set -e

sudo mkdir -p /sys/fs/cgroup/mydocker
echo "+memory +cpu +pids" | sudo tee /sys/fs/cgroup/cgroup.subtree_control
echo "100M" | sudo tee /sys/fs/cgroup/mydocker/memory.max
echo "50000 100000" | sudo tee /sys/fs/cgroup/mydocker/cpu.max
echo "20" | sudo tee /sys/fs/cgroup/mydocker/pids.max
echo $$ | sudo tee /sys/fs/cgroup/mydocker/cgroup.procs

exec unshare --pid --uts --mount --net --ipc --user --map-root-user --mount-proc --fork \
    sh -c "ip link set lo up && exec /usr/sbin/capsh --drop=cap_sys_time -- -c './api'"
