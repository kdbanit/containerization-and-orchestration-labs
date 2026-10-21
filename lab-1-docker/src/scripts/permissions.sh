#!/usr/bin/env bash
apt install gcc libcap-dev libseccomp-dev
gcc src/cap_seccomp_launcher.c -o bin/launcher -Wall -Wextra -lcap -lseccomp

cp src/check_cap_seccomp.py /tmp/check_cap_seccomp.py
chmod 644 /tmp/check_cap_seccomp.py

bin/launcher /usr/bin/python3 /tmp/check_cap_seccomp.py